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

// Analyzer constructs the ownfunc analyzer with the given configuration.
func (cfg *Ownfunc) Analyzer() *analysis.Analyzer {
	cfg.applyDefaults()
	return &analysis.Analyzer{
		Name:     "ownfunc",
		Doc:      "reports unexported package functions used exclusively by methods of a single receiver type",
		Requires: []*analysis.Analyzer{inspect.Analyzer},
		Run:      cfg.run,
	}
}

// ErrInvalid is invalid.
var ErrInvalid = errors.New("invalid")

func (cfg *Ownfunc) run(pass *analysis.Pass) (any, error) {
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

func (cfg *Ownfunc) report(candidates map[*types.Func]*candidate, pass *analysis.Pass) {
	for _, cand := range candidates {
		if cand.disqualify || cand.calls < cfg.MinCalls || cand.owner == nil {
			continue
		}
		promoted := cfg.findPromotedParam(pass, cand)
		cand.unfixable = promoted == nil && cand.unfixableWithoutPromotion()
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
				cand.suggestedFix(pass, promoted),
			}
		}
		pass.Report(diag)
	}
}

func (cfg *Ownfunc) candidates(pass *analysis.Pass) map[*types.Func]*candidate {
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

func (cfg *Ownfunc) ignoreFile(pass *analysis.Pass, pos token.Pos) bool {
	if !cfg.IgnoreTestFilesEnabled() {
		return false
	}
	filename := pass.Fset.Position(pos).Filename
	return strings.HasSuffix(filename, "_test.go")
}

func (cfg *Ownfunc) inspect(pass *analysis.Pass, candidates map[*types.Func]*candidate) error {
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

func (cfg *Ownfunc) inspectIdent(
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

func (cfg *Ownfunc) inspectCallExpr(
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
func (cfg *Ownfunc) recordCallSite(
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

func (cfg *Ownfunc) qualifiedOwner(pass *analysis.Pass,
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
func (cfg *Ownfunc) receiverName(fn *ast.FuncDecl) string {
	if fn == nil || fn.Recv == nil || len(fn.Recv.List) == 0 || len(fn.Recv.List[0].Names) == 0 {
		return ""
	}
	name := fn.Recv.List[0].Names[0].Name
	if name == "_" {
		return ""
	}
	return name
}

// unfixableWithoutPromotion reports whether decl cannot safely get a
// synthesized receiver: either owner is generic and has no other way to
// supply its type argument, every caller's own receiver is blank/unnamed, or
// the only safe synthesized name would collide with decl's own body (see
// receiverNameOrBlank) while decl also needs that name for a recursive call.
func (cand *candidate) unfixableWithoutPromotion() bool {
	if cand.owner.TypeParams().Len() > 0 || cand.blankReceiverSites {
		return true
	}
	_, ok := cand.receiverNameOrOmitted()
	return !ok
}

// receiverNameOrOmitted resolves the name for decl's synthesized receiver
// clause, borrowed from the first call site's own receiver. If that name is
// already used somewhere in decl's own signature or body, reusing it as the
// receiver would collide with an existing declaration there — e.g. a
// "name, err := ..." short variable declaration would silently reassign the
// receiver instead of declaring a new local — so the name is omitted from
// the receiver clause instead. An omitted name cannot itself be referenced,
// so it is unusable when decl also calls itself recursively; ok reports
// whether a safe name (possibly omitted) was found.
func (cand *candidate) receiverNameOrOmitted() (name string, ok bool) {
	name = cand.sites[0].receiver
	if !cand.receiverNameCollides(name) {
		return name, true
	}
	if len(cand.recursiveCalls) > 0 {
		return "", false
	}
	return "", true
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
func (cand *candidate) suggestedFix(pass *analysis.Pass, promoted *promotedParam) analysis.SuggestedFix {
	if promoted != nil {
		return cand.suggestedFixPromoted(pass, promoted)
	}
	return cand.suggestedFixReceiver()
}

// receiverClauseEdit inserts a "(name star Owner[typeParams]) " receiver
// clause right before decl's name, shared by both the synthesized and
// promoted fixes. typeParams is "" for a non-generic owner. name is "" when
// the receiver is unused, in which case it is omitted from the clause
// entirely rather than written as "_".
func (cand *candidate) receiverClauseEdit(name, star, typeParams string) analysis.TextEdit {
	prefix := ""
	if name != "" {
		prefix = name + " "
	}
	return analysis.TextEdit{
		Pos:     cand.decl.Name.Pos(),
		End:     cand.decl.Name.Pos(),
		NewText: fmt.Appendf(nil, "(%s%s%s%s) ", prefix, star, cand.owner.Obj().Name(), typeParams),
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
	recv, _ := cand.receiverNameOrOmitted()
	edits := []analysis.TextEdit{cand.receiverClauseEdit(recv, star, "")}
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
	testRecv := cand.testCallReceiver(star)
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

// testCallReceiver returns the receiver expression used to synthesize an
// instance of owner for a dangling test-file call. A pointer receiver just
// needs new(Owner). A value receiver would need to dereference that pointer,
// but when owner's underlying type accepts nil as a conversion (a slice,
// map, chan, func, pointer, or interface), Owner(nil) is the more idiomatic
// zero-value expression and is used instead.
func (cand *candidate) testCallReceiver(star string) string {
	name := cand.owner.Obj().Name()
	if star != "" {
		return fmt.Sprintf("new(%s)", name)
	}
	if cand.ownerAcceptsNil() {
		return name + "(nil)"
	}
	return fmt.Sprintf("(*new(%s))", name)
}

// ownerAcceptsNil reports whether owner's underlying type is one nil
// converts to directly.
func (cand *candidate) ownerAcceptsNil() bool {
	switch cand.owner.Underlying().(type) {
	case *types.Slice, *types.Map, *types.Chan, *types.Signature, *types.Pointer, *types.Interface:
		return true
	default:
		return false
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
// pointer-ness exactly as declared, and rewrites every call site so the
// former argument expression becomes the receiver instead. The receiver
// keeps the parameter's own name unless promoted.receiverName renames it to
// match owner's established receiver-name convention, in which case every
// occurrence inside decl is rewritten too (see newPromotedParam). When owner
// is generic, promoted.typeParam also moves from decl's own type-parameter
// list into the receiver's, since a method cannot declare type parameters
// itself.
func (cand *candidate) suggestedFixPromoted(pass *analysis.Pass, promoted *promotedParam) analysis.SuggestedFix {
	name := promoted.receiverName
	star := ""
	if promoted.pointer {
		star = "*"
	}
	typeParams := ""
	edits := []analysis.TextEdit{}
	if promoted.typeParam != nil {
		typeParams = "[" + promoted.typeParam.Names[0].Name + "]"
		edits = append(edits, cand.removeTypeParamsEdit())
	}
	edits = append(edits, cand.receiverClauseEdit(name, star, typeParams), cand.removeParamEdit(promoted))
	for _, ident := range promoted.renameIdents {
		edits = append(edits, analysis.TextEdit{Pos: ident.Pos(), End: ident.End(), NewText: []byte(name)})
	}
	for _, site := range cand.sites {
		edits = append(edits, cand.promotedCallEdits(pass, site.ident, site.call, promoted)...)
	}
	for _, site := range cand.recursiveCalls {
		edits = append(edits, cand.promotedCallEdits(pass, site.ident, site.call, promoted)...)
	}
	for _, site := range cand.testCalls {
		edits = append(edits, cand.promotedCallEdits(pass, site.ident, site.call, promoted)...)
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

// removeTypeParamsEdit deletes decl's own type-parameter clause (e.g.
// "[T any]"), needed when that type parameter is promoted into the
// receiver's type-parameter list instead.
func (cand *candidate) removeTypeParamsEdit() analysis.TextEdit {
	tparams := cand.decl.Type.TypeParams
	return analysis.TextEdit{Pos: tparams.Pos(), End: tparams.End()}
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
	pass *analysis.Pass,
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
		NewText: []byte(cand.receiverExprText(pass, arg, promoted.pointer) + "."),
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
// "recv.Method()". When expr's own static type isn't already owner (or
// *owner, per pointer), it's wrapped in an explicit conversion first — needed
// when the promoted parameter's declared type was only structurally
// identical to owner rather than owner itself (see matchUnderlyingOwnerParam;
// Go guarantees the conversion is valid, since expr was already assignable to
// that structurally-identical declared type). Otherwise a bare identifier is
// left unparenthesized; anything else (a composite literal, a unary "&x", a
// binary expression, ...) is wrapped in parens, since ".Method()" binds to
// only a primary expression and something like "&Some{}.Method()" would
// otherwise parse as "&(Some{}.Method())".
func (cand *candidate) receiverExprText(pass *analysis.Pass, expr ast.Expr, pointer bool) string {
	text := cand.exprText(expr)
	if cand.exprNeedsOwnerConversion(pass, expr, pointer) {
		return cand.owner.Obj().Name() + "(" + text + ")"
	}
	if _, ok := expr.(*ast.Ident); ok {
		return text
	}
	return "(" + text + ")"
}

// exprNeedsOwnerConversion reports whether expr's own static type isn't
// already owner (or *owner, when pointer), meaning the promoted call site
// must wrap it in an explicit conversion for the resulting method call to
// type-check. It compares by declared-type identity (Obj()) rather than
// types.Identical, since two of owner's own methods each declare their own
// type-parameter objects in their receiver clause when owner is generic (see
// sameNamedType), which types.Identical would otherwise see as different types.
func (cand *candidate) exprNeedsOwnerConversion(pass *analysis.Pass, expr ast.Expr, pointer bool) bool {
	t := pass.TypesInfo.TypeOf(expr)
	if pointer {
		ptr, ok := t.(*types.Pointer)
		if !ok {
			return true
		}
		t = ptr.Elem()
	}
	named, ok := t.(*types.Named)
	if !ok {
		return true
	}
	return named.Obj() != cand.owner.Obj()
}

// findPromotedParam reports whether decl already takes owner (or *owner) as
// an explicit, singly-named parameter; such a parameter is a receiver in
// disguise and should become one instead of decl gaining a receiver of its
// own alongside it.
func (cfg *Ownfunc) findPromotedParam(pass *analysis.Pass, cand *candidate) *promotedParam {
	if cand.decl.Type.Params == nil {
		return nil
	}
	index := 0
	for _, field := range cand.decl.Type.Params.List {
		if len(field.Names) == 1 {
			if pp := cfg.matchOwnerParam(pass, cand, field, index); pp != nil {
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
func (cfg *Ownfunc) matchOwnerParam(
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
		return cfg.matchUnderlyingOwnerParam(pass, cand, field, t, pointer, index)
	}
	if !cfg.sameNamedType(named, cand.owner) {
		return nil
	}
	if cand.owner.TypeParams().Len() == 0 {
		return cfg.newPromotedParam(pass, cand, field, pointer, index, nil)
	}
	return cfg.matchGenericOwnerParam(pass, cand, named, field, pointer, index)
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
func (cfg *Ownfunc) matchUnderlyingOwnerParam(
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
	return cfg.newPromotedParam(pass, cand, field, false, index, nil)
}

// canConvertAllSites reports whether every recorded call site — genuine,
// recursive, and test — can safely be rewritten by promotedCallEdits: for
// each site whose argument at index isn't already owner-typed,
// exprNeedsOwnerConversion's "Owner(expr)" conversion syntax must actually
// resolve to owner at that position, which it won't when owner's own name is
// shadowed there (see ownerNameShadowed).
func (cand *candidate) canConvertAllSites(pass *analysis.Pass, index int) bool {
	for _, site := range slices.Concat(cand.sites, cand.recursiveCalls, cand.testCalls) {
		if index >= len(site.call.Args) {
			return false
		}
		arg := site.call.Args[index]
		if !cand.exprNeedsOwnerConversion(pass, arg, false) {
			continue
		}
		if cand.ownerNameShadowed(pass, arg.Pos()) {
			return false
		}
	}
	return true
}

// ownerNameShadowed reports whether owner's own name resolves to something
// other than owner itself at pos — e.g. a local variable or parameter with
// the same name — which would make an "Owner(expr)" conversion written at pos
// resolve incorrectly, or fail to compile outright if the shadowing object
// isn't callable.
func (cand *candidate) ownerNameShadowed(pass *analysis.Pass, pos token.Pos) bool {
	scope := pass.Pkg.Scope().Innermost(pos)
	if scope == nil {
		return false
	}
	_, obj := scope.LookupParent(cand.owner.Obj().Name(), pos)
	return obj != cand.owner.Obj()
}

// matchGenericOwnerParam extends matchOwnerParam to a generic owner: the
// promotion is only safe when decl declares exactly one type parameter and
// field's type argument for owner is that same type parameter, since a
// method cannot declare type parameters of its own — anything left over
// after binding one to the receiver could not be expressed.
func (cfg *Ownfunc) matchGenericOwnerParam(
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
	return cfg.newPromotedParam(pass, cand, field, pointer, index, tparams.List[0])
}

// newPromotedParam builds the promotedParam for field, preferring to rename
// its receiver to owner's established receiver-name convention (see
// establishedReceiverName) over keeping field's own name, when that rename
// is unambiguous: every occurrence of field's own name inside decl must
// resolve to field itself, and the established name must not already be
// used by anything else in decl.
func (cfg *Ownfunc) newPromotedParam(
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
	established := cfg.establishedReceiverName(cand)
	if established == "" || established == pp.receiverName {
		return pp
	}
	obj := pass.TypesInfo.Defs[field.Names[0]]
	if cfg.identsCollide(pass, cand.decl, obj, established) {
		return pp
	}
	pp.receiverName = established
	pp.renameIdents = cfg.identsOfObject(pass, cand.decl, obj)
	return pp
}

// establishedReceiverName returns the most common receiver name across
// owner's existing methods (ties broken by first occurrence), or "" when no
// method has a named, non-blank receiver.
func (cfg *Ownfunc) establishedReceiverName(cand *candidate) string {
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
func (cfg *Ownfunc) identsOfObject(pass *analysis.Pass, decl *ast.FuncDecl, obj types.Object) []*ast.Ident {
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
func (cfg *Ownfunc) identsCollide(pass *analysis.Pass, decl *ast.FuncDecl, obj types.Object, name string) bool {
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

func (cfg *Ownfunc) extractCalleeIdent(expr ast.Expr) *ast.Ident {
	switch e := expr.(type) {
	case *ast.Ident:
		return e
	case *ast.ParenExpr:
		return cfg.extractCalleeIdent(e.X)
	default:
		return nil
	}
}

func (cfg *Ownfunc) canonicalReceiverType(pass *analysis.Pass, fn *ast.FuncDecl) *types.Named {
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
func (cfg *Ownfunc) isDirectCallee(ident *ast.Ident, stack []ast.Node) bool {
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

func (cfg *Ownfunc) isIgnoredReceiver(named *types.Named, ignored []string) bool {
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
func (cfg *Ownfunc) sameNamedType(a, b *types.Named) bool {
	return a.Obj() == b.Obj()
}

func (cfg *Ownfunc) compileRegexes(patterns []string) []*regexp.Regexp {
	var result []*regexp.Regexp
	for _, p := range patterns {
		if re, err := regexp.Compile(p); err == nil {
			result = append(result, re)
		}
	}
	return result
}

func (cfg *Ownfunc) matchesAny(name string, regexes []*regexp.Regexp) bool {
	for _, re := range regexes {
		if re.MatchString(name) {
			return true
		}
	}
	return false
}
