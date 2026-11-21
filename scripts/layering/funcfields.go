package main

import (
	"bufio"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// The modules call each other with plain functions: a struct field of
// function type in host/access, host/sites, host/data or host/ops (their
// routes packages aside) is how one module reaches another through a hook,
// so each one must be named in funcFieldsFile with the reason it stays (a
// product extension point, an external provider, a test clock). A new one
// fails the check; one that is gone must leave the list.
const funcFieldsFile = "scripts/layering/funcfields.txt"

var funcFieldRoots = []string{"host/access", "host/sites", "host/data", "host/ops"}

func routesDir(path string) bool {
	return strings.HasSuffix(path, "http") || strings.Contains(path, "http/")
}

// funcFields lists "dir Struct.Field" for every func-typed struct field in
// the modules' non-test Go files, a named function type of the same package
// included.
func funcFields(dir string) ([]string, error) {
	var out []string
	for _, root := range funcFieldRoots {
		err := filepath.WalkDir(filepath.Join(dir, root), func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(dir, path)
			if d.IsDir() {
				if routesDir(rel) || d.Name() == "testdata" {
					return filepath.SkipDir
				}
				found, err := dirFuncFields(path, filepath.ToSlash(rel))
				out = append(out, found...)
				return err
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	sort.Strings(out)
	return out, nil
}

func dirFuncFields(path, rel string) ([]string, error) {
	pkgs, err := parser.ParseDir(token.NewFileSet(), path, func(fi os.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, p := range pkgs {
		funcTypes := map[string]bool{}
		for _, f := range p.Files {
			for _, decl := range f.Decls {
				if g, ok := decl.(*ast.GenDecl); ok {
					for _, spec := range g.Specs {
						if ts, ok := spec.(*ast.TypeSpec); ok {
							if _, ok := ts.Type.(*ast.FuncType); ok {
								funcTypes[ts.Name.Name] = true
							}
						}
					}
				}
			}
		}
		isFunc := func(t ast.Expr) bool {
			switch t := t.(type) {
			case *ast.FuncType:
				return true
			case *ast.Ident:
				return funcTypes[t.Name]
			}
			return false
		}
		for _, f := range p.Files {
			ast.Inspect(f, func(n ast.Node) bool {
				ts, ok := n.(*ast.TypeSpec)
				if !ok {
					return true
				}
				st, ok := ts.Type.(*ast.StructType)
				if !ok {
					return true
				}
				for _, field := range st.Fields.List {
					if !isFunc(field.Type) {
						continue
					}
					for _, name := range field.Names {
						out = append(out, rel+" "+ts.Name.Name+"."+name.Name)
					}
				}
				return true
			})
		}
	}
	return out, nil
}

// checkFuncFields compares the modules' func-typed fields with the list.
func checkFuncFields(dir string) error {
	found, err := funcFields(dir)
	if err != nil {
		return err
	}
	f, err := os.Open(filepath.Join(dir, funcFieldsFile))
	if err != nil {
		return err
	}
	defer f.Close()
	listed := map[string]bool{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line, _, _ := strings.Cut(sc.Text(), "#")
		if fields := strings.Fields(line); len(fields) == 2 {
			listed[fields[0]+" "+fields[1]] = true
		} else if len(fields) != 0 {
			return fmt.Errorf("%s: want \"<dir> <Struct.Field>  # why\": %q", funcFieldsFile, sc.Text())
		}
	}
	if err := sc.Err(); err != nil {
		return err
	}
	var issues []string
	for _, k := range found {
		if !listed[k] {
			issues = append(issues, "func-typed field "+k+": call the owning module's function directly instead (or, for a product hook or external provider, list it in "+funcFieldsFile+" with the reason)")
		}
		delete(listed, k)
	}
	for k := range listed {
		issues = append(issues, "func-typed field "+k+" is gone: remove it from "+funcFieldsFile)
	}
	if len(issues) > 0 {
		sort.Strings(issues)
		return fmt.Errorf("%s", strings.Join(issues, "\n"))
	}
	return nil
}
