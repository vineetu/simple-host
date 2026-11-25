package sites_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"simplehost/core/web"
	sites "simplehost/sites/core"
)

// TestWriteGatePages is not a check: with SH_GATE_PAGES set to a folder it
// writes every gate and status page there, for the owner app's screen
// catalogue (make web-pages). The inline stylesheet becomes a link to
// /pages.css, so the catalogue shows the pages with the stylesheet as built now.
func TestWriteGatePages(t *testing.T) {
	dir := os.Getenv("SH_GATE_PAGES")
	if dir == "" {
		t.Skip("SH_GATE_PAGES not set")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	ana := web.Caller{ID: "p-cy", Email: "cy@example.com", Via: "site"}
	pages := []struct {
		name   string
		d      sites.Decision
		status int
		note   string
		who    *web.Caller
	}{
		{"gate-passcode", sites.NeedCode, 0, "", nil},
		{"gate-passcode-wrong", sites.NeedCode, http.StatusUnauthorized, "That passcode is not right. Check it and try again.", nil},
		{"gate-private", sites.NeedSignIn, 0, "", nil},
		{"gate-ask", sites.AskAccess, 0, "", &ana},
		{"gate-asked", sites.AskAccess, http.StatusOK, "We told the owner. You can open the site once they add you.", &ana},
		{"gate-offline", sites.Offline, 0, "", nil},
		{"gate-takendown", sites.TakenDown, 0, "", nil},
		{"gate-notfound", "", 0, "", nil},
	}
	style := regexp.MustCompile(`(?s)<style>.*</style>`)
	for _, p := range pages {
		r := httptest.NewRequest("GET", "https://club--ana.simple-host.site/menu", nil)
		if p.who != nil {
			r = web.WithCaller(r, *p.who)
		}
		w := httptest.NewRecorder()
		if p.d == "" {
			sites.NotFoundPage(w, r)
		} else {
			sites.GatePage(w, r, p.d, p.status, p.note)
		}
		html := style.ReplaceAllString(w.Body.String(), `<link rel="stylesheet" href="/pages.css">`)
		if err := os.WriteFile(filepath.Join(dir, p.name+".html"), []byte(html), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}
