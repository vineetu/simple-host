// Command edgecount measures how thin the HTTP edges are. For each routes
// package it counts the handlers (functions taking an http.ResponseWriter and
// an *http.Request) that contain SQL, that call more than one function of the
// platform modules (host/access, host/sites, host/data, host/ops and their
// non-HTTP subpackages), and that are over 40 lines, plus the package's
// sql.ErrNoRows references.
//
//	go run ./scripts/edgecount
package main

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"os"
	"regexp"
	"sort"
	"strings"

	"golang.org/x/tools/go/packages"
)

const module = "github.com/vsriram/simple-host/"

var routesPackages = []string{
	"host/access/accesshttp",
	"host/sites/siteshttp",
	"host/data/datahttp",
	"host/mcp",
	"products/hack/hackhttp",
}

var sqlText = regexp.MustCompile(`(?is)\b(?:SELECT\s+[\w\s,.*()=$']{1,300}?\bFROM\s+\w|INSERT\s+INTO\s+\w|UPDATE\s+\w+\s+SET\s+\w|DELETE\s+FROM\s+\w)`)

// moduleFunc reports whether obj is a function or method of a platform
// module package (not a routes package and not the shared HTTP helpers).
func moduleFunc(obj types.Object) bool {
	if obj == nil || obj.Pkg() == nil {
		return false
	}
	p := strings.TrimPrefix(obj.Pkg().Path(), module)
	if !(strings.HasPrefix(p, "host/access") || strings.HasPrefix(p, "host/sites") || strings.HasPrefix(p, "host/data") || strings.HasPrefix(p, "host/ops") || strings.HasPrefix(p, "products/hack/app")) {
		return false
	}
	// The credential middleware and its request-context getters (auth) are
	// transport, like reading a header.
	if p == "host/access/auth" {
		return false
	}
	for _, seg := range strings.Split(p, "/") {
		if strings.HasSuffix(seg, "http") {
			return false
		}
	}
	_, ok := obj.(*types.Func)
	return ok
}

func isHandler(info *types.Info, ft *ast.FuncType) bool {
	var w, r bool
	for _, f := range ft.Params.List {
		t := info.TypeOf(f.Type)
		if t == nil {
			continue
		}
		switch t.String() {
		case "net/http.ResponseWriter":
			w = true
		case "*net/http.Request":
			r = true
		}
	}
	return w && r
}

type counts struct{ handlers, thin, sql, multi, long, noRows int }

func main() {
	cfg := &packages.Config{Mode: packages.NeedName | packages.NeedSyntax | packages.NeedTypes | packages.NeedTypesInfo | packages.NeedFiles}
	var patterns []string
	for _, p := range routesPackages {
		patterns = append(patterns, "./"+p)
	}
	pkgs, err := packages.Load(cfg, patterns...)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	verbose := len(os.Args) > 1 && os.Args[1] == "-v"
	result := map[string]*counts{}
	for _, pkg := range pkgs {
		result[strings.TrimPrefix(pkg.PkgPath, module)] = countPackage(pkg, verbose)
	}
	names := make([]string, 0, len(result))
	for n := range result {
		names = append(names, n)
	}
	sort.Strings(names)
	fmt.Printf("%-26s %8s %5s %5s %7s %6s %9s\n", "package", "handlers", "thin", "sql", "calls>1", "len>40", "ErrNoRows")
	for _, n := range names {
		c := result[n]
		fmt.Printf("%-26s %8d %5d %5d %7d %6d %9d\n", n, c.handlers, c.thin, c.sql, c.multi, c.long, c.noRows)
	}
}

// countPackage counts the handlers and sql.ErrNoRows references in the
// non-test files of pkg; verbose prints each handler that is not thin.
func countPackage(pkg *packages.Package, verbose bool) *counts {
	c := &counts{}
	for _, file := range pkg.Syntax {
		fname := pkg.Fset.Position(file.Pos()).Filename
		if strings.HasSuffix(fname, "_test.go") {
			continue
		}
		ast.Inspect(file, func(n ast.Node) bool {
			if sel, ok := n.(*ast.SelectorExpr); ok && sel.Sel.Name == "ErrNoRows" {
				if id, ok := sel.X.(*ast.Ident); ok && id.Name == "sql" {
					c.noRows++
				}
			}
			fd, ok := n.(*ast.FuncDecl)
			if !ok || fd.Body == nil || !isHandler(pkg.TypesInfo, fd.Type) {
				return true
			}
			c.countHandler(pkg, fd, verbose)
			return true
		})
	}
	return c
}

// countHandler classes one handler as thin, or as having SQL, calling more
// than one module function, or running over 40 lines.
func (c *counts) countHandler(pkg *packages.Package, fd *ast.FuncDecl, verbose bool) {
	c.handlers++
	hasSQL, callees := handlerBody(pkg.TypesInfo, fd.Body)
	lines := pkg.Fset.Position(fd.End()).Line - pkg.Fset.Position(fd.Pos()).Line + 1
	extra := false
	if hasSQL {
		c.sql++
		extra = true
	}
	if callees > 1 {
		c.multi++
		extra = true
	}
	if lines > 40 {
		c.long++
		extra = true
	}
	if !extra {
		c.thin++
	} else if verbose {
		pos := pkg.Fset.Position(fd.Pos())
		fmt.Printf("  %s:%d %s sql=%v calls=%d lines=%d\n", strings.TrimPrefix(pos.Filename, mustWd()+"/"), pos.Line, fd.Name.Name, hasSQL, callees, lines)
	}
}

// handlerBody reports whether body holds SQL text or a database/sql call,
// and how many distinct module functions it calls.
func handlerBody(info *types.Info, body *ast.BlockStmt) (hasSQL bool, callees int) {
	called := map[types.Object]bool{}
	ast.Inspect(body, func(m ast.Node) bool {
		switch x := m.(type) {
		case *ast.BasicLit:
			if x.Kind == token.STRING && sqlText.MatchString(x.Value) {
				hasSQL = true
			}
		case *ast.CallExpr:
			var id *ast.Ident
			switch f := x.Fun.(type) {
			case *ast.Ident:
				id = f
			case *ast.SelectorExpr:
				id = f.Sel
			}
			if id != nil {
				obj := info.Uses[id]
				if moduleFunc(obj) {
					called[obj] = true
				}
				if fn, ok := obj.(*types.Func); ok && fn.Pkg() != nil && fn.Pkg().Path() == "database/sql" {
					hasSQL = true
				}
			}
		}
		return true
	})
	return hasSQL, len(called)
}

func mustWd() string {
	wd, _ := os.Getwd()
	return wd
}
