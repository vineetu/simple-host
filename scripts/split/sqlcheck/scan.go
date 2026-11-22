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

// The sole product-table query allowed in platform code: D3-S6a's read-only
// adoption evidence probe. refactor-board/NOTES.md, 2026-11-16 07:20 UTC.
var productProbeAllowlist = []finding{{File: "host/server/product.go", Decl: "productEvidence", Text: "SELECT EXISTS (SELECT 1 FROM public.events)"}}

func allowedProductProbe(f finding) bool {
	for _, allowed := range productProbeAllowlist {
		if f.File == allowed.File && f.Decl == allowed.Decl && f.Text == allowed.Text {
			return true
		}
	}
	return false
}

// scanner holds what every pass of one scan shares: the options, the product
// objects and their lookups, the constant definitions, and the result.
type scanner struct {
	root    string
	o       options
	objects []object
	defs    map[types.Object]constDef
	names   map[string]bool
	columns map[string]bool
	amb     map[string][]string
	result  scanResult
}

func scan(root string, o options, objects []object, z schemaInfo) (scanResult, error) {
	s := &scanner{root: root, o: o, objects: objects, result: scanResult{ProseCounts: map[string]int{}}}
	cfg := &packages.Config{Dir: root, Tests: o.Tests, Mode: packages.NeedName | packages.NeedFiles | packages.NeedCompiledGoFiles | packages.NeedImports | packages.NeedTypes | packages.NeedSyntax | packages.NeedTypesInfo | packages.NeedEmbedPatterns | packages.NeedEmbedFiles | packages.NeedModule, Env: append(os.Environ(), "GOWORK=off", "GOFLAGS=-mod=mod")}
	pkgs, err := packages.Load(cfg, "./...")
	if err != nil {
		return s.result, err
	}
	if err := s.collectConstants(pkgs); err != nil {
		return s.result, err
	}
	s.indexObjects(z)
	seenFiles, seenEmbed := map[string]bool{}, map[string]bool{}
	for _, p := range pkgs {
		if !s.platform(p) {
			continue
		}
		for _, f := range p.Syntax {
			filename := p.Fset.Position(f.Pos()).Filename
			if seenFiles[filename] || (!o.Tests && strings.HasSuffix(filename, "_test.go")) {
				continue
			}
			seenFiles[filename] = true
			s.scanFile(p, f)
		}
		if err := s.scanEmbeds(p, seenEmbed); err != nil {
			return s.result, err
		}
	}
	s.sortSections()
	return s.result, nil
}

// inModule reports whether p belongs to the scanned module itself.
func (s *scanner) inModule(p *packages.Package) bool {
	return p.Module != nil && filepath.Clean(p.Module.Dir) == filepath.Clean(s.root)
}

// platform reports whether p is platform code: in the module, not under a
// product directory, and not this checker.
func (s *scanner) platform(p *packages.Package) bool {
	if !s.inModule(p) {
		return false
	}
	dir := ""
	if len(p.GoFiles) > 0 {
		dir = rel(s.root, filepath.Dir(p.GoFiles[0]))
	}
	return !productPath(dir) && dir != "scripts/split/sqlcheck"
}

// collectConstants records every constant declared in the module so string
// findings can name the constants they were built from. A platform package
// that failed to load stops the scan.
func (s *scanner) collectConstants(pkgs []*packages.Package) error {
	s.defs = map[types.Object]constDef{}
	for _, p := range pkgs {
		if !s.inModule(p) {
			continue
		}
		if s.platform(p) && len(p.Errors) > 0 {
			return fmt.Errorf("load platform package %s: %v", p.PkgPath, p.Errors)
		}
		for _, f := range p.Syntax {
			ast.Inspect(f, func(node ast.Node) bool {
				if g, ok := node.(*ast.GenDecl); ok && g.Tok == token.CONST {
					s.recordConstDecl(p, g)
				}
				return true
			})
		}
	}
	return nil
}

// recordConstDecl records one const block, giving names without values the
// values of the spec before them, as iota-style blocks do.
func (s *scanner) recordConstDecl(p *packages.Package, g *ast.GenDecl) {
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
				s.defs[p.TypesInfo.Defs[name]] = constDef{Expr: values[i], Info: p.TypesInfo, File: rel(s.root, pos.Filename), Line: pos.Line, Name: name.Name}
			}
		}
	}
}

// indexObjects builds the name lookups: every product object name, which of
// them are columns, and for each name the platform columns that share it.
func (s *scanner) indexObjects(z schemaInfo) {
	s.names = map[string]bool{}
	s.columns = map[string]bool{}
	for _, ob := range s.objects {
		if ob.Kind == "column" {
			s.columns[objectName(ob)] = true
		}
	}
	s.amb = map[string][]string{}
	for _, ob := range s.objects {
		n := objectName(ob)
		s.names[n] = true
		for _, c := range z.Columns[n] {
			table := strings.SplitN(c, ".", 2)[0]
			if !s.productTable(table) && !(ob.Kind == "column" && ob.Name == c) {
				s.amb[n] = append(s.amb[n], c)
			}
		}
		s.amb[n] = unique(s.amb[n])
	}
}

// productTable reports whether table is one of the product objects.
func (s *scanner) productTable(table string) bool {
	hack := false
	for _, x := range s.objects {
		hack = hack || (x.Kind == "table" && x.Name == table)
	}
	return hack
}

// parts lists the constants, transitively, that expression e is built from.
func (s *scanner) parts(info *types.Info, e ast.Expr) []string {
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
			d, ok := s.defs[ob]
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

// matchNames finds the product names in f's text: good holds names in an
// unambiguous position, ambiguous the platform columns a bare shared name
// could also mean.
func (s *scanner) matchNames(f finding) (good, ambiguous map[string]bool) {
	good, ambiguous = map[string]bool{}, map[string]bool{}
	matchText := f.Text
	if routeRE.MatchString(matchText) {
		matchText = strings.NewReplacer("/", " ", "-", " ").Replace(matchText)
	}
	for _, n := range sortedKeys(s.names) {
		for _, i := range occurrences(matchText, n, s.columns[n]) {
			if !s.columns[n] && len(s.amb[n]) > 0 && !tablePosition(f.Text, i, n) {
				for _, c := range s.amb[n] {
					ambiguous[c] = true
				}
			} else {
				good[n] = true
			}
		}
	}
	return good, ambiguous
}

// add files f in its report section: lists, product SQL (static or runtime),
// unmatched runtime SQL, ambiguous SQL, or other text naming product objects.
func (s *scanner) add(f finding, runtime bool, list bool) {
	if allowedProductProbe(f) {
		return
	}
	good, ambiguous := s.matchNames(f)
	if list {
		s.result.Sections[5] = append(s.result.Sections[5], f)
		return
	}
	if len(good) == 0 && len(ambiguous) == 0 {
		if runtime && looksSQL(f.Text) {
			s.result.Sections[3] = append(s.result.Sections[3], f)
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
			s.result.Sections[sec] = append(s.result.Sections[sec], f)
		} else {
			f.Names = sortedKeys(ambiguous)
			s.result.Sections[4] = append(s.result.Sections[4], f)
		}
		return
	}
	s.addText(f, good, ambiguous)
}

// addText files non-SQL text: single ordinary words only count as prose
// unless -prose is set, and the remaining names are listed in section 6.
func (s *scanner) addText(f finding, good, ambiguous map[string]bool) {
	for n := range ambiguous {
		good[strings.SplitN(n, ".", 2)[1]] = true
	}
	var listed []string
	for _, n := range sortedKeys(good) {
		if !strings.Contains(n, "_") && !s.o.Prose {
			s.result.ProseCounts[n]++
		} else {
			listed = append(listed, n)
		}
	}
	if len(listed) > 0 {
		f.Names = listed
		s.result.Sections[6] = append(s.result.Sections[6], f)
	}
}

// scanEmbeds reports product names in the statements of p's embedded SQL
// files outside host/db.
func (s *scanner) scanEmbeds(p *packages.Package, seenEmbed map[string]bool) error {
	for _, file := range p.EmbedFiles {
		path := rel(s.root, file)
		if seenEmbed[file] || strings.HasPrefix(path, "host/db/") || filepath.Ext(file) != ".sql" {
			continue
		}
		seenEmbed[file] = true
		b, e := os.ReadFile(file)
		if e != nil {
			return e
		}
		for _, st := range splitSQL(string(b)) {
			var ns []string
			for n := range s.names {
				if len(occurrences(st.Text, n, s.columns[n])) > 0 {
					ns = append(ns, n)
				}
			}
			if len(ns) > 0 {
				sort.Strings(ns)
				s.result.Sections[2] = append(s.result.Sections[2], finding{File: path, Line: st.Line, Decl: "go:embed", Text: st.Text, Names: ns})
			}
		}
	}
	return nil
}

// sortSections orders every section by file, line, declaration and text.
func (s *scanner) sortSections() {
	for i := range s.result.Sections {
		sort.SliceStable(s.result.Sections[i], func(a, b int) bool {
			x, y := s.result.Sections[i][a], s.result.Sections[i][b]
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
}

// fileScan is the state of one Go file's passes: its parent links and the
// nodes earlier passes have already claimed.
type fileScan struct {
	*scanner
	p             *packages.Package
	f             *ast.File
	parents       map[ast.Node]ast.Node
	consumed      map[ast.Node]bool
	handledLists  map[ast.Node]bool
	runtimeChains map[ast.Node]bool
}

// scanFile runs the passes over one file in their fixed order; each pass
// claims nodes so later passes do not report them again.
func (s *scanner) scanFile(p *packages.Package, f *ast.File) {
	fs := &fileScan{scanner: s, p: p, f: f, parents: map[ast.Node]ast.Node{}, consumed: map[ast.Node]bool{}, handledLists: map[ast.Node]bool{}, runtimeChains: map[ast.Node]bool{}}
	var stack []ast.Node
	ast.Inspect(f, func(n ast.Node) bool {
		if n == nil {
			stack = stack[:len(stack)-1]
			return false
		}
		if len(stack) > 0 {
			fs.parents[n] = stack[len(stack)-1]
		}
		stack = append(stack, n)
		return true
	})
	// A fully constant Join is one string finding at the call, not a list.
	ast.Inspect(f, fs.constantJoin)
	// Lists are classified first so their literals cannot leak into SQL/prose.
	ast.Inspect(f, fs.listLiteral)
	// Each function has its own builder writes, including nested function literals.
	ast.Inspect(f, fs.builderWrites)
	// Capture runtime roots before constants; constant operands belong to that root.
	ast.Inspect(f, fs.runtimeString)
	ast.Inspect(f, fs.constantString)
}

// decl names the declaration enclosing n: Recv.Method, a function, a value
// or a type, or "-" at file level.
func (fs *fileScan) decl(n ast.Node) string {
	for n != nil {
		switch d := n.(type) {
		case *ast.FuncDecl:
			if d.Recv != nil && len(d.Recv.List) > 0 {
				r := strings.TrimPrefix(source(fs.p.Fset, d.Recv.List[0].Type), "*")
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
		n = fs.parents[n]
	}
	return "-"
}

func (fs *fileScan) makeFinding(e ast.Node, t string) finding {
	pos := fs.p.Fset.Position(e.Pos())
	return finding{File: rel(fs.root, pos.Filename), Line: pos.Line, Decl: fs.decl(e), Text: t}
}

// consumeAll claims n and every node under it.
func (fs *fileScan) consumeAll(n ast.Node) {
	ast.Inspect(n, func(n ast.Node) bool {
		if n != nil {
			fs.consumed[n] = true
		}
		return true
	})
}

// stringsJoin reports whether call is strings.Join(literal, constant
// separator) over a string slice literal, returning the literal and separator.
func (fs *fileScan) stringsJoin(call *ast.CallExpr) (*ast.CompositeLit, string, bool) {
	lit, ok := call.Args[0].(*ast.CompositeLit)
	sep, constantSep := constString(fs.p.TypesInfo, call.Args[1])
	if !ok || !stringSlice(fs.p.TypesInfo.TypeOf(lit)) || !constantSep {
		return nil, "", false
	}
	return lit, sep, true
}

// constantJoin reports a strings.Join of only constants as one finding and
// claims the call.
func (fs *fileScan) constantJoin(n ast.Node) bool {
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
	pkg, ok := fs.p.TypesInfo.Uses[id].(*types.PkgName)
	if !ok || pkg.Imported().Path() != "strings" {
		return true
	}
	lit, sep, ok := fs.stringsJoin(call)
	if !ok {
		return true
	}
	var texts []string
	for _, elt := range lit.Elts {
		text, ok := constString(fs.p.TypesInfo, elt)
		if !ok {
			return true
		}
		texts = append(texts, text)
	}
	fi := fs.makeFinding(call, strings.Join(texts, sep))
	fi.Parts = fs.parts(fs.p.TypesInfo, call)
	fs.add(fi, false, false)
	fs.consumeAll(call)
	return false
}

// listLiteral reports the product names held in a string list or map
// literal.
func (fs *fileScan) listLiteral(n ast.Node) bool {
	lit, ok := n.(*ast.CompositeLit)
	if fs.consumed[n] {
		return false
	}
	if !ok || fs.handledLists[n] {
		return true
	}
	t := fs.p.TypesInfo.TypeOf(lit)
	if t == nil {
		return true
	}
	if fs.stringListLiteral(lit, t) {
		fs.emitListElements(lit)
	}
	return true
}

// stringListLiteral reports whether lit of type t holds strings worth listing.
// A map to interface values is not a list, but its product-name keys are
// claimed so they are not reported as prose.
func (fs *fileScan) stringListLiteral(lit *ast.CompositeLit, t types.Type) bool {
	switch u := t.Underlying().(type) {
	case *types.Map:
		if _, ok := u.Elem().Underlying().(*types.Interface); ok {
			fs.consumeNameKeys(lit)
			return false
		}
		return isString(u.Key()) || isString(u.Elem()) || stringSlice(u.Elem())
	case *types.Slice:
		return isString(u.Elem())
	case *types.Array:
		return isString(u.Elem())
	}
	return false
}

// consumeNameKeys claims the constant keys of lit that are product names.
func (fs *fileScan) consumeNameKeys(lit *ast.CompositeLit) {
	for _, elt := range lit.Elts {
		if kv, ok := elt.(*ast.KeyValueExpr); ok {
			if key, ok := constString(fs.p.TypesInfo, kv.Key); ok && fs.names[strings.TrimSpace(key)] {
				fs.consumed[kv.Key] = true
			}
		}
	}
}

// emitListElements reports each key, value and plain element of lit; a key's
// inner string list is reported with that key as its table.
func (fs *fileScan) emitListElements(lit *ast.CompositeLit) {
	var elems []ast.Expr
	for _, e := range lit.Elts {
		if kv, ok := e.(*ast.KeyValueExpr); ok {
			fs.emitListEntry(kv.Key, "")
			fs.emitListEntry(kv.Value, "")
			table, _ := constString(fs.p.TypesInfo, kv.Key)
			if inner, ok := kv.Value.(*ast.CompositeLit); ok && stringList(fs.p.TypesInfo.TypeOf(inner)) {
				for _, v := range inner.Elts {
					fs.emitListEntry(v, table)
				}
				fs.handledLists[inner] = true
			}
		} else {
			elems = append(elems, e)
		}
	}
	for _, e := range elems {
		fs.emitListEntry(e, "")
	}
}

// emitListEntry reports a constant list entry that names product objects,
// where table, if set, qualifies a bare column name, and claims it.
func (fs *fileScan) emitListEntry(e ast.Expr, table string) {
	s, ok := constString(fs.p.TypesInfo, e)
	if !ok {
		return
	}
	s = strings.TrimSpace(s)
	var found []string
	for _, ob := range fs.objects {
		name := objectName(ob)
		match := s == name || ob.Kind == "table" && strings.HasPrefix(s, name+".") || ob.Kind == "column" && s == ob.Name
		if ob.Kind == "column" && table != "" && s == name && strings.HasPrefix(ob.Name, table+".") {
			match = true
			name = ob.Name
		}
		if match {
			if len(fs.amb[name]) > 0 {
				name += " (also the platform column " + strings.Join(fs.amb[name], ", ") + ")"
			}
			found = append(found, name)
		}
	}
	if len(found) > 0 {
		fi := fs.makeFinding(e, s)
		fi.Names = unique(found)
		fs.add(fi, false, true)
		fs.consumeAll(e)
	}
}

// builderGroup is one builder's writes within a function: its first SQL
// WriteString and the runtime operands written to it.
type builderGroup struct {
	first    *ast.CallExpr
	text     string
	operands []string
}

// receiverKey identifies a builder variable or field path.
type receiverKey struct {
	root   types.Object
	fields string
}

// builderWrites reports, per function, each strings.Builder or bytes.Buffer
// that receives SQL text together with runtime operands.
func (fs *fileScan) builderWrites(n ast.Node) bool {
	switch n.(type) {
	case *ast.FuncDecl, *ast.FuncLit:
	default:
		return true
	}
	groups := map[receiverKey]*builderGroup{}
	ast.Inspect(n, func(inner ast.Node) bool {
		if inner != n {
			switch inner.(type) {
			case *ast.FuncDecl, *ast.FuncLit:
				return false
			}
		}
		if call, ok := inner.(*ast.CallExpr); ok {
			fs.recordBuilderWrite(groups, call)
		}
		return true
	})
	for _, g := range groups {
		if g.first != nil && len(g.operands) > 0 {
			fi := fs.makeFinding(g.first, g.text)
			fi.Decl += " (strings.Builder)"
			fi.Operands = unique(g.operands)
			fs.result.Sections[3] = append(fs.result.Sections[3], fi)
		}
	}
	return true
}

// builderCall reports whether call writes to a builder (WriteString or
// fmt.Fprintf), returning the builder, the written arguments and whether it
// is a WriteString.
func (fs *fileScan) builderCall(call *ast.CallExpr) (receiverKey, []ast.Expr, bool) {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return receiverKey{}, nil, false
	}
	if sel.Sel.Name == "WriteString" && len(call.Args) == 1 {
		return fs.builderReceiver(sel.X), call.Args, true
	}
	if sel.Sel.Name == "Fprintf" && len(call.Args) >= 2 {
		if id, ok := sel.X.(*ast.Ident); ok {
			if pkg, ok := fs.p.TypesInfo.Uses[id].(*types.PkgName); ok && pkg.Imported().Path() == "fmt" {
				return fs.builderReceiver(call.Args[0]), call.Args[1:], false
			}
		}
	}
	return receiverKey{}, nil, false
}

// recordBuilderWrite adds call's runtime operands and first SQL text to its
// builder's group.
func (fs *fileScan) recordBuilderWrite(groups map[receiverKey]*builderGroup, call *ast.CallExpr) {
	ob, args, write := fs.builderCall(call)
	if ob.root == nil {
		return
	}
	g := groups[ob]
	if g == nil {
		g = &builderGroup{}
		groups[ob] = g
	}
	for _, a := range args {
		if v := fs.p.TypesInfo.Types[a]; v.Value == nil {
			g.operands = append(g.operands, truncate(source(fs.p.Fset, a), 40))
		}
	}
	if write {
		if text, ok := constString(fs.p.TypesInfo, args[0]); ok && looksSQL(text) && g.first == nil {
			g.first = call
			g.text = text
		}
	}
}

// builderKey is the variable and field path of e.
func (fs *fileScan) builderKey(e ast.Expr) receiverKey {
	switch v := e.(type) {
	case *ast.Ident:
		return receiverKey{root: fs.p.TypesInfo.ObjectOf(v)}
	case *ast.SelectorExpr:
		k := fs.builderKey(v.X)
		k.fields += "." + v.Sel.Name
		return k
	case *ast.ParenExpr:
		return fs.builderKey(v.X)
	case *ast.StarExpr:
		return fs.builderKey(v.X)
	}
	return receiverKey{}
}

// builderReceiver is e's key when e is a strings.Builder or bytes.Buffer, and
// the zero key otherwise.
func (fs *fileScan) builderReceiver(e ast.Expr) receiverKey {
	if u, ok := e.(*ast.UnaryExpr); ok && u.Op == token.AND {
		e = u.X
	}
	t := fs.p.TypesInfo.TypeOf(e)
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
	return fs.builderKey(e)
}

// known renders x with constants inlined and other operands as ‹?name›,
// appending those operands; it claims the constants and marks the
// concatenation chain so its inner nodes are not reported again.
func (fs *fileScan) known(x ast.Expr, operands *[]string) string {
	if s, ok := constString(fs.p.TypesInfo, x); ok {
		fs.consumed[x] = true
		return s
	}
	if b, ok := x.(*ast.BinaryExpr); ok && b.Op == token.ADD && stringType(fs.p.TypesInfo, b) {
		fs.runtimeChains[b] = true
		return fs.known(b.X, operands) + fs.known(b.Y, operands)
	}
	if par, ok := x.(*ast.ParenExpr); ok {
		fs.runtimeChains[par] = true
		return fs.known(par.X, operands)
	}
	name := truncate(source(fs.p.Fset, x), 40)
	*operands = append(*operands, name)
	return "‹?" + name + "›"
}

// runtimeString reports strings built at run time: concatenations, +=,
// strings.Join with runtime elements, and fmt formatting.
func (fs *fileScan) runtimeString(n ast.Node) bool {
	if n == nil || fs.consumed[n] {
		return false
	}
	if fs.runtimeChains[n] {
		return true
	}
	var e ast.Expr
	var text string
	var operands []string
	switch x := n.(type) {
	case *ast.BinaryExpr:
		if x.Op != token.ADD || !stringType(fs.p.TypesInfo, x) {
			return true
		}
		if _, ok := constString(fs.p.TypesInfo, x); ok {
			return true
		}
		e = x
		text = fs.known(x, &operands)
	case *ast.AssignStmt:
		if x.Tok != token.ADD_ASSIGN || len(x.Lhs) != 1 || !stringType(fs.p.TypesInfo, x.Lhs[0]) {
			return true
		}
		e = x.Rhs[0]
		text = fs.known(x.Lhs[0], &operands) + fs.known(e, &operands)
	case *ast.CallExpr:
		var found, descend bool
		text, found, descend = fs.runtimeCall(x, &operands)
		if !found {
			return descend
		}
		e = x
	default:
		return true
	}
	fi := fs.makeFinding(n, text)
	fi.Parts = fs.parts(fs.p.TypesInfo, e)
	fi.Operands = operands
	fs.add(fi, true, false)
	return true
}

// runtimeCall renders a strings.Join or fmt call built at run time. found
// says whether it is one; descend, when not, whether to look inside it.
func (fs *fileScan) runtimeCall(x *ast.CallExpr, operands *[]string) (text string, found, descend bool) {
	sel, ok := x.Fun.(*ast.SelectorExpr)
	if !ok {
		return "", false, true
	}
	id, ok := sel.X.(*ast.Ident)
	if !ok {
		return "", false, true
	}
	pn, ok := fs.p.TypesInfo.Uses[id].(*types.PkgName)
	if ok && pn.Imported().Path() == "strings" && sel.Sel.Name == "Join" && len(x.Args) == 2 {
		return fs.dynamicJoin(x, operands)
	}
	if !ok || pn.Imported().Path() != "fmt" {
		return "", false, true
	}
	return fs.formatCall(x, sel.Sel.Name, operands)
}

// dynamicJoin renders a strings.Join whose literal has at least one runtime
// element; a fully constant Join was already reported by constantJoin.
func (fs *fileScan) dynamicJoin(x *ast.CallExpr, operands *[]string) (string, bool, bool) {
	lit, sep, ok := fs.stringsJoin(x)
	if !ok {
		return "", false, true
	}
	dynamic := false
	for _, elt := range lit.Elts {
		if _, ok := constString(fs.p.TypesInfo, elt); !ok {
			dynamic = true
		}
	}
	if !dynamic {
		return "", false, true
	}
	var texts []string
	for _, elt := range lit.Elts {
		texts = append(texts, fs.known(elt, operands))
	}
	fs.consumed[x.Args[1]] = true
	return strings.Join(texts, sep), true, true
}

// formatCall renders an fmt Sprintf, Sprint, Fprintf or Appendf with a
// constant format; fmt.Errorf is claimed whole and never reported.
func (fs *fileScan) formatCall(x *ast.CallExpr, fn string, operands *[]string) (string, bool, bool) {
	idx := 0
	switch fn {
	case "Errorf":
		fs.consumeAll(x)
		return "", false, false
	case "Sprintf", "Sprint":
	case "Fprintf", "Appendf":
		idx = 1
	default:
		return "", false, true
	}
	if idx >= len(x.Args) {
		return "", false, true
	}
	s, ok := constString(fs.p.TypesInfo, x.Args[idx])
	if !ok {
		return "", false, true
	}
	fs.consumed[x.Args[idx]] = true
	for _, a := range x.Args[idx+1:] {
		if _, ok := constString(fs.p.TypesInfo, a); !ok {
			*operands = append(*operands, truncate(source(fs.p.Fset, a), 40))
		}
	}
	return s, true, true
}

// constantString reports each remaining unclaimed constant string.
func (fs *fileScan) constantString(n ast.Node) bool {
	if n == nil || fs.consumed[n] {
		return false
	}
	e, ok := n.(ast.Expr)
	if !ok {
		return true
	}
	s, ok := constString(fs.p.TypesInfo, e)
	if !ok {
		return true
	}
	fi := fs.makeFinding(e, s)
	fi.Parts = fs.parts(fs.p.TypesInfo, e)
	if lit, ok := e.(*ast.BasicLit); ok && strings.HasPrefix(lit.Value, "`") {
		fi.NameLines = fs.nameLines(fi.Line, s)
	}
	fs.add(fi, false, false)
	return false
}

// nameLines lists the lines of a raw string starting at line that name a
// product object, or nil when they all sit on the first line.
func (fs *fileScan) nameLines(line int, s string) []int {
	lineSet := map[int]bool{}
	for name := range fs.names {
		for _, i := range occurrences(s, name, fs.columns[name]) {
			lineSet[line+strings.Count(s[:i], "\n")] = true
		}
	}
	later := false
	var lines []int
	for l := range lineSet {
		later = later || l != line
		lines = append(lines, l)
	}
	sort.Ints(lines)
	if !later {
		return nil
	}
	return lines
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
