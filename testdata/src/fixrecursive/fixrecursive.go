package fixrecursive

import "go/ast"

type Walker struct {
	depth int
}

// unwrapParens strips surrounding parentheses from expr, recursing on itself
// until a non-paren expression is reached. Its recursive call must not
// disqualify it from being flagged as a candidate method of Walker.
func unwrapParens(expr ast.Expr) ast.Expr { // want "unwrapParens is called only from methods of \\*Walker; consider making it an unexported method"
	if paren, ok := expr.(*ast.ParenExpr); ok {
		return unwrapParens(paren.X)
	}
	return expr
}

func (w *Walker) Visit(expr ast.Expr) ast.Expr {
	return unwrapParens(expr)
}

func (w *Walker) VisitTwice(expr ast.Expr) ast.Expr {
	return unwrapParens(expr)
}
