package main

import (
	"bufio"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Table ownership (-owners): a table is read and written only by its owner
// package (scripts/split/table-owners.txt); other packages call that
// package's functions. The few cross-table queries kept elsewhere are listed
// with their reason in scripts/split/table-owner-exceptions.txt.

// A relation after FROM, JOIN, UPDATE, INTO, USING (DELETE ... USING) or
// TRUNCATE [TABLE], optionally ONLY and schema-qualified; ownerListRE
// continues a FROM's comma list (FROM a x, b y).
var ownerTableRE = regexp.MustCompile(`(?i)\b(?:FROM|JOIN|UPDATE|INTO|USING|TRUNCATE(?:\s+TABLE)?)\s+(?:ONLY\s+)?(?:"?public"?\.)?"?([a-z_][a-z0-9_]*)\b`)
var ownerListRE = regexp.MustCompile(`(?i)^(?:\s+(?:AS\s+)?[a-z_][a-z0-9_]*)?\s*,\s*(?:"?public"?\.)?"?([a-z_][a-z0-9_]*)\b`)

// tableUse is one SQL reference to a table in non-test Go code.
type tableUse struct {
	File, Pkg, Table string
	Line             int
}

type tableOwner struct{ Pkg, Door string }

type ownerException struct{ Path, Table, Reason string }

// tableUses lists every FROM/JOIN/UPDATE/INTO <table> in string literals of
// the non-test Go files under host, products and cmd, for tables in tables.
func tableUses(root string, tables map[string]bool) ([]tableUse, error) {
	var out []tableUse
	fset := token.NewFileSet()
	for _, top := range []string{"host", "products", "cmd"} {
		err := filepath.WalkDir(filepath.Join(root, top), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if d.Name() == "testdata" {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
			if err != nil {
				return err
			}
			file := rel(root, path)
			ast.Inspect(f, func(n ast.Node) bool {
				// pq.CopyIn("table", columns...) writes table.
				if call, ok := n.(*ast.CallExpr); ok && len(call.Args) > 0 {
					if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "CopyIn" {
						if lit, ok := call.Args[0].(*ast.BasicLit); ok && lit.Kind == token.STRING {
							if t, err := strconv.Unquote(lit.Value); err == nil && tables[t] {
								out = append(out, tableUse{File: file, Pkg: filepath.ToSlash(filepath.Dir(file)), Table: t, Line: fset.Position(lit.Pos()).Line})
							}
						}
					}
				}
				lit, ok := n.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					return true
				}
				s, err := strconv.Unquote(lit.Value)
				if err != nil || !looksSQL(s) {
					return true
				}
				line := fset.Position(lit.Pos()).Line
				use := func(t string, at int) {
					if t = strings.ToLower(t); tables[t] {
						out = append(out, tableUse{File: file, Pkg: filepath.ToSlash(filepath.Dir(file)), Table: t,
							Line: line + strings.Count(s[:at], "\n")})
					}
				}
				for _, m := range ownerTableRE.FindAllStringSubmatchIndex(s, -1) {
					use(s[m[2]:m[3]], m[0])
					if !strings.EqualFold(s[m[0]:m[0]+4], "FROM") {
						continue
					}
					for rest := m[1]; ; {
						l := ownerListRE.FindStringSubmatchIndex(s[rest:])
						if l == nil {
							break
						}
						use(s[rest+l[2]:rest+l[3]], rest)
						rest += l[1]
					}
				}
				return true
			})
			return nil
		})
		if err != nil && !os.IsNotExist(err) {
			return nil, err
		}
	}
	return out, nil
}

// readRows reads a "a | b | c" file, skipping blank and # lines.
func readRows(path string, n int) ([][]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var rows [][]string
	sc := bufio.NewScanner(f)
	for line := 1; sc.Scan(); line++ {
		t := strings.TrimSpace(sc.Text())
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		cols := strings.SplitN(t, "|", n)
		if len(cols) != n {
			return nil, fmt.Errorf("%s:%d: want %d columns separated by |", path, line, n)
		}
		for i := range cols {
			cols[i] = strings.TrimSpace(cols[i])
		}
		rows = append(rows, cols)
	}
	return rows, sc.Err()
}

func (e ownerException) covers(u tableUse) bool {
	return (e.Table == "*" || e.Table == u.Table) &&
		(u.File == e.Path || strings.HasPrefix(u.File, strings.TrimSuffix(e.Path, "/")+"/"))
}

// checkOwners fails on every query of an owned table outside its owner
// package that no exception covers. propose prints, instead, every table
// that exactly one package queries today (the rows to add to the owners
// file so those tables stay clean).
func checkOwners(root string, z schemaInfo, propose bool, out io.Writer) (int, error) {
	uses, err := tableUses(root, z.Tables)
	if err != nil {
		return 0, err
	}
	if propose {
		pkgs := map[string]map[string]bool{}
		for _, u := range uses {
			if pkgs[u.Table] == nil {
				pkgs[u.Table] = map[string]bool{}
			}
			pkgs[u.Table][u.Pkg] = true
		}
		for _, t := range sortedKeys(pkgs) {
			if len(pkgs[t]) == 1 {
				fmt.Fprintf(out, "%s | %s | its functions\n", t, sortedKeys(pkgs[t])[0])
			}
		}
		return 0, nil
	}
	rows, err := readRows(filepath.Join(root, "scripts/split/table-owners.txt"), 3)
	if err != nil {
		return 0, err
	}
	owners := map[string]tableOwner{}
	for _, r := range rows {
		owners[r[0]] = tableOwner{Pkg: r[1], Door: r[2]}
	}
	rows, err = readRows(filepath.Join(root, "scripts/split/table-owner-exceptions.txt"), 3)
	if err != nil {
		return 0, err
	}
	var exceptions []ownerException
	for _, r := range rows {
		exceptions = append(exceptions, ownerException{Path: r[0], Table: r[1], Reason: r[2]})
	}
	sort.Slice(uses, func(i, j int) bool {
		if uses[i].File != uses[j].File {
			return uses[i].File < uses[j].File
		}
		return uses[i].Line < uses[j].Line
	})
	bad := 0
	used := make([]bool, len(exceptions))
	for _, u := range uses {
		o, ok := owners[u.Table]
		if !ok || o.Pkg == u.Pkg {
			continue
		}
		covered := false
		for i, e := range exceptions {
			if e.covers(u) {
				covered, used[i] = true, true
			}
		}
		if !covered {
			bad++
			fmt.Fprintf(out, "%s:%d: queries %s, which only %s reads and writes; call %s instead\n", u.File, u.Line, u.Table, o.Pkg, o.Door)
		}
	}
	for i, e := range exceptions {
		if !used[i] {
			bad++
			fmt.Fprintf(out, "scripts/split/table-owner-exceptions.txt: %s | %s covers nothing any more; remove it\n", e.Path, e.Table)
		}
	}
	if bad > 0 {
		fmt.Fprintf(out, "sqlcheck -owners: %d queries of another package's table (scripts/split/table-owners.txt)\n", bad)
		return 1, nil
	}
	return 0, nil
}
