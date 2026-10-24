package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAuthMarkdown(t *testing.T) {
	mux := http.NewServeMux()
	RegisterUIRoutes(mux, "https://simple-host.app", chromeTestHandler())
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
	previousMode, previousBase, previousEmail, previousFacts := hackMode, hackInkBaseURL, emailSignInEnabled, instanceFacts
	t.Cleanup(func() {
		SetHackMode(previousMode)
		SetHackInstanceURL(previousBase)
		SetEmailSignIn(previousEmail)
		instanceFacts = previousFacts
	})
	for _, tc := range []struct {
		name, base   string
		hack, email  bool
		providers    []string
		want, absent []string
	}{
		{"host unchanged", "https://simple-host.app", false, true, []string{"google"}, []string{"POST `/v1/auth`"}, nil},
		{"host no email", "https://box.example", false, false, nil, []string{"Email-code sign-in is unavailable", "supplied locally by the operator"}, []string{"POST `/v1/auth`", "type in a", "whose human operator can read an email"}},
		{"host OIDC", "https://box.example", false, false, []string{"oidc"}, []string{"Acme SSO (company OpenID Connect)", "Email-code sign-in is unavailable"}, []string{"POST `/v1/auth`"}},
		{"hosted Hack", "https://simple-hack.app", true, true, []string{"google"}, []string{"an emailed code or Google", "https://simple-hack.app/mcp"}, []string{"Email-code sign-in is unavailable"}},
		{"company Hack", "https://hack.company.example", true, false, []string{"oidc"}, []string{"Acme SSO (company OpenID Connect)", "EMAIL_SIGNIN=off", "https://hack.company.example/mcp"}, []string{"simple-host.app", "simple-hack.app", "an emailed code or Google"}},
		{"company Hack with email", "https://hack.company.example", true, true, []string{"oidc"}, []string{"an emailed code or Acme SSO", "OIDC_CLIENT_ID"}, []string{"Email-code sign-in is unavailable"}},
		{"Hack no sign-in", "http://localhost:18480", true, false, nil, []string{"Browser sign-in is unavailable", "Email-code sign-in is unavailable"}, []string{"an emailed code or Google"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			SetHackMode(tc.hack)
			SetHackInstanceURL(tc.base)
			SetEmailSignIn(tc.email)
			instanceFacts = InstanceFacts{SignInProviders: tc.providers, OIDCLabel: "Acme SSO"}
			mux := http.NewServeMux()
			RegisterUIRoutes(mux, tc.base, chromeTestHandler())
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
			if tc.hack {
				for _, want := range []string{"hack_get_my_teams", "hack_select_team", "deadline", "code_challenge_method=S256", "Never request codes", "/oauth/revoke"} {
					if !strings.Contains(body, want) {
						t.Errorf("missing %q", want)
					}
				}
				if strings.Contains(body, "{sign_in}") {
					t.Error("unfilled template")
				}
			} else if tc.email {
				original, _ := staticFiles.ReadFile("static/auth.md")
				if body != string(original) {
					t.Error("hosted Host text changed")
				}
			}
		})
	}
}
