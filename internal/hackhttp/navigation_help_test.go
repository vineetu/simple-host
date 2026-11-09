package hackhttp

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"sort"
	"strconv"
	"strings"
	"testing"
)

func TestNavigationHelpPresentationEnums(t *testing.T) {
	// hackhttp cannot import a rules package: inspect the JSON source embedded
	// by hackevents.NavigationHelp instead of adding a forbidden dependency.
	raw, err := os.ReadFile("../rules/hackevents/navigation-help.json")
	if err != nil {
		t.Fatal(err)
	}
	var help struct {
		Records []struct {
			ID      string
			Options []struct{ Label, Means string }
		}
	}
	if err := json.Unmarshal(raw, &help); err != nil {
		t.Fatal(err)
	}
	options := map[string]map[string]bool{}
	for _, r := range help.Records {
		options[r.ID] = map[string]bool{}
		for _, o := range r.Options {
			if o.Means != "" {
				options[r.ID][o.Label] = true
			}
		}
	}
	check := func(record string, labels []string) {
		t.Helper()
		for _, label := range labels {
			if !options[record][label] {
				t.Errorf("help record %s: add server option %q with its exact UI label and meaning", record, label)
			}
		}
		if len(options[record]) != len(labels) {
			t.Errorf("help record %s: expected %d server options, got %d", record, len(labels), len(options[record]))
		}
	}
	title := func(value string) string { return strings.ToUpper(value[:1]) + value[1:] }
	styles := []string{"Classic"}
	for _, theme := range hackThemes {
		styles = append(styles, title(theme))
	}
	check("look.style", styles)
	colours := []string{"Automatic"}
	for _, crayon := range hackCrayons {
		colours = append(colours, title(crayon.Name))
	}
	check("look.colour", colours)
	stageLabels := map[string]string{"draft": "Not open yet", "open": "Sign-up open", "building": "Building", "closed": "Entries closed", "judging": "Judging", "results": "Results", "archived": "Ended"}
	stages := []string{}
	for _, stage := range hackStagesOfferedList {
		label, ok := stageLabels[stage]
		if !ok {
			t.Errorf("stage %s: add its UI label to the parity test and the stage help record", stage)
		}
		stages = append(stages, label)
	}
	check("stage", stages)
	// There is no exported mode catalogue yet. Parse validation syntax rather
	// than depending on whitespace, operand order or comparison spelling.
	judging, err := os.ReadFile("hack_judging.go")
	if err != nil {
		t.Fatal(err)
	}
	modes := navigationAssignmentModes(t, string(judging))
	if len(modes) == 0 {
		t.Fatal("assignment mode validation moved: update its help parity extractor")
	}
	for i := range modes {
		modes[i] = title(modes[i])
	}
	check("judging.mode", modes)
}

// Handle equivalent ==/!= comparisons (in either order), parentheses,
// dereferences and switch cases. Comments and error-message prose never count.
func navigationAssignmentModes(t *testing.T, source string) []string {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), "judging.go", source, 0)
	if err != nil {
		t.Fatal(err)
	}
	var isMode func(ast.Expr) bool
	isMode = func(expr ast.Expr) bool {
		switch v := expr.(type) {
		case *ast.SelectorExpr:
			return v.Sel.Name == "AssignmentMode"
		case *ast.StarExpr:
			return isMode(v.X)
		case *ast.ParenExpr:
			return isMode(v.X)
		}
		return false
	}
	seen := map[string]bool{}
	add := func(expr ast.Expr) {
		if value, ok := expr.(*ast.BasicLit); ok && value.Kind == token.STRING {
			mode, err := strconv.Unquote(value.Value)
			if err == nil && mode != "" {
				seen[mode] = true
			}
		}
	}
	ast.Inspect(file, func(node ast.Node) bool {
		switch v := node.(type) {
		case *ast.BinaryExpr:
			if v.Op == token.EQL || v.Op == token.NEQ {
				if isMode(v.X) {
					add(v.Y)
				} else if isMode(v.Y) {
					add(v.X)
				}
			}
		case *ast.SwitchStmt:
			if isMode(v.Tag) {
				for _, statement := range v.Body.List {
					for _, expr := range statement.(*ast.CaseClause).List {
						add(expr)
					}
				}
			}
		}
		return true
	})
	modes := make([]string, 0, len(seen))
	for mode := range seen {
		modes = append(modes, mode)
	}
	sort.Strings(modes)
	return modes
}

func TestNavigationAssignmentModeExtractor(t *testing.T) {
	for _, body := range []string{
		`if (*req.AssignmentMode) != "open" && "manual" != *req.AssignmentMode { return }; if *req.AssignmentMode == "panel" || "automatic" == *req.AssignmentMode { return }`,
		"switch (*req.AssignmentMode) { case \"open\", \"manual\", \"automatic\", \"panel\": return }",
	} {
		modes := navigationAssignmentModes(t, "package fixture; func validate() {"+body+` /* *req.AssignmentMode != "retired" */ }`)
		if strings.Join(modes, ",") != "automatic,manual,open,panel" {
			t.Errorf("validation modes: %v", modes)
		}
	}
}
