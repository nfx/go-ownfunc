package ownfunc

import (
	"bytes"
	"cmp"
	"fmt"
	"go/ast"
	"go/format"
	"go/token"
	"go/types"
	"slices"
	"strings"

	"golang.org/x/tools/go/analysis"
)

type candidate struct {
	decl              *ast.FuncDecl
	obj               *types.Func
	owner             *types.Named
	calls             int
	disqualify        bool
	unfixable         bool
	blankReceiverName string
	namedCallers      []*ast.FuncDecl
	renameEdits       []analysis.TextEdit
	sites             []callSite
	recursiveCalls    []callSite
	testCalls         []callSite
	fset              *token.FileSet
}

// unfixableWithoutPromotion reports whether decl cannot safely get a
// synthesized receiver: either some call site's caller still has no receiver
// identifier in scope — its own receiver was blank and resolveBlankReceivers
// couldn't safely name it — or the only safe synthesized name for decl's own
// receiver would collide with decl's own body (see receiverNameOrOmitted)
// while decl also needs that name for a recursive call. It is never called
// for a candidate whose owner is generic and whose own type-parameter list
// couldn't be proven, via declTypeParamMatchesOwner, to bind the same way a
// synthesized receiver would — report skips reporting those entirely rather
// than reaching this method, since decl genuinely cannot become any method of
// owner in that case (see report).
func (c *candidate) unfixableWithoutPromotion() bool {
	if c.hasBlankReceiverSite() {
		return true
	}
	_, ok := c.receiverNameOrOmitted()
	return !ok
}

// hasBlankReceiverSite reports whether any recorded call site still has no
// receiver identifier to qualify it with. resolveBlankReceivers runs before
// this is checked, so a site only remains blank here when its caller
// couldn't safely be given a synthesized receiver name.
func (c *candidate) hasBlankReceiverSite() bool {
	for _, site := range c.sites {
		if site.receiver == "" {
			return true
		}
	}
	return false
}

// resolveBlankReceivers attempts to give every blank-receiver call site's
// caller a real receiver name, using owner's established receiver-name
// convention (established), so those sites can be qualified like any other
// call. When established already collides with something in a caller's own
// signature or body — typically a local variable reusing the same short
// name — renameCollidingLocals first tries to free it by suffixing every
// occurrence of the colliding declaration with "V", "V2", "V3", and so on.
// Nothing is mutated, and false is returned, when established is empty or a
// caller still can't be freed up even after exhausting those attempts. On
// success, each resolved site's
// receiver is set to established, its caller is recorded in c.namedCallers
// (deduplicated, since the same caller can hold more than one call site) so
// suggestedFixReceiver can add the matching receiver-clause edit, and any
// rename edits collected along the way are recorded in c.renameEdits.
func (c *candidate) resolveBlankReceivers(pass *analysis.Pass, established string) bool {
	var toName []int
	var callers []*ast.FuncDecl
	var renames []analysis.TextEdit
	seen := make(map[*ast.FuncDecl]bool)
	for i, site := range c.sites {
		if site.receiver != "" {
			continue
		}
		if established == "" {
			return false
		}
		toName = append(toName, i)
		if seen[site.caller] {
			continue
		}
		seen[site.caller] = true
		if c.identCollidesIn(site.caller, established) {
			edits, ok := c.renameCollidingLocals(pass, site.caller, established)
			if !ok {
				return false
			}
			renames = append(renames, edits...)
		}
		callers = append(callers, site.caller)
	}
	for _, i := range toName {
		c.sites[i].receiver = established
	}
	c.blankReceiverName = established
	c.namedCallers = callers
	c.renameEdits = renames
	return true
}

// renameCollidingLocals frees established for use as caller's receiver name
// by suffixing every occurrence of whichever local declaration(s) inside
// caller already use that name with the first free name freeRenamedName
// finds (e.g. "e" becomes "eV", or "eV2" if "eV" is itself taken), so the
// receiver and the renamed local no longer share an identifier. ok is false
// when no such declaration is found, or every candidate name up to
// maxRenameSuffix collides with something else in caller, in which case no
// edits are returned and the rename is abandoned.
func (c *candidate) renameCollidingLocals(
	pass *analysis.Pass,
	caller *ast.FuncDecl,
	established string,
) (edits []analysis.TextEdit, ok bool) {
	renamed := c.freeRenamedName(caller, established)
	if renamed == "" {
		return nil, false
	}
	objs := c.collidingObjects(pass, caller, established)
	if len(objs) == 0 {
		return nil, false
	}
	for _, obj := range objs {
		for _, ident := range c.declAndUsesOfObject(pass, caller, obj) {
			edits = append(edits, analysis.TextEdit{Pos: ident.Pos(), End: ident.End(), NewText: []byte(renamed)})
		}
	}
	return edits, true
}

// maxRenameSuffix caps how many suffixed candidate names freeRenamedName
// tries before giving up; established+"V1000" also colliding is never
// realistically hit.
const maxRenameSuffix = 1000

// freeRenamedName finds the first name, in the established+"V",
// established+"V2", established+"V3", ... sequence, that doesn't collide with
// anything already declared in caller. Returns "" if every candidate up to
// established+"V"+maxRenameSuffix collides.
func (c *candidate) freeRenamedName(caller *ast.FuncDecl, established string) string {
	name := established + "V"
	if !c.identCollidesIn(caller, name) {
		return name
	}
	for n := 2; n <= maxRenameSuffix; n++ {
		name = fmt.Sprintf("%sV%d", established, n)
		if !c.identCollidesIn(caller, name) {
			return name
		}
	}
	return ""
}

// collidingObjects returns every distinct object declared inside caller
// under the exact name established — normally just one (e.g. a single loop
// variable), but a name can be redeclared in separate, non-overlapping
// blocks within the same function, so all of them are collected.
func (c *candidate) collidingObjects(pass *analysis.Pass, caller *ast.FuncDecl, established string) []types.Object {
	seen := make(map[types.Object]bool)
	var objs []types.Object
	ast.Inspect(caller, func(n ast.Node) bool {
		ident, ok := n.(*ast.Ident)
		if !ok || ident.Name != established {
			return true
		}
		obj := pass.TypesInfo.Defs[ident]
		if obj != nil && !seen[obj] {
			seen[obj] = true
			objs = append(objs, obj)
		}
		return true
	})
	return objs
}

// declAndUsesOfObject collects every *ast.Ident inside decl that either
// declares obj or resolves to it: the local's own declaration site plus
// every place it's read afterward, needed to rename a colliding local
// everywhere it appears rather than just where it's used.
func (c *candidate) declAndUsesOfObject(pass *analysis.Pass, decl *ast.FuncDecl, obj types.Object) []*ast.Ident {
	var idents []*ast.Ident
	ast.Inspect(decl, func(n ast.Node) bool {
		ident, ok := n.(*ast.Ident)
		if !ok {
			return true
		}
		if pass.TypesInfo.Defs[ident] == obj || pass.TypesInfo.Uses[ident] == obj {
			idents = append(idents, ident)
		}
		return true
	})
	return idents
}

// nameCallerReceiverEdit gives caller's own receiver clause the name
// established, when it currently has none. It inserts the name before the
// receiver's type when the clause declares no identifier at all (e.g.
// "(*Engine)"), or replaces the existing blank identifier in place when it
// does (e.g. "(_ *Engine)").
func (c *candidate) nameCallerReceiverEdit(caller *ast.FuncDecl, established string) analysis.TextEdit {
	field := caller.Recv.List[0]
	if len(field.Names) == 0 {
		return analysis.TextEdit{
			Pos:     field.Type.Pos(),
			End:     field.Type.Pos(),
			NewText: []byte(established + " "),
		}
	}
	blank := field.Names[0]
	return analysis.TextEdit{
		Pos:     blank.Pos(),
		End:     blank.End(),
		NewText: []byte(established),
	}
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
func (c *candidate) receiverNameOrOmitted() (name string, ok bool) {
	name = c.sites[0].receiver
	if !c.identCollidesIn(c.decl, name) {
		return name, true
	}
	if len(c.recursiveCalls) > 0 {
		return "", false
	}
	return "", true
}

// identCollidesIn reports whether name is already used anywhere in decl's
// signature or body.
func (c *candidate) identCollidesIn(decl *ast.FuncDecl, name string) bool {
	collides := false
	ast.Inspect(decl, func(n ast.Node) bool {
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
func (c *candidate) suggestedFix(pass *analysis.Pass, promoted *promotedParam) analysis.SuggestedFix {
	if promoted != nil {
		return c.suggestedFixPromoted(pass, promoted)
	}
	return c.suggestedFixReceiver()
}

// receiverClauseEdit inserts a "(name star Owner[typeParams]) " receiver
// clause right before decl's name, shared by both the synthesized and
// promoted fixes. typeParams is "" for a non-generic owner. name is "" when
// the receiver is unused, in which case it is omitted from the clause
// entirely rather than written as "_".
func (c *candidate) receiverClauseEdit(name, star, typeParams string) analysis.TextEdit {
	prefix := ""
	if name != "" {
		prefix = name + " "
	}
	return analysis.TextEdit{
		Pos:     c.decl.Name.Pos(),
		End:     c.decl.Name.Pos(),
		NewText: fmt.Appendf(nil, "(%s%s%s%s) ", prefix, star, c.owner.Obj().Name(), typeParams),
	}
}

// ownerTypeParamsClause returns owner's own type-parameter names as a
// receiver clause suffix (e.g. "[T]" or "[K, V]"), or "" when owner isn't
// generic. Used to synthesize a receiver for decl when decl has no type
// parameters of its own to bind to owner's — the names are declared fresh
// on the receiver and left unused by decl's body, which is valid regardless
// of what owner's type argument actually is at each call site.
func (c *candidate) ownerTypeParamsClause() string {
	tparams := c.owner.TypeParams()
	if tparams.Len() == 0 {
		return ""
	}
	names := make([]string, tparams.Len())
	for i := range names {
		names[i] = tparams.At(i).Obj().Name()
	}
	return "[" + strings.Join(names, ", ") + "]"
}

// suggestedFixReceiver builds the edits that turn decl into a method of
// owner by adding a synthesized receiver clause and qualifying every
// recorded call site, including recursive self-calls, which take the new
// method's own receiver name, and dangling test-file calls, which get a
// freshly literal owner instance since no receiver is in scope there. Any
// caller in c.namedCallers also gets its own previously blank receiver
// clause named, via resolveBlankReceivers, so its call sites can be
// qualified too, alongside c.renameEdits for any local that had to be
// renamed out of the way first. The receiver is a pointer unless owner's
// existing methods are exclusively value-receiver. When owner is generic,
// the receiver's type-parameter names come from decl's own type-parameter
// list if it has one (moved there since declTypeParamMatchesOwner proved it
// matches, and removed from decl's own list since a method can't declare
// type parameters of its own) or otherwise from owner's, left unused in
// decl's body.
func (c *candidate) suggestedFixReceiver() analysis.SuggestedFix {
	star := ""
	if c.ownerUsesPointerReceiver() {
		star = "*"
	}
	recv, _ := c.receiverNameOrOmitted()
	typeParams := c.ownerTypeParamsClause()
	edits := []analysis.TextEdit{}
	if c.decl.Type.TypeParams != nil {
		typeParams = "[" + c.decl.Type.TypeParams.List[0].Names[0].Name + "]"
		edits = append(edits, c.removeTypeParamsEdit())
	}
	edits = append(edits, c.receiverClauseEdit(recv, star, typeParams))
	edits = append(edits, c.renameEdits...)
	for _, caller := range c.namedCallers {
		edits = append(edits, c.nameCallerReceiverEdit(caller, c.blankReceiverName))
	}
	for _, site := range c.sites {
		edits = append(edits, analysis.TextEdit{
			Pos:     site.ident.Pos(),
			End:     site.ident.Pos(),
			NewText: []byte(site.receiver + "."),
		})
	}
	for _, site := range c.recursiveCalls {
		edits = append(edits, analysis.TextEdit{
			Pos:     site.ident.Pos(),
			End:     site.ident.Pos(),
			NewText: []byte(recv + "."),
		})
	}
	testRecv := c.testCallReceiver(star)
	for _, site := range c.testCalls {
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
		Message:   fmt.Sprintf("Convert %s to a method of %s%s", c.obj.Name(), star, c.owner.Obj().Name()),
		TextEdits: edits,
	}
}

// testCallReceiver returns the receiver expression used to synthesize an
// instance of owner for a dangling test-file call. A pointer receiver just
// needs new(Owner). A value receiver would need to dereference that pointer,
// but when owner's underlying type accepts nil as a conversion (a slice,
// map, chan, func, pointer, or interface), Owner(nil) is the more idiomatic
// zero-value expression and is used instead.
func (c *candidate) testCallReceiver(star string) string {
	name := c.owner.Obj().Name()
	if star != "" {
		return fmt.Sprintf("new(%s)", name)
	}
	if c.ownerAcceptsNil() {
		return name + "(nil)"
	}
	return fmt.Sprintf("(*new(%s))", name)
}

// ownerAcceptsNil reports whether owner's underlying type is one nil
// converts to directly.
func (c *candidate) ownerAcceptsNil() bool {
	switch c.owner.Underlying().(type) {
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
func (c *candidate) ownerUsesPointerReceiver() bool {
	hasPointer := false
	hasValue := false
	for i := range c.owner.NumMethods() {
		recv := c.owner.Method(i).Signature().Recv()
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
func (c *candidate) suggestedFixPromoted(pass *analysis.Pass, promoted *promotedParam) analysis.SuggestedFix {
	name := promoted.receiverName
	star := ""
	if promoted.pointer {
		star = "*"
	}
	typeParams := ""
	edits := []analysis.TextEdit{}
	if promoted.typeParam != nil {
		typeParams = "[" + promoted.typeParam.Names[0].Name + "]"
		edits = append(edits, c.removeTypeParamsEdit())
	}
	edits = append(edits, c.receiverClauseEdit(name, star, typeParams), c.removeParamEdit(promoted))
	for _, ident := range promoted.renameIdents {
		edits = append(edits, analysis.TextEdit{Pos: ident.Pos(), End: ident.End(), NewText: []byte(name)})
	}
	for _, site := range c.sites {
		edits = append(edits, c.promotedCallEdits(pass, site.ident, site.call, promoted)...)
	}
	for _, site := range c.recursiveCalls {
		edits = append(edits, c.promotedCallEdits(pass, site.ident, site.call, promoted)...)
	}
	for _, site := range c.testCalls {
		edits = append(edits, c.promotedCallEdits(pass, site.ident, site.call, promoted)...)
	}
	slices.SortFunc(edits, func(left, right analysis.TextEdit) int {
		return cmp.Compare(left.Pos, right.Pos)
	})
	return analysis.SuggestedFix{
		Message: fmt.Sprintf(
			"Convert %s to a method of %s%s, promoting parameter %s to receiver",
			c.obj.Name(), star, c.owner.Obj().Name(), name,
		),
		TextEdits: edits,
	}
}

// removeTypeParamsEdit deletes decl's own type-parameter clause (e.g.
// "[T any]"), needed when that type parameter is promoted into the
// receiver's type-parameter list instead.
func (c *candidate) removeTypeParamsEdit() analysis.TextEdit {
	tparams := c.decl.Type.TypeParams
	return analysis.TextEdit{Pos: tparams.Pos(), End: tparams.End()}
}

// removeParamEdit deletes promoted's field from decl's parameter list,
// swallowing whichever adjacent comma keeps the remaining list valid.
func (c *candidate) removeParamEdit(promoted *promotedParam) analysis.TextEdit {
	list := c.decl.Type.Params.List
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
func (c *candidate) promotedCallEdits(
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
		NewText: []byte(c.receiverExprText(pass, arg, promoted.pointer) + "."),
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
func (c *candidate) exprText(expr ast.Expr) string {
	var buf bytes.Buffer
	err := format.Node(&buf, c.fset, expr)
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
func (c *candidate) receiverExprText(pass *analysis.Pass, expr ast.Expr, pointer bool) string {
	text := c.exprText(expr)
	if c.exprNeedsOwnerConversion(pass, expr, pointer) {
		return c.owner.Obj().Name() + "(" + text + ")"
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
func (c *candidate) exprNeedsOwnerConversion(pass *analysis.Pass, expr ast.Expr, pointer bool) bool {
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
	return named.Obj() != c.owner.Obj()
}

// canConvertAllSites reports whether every recorded call site — genuine,
// recursive, and test — can safely be rewritten by promotedCallEdits: for
// each site whose argument at index isn't already owner-typed,
// exprNeedsOwnerConversion's "Owner(expr)" conversion syntax must actually
// resolve to owner at that position, which it won't when owner's own name is
// shadowed there (see ownerNameShadowed).
func (c *candidate) canConvertAllSites(pass *analysis.Pass, index int) bool {
	for _, site := range slices.Concat(c.sites, c.recursiveCalls, c.testCalls) {
		if index >= len(site.call.Args) {
			return false
		}
		arg := site.call.Args[index]
		if !c.exprNeedsOwnerConversion(pass, arg, false) {
			continue
		}
		if c.ownerNameShadowed(pass, arg.Pos()) {
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
func (c *candidate) ownerNameShadowed(pass *analysis.Pass, pos token.Pos) bool {
	scope := pass.Pkg.Scope().Innermost(pos)
	if scope == nil {
		return false
	}
	_, obj := scope.LookupParent(c.owner.Obj().Name(), pos)
	return obj != c.owner.Obj()
}
