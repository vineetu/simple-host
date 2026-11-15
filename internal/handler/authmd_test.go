package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vsriram/simple-host/internal/edition"
)

func TestAuthMarkdown(t *testing.T) {
	mux := http.NewServeMux()
	RegisterUIRoutes(mux, "https://simple-host.app", chromeTestHandler(edition.Host))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/auth.md", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != "text/markdown; charset=utf-8" {
		t.Fatalf("Content-Type = %q", got)
	}
	for _, heading := range []string{"## Discover", "## Pick a method", "## Register", "## Claim", "## Exchange", "## Use the access_token", "## Errors", "## Revocation"} {
		if !strings.Contains(rec.Body.String(), heading+"\n") {
			t.Errorf("missing heading %q", heading)
		}
	}
}

func TestAuthMarkdownConfiguration(t *testing.T) {
	previousEmail, previousFacts := emailSignInEnabled, instanceFacts
	t.Cleanup(func() {
		SetEmailSignIn(previousEmail)
		instanceFacts = previousFacts
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
			SetEmailSignIn(tc.email)
			instanceFacts = InstanceFacts{SignInProviders: tc.providers, OIDCLabel: "Acme SSO"}
			sh := chromeTestHandler(edition.Host)
			sh.assets = testAssetPipeline(sh.ed, tc.base)
			mux := http.NewServeMux()
			RegisterUIRoutes(mux, tc.base, sh)
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
				original, _ := staticFiles.ReadFile("static/auth.md")
				if body != string(original) {
					t.Error("hosted Host text changed")
				}
			}
		})
	}
}
