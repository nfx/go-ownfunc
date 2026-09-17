// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: Apache-2.0

package ownfunc

import (
	"bytes"
	"cmp"
	"errors"
	"fmt"
	"go/ast"
	"go/format"
	"go/token"
	"go/types"
	"regexp"
	"slices"
	"strings"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/passes/inspect"
	"golang.org/x/tools/go/ast/inspector"
)

// callSite records where a candidate free function is invoked: the call
// itself, the identifier naming it, and (for genuine call sites, not
// recursive or test-file ones) the receiver identifier in scope at that
// point (the caller is always a method, per the disqualification rules in
// the run phase).
type callSite struct {
	ident    *ast.Ident
	call     *ast.CallExpr
	receiver string
}

type candidate struct {
	decl               *ast.FuncDecl
	obj                *types.Func
	owner              *types.Named
	calls              int
	disqualify         bool
	unfixable          bool
	blankReceiverSites bool
	sites              []callSite
	recursiveCalls     []callSite
	testCalls          []callSite
	fset               *token.FileSet
}

// promotedParam identifies a candidate's own parameter that already carries
// owner (or *owner), so the fix should promote it to a receiver instead of
// synthesizing one.
type promotedParam struct {
	field   *ast.Field
	pointer bool
	index   int
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
		promoted := cfg.findPromotedParam(pass, cand)
		if cand.owner.TypeParams().Len() > 0 {
			cand.unfixable = true
		} else if promoted == nil {
			if cand.blankReceiverSites {
				cand.unfixable = true
			}
			if _, ok := cand.receiverNameOrBlank(); !ok {
				cand.unfixable = true
			}
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
			diag.SuggestedFixes = []analysis.SuggestedFix{
				cand.suggestedFix(promoted),
			}
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
				fset: pass.Fset,
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
		cand.testCalls = append(cand.testCalls, callSite{
			ident: ident,
			call:  node,
		})
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
		cand.recursiveCalls = append(cand.recursiveCalls, callSite{
			ident: ident,
			call:  node,
		})
		return true
	}
	cfg.recordCallSite(pass, caller, ident, node, cand)
	return true
}

// recordCallSite updates cand with a genuine, non-recursive call from caller:
// tracking the owner type, the call count, and the rewrite site for the
// suggested fix. A blank/unnamed caller receiver is recorded rather than
// rejected outright, since it only matters when the fix ends up needing a
// synthesized receiver (see findPromotedParam).
func (cfg *Config) recordCallSite(
	pass *analysis.Pass,
	caller *ast.FuncDecl,
	ident *ast.Ident,
	call *ast.CallExpr,
	cand *candidate,
) {
	owner, done := cfg.qualifiedOwner(pass, caller, cand)
	if done {
		return
	}
	if cand.owner == nil {
		cand.owner = owner
	} else if !cfg.sameNamedType(cand.owner, owner) {
		cand.disqualify = true
	}
	cand.calls++
	recv := cfg.receiverName(caller)
	if recv == "" {
		cand.blankReceiverSites = true
	}
	cand.sites = append(cand.sites, callSite{
		ident:    ident,
		call:     call,
		receiver: recv,
	})
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

// receiverNameOrBlank resolves the name for decl's synthesized receiver
// clause, borrowed from the first call site's own receiver. If that name is
// already used somewhere in decl's own signature or body, reusing it as the
// receiver would collide with an existing declaration there — e.g. a
// "name, err := ..." short variable declaration would silently reassign the
// receiver instead of declaring a new local — so the blank identifier is
// used instead. The blank identifier cannot itself be referenced, so it is
// unusable when decl also calls itself recursively; ok reports whether a
// safe name was found.
func (cand *candidate) receiverNameOrBlank() (name string, ok bool) {
	name = cand.sites[0].receiver
	if !cand.receiverNameCollides(name) {
		return name, true
	}
	if len(cand.recursiveCalls) > 0 {
		return "", false
	}
	return "_", true
}

// receiverNameCollides reports whether name is already used anywhere in
// decl's signature or body.
func (cand *candidate) receiverNameCollides(name string) bool {
	collides := false
	ast.Inspect(cand.decl, func(n ast.Node) bool {
		if ident, ok := n.(*ast.Ident); ok && ident.Name == name {
			collides = true
		}
		return !collides
	})
	return collides
}

// suggestedFix builds the edits that turn decl into a method of owner. When
// decl already takes owner (or *owner) as one of its own parameters,
// promoted is set and that parameter becomes the receiver instead of a
// synthesized one (see suggestedFixPromoted); otherwise a fresh receiver is
// added (see suggestedFixReceiver).
func (cand *candidate) suggestedFix(promoted *promotedParam) analysis.SuggestedFix {
	if promoted != nil {
		return cand.suggestedFixPromoted(promoted)
	}
	return cand.suggestedFixReceiver()
}

// receiverClauseEdit inserts a "(name star Owner) " receiver clause right
// before decl's name, shared by both the synthesized and promoted fixes.
func (cand *candidate) receiverClauseEdit(name, star string) analysis.TextEdit {
	return analysis.TextEdit{
		Pos:     cand.decl.Name.Pos(),
		End:     cand.decl.Name.Pos(),
		NewText: fmt.Appendf(nil, "(%s %s%s) ", name, star, cand.owner.Obj().Name()),
	}
}

// suggestedFixReceiver builds the edits that turn decl into a method of
// owner by adding a synthesized receiver clause and qualifying every
// recorded call site, including recursive self-calls, which take the new
// method's own receiver name, and dangling test-file calls, which get a
// freshly literal owner instance since no receiver is in scope there. The
// receiver is a pointer unless owner's existing methods are exclusively
// value-receiver.
func (cand *candidate) suggestedFixReceiver() analysis.SuggestedFix {
	star := ""
	if cand.ownerUsesPointerReceiver() {
		star = "*"
	}
	recv, _ := cand.receiverNameOrBlank()
	edits := []analysis.TextEdit{cand.receiverClauseEdit(recv, star)}
	for _, site := range cand.sites {
		edits = append(edits, analysis.TextEdit{
			Pos:     site.ident.Pos(),
			End:     site.ident.Pos(),
			NewText: []byte(site.receiver + "."),
		})
	}
	for _, site := range cand.recursiveCalls {
		edits = append(edits, analysis.TextEdit{
			Pos:     site.ident.Pos(),
			End:     site.ident.Pos(),
			NewText: []byte(recv + "."),
		})
	}
	testRecv := fmt.Sprintf("new(%s)", cand.owner.Obj().Name())
	if star == "" {
		testRecv = fmt.Sprintf("(*new(%s))", cand.owner.Obj().Name())
	}
	for _, site := range cand.testCalls {
		edits = append(edits, analysis.TextEdit{
			Pos:     site.ident.Pos(),
			End:     site.ident.Pos(),
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

// suggestedFixPromoted turns decl into a method by promoting its own
// owner-typed parameter into the receiver clause, keeping that parameter's
// name and pointer-ness exactly as declared, and rewrites every call site so
// the former argument expression becomes the receiver instead.
func (cand *candidate) suggestedFixPromoted(promoted *promotedParam) analysis.SuggestedFix {
	name := promoted.field.Names[0].Name
	star := ""
	if promoted.pointer {
		star = "*"
	}
	edits := []analysis.TextEdit{cand.receiverClauseEdit(name, star), cand.removeParamEdit(promoted)}
	for _, site := range cand.sites {
		edits = append(edits, cand.promotedCallEdits(site.ident, site.call, promoted)...)
	}
	for _, site := range cand.recursiveCalls {
		edits = append(edits, cand.promotedCallEdits(site.ident, site.call, promoted)...)
	}
	for _, site := range cand.testCalls {
		edits = append(edits, cand.promotedCallEdits(site.ident, site.call, promoted)...)
	}
	slices.SortFunc(edits, func(left, right analysis.TextEdit) int {
		return cmp.Compare(left.Pos, right.Pos)
	})
	return analysis.SuggestedFix{
		Message: fmt.Sprintf(
			"Convert %s to a method of %s%s, promoting parameter %s to receiver",
			cand.obj.Name(), star, cand.owner.Obj().Name(), name,
		),
		TextEdits: edits,
	}
}

// removeParamEdit deletes promoted's field from decl's parameter list,
// swallowing whichever adjacent comma keeps the remaining list valid.
func (cand *candidate) removeParamEdit(promoted *promotedParam) analysis.TextEdit {
	list := cand.decl.Type.Params.List
	pos, end := promoted.field.Pos(), promoted.field.End()
	for i, field := range list {
		if field != promoted.field {
			continue
		}
		if i+1 < len(list) {
			end = list[i+1].Pos()
		} else if i > 0 {
			pos = list[i-1].End()
		}
		break
	}
	return analysis.TextEdit{Pos: pos, End: end}
}

// promotedCallEdits rewrites one call site once its owner-typed argument is
// promoted to a receiver: the argument's own source text becomes the
// receiver expression, prefixed at ident, and the argument itself is dropped
// from the call.
func (cand *candidate) promotedCallEdits(
	ident *ast.Ident,
	call *ast.CallExpr,
	promoted *promotedParam,
) []analysis.TextEdit {
	if promoted.index >= len(call.Args) {
		return nil
	}
	arg := call.Args[promoted.index]
	edits := []analysis.TextEdit{{
		Pos:     ident.Pos(),
		End:     ident.Pos(),
		NewText: []byte(cand.receiverExprText(arg) + "."),
	}}
	pos, end := arg.Pos(), arg.End()
	switch {
	case promoted.index+1 < len(call.Args):
		end = call.Args[promoted.index+1].Pos()
	case promoted.index > 0:
		pos = call.Args[promoted.index-1].End()
	default:
		// the promoted parameter is the call's only argument; pos/end
		// already span exactly it.
	}
	return append(edits, analysis.TextEdit{Pos: pos, End: end})
}

// exprText re-renders expr as source text, used to lift a call argument's
// exact expression into receiver position.
func (cand *candidate) exprText(expr ast.Expr) string {
	var buf bytes.Buffer
	err := format.Node(&buf, cand.fset, expr)
	if err != nil {
		return ""
	}
	return buf.String()
}

// receiverExprText renders expr for use immediately before a selector, e.g.
// "recv.Method()". A bare identifier is left unparenthesized; anything else
// (a composite literal, a unary "&x", a binary expression, ...) is wrapped in
// parens, since ".Method()" binds to only a primary expression and something
// like "&Some{}.Method()" would otherwise parse as "&(Some{}.Method())".
func (cand *candidate) receiverExprText(expr ast.Expr) string {
	text := cand.exprText(expr)
	if _, ok := expr.(*ast.Ident); ok {
		return text
	}
	return "(" + text + ")"
}

// findPromotedParam reports whether decl already takes owner (or *owner) as
// an explicit, singly-named parameter; such a parameter is a receiver in
// disguise and should become one instead of decl gaining a receiver of its
// own alongside it.
func (cfg *Config) findPromotedParam(pass *analysis.Pass, cand *candidate) *promotedParam {
	if cand.decl.Type.Params == nil {
		return nil
	}
	index := 0
	for _, field := range cand.decl.Type.Params.List {
		if len(field.Names) == 1 {
			if pp := cfg.matchOwnerParam(pass, cand.owner, field, index); pp != nil {
				return pp
			}
		}
		index += max(len(field.Names), 1)
	}
	return nil
}

// matchOwnerParam reports whether field's type is owner or *owner, returning
// the promotedParam describing it, or nil when it isn't.
func (cfg *Config) matchOwnerParam(
	pass *analysis.Pass,
	owner *types.Named,
	field *ast.Field,
	index int,
) *promotedParam {
	t := pass.TypesInfo.TypeOf(field.Type)
	pointer := false
	if ptr, ok := t.(*types.Pointer); ok {
		t, pointer = ptr.Elem(), true
	}
	named, ok := t.(*types.Named)
	if !ok || !cfg.sameNamedType(named, owner) {
		return nil
	}
	return &promotedParam{field: field, pointer: pointer, index: index}
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

// sameNamedType reports whether a and b are the same declared type,
// comparing declaration identity rather than using types.Identical: two
// methods of a generic type each declare their own type-parameter objects
// in their receiver clause (e.g. "Box[T]" vs "Box[U]"), so types.Identical
// reports those receivers as different even though they name the same type.
func (cfg *Config) sameNamedType(a, b *types.Named) bool {
	return a.Obj() == b.Obj()
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
