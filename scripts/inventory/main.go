// inventory generates the route and MCP tool documentation from registrations.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/vsriram/simple-host/internal/mcp"
)

type route struct {
	Method    string `json:"method"`
	Path      string `json:"path"`
	FeatureID string `json:"feature_id"`
	Source    string `json:"source"`
	Package   string `json:"package"`
	Dynamic   bool   `json:"dynamic"`
}
type tool struct {
	Name      string `json:"name"`
	Family    string `json:"family"`
	Edition   string `json:"edition"`
	FeatureID string `json:"feature_id"`
}
type inventory struct {
	Routes []route `json:"routes"`
	Tools  []tool  `json:"tools"`
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: inventory routes|tools|write|check")
	}
	if args[0] == "tools" {
		flags := flag.NewFlagSet("tools", flag.ContinueOnError)
		edition := flags.String("edition", "host", "host, hack, or screen")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if *edition != "host" && *edition != "hack" && *edition != "screen" {
			return fmt.Errorf("unknown edition: %s", *edition)
		}
		var names []string
		for _, t := range mcp.Inventory() {
			if t.Edition == *edition {
				names = append(names, t.Name)
			}
		}
		sort.Strings(names)
		for _, name := range names {
			fmt.Println(name)
		}
		return nil
	}
	if len(args) != 1 {
		return fmt.Errorf("unexpected arguments: %v", args[1:])
	}
	if args[0] != "routes" && args[0] != "write" && args[0] != "check" {
		return fmt.Errorf("unknown command: %s", args[0])
	}
	if err := validateDynamicInputs("."); err != nil {
		return err
	}
	routes, err := scanRoutes(".", dynamicSites)
	if err != nil {
		return err
	}
	if args[0] == "routes" {
		for _, r := range routes {
			fmt.Printf("%s\t%s\t%s\n", r.Method, r.Path, r.Source)
		}
		return nil
	}
	features, err := os.ReadFile("FEATURES.md")
	if err != nil {
		return err
	}
	placements, err := parseFeatures(string(features))
	if err != nil {
		return err
	}
	inv := inventory{Routes: routes, Tools: []tool{}}
	for i := range inv.Routes {
		inv.Routes[i].FeatureID = placements.resolve(inv.Routes[i].Path, false)
	}
	for _, t := range mcp.Inventory() {
		inv.Tools = append(inv.Tools, tool{t.Name, t.Family, t.Edition, placements.resolve(t.Name, true)})
	}
	sort.Slice(inv.Tools, func(i, j int) bool {
		a, b := inv.Tools[i], inv.Tools[j]
		if a.Edition != b.Edition {
			return a.Edition < b.Edition
		}
		return a.Name < b.Name
	})
	files, err := generated(inv)
	if err != nil {
		return err
	}
	var problems []string
	if args[0] == "check" {
		registry, err := os.ReadFile("docs/feature-ids.txt")
		if err != nil {
			return err
		}
		if err := validateFeatureIDs(string(features), string(registry)); err != nil {
			problems = append(problems, err.Error())
		}
	}
	for _, name := range []string{"routes.md", "mcp-tools.md", "inventory.json"} {
		path := "docs/generated/" + name
		if args[0] == "write" {
			if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
				return err
			}
			if err := os.WriteFile(path, files[name], 0644); err != nil {
				return err
			}
		} else {
			current, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(current, files[name]) {
				problems = append(problems, "stale "+path+"; run make inventory")
			}
		}
	}
	if args[0] == "check" {
		for _, r := range inv.Routes {
			if r.FeatureID == "UNPLACED" {
				problems = append(problems, fmt.Sprintf("unplaced route: %s %s (%s)", r.Method, r.Path, r.Source))
			}
		}
		for _, t := range inv.Tools {
			if t.FeatureID == "UNPLACED" {
				problems = append(problems, fmt.Sprintf("unplaced tool: %s (%s)", t.Name, t.Edition))
			}
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("%s", strings.Join(problems, "\n"))
	}
	fmt.Printf("%s: %d route registrations, %d tools\n", args[0], len(inv.Routes), len(inv.Tools))
	return nil
}

// Each dynamic entry is keyed by file + enclosing function, then by its order
// among dynamic calls in that function. Checking the expression as well means
// adding, removing or moving a dynamic registration requires reviewing the table.
type dynamicSite struct {
	Expression string
	Patterns   []string
}
type dynamicTable map[string][]dynamicSite

func muxReceiver(e ast.Expr) bool {
	switch x := e.(type) {
	case *ast.Ident:
		return x.Name == "mux"
	case *ast.SelectorExpr:
		return x.Sel.Name == "mux"
	}
	return false
}
func scanRoutes(root string, table dynamicTable) ([]route, error) {
	var routes []route
	seen := map[string]int{}
	for _, dir := range []string{"cmd", "internal"} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			file, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			file = filepath.ToSlash(file)
			fset := token.NewFileSet()
			tree, err := parser.ParseFile(fset, path, nil, 0)
			if err != nil {
				return err
			}
			for _, decl := range tree.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok {
					continue
				}
				key := file + ":" + fn.Name.Name
				var scanErr error
				ast.Inspect(fn, func(n ast.Node) bool {
					if scanErr != nil {
						return false
					}
					call, ok := n.(*ast.CallExpr)
					if !ok {
						return true
					}
					sel, ok := call.Fun.(*ast.SelectorExpr)
					if !ok || (sel.Sel.Name != "Handle" && sel.Sel.Name != "HandleFunc") || !muxReceiver(sel.X) || len(call.Args) == 0 {
						return true
					}
					var patterns []string
					lit, literal := call.Args[0].(*ast.BasicLit)
					literal = literal && lit.Kind == token.STRING
					if literal {
						pattern, err := strconv.Unquote(lit.Value)
						if err != nil {
							scanErr = err
							return false
						}
						patterns = []string{pattern}
					} else {
						var expression bytes.Buffer
						if err := format.Node(&expression, fset, call.Args[0]); err != nil {
							scanErr = err
							return false
						}
						idx := seen[key]
						seen[key]++
						sites := table[key]
						if idx >= len(sites) || sites[idx].Expression != expression.String() {
							scanErr = fmt.Errorf("unknown dynamic registration: %s:%d (%s, call %d): %s", file, fset.Position(call.Pos()).Line, fn.Name.Name, idx+1, expression.String())
							return false
						}
						patterns = sites[idx].Patterns
					}
					for _, pattern := range patterns {
						method, path, found := strings.Cut(pattern, " ")
						if !found {
							path = method
							method = "ANY"
						}
						routes = append(routes, route{Method: method, Path: path, Source: fmt.Sprintf("%s:%d", file, fset.Position(call.Pos()).Line), Package: filepath.ToSlash(filepath.Dir(file)), Dynamic: !literal})
					}
					return true
				})
				if scanErr != nil {
					return scanErr
				}
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	for key, sites := range table {
		if seen[key] != len(sites) {
			return nil, fmt.Errorf("dynamic table is stale: %s: found %d calls, want %d", key, seen[key], len(sites))
		}
	}
	sort.Slice(routes, func(i, j int) bool {
		a, b := routes[i], routes[j]
		if a.Method != b.Method {
			return a.Method < b.Method
		}
		if a.Path != b.Path {
			return a.Path < b.Path
		}
		return a.Source < b.Source
	})
	return routes, nil
}

var anchorRE = regexp.MustCompile(`<a id="([^"]+)"></a>`)
var idRE = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
var codeSpanRE = regexp.MustCompile("`([^`]+)`")
var numberedSectionRE = regexp.MustCompile(`^## [0-9]+\.`)
var indexRE = regexp.MustCompile(`^## (21|22)\.`)

type placement struct{ text, id string }
type featurePlacements []placement

func parseFeatures(text string) (featurePlacements, error) {
	var out featurePlacements
	ids := map[string]bool{}
	current := ""
	index := false
	numbered := false
	for i, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, "## ") {
			index = indexRE.MatchString(line)
			numbered = numberedSectionRE.MatchString(line)
			current = ""
		}
		anchors := anchorRE.FindAllStringSubmatch(line, -1)
		required := strings.HasPrefix(line, "## ") || strings.HasPrefix(line, "### ") ||
			(numbered && strings.HasPrefix(line, "**") && !strings.HasPrefix(line, "**Status:"))
		if required && (len(anchors) != 1 || !strings.HasSuffix(line, anchors[0][0])) {
			return nil, fmt.Errorf("FEATURES.md:%d: required feature must have exactly one anchor at line end", i+1)
		}
		if len(anchors) > 1 {
			return nil, fmt.Errorf("FEATURES.md:%d: multiple anchors", i+1)
		}
		if len(anchors) == 1 {
			id := anchors[0][1]
			if !idRE.MatchString(id) || len(id) > 40 || ids[id] {
				return nil, fmt.Errorf("FEATURES.md:%d: invalid or duplicate feature ID %q", i+1, id)
			}
			ids[id] = true
			current = id
		}
		if !index && current != "" {
			out = append(out, placement{line, current})
		}
	}
	return out, nil
}
func (p featurePlacements) resolve(name string, isTool bool) string {
	pattern := "`" + regexp.QuoteMeta(name) + "`"
	if !isTool {
		pattern = "^(?:[A-Z]+(?:/[A-Z]+)* )?" + regexp.QuoteMeta(name) + `([^A-Za-z0-9_./{}-]|$)`
	}
	re := regexp.MustCompile(pattern)
	for _, line := range p {
		if isTool {
			if re.MatchString(line.text) {
				return line.id
			}
		} else if strings.Contains(line.text, name) {
			for _, span := range codeSpanRE.FindAllStringSubmatch(line.text, -1) {
				if re.MatchString(span[1]) {
					return line.id
				}
			}
		}
	}
	return "UNPLACED"
}
func generated(inv inventory) (map[string][]byte, error) {
	const header = "Generated by `make inventory`; do not edit.\n\n"
	var routes, tools strings.Builder
	routes.WriteString(header)
	packages := map[string][]route{}
	for _, r := range inv.Routes {
		packages[r.Package] = append(packages[r.Package], r)
	}
	var names []string
	for name := range packages {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		fmt.Fprintf(&routes, "## %s\n\n| Method | Path | Feature ID | Source |\n|---|---|---|---|\n", name)
		for _, r := range packages[name] {
			fmt.Fprintf(&routes, "| %s | `%s` | %s | %s |\n", r.Method, r.Path, r.FeatureID, r.Source)
		}
		routes.WriteString("\n")
	}
	tools.WriteString(header)
	for _, edition := range []string{"host", "hack", "screen"} {
		fmt.Fprintf(&tools, "## %s\n\n| Tool | Family | Feature ID |\n|---|---|---|\n", edition)
		for _, t := range inv.Tools {
			if t.Edition == edition {
				fmt.Fprintf(&tools, "| `%s` | %s | %s |\n", t.Name, t.Family, t.FeatureID)
			}
		}
		tools.WriteString("\n")
	}
	data, err := json.MarshalIndent(inv, "", "  ")
	if err != nil {
		return nil, err
	}
	return map[string][]byte{"routes.md": []byte(strings.TrimRight(routes.String(), "\n") + "\n"), "mcp-tools.md": []byte(strings.TrimRight(tools.String(), "\n") + "\n"), "inventory.json": append(data, '\n')}, nil
}

// The registry preserves IDs even after their feature has been retired.
func validateFeatureIDs(features, registry string) error {
	anchors := map[string]bool{}
	for _, match := range anchorRE.FindAllStringSubmatch(features, -1) {
		anchors[match[1]] = true
	}
	entries := strings.Split(strings.TrimSuffix(registry, "\n"), "\n")
	if !sort.StringsAreSorted(entries) {
		return fmt.Errorf("docs/feature-ids.txt: IDs must be sorted")
	}
	known := map[string]bool{}
	for _, entry := range entries {
		fields := strings.Fields(entry)
		if len(fields) < 1 || len(fields) > 2 || (len(fields) == 2 && fields[1] != "retired") {
			return fmt.Errorf("docs/feature-ids.txt: invalid entry %q", entry)
		}
		id := fields[0]
		if !idRE.MatchString(id) || len(id) > 40 || known[id] {
			return fmt.Errorf("docs/feature-ids.txt: invalid or duplicate ID %q", id)
		}
		known[id] = true
		if len(fields) == 1 && !anchors[id] {
			return fmt.Errorf("docs/feature-ids.txt: non-retired ID %q missing from FEATURES.md", id)
		}
	}
	for _, match := range anchorRE.FindAllStringSubmatch(features, -1) {
		if !known[match[1]] {
			return fmt.Errorf("FEATURES.md: anchor %q missing from docs/feature-ids.txt", match[1])
		}
	}
	return nil
}
