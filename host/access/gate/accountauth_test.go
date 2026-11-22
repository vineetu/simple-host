package gate

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	accountapp "github.com/vsriram/simple-host/host/access"
	"github.com/vsriram/simple-host/host/web"

	"github.com/vsriram/simple-host/host/config"
)

// Google account sign-in is served only on the app's own
// address; on a site's, a person's or a connected address it answers a plain
// 403 that points at visitor sign-in, and a page on a site cannot reach the
// apex routes cross-origin either.
func TestAccountAuthOnlyOnAppAddress(t *testing.T) {
	mux := http.NewServeMux()
	oauth := newTestOAuthHandler(nil, accountapp.New(nil), config.Config{PublicBaseURL: "https://simple-host.app"})
	oauth.Register(mux)
	app := web.CORS(mux)

	routes := []struct{ method, path string }{
		{"GET", "/v1/auth/oauth/google/callback"},
	}
	hosts := []string{"shop.alice.simple-host.app", "shop.example.org", "sites.simple-host.app", "simple-host.app.evil.example"}
	for _, rt := range routes {
		for _, host := range hosts {
			req := httptest.NewRequest(rt.method, "https://"+host+rt.path, strings.NewReader(`{"email":"customer@example.com","code":"123456"}`))
			req.Header.Set("Origin", "https://"+host)
			rec := httptest.NewRecorder()
			app.ServeHTTP(rec, req)
			body := rec.Body.String()
			if rec.Code != http.StatusForbidden || !strings.Contains(body, `"code":"account_auth_unavailable"`) {
				t.Fatalf("%s %s on %s: %d %s", rt.method, rt.path, host, rec.Code, body)
			}
			if len(rec.Result().Cookies()) != 0 || strings.Contains(body, "api_key") {
				t.Fatalf("%s on %s issued a credential", rt.path, host)
			}
		}
		// The apex, called cross-origin from a hosted page: refused too.
		req := httptest.NewRequest(rt.method, "https://simple-host.app"+rt.path, strings.NewReader(`{}`))
		req.Header.Set("Origin", "https://shop.alice.simple-host.app")
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("%s from a site origin: %d %s", rt.path, rec.Code, rec.Body.String())
		}
	}

}
