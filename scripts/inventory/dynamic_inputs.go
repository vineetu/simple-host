package main

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
)

// Check the finite inputs behind the hand-expanded table as well as the call
// expressions. Expected values come from the table, not a second route list.
type dynamicInput struct {
	File, Declaration, Kind string
	Values                  []string
	Registration            int    // 1-based dynamic registration within Declaration; zero for named inputs.
	RangeExpr               string // For loops over named inputs rather than literal lists.
}

func patternInputs(sites []dynamicSite, prefix, suffix string) []string {
	var values []string
	for _, site := range sites {
		for _, pattern := range site.Patterns {
			values = append(values, strings.TrimSuffix(strings.TrimPrefix(pattern, prefix), suffix))
		}
	}
	return values
}
func validateDynamicInputs(root string) error {
	ui := dynamicSites["internal/handler/ui.go:RegisterUIRoutes"]
	connector := dynamicSites["internal/handler/connector.go:Register"]
	hackVersions := dynamicSites["products/hack/hackhttp/hack_get_started.go:RegisterHackGetStarted"]
	ownFiles := dynamicSites["products/hack/asset_files.go:RegisterOwnFiles"]
	hackFilm := dynamicSites["products/hack/hackhttp/hack_film.go:RegisterHackFilm"]
	inputs := []dynamicInput{
		{"internal/server/build.go", "Build", "range", nil, 1, "opts.Redirects"},
		{"cmd/simple-host/main.go", "compatibilityRedirects", "keys", patternInputs(dynamicSites["internal/server/build.go:Build"], "", ""), 0, ""},
		{"internal/handler/instancehost.go", "rewrittenAssets", "values", patternInputs(ui[:2], "GET /", ""), 0, ""},
		{"internal/handler/ui.go", "RegisterUIRoutes", "range", nil, 1, "rewrittenAssets"},
		{"internal/handler/ui.go", "RegisterUIRoutes", "range", patternInputs(ui[1:2], "GET /", ""), 2, ""},
		{"internal/handler/ui.go", "RegisterUIRoutes", "range", nil, 3, "iconFiles()"},
		{"internal/handler/ui.go", "iconFiles", "keys", patternInputs(ui[2:3], "GET /", ""), 0, ""},
		{"internal/handler/connector.go", "museClientPath", "values", patternInputs(connector[:1], "GET ", ""), 0, ""},
		{"internal/handler/connector.go", "Register", "range", patternInputs(connector[1:], "", " /mcp"), 2, ""},
		{"products/hack/hackhttp/hack_ui.go", "RegisterHackUI", "range", patternInputs(dynamicSites["products/hack/hackhttp/hack_ui.go:RegisterHackUI"], "", ""), 1, ""},
		{"products/hack/hackhttp/hack_get_started.go", "RegisterHackGetStarted", "range", patternInputs(hackVersions[:1], "GET /simple-hack-skills-only-", ".zip"), 1, ""},
		{"products/hack/hackhttp/hack_get_started.go", "RegisterHackGetStarted", "range", patternInputs(hackVersions[1:], "GET /simple-hack-skills-only-", ".zip"), 2, ""},
		{"products/hack/asset_files.go", "RegisterOwnFiles", "range", nil, 1, "servedOwnFiles()"},
		{"products/hack/asset_files.go", "servedOwnFiles", "keys", patternInputs(ownFiles[:1], "GET /", ""), 0, ""},
		{"products/hack/asset_files.go", "RegisterOwnFiles", "range", nil, 2, "ownFolders"},
		{"products/hack/asset_files.go", "ownFolders", "values", patternInputs(ownFiles[1:], "GET /", "/{$}"), 0, ""},
		{"products/hack/hackhttp/hack_film.go", "RegisterHackFilm", "range", nil, 1, "hackFilmTypes()"},
		{"products/hack/hackhttp/hack_film.go", "hackFilmTypes", "keys", patternInputs(hackFilm[:1], "GET /film/", ""), 0, ""},
		{"products/hack/hackhttp/hack_film.go", "RegisterHackFilm", "range", nil, 2, "hackCompaniesFilmTypes()"},
		{"products/hack/hackhttp/hack_film.go", "hackCompaniesFilmTypes", "keys", patternInputs(hackFilm[1:], "GET /film/", ""), 0, ""},
		{"products/hack/hackhttp/hack_assets.go", "HackSkillNames", "values", []string{"run-hackathon", "join-hackathon", "judge-hackathon", "website-deploy", "website-deploy-builder"}, 0, ""},
	}
	for registration := 4; registration <= 7; registration++ {
		inputs = append(inputs, dynamicInput{"internal/handler/ui.go", "RegisterUIRoutes", "range", nil, registration, "assets.bundledSkillDirs()"})
	}
	for _, input := range inputs {
		if err := validateDynamicInput(root, input); err != nil {
			return err
		}
	}
	// bundledSkillDirs reads these embedded directories rather than a Go list.
	for product, bundle := range map[string]string{"host": "internal/webassets/simple-host-website/skills", "hack": "products/hack/webassets/hack-toolkit/skills"} {
		var dirs []string
		entries, err := os.ReadDir(filepath.Join(root, bundle))
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			if _, err := os.Stat(filepath.Join(root, bundle, entry.Name(), "SKILL.md")); err == nil {
				dirs = append(dirs, entry.Name())
			}
		}
		if !reflect.DeepEqual(uniqueStrings(dirs), uniqueStrings(productSkills[product])) {
			return fmt.Errorf("dynamic inputs changed: %s bundled skill directories; review scripts/inventory/dynamic.go", product)
		}
	}
	return nil
}
func validateDynamicInput(root string, input dynamicInput) error {
	tree, err := parser.ParseFile(token.NewFileSet(), filepath.Join(root, input.File), nil, 0)
	if err != nil {
		return err
	}
	var values []string
	found := false
	literalInputs := true
	var collect func(ast.Node)
	collect = func(node ast.Node) {
		switch n := node.(type) {
		case *ast.BasicLit:
			if n.Kind != token.STRING {
				literalInputs = false
				return
			}
			value, _ := strconv.Unquote(n.Value)
			values = append(values, value)
		case *ast.CompositeLit:
			for _, element := range n.Elts {
				collect(element)
			}
		default:
			literalInputs = false
		}
	}
	ast.Inspect(tree, func(n ast.Node) bool {
		if spec, ok := n.(*ast.ValueSpec); ok && input.Kind == "values" {
			for i, name := range spec.Names {
				if name.Name == input.Declaration && i < len(spec.Values) {
					found = true
					collect(spec.Values[i])
				}
			}
		}
		fn, ok := n.(*ast.FuncDecl)
		if !ok || fn.Name.Name != input.Declaration {
			return true
		}
		if input.Kind == "range" {
			loop := registrationRange(fn, input.Registration)
			if loop == nil {
				return false
			}
			found = true
			if input.RangeExpr != "" {
				var expression bytes.Buffer
				if err := format.Node(&expression, token.NewFileSet(), loop.X); err != nil || expression.String() != input.RangeExpr {
					literalInputs = false
				}
			} else {
				collect(loop.X)
			}
		} else if input.Kind == "keys" {
			found = true
			ast.Inspect(fn, func(n ast.Node) bool {
				if pair, ok := n.(*ast.KeyValueExpr); ok {
					collect(pair.Key)
				}
				return true
			})
		}
		return false
	})
	if !found || !literalInputs || !reflect.DeepEqual(uniqueStrings(values), uniqueStrings(input.Values)) {
		return fmt.Errorf("dynamic inputs changed: %s:%s (registration %d): got %v, table expects %v; review scripts/inventory/dynamic.go", input.File, input.Declaration, input.Registration, uniqueStrings(values), uniqueStrings(input.Values))
	}
	return nil
}
func uniqueStrings(values []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, value := range values {
		if !seen[value] {
			seen[value] = true
			out = append(out, value)
		}
	}
	sort.Strings(out)
	return out
}

// Select only the range enclosing this dynamic mux registration, so unrelated
// loops cannot affect its inputs and separate registrations keep separate lists.
func registrationRange(fn *ast.FuncDecl, ordinal int) *ast.RangeStmt {
	var target *ast.CallExpr
	seen := 0
	ast.Inspect(fn, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || (sel.Sel.Name != "Handle" && sel.Sel.Name != "HandleFunc") || !muxReceiver(sel.X) || len(call.Args) == 0 {
			return true
		}
		if lit, ok := call.Args[0].(*ast.BasicLit); ok && lit.Kind == token.STRING {
			return true
		}
		seen++
		if seen == ordinal {
			target = call
		}
		return true
	})
	if target == nil {
		return nil
	}
	variables := map[string]bool{}
	ast.Inspect(target.Args[0], func(n ast.Node) bool {
		if name, ok := n.(*ast.Ident); ok {
			variables[name.Name] = true
		}
		return true
	})
	var enclosing *ast.RangeStmt
	ast.Inspect(fn, func(n ast.Node) bool {
		if loop, ok := n.(*ast.RangeStmt); ok && loop.Pos() < target.Pos() && loop.End() >= target.End() {
			for _, variable := range []ast.Expr{loop.Key, loop.Value} {
				if name, ok := variable.(*ast.Ident); ok && variables[name.Name] {
					enclosing = loop
				}
			}
		}
		return true
	})
	return enclosing
}
