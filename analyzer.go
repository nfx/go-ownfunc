// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: Apache-2.0

package ownfunc

import (
	"errors"
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"maps"
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
// the run phase) and the caller's own FuncDecl, needed to compare its
// receiver's type argument against decl's own inferred one for a candidate
// whose owner is generic (see declTypeParamMatchesOwner).
type callSite struct {
	ident    *ast.Ident
	call     *ast.CallExpr
	receiver string
	caller   *ast.FuncDecl
}

// promotedParam identifies a candidate's own parameter that already carries
// owner (or *owner), so the fix should promote it to a receiver instead of
// synthesizing one. typeParam is set when owner is generic: it names decl's
// own type-parameter field that must move from decl's type-parameter list
// into the receiver's, since a Go method cannot declare type parameters of
// its own. receiverName is the name to give the promoted receiver: normally
// field's own name, but renamed to match owner's established receiver-name
// convention when that's safe (see newPromotedParam); renameIdents then
// lists field's occurrences inside decl that must be rewritten to match.
type promotedParam struct {
	field        *ast.Field
	pointer      bool
	index        int
	typeParam    *ast.Field
	receiverName string
	renameIdents []*ast.Ident
}

type suggestedFixMode int

const (
	suggestedFixesCombined suggestedFixMode = iota
	suggestedFixesFileLocal
)

// Analyzer constructs the ownfunc analyzer with the given configuration.
func (o *Ownfunc) Analyzer() *analysis.Analyzer {
	return o.newAnalyzer(suggestedFixesCombined)
}

// golangciAnalyzer constructs suggested fixes that are scoped to the file of
// their diagnostic. golangci-lint applies all edits from an issue to that one
// file, so cross-file fixes must be reported separately.
func (o *Ownfunc) golangciAnalyzer() *analysis.Analyzer {
	return o.newAnalyzer(suggestedFixesFileLocal)
}

func (o *Ownfunc) newAnalyzer(fixMode suggestedFixMode) *analysis.Analyzer {
	o.applyDefaults()
	return &analysis.Analyzer{
		Name:     "ownfunc",
		Doc:      "reports unexported package functions used exclusively by methods of a single receiver type",
		Requires: []*analysis.Analyzer{inspect.Analyzer},
		Run: func(pass *analysis.Pass) (any, error) {
			return o.run(pass, fixMode)
		},
	}
}

// ErrInvalid is invalid.
var ErrInvalid = errors.New("invalid")

func (o *Ownfunc) run(pass *analysis.Pass, fixMode suggestedFixMode) (any, error) {
	candidates := o.candidates(pass)
	if len(candidates) == 0 {
		return nil, nil
	}
	err := o.inspect(pass, candidates)
	if err != nil {
		return nil, err
	}
	o.report(candidates, pass, fixMode)
	return nil, nil
}

func (o *Ownfunc) report(
	candidates map[*types.Func]*candidate,
	pass *analysis.Pass,
	fixMode suggestedFixMode,
) {
	for _, cand := range candidates {
		o.reportCandidate(cand, pass, fixMode)
	}
}

func (o *Ownfunc) reportCandidate(cand *candidate, pass *analysis.Pass, fixMode suggestedFixMode) {
	if cand.disqualify || cand.calls < o.MinCalls || cand.owner == nil {
		return
	}
	promoted := o.findPromotedParam(pass, cand)
	if promoted == nil && o.skipCandidate(cand, pass) {
		return
	}
	if promoted == nil {
		cand.resolveBlankReceivers(pass, o.establishedReceiverName(cand))
	}
	cand.unfixable = promoted == nil && cand.unfixableWithoutPromotion()
	diag := analysis.Diagnostic{
		Pos: cand.decl.Name.Pos(),
		Message: fmt.Sprintf(
			"%s is called only from methods of *%s; consider making it an unexported method",
			cand.obj.Name(),
			cand.owner.Obj().Name(),
		),
	}
	if cand.unfixable {
		pass.Report(diag)
		return
	}
	if fixMode == suggestedFixesFileLocal {
		o.reportFileLocalFixes(cand, pass, promoted, diag)
		return
	}
	diag.SuggestedFixes = []analysis.SuggestedFix{cand.suggestedFix(pass, promoted)}
	pass.Report(diag)
}

// reportFileLocalFixes emits one diagnostic per edited file so golangci-lint
// applies every offset to its matching source file, including _test.go files.
func (o *Ownfunc) reportFileLocalFixes(
	cand *candidate,
	pass *analysis.Pass,
	promoted *promotedParam,
	diag analysis.Diagnostic,
) {
	fix := cand.suggestedFix(pass, promoted)
	fixes := make(map[string][]analysis.TextEdit)
	for _, edit := range fix.TextEdits {
		filename := pass.Fset.Position(edit.Pos).Filename
		fixes[filename] = append(fixes[filename], edit)
	}
	filenames := slices.Collect(maps.Keys(fixes))
	slices.Sort(filenames)
	for _, filename := range filenames {
		edits := fixes[filename]
		fileDiag := diag
		fileDiag.Pos = edits[0].Pos
		fileDiag.SuggestedFixes = []analysis.SuggestedFix{{
			Message:   fix.Message,
			TextEdits: edits,
		}}
		pass.Report(fileDiag)
	}
}

// skipCandidate flags generic owner plus decl's own type parameter,
// with no promotable param to prove the binding, means "consider
// making it a method" is only sound advice when declTypeParamMatchesOwner
// confirms every call site agrees on decl's type parameter;
// otherwise decl cannot become any method of owner at all (not merely
//
//	one this tool can't auto-fix), so the diagnostic itself would be
//
// misleading and is skipped.
func (o *Ownfunc) skipCandidate(cand *candidate, pass *analysis.Pass) bool {
	return cand.owner.TypeParams().Len() > 0 && cand.decl.Type.TypeParams != nil &&
		!o.declTypeParamMatchesOwner(pass, cand)
}

func (o *Ownfunc) candidates(pass *analysis.Pass) map[*types.Func]*candidate {
	candidates := make(map[*types.Func]*candidate)
	ignoredRegexes := o.compileRegexes(o.IgnoredFunctions)
	for _, file := range pass.Files {
		if o.ignoreFile(pass, file.Pos()) {
			continue
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv != nil || fn.Name.IsExported() {
				continue
			}
			if o.matchesAny(fn.Name.Name, ignoredRegexes) {
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

func (o *Ownfunc) ignoreFile(pass *analysis.Pass, pos token.Pos) bool {
	if !o.IgnoreTestFilesEnabled() {
		return false
	}
	filename := pass.Fset.Position(pos).Filename
	return strings.HasSuffix(filename, "_test.go")
}

func (o *Ownfunc) inspect(pass *analysis.Pass, candidates map[*types.Func]*candidate) error {
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
			return o.inspectCallExpr(pass, node, enclosing, candidates)
		case *ast.Ident:
			if !push {
				return true
			}
			return o.inspectIdent(pass, node, stack, candidates)
		default:
			return true
		}
		return true
	})
	return nil
}

func (o *Ownfunc) inspectIdent(
	pass *analysis.Pass,
	node *ast.Ident,
	stack []ast.Node,
	candidates map[*types.Func]*candidate,
) bool {
	if o.AllowFunctionValues {
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
	if o.ignoreFile(pass, node.Pos()) {
		return true
	}
	if !o.isDirectCallee(node, stack) {
		cand.disqualify = true
	}
	return true
}

func (o *Ownfunc) inspectCallExpr(
	pass *analysis.Pass,
	node *ast.CallExpr,
	enclosing []*ast.FuncDecl,
	candidates map[*types.Func]*candidate,
) bool {
	ident := o.extractCalleeIdent(node.Fun)
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
	if o.ignoreFile(pass, node.Pos()) {
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
	o.recordCallSite(pass, caller, ident, node, cand)
	return true
}

// recordCallSite updates cand with a genuine, non-recursive call from caller:
// tracking the owner type, the call count, and the rewrite site for the
// suggested fix. A blank/unnamed caller receiver is recorded as "" rather
// than rejected outright, since it only matters when the fix ends up needing
// a synthesized receiver — resolveBlankReceivers then tries to name it.
func (o *Ownfunc) recordCallSite(
	pass *analysis.Pass,
	caller *ast.FuncDecl,
	ident *ast.Ident,
	call *ast.CallExpr,
	cand *candidate,
) {
	owner, done := o.qualifiedOwner(pass, caller, cand)
	if done {
		return
	}
	if cand.owner == nil {
		cand.owner = owner
	} else if !o.sameNamedType(cand.owner, owner) {
		cand.disqualify = true
	}
	cand.calls++
	recv := o.receiverName(caller)
	cand.sites = append(cand.sites, callSite{
		ident:    ident,
		call:     call,
		receiver: recv,
		caller:   caller,
	})
}

func (o *Ownfunc) qualifiedOwner(pass *analysis.Pass,
	caller *ast.FuncDecl, cand *candidate) (*types.Named, bool) {
	owner := o.canonicalReceiverType(pass, caller)
	if owner == nil || o.isIgnoredReceiver(owner, o.IgnoredReceiverTypes) {
		cand.disqualify = true
		return nil, true
	}
	return owner, false
}

// receiverName returns fn's receiver identifier, or "" when the receiver is
// unnamed or blank, in which case no call site can be rewritten to a method call.
func (o *Ownfunc) receiverName(fn *ast.FuncDecl) string {
	if fn == nil || fn.Recv == nil || len(fn.Recv.List) == 0 || len(fn.Recv.List[0].Names) == 0 {
		return ""
	}
	name := fn.Recv.List[0].Names[0].Name
	if name == "_" {
		return ""
	}
	return name
}

// findPromotedParam reports whether decl already takes owner (or *owner) as
// an explicit, singly-named parameter; such a parameter is a receiver in
// disguise and should become one instead of decl gaining a receiver of its
// own alongside it.
func (o *Ownfunc) findPromotedParam(pass *analysis.Pass, cand *candidate) *promotedParam {
	if cand.decl.Type.Params == nil {
		return nil
	}
	index := 0
	for _, field := range cand.decl.Type.Params.List {
		if len(field.Names) == 1 {
			if pp := o.matchOwnerParam(pass, cand, field, index); pp != nil {
				return pp
			}
		}
		index += max(len(field.Names), 1)
	}
	return nil
}

// matchOwnerParam reports whether field's type is owner or *owner, returning
// the promotedParam describing it, or nil when it isn't. When owner is
// generic, the match additionally requires decl's own type-parameter list to
// be exactly the type argument owner is instantiated with here (see
// matchGenericOwnerParam), since a Go method cannot declare type parameters
// of its own.
func (o *Ownfunc) matchOwnerParam(
	pass *analysis.Pass,
	cand *candidate,
	field *ast.Field,
	index int,
) *promotedParam {
	t := pass.TypesInfo.TypeOf(field.Type)
	pointer := false
	if ptr, ok := t.(*types.Pointer); ok {
		t, pointer = ptr.Elem(), true
	}
	named, ok := t.(*types.Named)
	if !ok {
		return o.matchUnderlyingOwnerParam(pass, cand, field, t, pointer, index)
	}
	if !o.sameNamedType(named, cand.owner) {
		return nil
	}
	if cand.owner.TypeParams().Len() == 0 {
		return o.newPromotedParam(pass, cand, field, pointer, index, nil)
	}
	return o.matchGenericOwnerParam(pass, cand, named, field, pointer, index)
}

// matchUnderlyingOwnerParam handles a field whose declared type, after any
// pointer indirection, isn't itself a named type but is structurally
// identical to owner's underlying type — the common idiom of declaring a
// parameter as e.g. "bars []Bar" using the plain slice type instead of the
// named bars type. Promotion is safe even though field's declared type is
// looser than owner: whatever a caller passes there was already required by
// the compiler to be assignable to that structurally-identical type, so it is
// always convertible to owner too, and promotedCallEdits wraps it in that
// conversion at each call site that needs it (see exprNeedsOwnerConversion).
// That conversion syntax, "Owner(expr)", only compiles when owner's own name
// isn't itself shadowed at the call site — which is common here, since the
// parameter being promoted tends to already share owner's name with whatever
// local variable calls it — so every site needing the conversion is checked
// first (see canConvertAllSites). Only owner being generic (a receiver clause
// can't bind a type parameter here) also rules this out.
func (o *Ownfunc) matchUnderlyingOwnerParam(
	pass *analysis.Pass,
	cand *candidate,
	field *ast.Field,
	t types.Type,
	pointer bool,
	index int,
) *promotedParam {
	if pointer || cand.owner.TypeParams().Len() > 0 {
		return nil
	}
	if !types.Identical(t, cand.owner.Underlying()) {
		return nil
	}
	if !cand.canConvertAllSites(pass, index) {
		return nil
	}
	return o.newPromotedParam(pass, cand, field, false, index, nil)
}

// matchGenericOwnerParam extends matchOwnerParam to a generic owner: the
// promotion is only safe when decl declares exactly one type parameter and
// field's type argument for owner is that same type parameter, since a
// method cannot declare type parameters of its own — anything left over
// after binding one to the receiver could not be expressed.
func (o *Ownfunc) matchGenericOwnerParam(
	pass *analysis.Pass,
	cand *candidate,
	named *types.Named,
	field *ast.Field,
	pointer bool,
	index int,
) *promotedParam {
	tparams := cand.decl.Type.TypeParams
	if tparams == nil || len(tparams.List) != 1 || len(tparams.List[0].Names) != 1 {
		return nil
	}
	if cand.owner.TypeParams().Len() != 1 || named.TypeArgs().Len() != 1 {
		return nil
	}
	declParam, ok := pass.TypesInfo.Defs[tparams.List[0].Names[0]].(*types.TypeName)
	if !ok {
		return nil
	}
	arg, ok := named.TypeArgs().At(0).(*types.TypeParam)
	if !ok || arg.Obj() != declParam {
		return nil
	}
	return o.newPromotedParam(pass, cand, field, pointer, index, tparams.List[0])
}

// declTypeParamMatchesOwner reports whether decl's own type parameter can be
// moved into a synthesized receiver's type-parameter list even though decl
// takes no promotable owner-typed argument (see matchGenericOwnerParam for
// that case). It requires decl and owner to each declare exactly one type
// parameter, and every genuine call site to instantiate decl's type
// parameter as exactly the calling method's own receiver type argument —
// proven the same way matchGenericOwnerParam proves a promoted param's type
// argument, but read from each call's recorded instantiation instead of a
// parameter's declared type, since here there is no such parameter.
func (o *Ownfunc) declTypeParamMatchesOwner(pass *analysis.Pass, cand *candidate) bool {
	tparams := cand.decl.Type.TypeParams
	if tparams == nil || len(tparams.List) != 1 || len(tparams.List[0].Names) != 1 {
		return false
	}
	if cand.owner.TypeParams().Len() != 1 {
		return false
	}
	for _, site := range cand.sites {
		if !o.siteInstantiatesOwnerTypeParam(pass, site) {
			return false
		}
	}
	return true
}

// siteInstantiatesOwnerTypeParam reports whether site's call to decl was
// inferred, at that call, to instantiate decl's type parameter with exactly
// site.caller's own receiver type argument.
func (o *Ownfunc) siteInstantiatesOwnerTypeParam(pass *analysis.Pass, site callSite) bool {
	inst, ok := pass.TypesInfo.Instances[site.ident]
	if !ok || inst.TypeArgs.Len() != 1 {
		return false
	}
	callArg, ok := inst.TypeArgs.At(0).(*types.TypeParam)
	if !ok {
		return false
	}
	callerOwner := o.canonicalReceiverType(pass, site.caller)
	if callerOwner == nil || callerOwner.TypeArgs().Len() != 1 {
		return false
	}
	callerArg, ok := callerOwner.TypeArgs().At(0).(*types.TypeParam)
	return ok && callArg == callerArg
}

// newPromotedParam builds the promotedParam for field, preferring to rename
// its receiver to owner's established receiver-name convention (see
// establishedReceiverName) over keeping field's own name, when that rename
// is unambiguous: every occurrence of field's own name inside decl must
// resolve to field itself, and the established name must not already be
// used by anything else in decl.
func (o *Ownfunc) newPromotedParam(
	pass *analysis.Pass,
	cand *candidate,
	field *ast.Field,
	pointer bool,
	index int,
	typeParam *ast.Field,
) *promotedParam {
	pp := &promotedParam{
		field:        field,
		pointer:      pointer,
		index:        index,
		typeParam:    typeParam,
		receiverName: field.Names[0].Name,
	}
	established := o.establishedReceiverName(cand)
	if established == "" || established == pp.receiverName {
		return pp
	}
	obj := pass.TypesInfo.Defs[field.Names[0]]
	if o.identsCollide(pass, cand.decl, obj, established) {
		return pp
	}
	pp.receiverName = established
	pp.renameIdents = o.identsOfObject(pass, cand.decl, obj)
	return pp
}

// establishedReceiverName returns the most common receiver name across
// owner's existing methods (ties broken by first occurrence), or "" when no
// method has a named, non-blank receiver.
func (o *Ownfunc) establishedReceiverName(cand *candidate) string {
	counts := make(map[string]int)
	var order []string
	for i := range cand.owner.NumMethods() {
		recv := cand.owner.Method(i).Signature().Recv()
		if recv == nil || recv.Name() == "" || recv.Name() == "_" {
			continue
		}
		if counts[recv.Name()] == 0 {
			order = append(order, recv.Name())
		}
		counts[recv.Name()]++
	}
	best := ""
	for _, name := range order {
		if best == "" || counts[name] > counts[best] {
			best = name
		}
	}
	return best
}

// identsOfObject collects every *ast.Ident inside decl that resolves to obj.
func (o *Ownfunc) identsOfObject(pass *analysis.Pass, decl *ast.FuncDecl, obj types.Object) []*ast.Ident {
	var idents []*ast.Ident
	ast.Inspect(decl, func(n ast.Node) bool {
		if ident, ok := n.(*ast.Ident); ok && pass.TypesInfo.Uses[ident] == obj {
			idents = append(idents, ident)
		}
		return true
	})
	return idents
}

// identsCollide reports whether decl already declares or uses some object
// named name other than obj, which would make renaming obj's occurrences to
// name unsafe (shadowing or reassigning an unrelated identifier).
func (o *Ownfunc) identsCollide(pass *analysis.Pass, decl *ast.FuncDecl, obj types.Object, name string) bool {
	collides := false
	ast.Inspect(decl, func(n ast.Node) bool {
		ident, ok := n.(*ast.Ident)
		if !ok || ident.Name != name || collides {
			return !collides
		}
		target := pass.TypesInfo.Defs[ident]
		if target == nil {
			target = pass.TypesInfo.Uses[ident]
		}
		collides = target != obj
		return !collides
	})
	return collides
}

func (o *Ownfunc) extractCalleeIdent(expr ast.Expr) *ast.Ident {
	switch e := expr.(type) {
	case *ast.Ident:
		return e
	case *ast.ParenExpr:
		return o.extractCalleeIdent(e.X)
	default:
		return nil
	}
}

func (o *Ownfunc) canonicalReceiverType(pass *analysis.Pass, fn *ast.FuncDecl) *types.Named {
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
func (o *Ownfunc) isDirectCallee(ident *ast.Ident, stack []ast.Node) bool {
	for i := len(stack) - 2; i >= 0; i-- {
		switch n := stack[i].(type) {
		case *ast.ParenExpr:
			continue
		case *ast.CallExpr:
			return o.extractCalleeIdent(n.Fun) == ident
		default:
			return false
		}
	}
	return false
}

func (o *Ownfunc) isIgnoredReceiver(named *types.Named, ignored []string) bool {
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
func (o *Ownfunc) sameNamedType(a, b *types.Named) bool {
	return a.Obj() == b.Obj()
}

func (o *Ownfunc) compileRegexes(patterns []string) []*regexp.Regexp {
	var result []*regexp.Regexp
	for _, p := range patterns {
		if re, err := regexp.Compile(p); err == nil {
			result = append(result, re)
		}
	}
	return result
}

func (o *Ownfunc) matchesAny(name string, regexes []*regexp.Regexp) bool {
	for _, re := range regexes {
		if re.MatchString(name) {
			return true
		}
	}
	return false
}
