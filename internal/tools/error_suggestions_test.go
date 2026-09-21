package tools

import (
	"go/ast"
	"go/parser"
	"go/token"
	"slices"
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

// isErrorLit reports whether lit is an mcpserver.{Domain,Infra,Data}Error
// composite literal.
func isErrorLit(lit *ast.CompositeLit) bool {
	sel, ok := lit.Type.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkgIdent, ok := sel.X.(*ast.Ident)
	return ok && pkgIdent.Name == "mcpserver" && errSuggestionTypeNames[sel.Sel.Name]
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
		if !ok || !isErrorLit(lit) {
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

// errSuggestionFuncs lists the functions in which every error literal must
// carry a real Suggestion. A whole-file check is not possible: execute_state.go
// and jira.go still hold many literals that rely on the default recovery text.
// These are the functions the Markdown-output branch filled in.
var errSuggestionFuncs = map[string][]string{
	"execute_state.go": {
		"execActionTaskContext",
		"execActionWaveSplit",
		"execActionWaveProgress",
		"execActionWaveDone",
		"execActionTaskDone",
		"execActionVerifyCompleteness",
	},
	"scaffold.go": {"ciScriptDrift", "scaffoldCI"},
	"jira.go":     {"jiraSave", "jiraClear"},
	"setup.go":    {"setupWritePlanTemplate"},
}

// TestFuncErrorLiteralsHaveRealSuggestions asserts that every
// mcpserver.*Error literal inside the functions in errSuggestionFuncs has a
// Suggestion with at least 40 characters of constant text that is not the
// generic boilerplate. It also fails when a listed function no longer exists
// or holds no error literal, so a rename cannot silently turn the check off.
func TestFuncErrorLiteralsHaveRealSuggestions(t *testing.T) {
	for file, funcs := range errSuggestionFuncs {
		t.Run(file, func(t *testing.T) {
			fset := token.NewFileSet()
			f, err := parser.ParseFile(fset, file, nil, 0)
			if err != nil {
				t.Fatalf("parse %s: %v", file, err)
			}
			found := map[string]int{}
			for _, decl := range f.Decls {
				fd, ok := decl.(*ast.FuncDecl)
				if !ok || !slices.Contains(funcs, fd.Name.Name) {
					continue
				}
				ast.Inspect(fd, func(n ast.Node) bool {
					lit, ok := n.(*ast.CompositeLit)
					if !ok || !isErrorLit(lit) {
						return true
					}
					found[fd.Name.Name]++
					where := fset.Position(lit.Pos())
					kind := lit.Type.(*ast.SelectorExpr).Sel.Name
					kv, present := suggestionLit(lit)
					if !present {
						t.Errorf("%s:%d (%s): %s is missing a Suggestion field", file, where.Line, fd.Name.Name, kind)
						return true
					}
					text, ok := suggestionText(kv.Value)
					if !ok {
						t.Errorf("%s:%d (%s): %s.Suggestion must be a string literal, a fmt.Sprintf with a literal format, or a \"+\" concatenation containing a literal", file, where.Line, fd.Name.Name, kind)
						return true
					}
					if text == errSuggestionBoilerplate {
						t.Errorf("%s:%d (%s): %s.Suggestion uses the generic boilerplate text", file, where.Line, fd.Name.Name, kind)
					}
					if len(text) < 40 {
						t.Errorf("%s:%d (%s): %s.Suggestion is shorter than 40 chars: %q", file, where.Line, fd.Name.Name, kind, text)
					}
					return true
				})
			}
			for _, name := range funcs {
				if found[name] == 0 {
					t.Errorf("%s: %s holds no error literal (renamed or removed?) — update errSuggestionFuncs", file, name)
				}
			}
		})
	}
}
