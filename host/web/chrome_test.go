package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vsriram/simple-host/host/config"

	"github.com/vsriram/simple-host/host/edition"
)

func TestSetupPageHasNoChrome(t *testing.T) {
	// The first-run wizard on an unconfigured box has no header or footer;
	// it carries the shared theme script alone (<!--sh:theme-->).
	raw, err := StaticFiles.ReadFile("static/setup.html")
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	testAssetPipeline(edition.Default(config.DefaultLimits()), "").ServeStaticPage("setup.html").ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	body := rec.Body.String()
	if strings.Contains(body, `class="sh-header"`) || strings.Contains(body, `class="sh-footer"`) || strings.Contains(body, "site.css") {
		t.Error("setup.html gained chrome")
	}
	if strings.Count(body, themeScriptNeedle) != 1 || strings.Contains(body, markerTheme) {
		t.Error("setup.html: want the shared theme script exactly once in place of its marker")
	}
	if want := string(addHostInk([]byte(strings.Replace(string(raw), markerTheme, themeScriptFor(t), 1)), "")); body != want {
		t.Error("setup.html differs from its shared theme and Host ink")
	}
}

// The hackathons page lives on Simple Hack. Simple Host's enterprise page
// links to it there. A Simple Hack install serves the page itself, so its copy
// links to its own address and never to the hosted service; on simple-hack.app
// that address is simple-hack.app.
func TestEnterpriseProductLink(t *testing.T) {
	for _, tc := range []struct {
		kind *edition.Edition
		base string
		want string
		not  string
	}{
		{platformTestEdition(), "", `href="https://simple-hack.app/hackathons"`, `href="/hackathons"`},
	} {
		page, err := testAssetPipeline(tc.kind, tc.base).BuildChromePage("enterprise.html", ChromeData{})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(page), tc.want) || strings.Contains(string(page), tc.not) {
			t.Errorf("%v at %q enterprise page: want %s and not %s", tc.kind, tc.base, tc.want, tc.not)
		}
	}
}
