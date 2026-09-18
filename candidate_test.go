// Copyright 2026 Serge Smertin
// SPDX-License-Identifier: Apache-2.0

package ownfunc

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

func parseFuncDecl(t *testing.T, body string) *ast.FuncDecl {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "test.go", "package p\n"+body, 0)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if ok {
			return fn
		}
	}
	t.Fatal("no func decl found")
	return nil
}

func TestFreeRenamedName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "no collision uses bare V suffix",
			body: "func Requeue() { q := 1; _ = q }",
			want: "qV",
		},
		{
			name: "V collision advances to V2",
			body: "func Requeue() { q, qV := 1, 2; _, _ = q, qV }",
			want: "qV2",
		},
		{
			name: "V and V2 collision advances to V3",
			body: "func Requeue() { q, qV, qV2 := 1, 2, 3; _, _, _ = q, qV, qV2 }",
			want: "qV3",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c := &candidate{}
			decl := parseFuncDecl(t, tt.body)
			got := c.freeRenamedName(decl, "q")
			if got != tt.want {
				t.Fatalf("freeRenamedName() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFreeRenamedNameExhaustsSuffixRange(t *testing.T) {
	t.Parallel()

	names := []string{"q", "qV"}
	for n := 2; n <= maxRenameSuffix; n++ {
		names = append(names, fmt.Sprintf("qV%d", n))
	}
	var body strings.Builder
	body.WriteString("func Requeue() {\n\tvar ")
	body.WriteString(strings.Join(names, ", "))
	body.WriteString(" int\n\t_ = q\n}")

	c := &candidate{}
	decl := parseFuncDecl(t, body.String())
	got := c.freeRenamedName(decl, "q")
	if got != "" {
		t.Fatalf("freeRenamedName() = %q, want empty", got)
	}
}
