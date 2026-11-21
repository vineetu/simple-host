package siteshttp

import (
	"strings"
	"testing"

	"github.com/vsriram/simple-host/host/edition"
)

// The docs website is gone; its old addresses redirect.
func TestDocsRedirects(t *testing.T) {
	mux := chromeTestMux(t, platformTestEdition())
	for route, want := range map[string]string{
		"/docs": "/llms.txt", "/docs/": "/llms.txt", "/docs/guides/storage/": "/llms.txt",
		"/docs.html": "/openapi.json", "/docs/api/": "/openapi.json",
	} {
		r := get(t, mux, "", route)
		if r.Code != 301 || r.Header().Get("Location") != want {
			t.Errorf("%s: %d, Location %q", route, r.Code, r.Header().Get("Location"))
		}
	}
}

func TestFeaturesConnectDomainLink(t *testing.T) {
	for _, kind := range []*edition.Edition{platformTestEdition()} {
		body := get(t, chromeTestMux(t, kind), "", "/features.html").Body.String()
		has := strings.Contains(body, `href="/skills/connect-domain/SKILL.md"`)
		if has != (kind.Identity().PersonalSites) {
			t.Errorf("%v: connect-domain link present=%v", kind, has)
		}
	}
}
