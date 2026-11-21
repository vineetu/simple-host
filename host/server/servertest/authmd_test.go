package servertest

import (
	"github.com/vsriram/simple-host/host/access/accesshttp"
	"github.com/vsriram/simple-host/host/sites/siteshttp"
	"github.com/vsriram/simple-host/host/web"
	"net/http"
	"strings"
	"testing"
)

func TestAuthMarkdownConfiguration(t *testing.T) {
	previousEmail, previousFacts := web.EmailSignInEnabled, web.CurrentInstance
	t.Cleanup(func() {
		accesshttp.SetEmailSignIn(previousEmail)
		web.CurrentInstance = previousFacts
	})
	for _, tc := range []struct {
		name, base   string
		email        bool
		providers    []string
		want, absent []string
	}{
		{"host unchanged", "https://simple-host.app", true, []string{"google"}, []string{"POST `/v1/auth`"}, nil},
		{"host no email", "https://box.example", false, nil, []string{"Email-code sign-in is unavailable", "supplied locally by the operator"}, []string{"POST `/v1/auth`", "type in a", "whose human operator can read an email"}},
		{"host OIDC", "https://box.example", false, []string{"oidc"}, []string{"Acme SSO (company OpenID Connect)", "Email-code sign-in is unavailable"}, []string{"POST `/v1/auth`"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			accesshttp.SetEmailSignIn(tc.email)
			web.CurrentInstance = web.InstanceFacts{SignInProviders: tc.providers, OIDCLabel: "Acme SSO"}
			sh := chromeTestHandler(platformTestEdition())
			sh.Assets = testAssetPipeline(sh.Ed, tc.base)
			mux := http.NewServeMux()
			siteshttp.RegisterUIRoutes(mux, tc.base, sh)
			rec := get(t, mux, "", "/auth.md")
			if rec.Code != 200 || rec.Header().Get("Content-Type") != "text/markdown; charset=utf-8" {
				t.Fatalf("auth: %d %v", rec.Code, rec.Header())
			}
			body := rec.Body.String()
			for _, want := range tc.want {
				if !strings.Contains(body, want) {
					t.Errorf("missing %q", want)
				}
			}
			for _, absent := range tc.absent {
				if strings.Contains(body, absent) {
					t.Errorf("unexpected %q", absent)
				}
			}
			if tc.email {
				original, _ := web.StaticFiles.ReadFile("static/auth.md")
				if body != string(original) {
					t.Error("hosted Host text changed")
				}
			}
		})
	}
}
