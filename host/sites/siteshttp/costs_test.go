package siteshttp

import (
	"github.com/vsriram/simple-host/host/web"
	"net/http"
	"os"
	"strings"
	"testing"
)

// /costs serves the page with its scripts, and the pages that should link
// to it do.
func TestCostsPageAndLinks(t *testing.T) {
	mux := chromeTestMux(t, platformTestEdition())
	rec := get(t, mux, "simple-host.app", "/costs")
	if rec.Code != http.StatusOK {
		t.Fatalf("/costs = %d", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{`src="/costs/calc.js"`, `src="/costs/costs.js"`, `id="in-existing"`, `id="in-ingress"`, `id="in-private"`, `id="in-traffic"`, `id="example"`} {
		if !strings.Contains(body, want) {
			t.Errorf("/costs lacks %s", want)
		}
	}
	for _, path := range []string{"/costs/prices.json", "/costs/calc.js", "/costs/costs.js"} {
		if rec := get(t, mux, "simple-host.app", path); rec.Code != http.StatusOK {
			t.Errorf("%s = %d", path, rec.Code)
		}
	}
	for _, f := range []string{"static/enterprise.html", "static/enterprise-brief.html", "static/setup/setup.js"} {
		b, err := web.StaticFiles.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(b), "/costs") {
			t.Errorf("%s does not link /costs", f)
		}
	}
	readme, err := os.ReadFile("../../../README.md")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(readme), "simple-host.app/costs") {
		t.Error("README does not link the cost calculator")
	}
}
