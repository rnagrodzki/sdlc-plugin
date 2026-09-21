package tools

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"testing"
)

// files lists the source files the "hand-written recovery text for the worst
// error sites" sweep is scoped to. The first ten come from task 5;
// plan_support.go and commit.go were added when the Markdown-output branch
// changed their error sites and filled in their missing Suggestions.
var errSuggestionFiles = []string{
	"ship_state.go",
	"execute_state.go",
	"validators.go",
	"scaffold.go",
	"jira.go",
	"pr.go",
	"review.go",
	"received_review.go",
	"dimensions_render.go",
	"setup.go",
	"plan_support.go",
	"commit.go",
}

const errSuggestionBoilerplate = "Check filesystem permissions and available disk space for the project root, then retry."

var errSuggestionTypeNames = map[string]bool{
	"DomainError": true,
	"InfraError":  true,
	"DataError":   true,
}

// suggestionLit finds the Suggestion field's value in an mcpserver error
// composite literal, if a Suggestion key is present at all.
func suggestionLit(lit *ast.CompositeLit) (kv *ast.KeyValueExpr, present bool) {
	for _, elt := range lit.Elts {
		k, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		key, ok := k.Key.(*ast.Ident)
		if !ok || key.Name != "Suggestion" {
			continue
		}
		return k, true
	}
	return nil, false
}

// suggestionText returns the constant text a Suggestion expression carries:
// a string literal, the format string of a fmt.Sprintf call, or the joined
// literal parts of a "+" concatenation. ok is false when the expression holds
// no string literal at all (a bare variable or an opaque call result).
func suggestionText(e ast.Expr) (text string, ok bool) {
	switch v := e.(type) {
	case *ast.BasicLit:
		if v.Kind != token.STRING {
			return "", false
		}
		s, err := strconv.Unquote(v.Value)
		if err != nil {
			return "", false
		}
		return s, true
	case *ast.ParenExpr:
		return suggestionText(v.X)
	case *ast.BinaryExpr:
		if v.Op != token.ADD {
			return "", false
		}
		l, lok := suggestionText(v.X)
		r, rok := suggestionText(v.Y)
		return l + r, lok || rok
	case *ast.CallExpr:
		sel, isSel := v.Fun.(*ast.SelectorExpr)
		if !isSel || len(v.Args) == 0 {
			return "", false
		}
		pkg, isIdent := sel.X.(*ast.Ident)
		if !isIdent || pkg.Name != "fmt" || sel.Sel.Name != "Sprintf" {
			return "", false
		}
		return suggestionText(v.Args[0])
	}
	return "", false
}

// walkErrorLiterals parses path and calls fn for every
// mcpserver.{Domain,Infra,Data}Error composite literal found in it.
func walkErrorLiterals(t *testing.T, path string, fn func(fset *token.FileSet, lit *ast.CompositeLit)) {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	ast.Inspect(f, func(n ast.Node) bool {
		lit, ok := n.(*ast.CompositeLit)
		if !ok {
			return true
		}
		sel, ok := lit.Type.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		pkgIdent, ok := sel.X.(*ast.Ident)
		if !ok || pkgIdent.Name != "mcpserver" || !errSuggestionTypeNames[sel.Sel.Name] {
			return true
		}
		fn(fset, lit)
		return true
	})
}

// TestErrorLiteralsNoBoilerplateSuggestion walks every mcpserver.*Error
// literal in the 10 files task 5 touches and asserts none of them still
// carry the generic, copy-pasted "Check filesystem permissions..."
// Suggestion. This pins the 14/15 boilerplate copies task 5 rewrites
// (pr.go, review.go, received_review.go, dimensions_render.go, jira.go,
// setup.go) without over-constraining the ~220 other literals in these
// files that other tasks/waves own and this task does not touch.
//
// A Suggestion built from a non-literal expression (fmt.Sprintf, a
// variable, ...) is skipped -- it cannot be the boilerplate constant and
// is out of this test's scope.
func TestErrorLiteralsNoBoilerplateSuggestion(t *testing.T) {
	for _, name := range errSuggestionFiles {
		path := name
		t.Run(path, func(t *testing.T) {
			walkErrorLiterals(t, path, func(fset *token.FileSet, lit *ast.CompositeLit) {
				kv, present := suggestionLit(lit)
				if !present {
					return
				}
				text, ok := suggestionText(kv.Value)
				if !ok {
					return
				}
				pos := fset.Position(lit.Pos())
				if text == "" {
					t.Errorf("%s:%d: Suggestion is an empty string literal", path, pos.Line)
				}
				if text == errSuggestionBoilerplate {
					t.Errorf("%s:%d: Suggestion still uses the generic boilerplate text", path, pos.Line)
				}
			})
		})
	}
}

// TestShipStateErrorLiteralsHaveRealSuggestions walks every
// mcpserver.*Error literal in ship_state.go -- task 5's fact sheet covers
// all 59 sites in this file, so "every literal" is exact here -- and
// asserts each one has a Suggestion whose constant text is at least 40
// characters and is not the generic boilerplate.
func TestShipStateErrorLiteralsHaveRealSuggestions(t *testing.T) {
	const path = "ship_state.go"
	walkErrorLiterals(t, path, func(fset *token.FileSet, lit *ast.CompositeLit) {
		pos := fset.Position(lit.Pos())
		sel := lit.Type.(*ast.SelectorExpr)

		kv, present := suggestionLit(lit)
		if !present {
			t.Errorf("%s:%d: %s is missing a Suggestion field", path, pos.Line, sel.Sel.Name)
			return
		}
		text, ok := suggestionText(kv.Value)
		if !ok {
			t.Errorf("%s:%d: %s.Suggestion must be a string literal, a fmt.Sprintf with a literal format, or a \"+\" concatenation containing a literal", path, pos.Line, sel.Sel.Name)
			return
		}
		if text == errSuggestionBoilerplate {
			t.Errorf("%s:%d: %s.Suggestion still uses the generic boilerplate text", path, pos.Line, sel.Sel.Name)
		}
		if len(text) < 40 {
			t.Errorf("%s:%d: %s.Suggestion is shorter than 40 chars: %q", path, pos.Line, sel.Sel.Name, text)
		}
	})
}
