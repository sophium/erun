package integration

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

// TestModelHoldsOnlyData keeps base/model to the data types modules exchange:
// types, their constants and methods, never free functions or package state.
func TestModelHoldsOnlyData(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(repoRoot(t), "erun-common", "base", "model")
	pkgs, err := parser.ParseDir(token.NewFileSet(), dir, func(fi fs.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, pkg := range pkgs {
		for name, file := range pkg.Files {
			for _, decl := range file.Decls {
				switch d := decl.(type) {
				case *ast.FuncDecl:
					if d.Recv == nil {
						t.Errorf("%s: func %s belongs in the package that uses it, not base/model", filepath.Base(name), d.Name.Name)
					}
				case *ast.GenDecl:
					if d.Tok == token.VAR {
						t.Errorf("%s: package var: base/model holds no state", filepath.Base(name))
					}
				}
			}
		}
	}
}
