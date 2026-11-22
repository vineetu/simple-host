package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// Production opens are deliberately restricted to the admission-owned
// mechanics. Resolve the import alias, so renaming sqlite3 cannot bypass this.
func TestSQLiteOpenAllowList(t *testing.T) {
	root := filepath.Join("..", "..")
	allowed := map[string]bool{
		"host/storage/runtime_sqlite.go":        true,
		"host/storage/runtime_sql_prepare.go":   true,
		"host/storage/runtime_prepare_rules.go": true,
		"host/storage/export_sqlite.go":         true,
		"host/storage/runtime_kv.go":            true,
		"host/data/storetest/storetest.go":      true, // test fixtures make a site's database file
	}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" || d.Name() == "vendor" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			return err
		}
		aliases := map[string]bool{}
		for _, imp := range f.Imports {
			name, _ := strconv.Unquote(imp.Path.Value)
			if name != "github.com/ncruces/go-sqlite3" {
				continue
			}
			alias := "sqlite3"
			if imp.Name != nil {
				alias = imp.Name.Name
			}
			aliases[alias] = true
		}
		rel, _ := filepath.Rel(root, path)
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			var name string
			switch f := call.Fun.(type) {
			case *ast.SelectorExpr:
				if id, ok := f.X.(*ast.Ident); ok && aliases[id.Name] {
					name = f.Sel.Name
				}
			case *ast.Ident:
				if aliases["."] {
					name = f.Name
				}
			}
			if (name == "Open" || name == "OpenContext") && !allowed[filepath.ToSlash(rel)] {
				t.Errorf("SQLite open outside allow-list: %s", rel)
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
