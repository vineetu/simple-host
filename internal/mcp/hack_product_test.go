package mcp

import (
	"flag"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"
)

// Until D3-S1c moves the screens-in-chat, this package's tests still need
// Simple Hack's tools: the screens' tests call them, and the shared tests
// below compare Host with Hack. An in-package test cannot import the product
// (it imports this package), so hack_wire_test.go, in the external test
// package, installs it here before any test runs.
var hackProduct struct {
	Contribution func() Contribution
	PageRecipe   func(topic, site string) (string, error)
}

// testConfig is cfg with Hack's contribution unless the test chose its own.
func testConfig(cfg Config) Config {
	if cfg.Contribution == nil {
		product := hackProduct.Contribution()
		cfg.Contribution = &product
	}
	return cfg
}

// hackContribution is the product's contribution with the screens this
// package still supplies; NewServer adds the app-only family to it.
func hackContribution() Contribution {
	product := hackProduct.Contribution()
	product.Apps = hackApps
	return product
}
func hackFamilies() []familyTools          { return withScreens(testConfig(Config{}).Contribution).Families }
func HackTools() []Tool                    { return hackDeclaredTools() }
func newRegistry(cfg Config) *toolRegistry { return buildRegistry(testConfig(cfg)) }

func HackPageRecipe(topic, site string) (string, error) { return hackProduct.PageRecipe(topic, site) }
func hackPageRecipeTool() Tool {
	return hackProduct.Contribution().SiteVariants["get_page_recipe"](Tool{}, Limits{})
}
func hackSiteCreateSite(tool Tool, limits Limits) Tool {
	return hackProduct.Contribution().SiteVariants["create_site"](tool, limits)
}
func eventStorageTools() []Tool {
	var tools []Tool
	for _, family := range hackProduct.Contribution().Families {
		for _, tool := range family.Tools {
			if strings.HasPrefix(tool.Name, "hack_event_storage_") {
				tools = append(tools, tool)
			}
		}
	}
	return tools
}

func hackDeclaredTools() []Tool {
	r := &toolRegistry{}
	r.addFamilies(editionHack, hackFamilies(), nil)
	return r.declaredTools(editionHack)
}

// hackPhotoTip is the product's wording, repeated here for the one shared
// test that checks where it appears.
const hackPhotoTip = "Compress photos before deploying: about 1600 px wide, WebP or JPEG at about 80% quality; phone photos are often 4–12 MB."

func sendMode(t *testing.T, s *Server, body, mode string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	req.RemoteAddr = "203.0.113.7:5555"
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(WithCaller(req.Context(), Caller{APIKey: "user-key", Mode: mode}))
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	return rec
}

func listedTools(t *testing.T, rec *httptest.ResponseRecorder) []map[string]any {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("tools/list %d %s", rec.Code, rec.Body.String())
	}
	raw := decode(t, rec)["result"].(map[string]any)["tools"].([]any)
	out := make([]map[string]any, len(raw))
	for i, item := range raw {
		out[i] = item.(map[string]any)
	}
	return out
}

// The product's own tests enforce output-schema coverage of the tools it
// declares (products/hack/mcp/hack_schema_coverage_test.go, SIM-384). The
// app-only family is still declared and tested here, so its half of that
// check stays here until D3-S1c.
var (
	hackOutputCoverageMu sync.Mutex
	hackOutputCoverage   = map[string]bool{}
)

// recordHackOutputValidated marks name as having had its real output checked
// against its own outputSchema with ValidateOutput.
func recordHackOutputValidated(name string) {
	hackOutputCoverageMu.Lock()
	hackOutputCoverage[name] = true
	hackOutputCoverageMu.Unlock()
}

// uncoveredAppOnlyTools lists, sorted, every app-only tool that was never
// passed to recordHackOutputValidated during this test run.
func uncoveredAppOnlyTools() []string {
	hackOutputCoverageMu.Lock()
	defer hackOutputCoverageMu.Unlock()
	var missing []string
	for _, family := range hackFamilies() {
		if family.Family != famHackAppOnly {
			continue
		}
		for _, tool := range family.Tools {
			if !hackOutputCoverage[tool.Name] {
				missing = append(missing, tool.Name)
			}
		}
	}
	sort.Strings(missing)
	return missing
}

// TestMain runs the coverage check after a full, unfiltered run only.
func TestMain(m *testing.M) {
	code := m.Run()
	if code == 0 {
		if runFlag := flag.Lookup("test.run"); runFlag == nil || runFlag.Value.String() == "" {
			if missing := uncoveredAppOnlyTools(); len(missing) > 0 {
				fmt.Fprintf(os.Stderr, "SIM-384: these app-only tools were never validated by a strict ValidateOutput call in this package's tests: %s\n", missing)
				code = 1
			}
		}
	}
	os.Exit(code)
}

// InstallHackProductForTest is called by hack_wire_test.go's init.
func InstallHackProductForTest(contribution func() Contribution, pageRecipe func(topic, site string) (string, error)) {
	hackProduct.Contribution, hackProduct.PageRecipe = contribution, pageRecipe
}
