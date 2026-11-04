package main

import (
	"fmt"
	"go/ast"
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
	skills := patternInputs(ui[5:6], "GET /skills/", "")
	inputs := []dynamicInput{
		{"internal/handler/instancehost.go", "rewrittenAssets", "values", patternInputs(ui[:2], "GET /", "")},
		{"internal/handler/ui.go", "RegisterUIRoutes", "ranges", patternInputs(ui[1:2], "GET /", "")},
		{"internal/handler/ui.go", "iconFiles", "keys", patternInputs(ui[2:3], "GET /", "")},
		{"internal/handler/connector.go", "museClientPath", "values", patternInputs(connector[:1], "GET ", "")},
		{"internal/handler/connector.go", "Register", "ranges", patternInputs(connector[1:], "", " /mcp")},
		{"internal/hackhttp/hack_ui.go", "RegisterHackUI", "ranges", patternInputs(dynamicSites["internal/hackhttp/hack_ui.go:RegisterHackUI"], "", "")},
		{"internal/hackhttp/hack_get_started.go", "RegisterHackGetStarted", "ranges", patternInputs(dynamicSites["internal/hackhttp/hack_get_started.go:RegisterHackGetStarted"], "GET /simple-hack-skills-only-", ".zip")},
		{"internal/handler/hack_assets.go", "HackSkillNames", "values", []string{"run-hackathon", "join-hackathon", "judge-hackathon", "website-deploy", "website-deploy-builder"}},
	}
	for _, input := range inputs {
		if err := validateDynamicInput(root, input); err != nil {
			return err
		}
	}
	// bundledSkillDirs reads these embedded directories rather than a Go list.
	var dirs []string
	for _, bundle := range []string{"simple-host-website/skills", "hack-toolkit/skills"} {
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
	}
	if !reflect.DeepEqual(uniqueStrings(dirs), uniqueStrings(skills)) {
		return fmt.Errorf("dynamic inputs changed: bundled skill directories; review scripts/inventory/dynamic.go")
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
		found = true
		ast.Inspect(fn, func(n ast.Node) bool {
			switch input.Kind {
			case "ranges":
				if loop, ok := n.(*ast.RangeStmt); ok {
					if list, ok := loop.X.(*ast.CompositeLit); ok {
						collect(list)
					}
				}
			case "keys":
				if pair, ok := n.(*ast.KeyValueExpr); ok {
					collect(pair.Key)
				}
			}
			return true
		})
		return false
	})
	if !found || !literalInputs || !reflect.DeepEqual(uniqueStrings(values), uniqueStrings(input.Values)) {
		return fmt.Errorf("dynamic inputs changed: %s:%s: got %v, table expects %v; review scripts/inventory/dynamic.go", input.File, input.Declaration, uniqueStrings(values), uniqueStrings(input.Values))
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
