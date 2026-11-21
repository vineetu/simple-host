package accesshttp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	accountapp "github.com/vsriram/simple-host/host/access"
	"github.com/vsriram/simple-host/host/access/auth"
)

func TestSSOCookieBoundary(t *testing.T) {
	// A source-level tripwire supplements the behavioral check: a future
	// handler cannot casually acquire the cookie outside the SSO namespace.
	for _, root := range []string{".", "../auth", "../../web", "../../sites/siteshttp", "../../data/datahttp", "../../handler"} {
		files, _ := filepath.Glob(root + "/*.go")
		for _, file := range files {
			if strings.HasSuffix(file, "_test.go") || file == "sso.go" {
				continue
			}
			body, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			for _, needle := range []string{"__Host-sh_sso", `"sh_sso"`, "ssoCookie(", "ssoCookieName(", "ssoCookieHost", "ssoCookieHTTP", "SSOCookieHost", "SSOCookieHTTP"} {
				if strings.Contains(string(body), needle) {
					t.Fatalf("SSO cookie reader escaped into %s", file)
				}
			}
		}
	}
	for _, https := range []bool{false, true} {
		r := httptest.NewRequest("GET", "http://app.test/v1/sso/hop", nil)
		if https {
			r.Header.Set("X-Forwarded-Proto", "https")
		}
		w := httptest.NewRecorder()
		setSSOCookie(w, r, "secret", 60)
		c := w.Result().Cookies()[0]
		if !c.HttpOnly || c.Secure != https || c.Path != "/" || c.Domain != "" || c.SameSite != http.SameSiteLaxMode {
			t.Fatal("cookie scope")
		}
	}
	r := httptest.NewRequest("GET", "https://app.test/v1/me", nil)
	r.AddCookie(&http.Cookie{Name: SSOCookieHost, Value: "secret"})
	if ssoCookie(r) != "" {
		t.Fatal("reader outside SSO")
	}
	called := false
	w := httptest.NewRecorder()
	auth.Middleware("admin", "id", accountapp.New(nil), 180*24*time.Hour)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true })).ServeHTTP(w, r)
	if called || w.Code != 401 {
		t.Fatal("middleware accepted SSO cookie")
	}
}
func TestSSOReturnAllowlist(t *testing.T) {
	for _, base := range []string{"https://simple-host.app", "https://simple-hack.app", "https://company.test"} {
		u, _ := url.Parse(base + "/v1/sso/hop?state=" + strings.Repeat("a", 64) + "&cn=" + testNonceHash(testNonce).String)
		if !ssoReturnToOK(u, base) {
			t.Fatal("provider return rejected")
		}
		u.RawQuery += "&return_to=https://evil.test"
		if ssoReturnToOK(u, base) {
			t.Fatal("provider open redirect")
		}
	}
}

type ssoOIDCProvider struct{ fakeProvider }

func (ssoOIDCProvider) Name() string { return "oidc" }
func (ssoOIDCProvider) Exchange(ctx context.Context, code, verifier string) (accountapp.ProviderIdentity, error) {
	id, err := (fakeProvider{}).Exchange(ctx, code, verifier)
	id.Provider = "oidc"
	return id, err
}
