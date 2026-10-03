package dedup_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

// TestAutoMergeCallSites — D-10, слой 4: автоматическое слияние в коде одно —
// вызов MergeEvents с "auto" в internal/dedup/pass.go внутри `if c.AutoMerge`;
// признак WouldAuto не стоит ни в одном условии на пути к MergeEvents.
func TestAutoMergeCallSites(t *testing.T) {
	root := filepath.Join("..", "..")
	type site struct {
		file    string
		guarded bool
		would   bool
	}
	var sites []site
	fset := token.NewFileSet()
	for _, dir := range []string{"internal", "cmd"} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return err
			}
			f, err := parser.ParseFile(fset, path, nil, 0)
			if err != nil {
				return err
			}
			var stack []ast.Node
			ast.Inspect(f, func(n ast.Node) bool {
				if n == nil {
					stack = stack[:len(stack)-1]
					return true
				}
				stack = append(stack, n)
				call, ok := n.(*ast.CallExpr)
				if !ok || !isMergeAuto(call) {
					return true
				}
				s := site{file: filepath.ToSlash(path)}
				for _, anc := range stack {
					ifs, ok := anc.(*ast.IfStmt)
					if !ok {
						continue
					}
					cond := exprText(ifs.Cond)
					if cond == "c.AutoMerge" {
						s.guarded = true
					}
					if strings.Contains(cond, "WouldAuto") {
						s.would = true
					}
				}
				sites = append(sites, s)
				return true
			})
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(sites) != 1 {
		t.Fatalf("вызовов MergeEvents(\"auto\") = %d (%+v), want 1", len(sites), sites)
	}
	s := sites[0]
	if !strings.HasSuffix(s.file, "internal/dedup/pass.go") || !s.guarded || s.would {
		t.Fatalf("вызов MergeEvents(\"auto\"): %+v; want internal/dedup/pass.go внутри if c.AutoMerge, без WouldAuto", s)
	}
}

// isMergeAuto — вызов <x>.MergeEvents(…, "auto", …).
func isMergeAuto(call *ast.CallExpr) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "MergeEvents" {
		return false
	}
	for _, a := range call.Args {
		if lit, ok := a.(*ast.BasicLit); ok && lit.Kind == token.STRING && lit.Value == `"auto"` {
			return true
		}
	}
	return false
}

// exprText — простое выражение условия как текст (идентификаторы и выборки).
func exprText(e ast.Expr) string {
	switch x := e.(type) {
	case *ast.Ident:
		return x.Name
	case *ast.SelectorExpr:
		return exprText(x.X) + "." + x.Sel.Name
	case *ast.BinaryExpr:
		return exprText(x.X) + " " + x.Op.String() + " " + exprText(x.Y)
	case *ast.UnaryExpr:
		return x.Op.String() + exprText(x.X)
	case *ast.ParenExpr:
		return "(" + exprText(x.X) + ")"
	case *ast.IndexExpr:
		return exprText(x.X) + "[" + exprText(x.Index) + "]"
	}
	return "?"
}
