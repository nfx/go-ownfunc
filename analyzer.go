// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: Apache-2.0

package ownfunc

import (
	"cmp"
	"errors"
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"regexp"
	"slices"
	"strings"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/passes/inspect"
	"golang.org/x/tools/go/ast/inspector"
)

// callSite records where a candidate free function is invoked, together with
// the receiver identifier in scope at that point (the caller is always a
// method, per the disqualification rules in the run phase).
type callSite struct {
	ident    *ast.Ident
	receiver string
}

type candidate struct {
	decl            *ast.FuncDecl
	obj             *types.Func
	owner           *types.Named
	calls           int
	disqualify      bool
	unfixable       bool
	sites           []callSite
	recursiveIdents []*ast.Ident
	testCallIdents  []*ast.Ident
}

// NewAnalyzer constructs the ownfunc analyzer with the given configuration.
func NewAnalyzer(cfg Config) *analysis.Analyzer {
	cfg.ApplyDefaults()
	return &analysis.Analyzer{
		Name:     "ownfunc",
		Doc:      "reports unexported package functions used exclusively by methods of a single receiver type",
		Requires: []*analysis.Analyzer{inspect.Analyzer},
		Run:      cfg.run,
	}
}

// ErrInvalid is invalid.
var ErrInvalid = errors.New("invalid")

func (cfg *Config) run(pass *analysis.Pass) (any, error) {
	candidates := cfg.candidates(pass)
	if len(candidates) == 0 {
		return nil, nil
	}
	err := cfg.inspect(pass, candidates)
	if err != nil {
		return nil, err
	}
	cfg.report(candidates, pass)
	return nil, nil
}

func (cfg *Config) report(candidates map[*types.Func]*candidate, pass *analysis.Pass) {
	for _, cand := range candidates {
		if cand.disqualify || cand.calls < cfg.MinCalls || cand.owner == nil {
			continue
		}
		diag := analysis.Diagnostic{
			Pos: cand.decl.Name.Pos(),
			Message: fmt.Sprintf(
				"%s is called only from methods of *%s; consider making it an unexported method",
				cand.obj.Name(),
				cand.owner.Obj().Name(),
			),
		}
		if !cand.unfixable {
			diag.SuggestedFixes = []analysis.SuggestedFix{cand.suggestedFix()}
		}
		pass.Report(diag)
	}
}

func (cfg *Config) candidates(pass *analysis.Pass) map[*types.Func]*candidate {
	candidates := make(map[*types.Func]*candidate)
	ignoredRegexes := cfg.compileRegexes(cfg.IgnoredFunctions)
	for _, file := range pass.Files {
		if cfg.ignoreFile(pass, file.Pos()) {
			continue
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv != nil || fn.Name.IsExported() {
				continue
			}
			if cfg.matchesAny(fn.Name.Name, ignoredRegexes) {
				continue
			}
			obj, ok := pass.TypesInfo.Defs[fn.Name].(*types.Func)
			if !ok || obj == nil {
				continue
			}
			candidates[obj] = &candidate{
				decl: fn,
				obj:  obj,
			}
		}
	}
	return candidates
}

func (cfg *Config) ignoreFile(pass *analysis.Pass, pos token.Pos) bool {
	if !cfg.IgnoreTestFilesEnabled() {
		return false
	}
	filename := pass.Fset.Position(pos).Filename
	return strings.HasSuffix(filename, "_test.go")
}

func (cfg *Config) inspect(pass *analysis.Pass, candidates map[*types.Func]*candidate) error {
	insp, ok := pass.ResultOf[inspect.Analyzer].(*inspector.Inspector)
	if !ok {
		return fmt.Errorf("%w: expected *inspector.Inspector", ErrInvalid)
	}
	var enclosing []*ast.FuncDecl
	nodeFilter := []ast.Node{
		(*ast.FuncDecl)(nil),
		(*ast.CallExpr)(nil),
		(*ast.Ident)(nil),
	}
	insp.WithStack(nodeFilter, func(n ast.Node, push bool, stack []ast.Node) bool {
		switch node := n.(type) {
		case *ast.FuncDecl:
			if push {
				enclosing = append(enclosing, node)
			} else {
				enclosing = enclosing[:len(enclosing)-1]
			}
		case *ast.CallExpr:
			if !push {
				return true
			}
			return cfg.inspectCallExpr(pass, node, enclosing, candidates)
		case *ast.Ident:
			if !push {
				return true
			}
			return cfg.inspectIdent(pass, node, stack, candidates)
		default:
			return true
		}
		return true
	})
	return nil
}

func (cfg *Config) inspectIdent(
	pass *analysis.Pass,
	node *ast.Ident,
	stack []ast.Node,
	candidates map[*types.Func]*candidate,
) bool {
	if cfg.AllowFunctionValues {
		return true
	}
	obj, ok := pass.TypesInfo.Uses[node].(*types.Func)
	if !ok {
		return true
	}
	cand, ok := candidates[obj]
	if !ok {
		return true
	}
	if cfg.ignoreFile(pass, node.Pos()) {
		return true
	}
	if !cfg.isDirectCallee(node, stack) {
		cand.disqualify = true
	}
	return true
}

func (cfg *Config) inspectCallExpr(
	pass *analysis.Pass,
	node *ast.CallExpr,
	enclosing []*ast.FuncDecl,
	candidates map[*types.Func]*candidate,
) bool {
	ident := cfg.extractCalleeIdent(node.Fun)
	if ident == nil {
		return true
	}
	obj, ok := pass.TypesInfo.Uses[ident].(*types.Func)
	if !ok {
		return true
	}
	cand, ok := candidates[obj]
	if !ok {
		return true
	}
	// a test-file call is not evidence for or against ownership (it's excluded
	// from disqualify/calls accounting below), but it still calls the bare
	// identifier directly, which breaks once decl becomes a method; record it
	// so suggestedFix can synthesize a receiver for it.
	if cfg.ignoreFile(pass, node.Pos()) {
		cand.testCallIdents = append(cand.testCallIdents, ident)
		return true
	}
	var caller *ast.FuncDecl
	if len(enclosing) > 0 {
		caller = enclosing[len(enclosing)-1]
	}
	// a recursive self-call says nothing about which type's methods use cand,
	// so it must not disqualify or count towards it; it still needs qualifying
	// once cand becomes a method, so record it separately for the fix.
	if caller == cand.decl {
		cand.recursiveIdents = append(cand.recursiveIdents, ident)
		return true
	}
	cfg.recordCallSite(pass, caller, ident, cand)
	return true
}

// recordCallSite updates cand with a genuine, non-recursive call from caller:
// tracking the owner type, the call count, and the rewrite site for the
// suggested fix.
func (cfg *Config) recordCallSite(pass *analysis.Pass, caller *ast.FuncDecl, ident *ast.Ident, cand *candidate) {
	owner, done := cfg.qualifiedOwner(pass, caller, cand)
	if done {
		return
	}
	if cand.owner == nil {
		cand.owner = owner
	} else if !types.Identical(cand.owner, owner) {
		cand.disqualify = true
	}
	cand.calls++
	recv := cfg.receiverName(caller)
	if recv == "" {
		cand.unfixable = true
	} else {
		cand.sites = append(cand.sites, callSite{ident: ident, receiver: recv})
	}
}

func (cfg *Config) qualifiedOwner(pass *analysis.Pass,
	caller *ast.FuncDecl, cand *candidate) (*types.Named, bool) {
	owner := cfg.canonicalReceiverType(pass, caller)
	if owner == nil || cfg.isIgnoredReceiver(owner, cfg.IgnoredReceiverTypes) {
		cand.disqualify = true
		return nil, true
	}
	return owner, false
}

// receiverName returns fn's receiver identifier, or "" when the receiver is
// unnamed or blank, in which case no call site can be rewritten to a method call.
func (cfg *Config) receiverName(fn *ast.FuncDecl) string {
	if fn == nil || fn.Recv == nil || len(fn.Recv.List) == 0 || len(fn.Recv.List[0].Names) == 0 {
		return ""
	}
	name := fn.Recv.List[0].Names[0].Name
	if name == "_" {
		return ""
	}
	return name
}

// suggestedFix builds the edits that turn decl into a method of owner: add the
// receiver clause and qualify every recorded call site, including recursive
// self-calls, which take the new method's own receiver name, and dangling
// test-file calls, which get a freshly literal owner instance since no
// receiver is in scope there. The receiver is a pointer unless owner's
// existing methods are exclusively value-receiver.
func (cand *candidate) suggestedFix() analysis.SuggestedFix {
	star := ""
	if cand.ownerUsesPointerReceiver() {
		star = "*"
	}
	recv := cand.sites[0].receiver
	edits := []analysis.TextEdit{{
		Pos:     cand.decl.Name.Pos(),
		End:     cand.decl.Name.Pos(),
		NewText: fmt.Appendf(nil, "(%s %s%s) ", recv, star, cand.owner.Obj().Name()),
	}}
	for _, site := range cand.sites {
		edits = append(edits, analysis.TextEdit{
			Pos:     site.ident.Pos(),
			End:     site.ident.Pos(),
			NewText: []byte(site.receiver + "."),
		})
	}
	for _, ident := range cand.recursiveIdents {
		edits = append(edits, analysis.TextEdit{
			Pos:     ident.Pos(),
			End:     ident.Pos(),
			NewText: []byte(recv + "."),
		})
	}
	amp := ""
	if star == "*" {
		amp = "&"
	}
	testRecv := fmt.Sprintf("(%s%s{})", amp, cand.owner.Obj().Name())
	for _, ident := range cand.testCallIdents {
		edits = append(edits, analysis.TextEdit{
			Pos:     ident.Pos(),
			End:     ident.Pos(),
			NewText: []byte(testRecv + "."),
		})
	}
	slices.SortFunc(edits, func(left, right analysis.TextEdit) int {
		return cmp.Compare(left.Pos, right.Pos)
	})
	return analysis.SuggestedFix{
		Message:   fmt.Sprintf("Convert %s to a method of %s%s", cand.obj.Name(), star, cand.owner.Obj().Name()),
		TextEdits: edits,
	}
}

// ownerUsesPointerReceiver reports whether owner's methods should get a
// pointer receiver: true when any existing method already uses one, or when
// owner has no methods yet; false only when every existing method is
// value-receiver.
func (cand *candidate) ownerUsesPointerReceiver() bool {
	hasPointer := false
	hasValue := false
	for i := range cand.owner.NumMethods() {
		recv := cand.owner.Method(i).Signature().Recv()
		if recv == nil {
			continue
		}
		if _, ok := recv.Type().(*types.Pointer); ok {
			hasPointer = true
		} else {
			hasValue = true
		}
	}
	return hasPointer || !hasValue
}

func (cfg *Config) extractCalleeIdent(expr ast.Expr) *ast.Ident {
	switch e := expr.(type) {
	case *ast.Ident:
		return e
	case *ast.ParenExpr:
		return cfg.extractCalleeIdent(e.X)
	default:
		return nil
	}
}

func (cfg *Config) canonicalReceiverType(pass *analysis.Pass, fn *ast.FuncDecl) *types.Named {
	if fn == nil || fn.Recv == nil || len(fn.Recv.List) == 0 {
		return nil
	}
	t := pass.TypesInfo.TypeOf(fn.Recv.List[0].Type)
	if t == nil {
		return nil
	}
	if ptr, ok := t.(*types.Pointer); ok {
		t = ptr.Elem()
	}
	named, ok := t.(*types.Named)
	if !ok {
		return nil
	}
	return named
}

// isDirectCallee reports whether ident is the callee of an enclosing CallExpr,
// walking through intervening parentheses.
func (cfg *Config) isDirectCallee(ident *ast.Ident, stack []ast.Node) bool {
	for i := len(stack) - 2; i >= 0; i-- {
		switch n := stack[i].(type) {
		case *ast.ParenExpr:
			continue
		case *ast.CallExpr:
			return cfg.extractCalleeIdent(n.Fun) == ident
		default:
			return false
		}
	}
	return false
}

func (cfg *Config) isIgnoredReceiver(named *types.Named, ignored []string) bool {
	if named == nil || named.Obj() == nil {
		return false
	}
	typeName := named.Obj().Name()
	return slices.Contains(ignored, typeName)
}

func (cfg *Config) compileRegexes(patterns []string) []*regexp.Regexp {
	var result []*regexp.Regexp
	for _, p := range patterns {
		if re, err := regexp.Compile(p); err == nil {
			result = append(result, re)
		}
	}
	return result
}

func (cfg *Config) matchesAny(name string, regexes []*regexp.Regexp) bool {
	for _, re := range regexes {
		if re.MatchString(name) {
			return true
		}
	}
	return false
}
