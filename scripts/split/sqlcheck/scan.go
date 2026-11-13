package main

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/constant"
	"go/format"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/tools/go/packages"
)

type options struct {
	Tests, Prose, Warn      bool
	Objects, Schema, Report string
}
type finding struct {
	File                   string
	Line                   int
	Decl, Text             string
	Names, Parts, Operands []string
	NameLines              []int
}
type scanResult struct {
	Sections    [7][]finding
	ProseCounts map[string]int
}

// Product-owned packages live under products/ after the D1-S4 move.
// A file named hack_*.go in any other package remains platform code.
var productPrefixes = []string{"products"}

func productPath(p string) bool {
	for _, v := range productPrefixes {
		if p == v || strings.HasPrefix(p, v+"/") {
			return true
		}
	}
	return false
}

var sqlUpperRE = regexp.MustCompile(`\b(?:SELECT|INSERT|UPDATE|DELETE|FROM|JOIN|WHERE|EXISTS|REFERENCES|RETURNING|INTO|USING|TRUNCATE|TABLE|TRIGGER|FUNCTION)\b`)
var sqlLowerRE = regexp.MustCompile(`(?is)\b(?:select\s.+\sfrom\s|insert\s+into\s|delete\s+from\s|update\s+\S+\s+set\s)`)

func looksSQL(s string) bool {
	if routeRE.MatchString(s) {
		return false
	}
	return sqlUpperRE.MatchString(s) || sqlLowerRE.MatchString(s)
}

var routeRE = regexp.MustCompile(`^(?:GET|POST|PUT|PATCH|DELETE|HEAD|OPTIONS)\s+/`)

var wordsRE = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]*`)

func tablePosition(s string, i int, n string) bool {
	end := i + len(n)
	if end < len(s) && s[end] == '"' {
		end++
	}
	if end < len(s) && s[end] == '.' {
		return true
	}
	before := strings.TrimSpace(strings.TrimSuffix(s[:i], `"`))
	before = regexp.MustCompile(`(?i)(?:"public"|public)\.$`).ReplaceAllString(before, "")
	words := wordsRE.FindAllString(before, -1)
	for j := len(words) - 1; j >= 0; j-- {
		w := strings.ToUpper(words[j])
		switch w {
		case "ONLY", "IF", "EXISTS", "NOT":
			continue
		case "FROM", "JOIN", "INTO", "UPDATE", "TABLE", "ON", "REFERENCES", "TRUNCATE":
			return true
		}
		break
	}
	if strings.HasSuffix(before, ",") {
		for j := len(words) - 1; j >= 0; j-- {
			switch strings.ToUpper(words[j]) {
			case "FROM":
				return true
			case "SELECT", "WHERE", "SET", "VALUES", "INSERT", "RETURNING", "ON":
				return false
			}
		}
	}
	return false
}
func constString(info *types.Info, e ast.Expr) (string, bool) {
	v, ok := info.Types[e]
	if !ok || v.Value == nil || v.Value.Kind() != constant.String {
		return "", false
	}
	return constant.StringVal(v.Value), true
}
func stringType(info *types.Info, e ast.Expr) bool {
	t := info.TypeOf(e)
	if t == nil {
		return false
	}
	b, ok := t.Underlying().(*types.Basic)
	return ok && b.Info()&types.IsString != 0
}
func source(fset *token.FileSet, n ast.Node) string {
	var b bytes.Buffer
	_ = format.Node(&b, fset, n)
	return b.String()
}
func rel(root, path string) string { r, _ := filepath.Rel(root, path); return filepath.ToSlash(r) }

type constDef struct {
	Expr ast.Expr
	Info *types.Info
	File string
	Line int
	Name string
}

func scan(root string, o options, objects []object, z schemaInfo) (scanResult, error) {
	result := scanResult{ProseCounts: map[string]int{}}
	cfg := &packages.Config{Dir: root, Tests: o.Tests, Mode: packages.NeedName | packages.NeedFiles | packages.NeedCompiledGoFiles | packages.NeedImports | packages.NeedTypes | packages.NeedSyntax | packages.NeedTypesInfo | packages.NeedEmbedPatterns | packages.NeedEmbedFiles | packages.NeedModule, Env: append(os.Environ(), "GOWORK=off", "GOFLAGS=-mod=mod")}
	pkgs, err := packages.Load(cfg, "./...")
	if err != nil {
		return result, err
	}
	platform := func(p *packages.Package) bool {
		if p.Module == nil || filepath.Clean(p.Module.Dir) != filepath.Clean(root) {
			return false
		}
		dir := ""
		if len(p.GoFiles) > 0 {
			dir = rel(root, filepath.Dir(p.GoFiles[0]))
		}
		return !productPath(dir) && dir != "scripts/split/sqlcheck"
	}
	defs := map[types.Object]constDef{}
	for _, p := range pkgs {
		if p.Module == nil || filepath.Clean(p.Module.Dir) != filepath.Clean(root) {
			continue
		}
		if platform(p) && len(p.Errors) > 0 {
			return result, fmt.Errorf("load platform package %s: %v", p.PkgPath, p.Errors)
		}
		for _, f := range p.Syntax {
			ast.Inspect(f, func(node ast.Node) bool {
				g, ok := node.(*ast.GenDecl)
				if !ok || g.Tok != token.CONST {
					return true
				}
				var previous []ast.Expr
				for _, spec := range g.Specs {
					v := spec.(*ast.ValueSpec)
					values := v.Values
					if len(values) == 0 {
						values = previous
					} else {
						previous = values
					}
					for i, name := range v.Names {
						if i < len(values) {
							pos := p.Fset.Position(name.Pos())
							defs[p.TypesInfo.Defs[name]] = constDef{Expr: values[i], Info: p.TypesInfo, File: rel(root, pos.Filename), Line: pos.Line, Name: name.Name}
						}
					}
				}
				return true
			})
		}
	}

	names := map[string]bool{}
	columns := map[string]bool{}
	for _, ob := range objects {
		if ob.Kind == "column" {
			columns[objectName(ob)] = true
		}
	}
	amb := map[string][]string{}
	for _, ob := range objects {
		n := objectName(ob)
		names[n] = true
		for _, c := range z.Columns[n] {
			table := strings.SplitN(c, ".", 2)[0]
			hack := false
			for _, x := range objects {
				hack = hack || (x.Kind == "table" && x.Name == table)
			}
			if !hack && !(ob.Kind == "column" && ob.Name == c) {
				amb[n] = append(amb[n], c)
			}
		}
		amb[n] = unique(amb[n])
	}
	parts := func(info *types.Info, e ast.Expr) []string {
		found := map[string]bool{}
		visited := map[types.Object]bool{}
		var walk func(*types.Info, ast.Expr)
		walk = func(in *types.Info, ex ast.Expr) {
			ast.Inspect(ex, func(n ast.Node) bool {
				id, ok := n.(*ast.Ident)
				if !ok {
					return true
				}
				ob := in.Uses[id]
				d, ok := defs[ob]
				if ok && !visited[ob] {
					visited[ob] = true
					found[fmt.Sprintf("%s:%d %s", d.File, d.Line, d.Name)] = true
					walk(d.Info, d.Expr)
				}
				return true
			})
		}
		walk(info, e)
		return sortedKeys(found)
	}
	add := func(f finding, runtime bool, list bool) {
		good, ambiguous := map[string]bool{}, map[string]bool{}
		matchText := f.Text
		if routeRE.MatchString(matchText) {
			matchText = strings.NewReplacer("/", " ", "-", " ").Replace(matchText)
		}
		for _, n := range sortedKeys(names) {
			for _, i := range occurrences(matchText, n, columns[n]) {
				if !columns[n] && len(amb[n]) > 0 && !tablePosition(f.Text, i, n) {
					for _, c := range amb[n] {
						ambiguous[c] = true
					}
				} else {
					good[n] = true
				}
			}
		}
		if list {
			result.Sections[5] = append(result.Sections[5], f)
			return
		}
		if len(good) == 0 && len(ambiguous) == 0 {
			if runtime && looksSQL(f.Text) {
				result.Sections[3] = append(result.Sections[3], f)
			}
			return
		}
		if looksSQL(f.Text) {
			if len(good) > 0 {
				f.Names = sortedKeys(good)
				sec := 0
				if runtime {
					sec = 1
				}
				result.Sections[sec] = append(result.Sections[sec], f)
			} else {
				f.Names = sortedKeys(ambiguous)
				result.Sections[4] = append(result.Sections[4], f)
			}
			return
		}
		for n := range ambiguous {
			good[strings.SplitN(n, ".", 2)[1]] = true
		}
		var listed []string
		for _, n := range sortedKeys(good) {
			if !strings.Contains(n, "_") && !o.Prose {
				result.ProseCounts[n]++
			} else {
				listed = append(listed, n)
			}
		}
		if len(listed) > 0 {
			f.Names = listed
			result.Sections[6] = append(result.Sections[6], f)
		}
	}
	seenFiles, seenEmbed := map[string]bool{}, map[string]bool{}
	for _, p := range pkgs {
		if !platform(p) {
			continue
		}
		for _, f := range p.Syntax {
			filename := p.Fset.Position(f.Pos()).Filename
			if seenFiles[filename] || (!o.Tests && strings.HasSuffix(filename, "_test.go")) {
				continue
			}
			seenFiles[filename] = true
			parents := map[ast.Node]ast.Node{}
			var stack []ast.Node
			ast.Inspect(f, func(n ast.Node) bool {
				if n == nil {
					stack = stack[:len(stack)-1]
					return false
				}
				if len(stack) > 0 {
					parents[n] = stack[len(stack)-1]
				}
				stack = append(stack, n)
				return true
			})
			decl := func(n ast.Node) string {
				for n != nil {
					switch d := n.(type) {
					case *ast.FuncDecl:
						if d.Recv != nil && len(d.Recv.List) > 0 {
							r := strings.TrimPrefix(source(p.Fset, d.Recv.List[0].Type), "*")
							return r + "." + d.Name.Name
						}
						return d.Name.Name
					case *ast.ValueSpec:
						if len(d.Names) > 0 {
							return d.Names[0].Name
						}
					case *ast.TypeSpec:
						return d.Name.Name
					}
					n = parents[n]
				}
				return "-"
			}
			makeFinding := func(e ast.Node, t string) finding {
				pos := p.Fset.Position(e.Pos())
				return finding{File: rel(root, pos.Filename), Line: pos.Line, Decl: decl(e), Text: t}
			}
			consumed := map[ast.Node]bool{}
			handledLists := map[ast.Node]bool{}
			// A fully constant Join is one string finding at the call, not a list.
			ast.Inspect(f, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok || len(call.Args) != 2 {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok || sel.Sel.Name != "Join" {
					return true
				}
				id, ok := sel.X.(*ast.Ident)
				if !ok {
					return true
				}
				pkg, ok := p.TypesInfo.Uses[id].(*types.PkgName)
				if !ok || pkg.Imported().Path() != "strings" {
					return true
				}
				lit, ok := call.Args[0].(*ast.CompositeLit)
				sep, constantSep := constString(p.TypesInfo, call.Args[1])
				if !ok || !stringSlice(p.TypesInfo.TypeOf(lit)) || !constantSep {
					return true
				}
				var texts []string
				for _, elt := range lit.Elts {
					text, ok := constString(p.TypesInfo, elt)
					if !ok {
						return true
					}
					texts = append(texts, text)
				}
				fi := makeFinding(call, strings.Join(texts, sep))
				fi.Parts = parts(p.TypesInfo, call)
				add(fi, false, false)
				ast.Inspect(call, func(n ast.Node) bool {
					if n != nil {
						consumed[n] = true
					}
					return true
				})
				return false
			})
			// Lists are classified first so their literals cannot leak into SQL/prose.
			ast.Inspect(f, func(n ast.Node) bool {
				lit, ok := n.(*ast.CompositeLit)
				if consumed[n] {
					return false
				}
				if !ok || handledLists[n] {
					return true
				}
				t := p.TypesInfo.TypeOf(lit)
				if t == nil {
					return true
				}
				var elems []ast.Expr
				switch u := t.Underlying().(type) {
				case *types.Map:
					if _, ok := u.Elem().Underlying().(*types.Interface); ok {
						for _, elt := range lit.Elts {
							if kv, ok := elt.(*ast.KeyValueExpr); ok {
								if key, ok := constString(p.TypesInfo, kv.Key); ok && names[strings.TrimSpace(key)] {
									consumed[kv.Key] = true
								}
							}
						}
						return true
					}
					if !isString(u.Key()) && !isString(u.Elem()) && !stringSlice(u.Elem()) {
						return true
					}
				case *types.Slice:
					if !isString(u.Elem()) {
						return true
					}
				case *types.Array:
					if !isString(u.Elem()) {
						return true
					}
				default:
					return true
				}
				emit := func(e ast.Expr, table string) {
					s, ok := constString(p.TypesInfo, e)
					if !ok {
						return
					}
					s = strings.TrimSpace(s)
					var found []string
					for _, ob := range objects {
						name := objectName(ob)
						match := s == name || ob.Kind == "table" && strings.HasPrefix(s, name+".") || ob.Kind == "column" && s == ob.Name
						if ob.Kind == "column" && table != "" && s == name && strings.HasPrefix(ob.Name, table+".") {
							match = true
							name = ob.Name
						}
						if match {
							if len(amb[name]) > 0 {
								name += " (also the platform column " + strings.Join(amb[name], ", ") + ")"
							}
							found = append(found, name)
						}
					}
					if len(found) > 0 {
						fi := makeFinding(e, s)
						fi.Names = unique(found)
						add(fi, false, true)
						ast.Inspect(e, func(n ast.Node) bool {
							if n != nil {
								consumed[n] = true
							}
							return true
						})
					}
				}
				for _, e := range lit.Elts {
					if kv, ok := e.(*ast.KeyValueExpr); ok {
						emit(kv.Key, "")
						emit(kv.Value, "")
						table, _ := constString(p.TypesInfo, kv.Key)
						if inner, ok := kv.Value.(*ast.CompositeLit); ok && stringList(p.TypesInfo.TypeOf(inner)) {
							for _, v := range inner.Elts {
								emit(v, table)
							}
							handledLists[inner] = true
						}
					} else {
						elems = append(elems, e)
					}
				}
				for _, e := range elems {
					emit(e, "")
				}
				return true
			})
			// Each function has its own builder writes, including nested function literals.
			ast.Inspect(f, func(n ast.Node) bool {
				switch n.(type) {
				case *ast.FuncDecl, *ast.FuncLit:
				default:
					return true
				}
				type writes struct {
					first    *ast.CallExpr
					text     string
					operands []string
				}
				type receiverKey struct {
					root   types.Object
					fields string
				}
				groups := map[receiverKey]*writes{}
				var key func(ast.Expr) receiverKey
				key = func(e ast.Expr) receiverKey {
					switch v := e.(type) {
					case *ast.Ident:
						return receiverKey{root: p.TypesInfo.ObjectOf(v)}
					case *ast.SelectorExpr:
						k := key(v.X)
						k.fields += "." + v.Sel.Name
						return k
					case *ast.ParenExpr:
						return key(v.X)
					case *ast.StarExpr:
						return key(v.X)
					}
					return receiverKey{}
				}
				receiver := func(e ast.Expr) receiverKey {
					if u, ok := e.(*ast.UnaryExpr); ok && u.Op == token.AND {
						e = u.X
					}
					t := p.TypesInfo.TypeOf(e)
					if t == nil {
						return receiverKey{}
					}
					if ptr, ok := t.(*types.Pointer); ok {
						t = ptr.Elem()
					}
					named, ok := t.(*types.Named)
					if !ok || named.Obj().Pkg() == nil {
						return receiverKey{}
					}
					path, name := named.Obj().Pkg().Path(), named.Obj().Name()
					if !(path == "strings" && name == "Builder" || path == "bytes" && name == "Buffer") {
						return receiverKey{}
					}
					return key(e)
				}
				ast.Inspect(n, func(inner ast.Node) bool {
					if inner != n {
						switch inner.(type) {
						case *ast.FuncDecl, *ast.FuncLit:
							return false
						}
					}
					call, ok := inner.(*ast.CallExpr)
					if !ok {
						return true
					}
					sel, ok := call.Fun.(*ast.SelectorExpr)
					if !ok {
						return true
					}
					var ob receiverKey
					var args []ast.Expr
					write := false
					if sel.Sel.Name == "WriteString" && len(call.Args) == 1 {
						ob = receiver(sel.X)
						args = call.Args
						write = true
					} else if sel.Sel.Name == "Fprintf" && len(call.Args) >= 2 {
						if id, ok := sel.X.(*ast.Ident); ok {
							if pkg, ok := p.TypesInfo.Uses[id].(*types.PkgName); ok && pkg.Imported().Path() == "fmt" {
								ob = receiver(call.Args[0])
								args = call.Args[1:]
							}
						}
					}
					if ob.root == nil {
						return true
					}
					g := groups[ob]
					if g == nil {
						g = &writes{}
						groups[ob] = g
					}
					for _, a := range args {
						if v := p.TypesInfo.Types[a]; v.Value == nil {
							g.operands = append(g.operands, truncate(source(p.Fset, a), 40))
						}
					}
					if write {
						if text, ok := constString(p.TypesInfo, args[0]); ok && looksSQL(text) && g.first == nil {
							g.first = call
							g.text = text
						}
					}
					return true
				})
				for _, g := range groups {
					if g.first != nil && len(g.operands) > 0 {
						fi := makeFinding(g.first, g.text)
						fi.Decl += " (strings.Builder)"
						fi.Operands = unique(g.operands)
						result.Sections[3] = append(result.Sections[3], fi)
					}
				}
				return true
			})
			// Capture runtime roots before constants; constant operands belong to that root.
			runtimeChains := map[ast.Node]bool{}
			ast.Inspect(f, func(n ast.Node) bool {
				if n == nil || consumed[n] {
					return n != nil && !consumed[n]
				}
				if runtimeChains[n] {
					return true
				}
				var e ast.Expr
				var text string
				var operands []string
				var known func(ast.Expr) string
				known = func(x ast.Expr) string {
					if s, ok := constString(p.TypesInfo, x); ok {
						consumed[x] = true
						return s
					}
					if b, ok := x.(*ast.BinaryExpr); ok && b.Op == token.ADD && stringType(p.TypesInfo, b) {
						runtimeChains[b] = true
						return known(b.X) + known(b.Y)
					}
					if par, ok := x.(*ast.ParenExpr); ok {
						runtimeChains[par] = true
						return known(par.X)
					}
					name := truncate(source(p.Fset, x), 40)
					operands = append(operands, name)
					return "‹?" + name + "›"
				}
				switch x := n.(type) {
				case *ast.BinaryExpr:
					if x.Op != token.ADD || !stringType(p.TypesInfo, x) {
						return true
					}
					if _, ok := constString(p.TypesInfo, x); ok {
						return true
					}
					e = x
					text = known(x)
				case *ast.AssignStmt:
					if x.Tok != token.ADD_ASSIGN || len(x.Lhs) != 1 || !stringType(p.TypesInfo, x.Lhs[0]) {
						return true
					}
					e = x.Rhs[0]
					text = known(x.Lhs[0]) + known(e)
				case *ast.CallExpr:
					sel, ok := x.Fun.(*ast.SelectorExpr)
					if !ok {
						return true
					}
					id, ok := sel.X.(*ast.Ident)
					if !ok {
						return true
					}
					pn, ok := p.TypesInfo.Uses[id].(*types.PkgName)
					if ok && pn.Imported().Path() == "strings" && sel.Sel.Name == "Join" && len(x.Args) == 2 {
						lit, ok := x.Args[0].(*ast.CompositeLit)
						sep, constantSep := constString(p.TypesInfo, x.Args[1])
						if !ok || !stringSlice(p.TypesInfo.TypeOf(lit)) || !constantSep {
							return true
						}
						dynamic := false
						for _, elt := range lit.Elts {
							if _, ok := constString(p.TypesInfo, elt); !ok {
								dynamic = true
							}
						}
						if !dynamic {
							return true
						}
						var texts []string
						for _, elt := range lit.Elts {
							texts = append(texts, known(elt))
						}
						e = x
						text = strings.Join(texts, sep)
						consumed[x.Args[1]] = true
						break
					}
					if !ok || pn.Imported().Path() != "fmt" {
						return true
					}
					idx := 0
					switch sel.Sel.Name {
					case "Errorf":
						ast.Inspect(x, func(n ast.Node) bool {
							if n != nil {
								consumed[n] = true
							}
							return true
						})
						return false
					case "Sprintf", "Sprint":
					case "Fprintf", "Appendf":
						idx = 1
					default:
						return true
					}
					if idx >= len(x.Args) {
						return true
					}
					s, ok := constString(p.TypesInfo, x.Args[idx])
					if !ok {
						return true
					}
					e = x
					consumed[x.Args[idx]] = true
					text = s
					for _, a := range x.Args[idx+1:] {
						if _, ok := constString(p.TypesInfo, a); !ok {
							operands = append(operands, truncate(source(p.Fset, a), 40))
						}
					}
				default:
					return true
				}
				fi := makeFinding(n, text)
				fi.Parts = parts(p.TypesInfo, e)
				fi.Operands = operands
				add(fi, true, false)
				return true
			})
			ast.Inspect(f, func(n ast.Node) bool {
				if n == nil || consumed[n] {
					return false
				}
				e, ok := n.(ast.Expr)
				if !ok {
					return true
				}
				s, ok := constString(p.TypesInfo, e)
				if !ok {
					return true
				}
				fi := makeFinding(e, s)
				fi.Parts = parts(p.TypesInfo, e)
				if lit, ok := e.(*ast.BasicLit); ok && strings.HasPrefix(lit.Value, "`") {
					lineSet := map[int]bool{}
					for name := range names {
						for _, i := range occurrences(s, name, columns[name]) {
							lineSet[fi.Line+strings.Count(s[:i], "\n")] = true
						}
					}
					later := false
					for line := range lineSet {
						later = later || line != fi.Line
						fi.NameLines = append(fi.NameLines, line)
					}
					sort.Ints(fi.NameLines)
					if !later {
						fi.NameLines = nil
					}
				}
				add(fi, false, false)
				return false
			})
		}
		for _, file := range p.EmbedFiles {
			path := rel(root, file)
			if seenEmbed[file] || strings.HasPrefix(path, "db/") || filepath.Ext(file) != ".sql" {
				continue
			}
			seenEmbed[file] = true
			b, e := os.ReadFile(file)
			if e != nil {
				return result, e
			}
			for _, st := range splitSQL(string(b)) {
				var ns []string
				for n := range names {
					if len(occurrences(st.Text, n, columns[n])) > 0 {
						ns = append(ns, n)
					}
				}
				if len(ns) > 0 {
					sort.Strings(ns)
					result.Sections[2] = append(result.Sections[2], finding{File: path, Line: st.Line, Decl: "go:embed", Text: st.Text, Names: ns})
				}
			}
		}
	}
	for i := range result.Sections {
		sort.SliceStable(result.Sections[i], func(a, b int) bool {
			x, y := result.Sections[i][a], result.Sections[i][b]
			if x.File != y.File {
				return x.File < y.File
			}
			if x.Line != y.Line {
				return x.Line < y.Line
			}
			if x.Decl != y.Decl {
				return x.Decl < y.Decl
			}
			return x.Text < y.Text
		})
	}
	return result, nil
}
func isString(t types.Type) bool {
	b, ok := t.Underlying().(*types.Basic)
	return ok && b.Info()&types.IsString != 0
}
func stringSlice(t types.Type) bool {
	s, ok := t.Underlying().(*types.Slice)
	return ok && isString(s.Elem())
}
func lineNumbers(a []int) string {
	var s []string
	for _, i := range a {
		s = append(s, strconv.Itoa(i))
	}
	return strings.Join(s, ", ")
}

func stringList(t types.Type) bool {
	if t == nil {
		return false
	}
	switch u := t.Underlying().(type) {
	case *types.Slice:
		return isString(u.Elem())
	case *types.Array:
		return isString(u.Elem())
	}
	return false
}
