package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFeaturePlacement(t *testing.T) {
	data, err := os.ReadFile("testdata/FEATURES.md")
	if err != nil {
		t.Fatal(err)
	}
	placements, err := parseFeatures(string(data))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		tool bool
		want string
	}{
		{"/v1/sites", false, "site-lists"}, // /v1/sites/{sitename} must not place the collection.
		{"/v1/sites/{sitename}", false, "sites"},
		{"/v1/sites/{sitename}/export", false, "site-export"},
		{"/v1/files/{path...}", false, "storage"},
		{"/v1/files/{path}", false, "UNPLACED"}, // normalization belongs only in docs-sync.
		{"create_site", true, "sites"},          // first placement wins, even when mentioned later.
		{"list_sites", true, "site-lists"},
		{"export_site", true, "site-export"},
		{"storage_tool", true, "storage"},
		{"site", true, "UNPLACED"}, // tools need an exact backticked name.
		{"index_only", true, "UNPLACED"},
		{"unplaced_only", true, "UNPLACED"},
		{"/index-only", false, "UNPLACED"},
		{"/unplaced-only", false, "UNPLACED"},
	} {
		if got := placements.resolve(tc.name, tc.tool); got != tc.want {
			t.Errorf("resolve(%q, %v) = %q, want %q", tc.name, tc.tool, got, tc.want)
		}
	}
	for _, text := range []string{
		`## A <a id="bad_ID"></a>`,
		"## A <a id=\"same\"></a>\n### B <a id=\"same\"></a>",
		`## A <a id="` + strings.Repeat("a", 41) + `"></a>`,
		`## A <a id="one"></a> <a id="two"></a>`,
		`## Missing anchor`,
		`### Missing anchor`,
		"## 1. Sites <a id=\"sites\"></a>\n**Missing lead.**",
		"## 1. Sites <a id=\"sites\"></a>\n**Status: live.** A new feature.",
		`## A <a id="one"></a> trailing`,
		"## 1. Sites <a id=\"sites\"></a>\n**Lead.** <a id=\"lead\"></a> trailing",
	} {
		if _, err := parseFeatures(text); err == nil {
			t.Errorf("accepted invalid anchors: %s", text)
		}
	}
}

func TestRouteParsingAndDynamicTable(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{"cmd", "internal/example"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0755); err != nil {
			t.Fatal(err)
		}
	}
	source := `package example
func Register() {
 mux.Handle("GET /literal", nil)
 h.mux.HandleFunc("/any", nil)
 mux.Handle("GET /"+name, nil)
 other.Handle("GET /not-a-mux", nil)
}
`
	path := filepath.Join(root, "internal/example/routes.go")
	if err := os.WriteFile(path, []byte(source), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "internal/example/routes_test.go"), []byte("invalid Go: ignored"), 0644); err != nil {
		t.Fatal(err)
	}
	table := dynamicTable{"internal/example/routes.go:Register": {{`"GET /" + name`, []string{"GET /dynamic-one", "GET /dynamic-two"}}}}
	routes, err := scanRoutes(root, table)
	if err != nil {
		t.Fatal(err)
	}
	if len(routes) != 4 {
		t.Fatalf("got %d registrations, want 4", len(routes))
	}
	if routes[0].Method != "ANY" || routes[0].Path != "/any" || routes[0].Source != "internal/example/routes.go:4" {
		t.Errorf("selector/ANY/source: %+v", routes[0])
	}
	if !routes[1].Dynamic || routes[1].Path != "/dynamic-one" || routes[3].Dynamic {
		t.Errorf("dynamic and literal routes: %+v", routes)
	}
	if _, err := scanRoutes(root, nil); err == nil || !strings.Contains(err.Error(), "unknown dynamic registration") {
		t.Errorf("unknown site: %v", err)
	}
	table["internal/example/routes.go:Register"][0].Expression = "changed"
	if _, err := scanRoutes(root, table); err == nil {
		t.Error("accepted changed dynamic expression")
	}
	table["internal/example/routes.go:Register"][0].Expression = `"GET /" + name`
	table["missing.go:Missing"] = []dynamicSite{{"missing", []string{"GET /missing"}}}
	if _, err := scanRoutes(root, table); err == nil || !strings.Contains(err.Error(), "stale") {
		t.Errorf("stale table: %v", err)
	}
}

func TestRouteTokenBoundaries(t *testing.T) {
	for _, text := range []string{
		"owner key/connector/admin",
		"`connector`/admin",
		"`GET /v1/admin`",
		"`/v1/admin`",
		"`GET /admin/settings`",
	} {
		if got := (featurePlacements{{text, "wrong"}}).resolve("/admin", false); got != "UNPLACED" {
			t.Errorf("fragment matched %q", text)
		}
	}
	for _, text := range []string{"`/admin`", "`GET /admin`", "`GET/POST /admin`", "`GET`/`POST /admin`"} {
		if got := (featurePlacements{{text, "admin"}}).resolve("/admin", false); got != "admin" {
			t.Errorf("route notation rejected: %q", text)
		}
	}
}

func TestFeatureIDRegistry(t *testing.T) {
	features := `## 1. Sites <a id="sites"></a>`
	for _, tc := range []struct {
		registry string
		valid    bool
	}{
		{"sites\n", true},
		{"old retired\nsites\n", true},
		{"other\nsites\n", false},  // active IDs cannot disappear.
		{"other retired\n", false}, // anchors must be registered.
		{"sites\nold retired\n", false},
		{"sites\nsites retired\n", false},
	} {
		if err := validateFeatureIDs(features, tc.registry); (err == nil) != tc.valid {
			t.Errorf("registry %q: %v", tc.registry, err)
		}
	}
	if _, err := parseFeatures("**Preamble**\n" + features + "\n**Status: live.**"); err != nil {
		t.Fatal(err)
	}
}

func TestDynamicInputChanges(t *testing.T) {
	root := t.TempDir()
	for _, tc := range []struct{ declaration, kind, source string }{
		{"assets", "values", `var assets = []string{"old"}`},
		{"musePath", "values", `const musePath = "old"`},
		{"Register", "range", `func Register() { for _, p := range []string{"old"} { mux.Handle(p,nil) } }`},
		{"icons", "keys", `func icons() map[string]string { return map[string]string{"old":"file.svg"} }`},
	} {
		input := dynamicInput{"routes.go", tc.declaration, tc.kind, []string{"old"}, 0, ""}
		if tc.kind == "range" {
			input.Registration = 1
		}
		for _, mutation := range []string{"", `"new"`, `"old" + suffix`} {
			source := tc.source
			changed := mutation != ""
			if changed {
				source = strings.ReplaceAll(source, `"old"`, mutation)
			}
			if err := os.WriteFile(filepath.Join(root, input.File), []byte("package fixture\n"+source), 0644); err != nil {
				t.Fatal(err)
			}
			if err := validateDynamicInput(root, input); (err != nil) != changed {
				t.Errorf("%s changed=%v: %v", tc.declaration, changed, err)
			}
		}
	}
}

func TestFencedFeatureExamples(t *testing.T) {
	for _, fence := range []string{"```", "~~~"} {
		text := "## 1. Sites <a id=\"sites\"></a>\n" + fence + "markdown\n## Example\n**Example**\n### Fake <a id=\"fake\"></a>\n`GET /example` and `example_tool`\n" + fence + "\n`GET /real`"
		placements, err := parseFeatures(text)
		if err != nil {
			t.Fatal(err)
		}
		if placements.resolve("/example", false) != "UNPLACED" || placements.resolve("example_tool", true) != "UNPLACED" || placements.resolve("/real", false) != "sites" {
			t.Errorf("fenced example changed placements: %+v", placements)
		}
		if err := validateFeatureIDs(text, "sites\n"); err != nil {
			t.Fatal(err)
		}
	}
	for _, status := range []string{"**Status: live.**  ", "**Status: live.** A feature. <a id=\"feature\"></a>"} {
		if _, err := parseFeatures("## 1. Sites <a id=\"sites\"></a>\n" + status); err != nil {
			t.Fatal(err)
		}
	}
}

func TestDynamicRegistrationInputs(t *testing.T) {
	root := t.TempDir()
	source := `package fixture
func Register() {
 for _, p := range []string{"old"} { mux.Handle(p,nil) }
 for _, p := range []string{"other"} { mux.Handle(p,nil) }
 for _, unused := range []string{"unrelated"} { _ = unused }
}`
	for _, moved := range []bool{false, true} {
		text := source
		if moved {
			text = strings.Replace(text, `[]string{"old"}`, `[]string{}`, 1)
			text = strings.Replace(text, `[]string{"other"}`, `[]string{"other","old"}`, 1)
		}
		if err := os.WriteFile(filepath.Join(root, "routes.go"), []byte(text), 0644); err != nil {
			t.Fatal(err)
		}
		for i, values := range [][]string{{"old"}, {"other"}} {
			err := validateDynamicInput(root, dynamicInput{"routes.go", "Register", "range", values, i + 1, ""})
			if (err != nil) != moved {
				t.Errorf("registration %d moved=%v: %v", i+1, moved, err)
			}
		}
	}
}

func TestRoutesUnderProducts(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "products", "hack")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "routes.go"), []byte(`package hack
func Register() { mux.Handle("GET /products", nil) }
`), 0644); err != nil {
		t.Fatal(err)
	}
	routes, err := scanRoutes(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(routes) != 1 || routes[0].Path != "/products" || routes[0].Source != "products/hack/routes.go:2" {
		t.Fatalf("products routes: %+v", routes)
	}
}
