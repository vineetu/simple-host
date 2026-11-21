package accesshttp

import (
	"net/http/httptest"
	"testing"

	model "github.com/vsriram/simple-host/host/model"
)

func TestNamedSourceSignupSource(t *testing.T) {
	r := httptest.NewRequest("POST", "/v1/auth/verify", nil)
	if s := websiteSignup(r, "email"); s.Source != model.SignupWebsite {
		t.Fatalf("plain website sign-up: %q", s.Source)
	}
	r = httptest.NewRequest("POST", "/v1/auth/verify?from=something-else", nil)
	if s := websiteSignup(r, "email"); s.Source != model.SignupFromPrefix+"something-else" {
		t.Fatalf("unnamed arrival value: %q", s.Source)
	}

	for fromValue, wantSource := range map[string]string{
		"artifact-hub": model.SignupArtifactHub,
		"vercel-ship":  model.SignupVercelShip,
		"agentcon":     model.SignupAgentCon,
	} {
		r := httptest.NewRequest("POST", "/v1/auth/verify", nil)
		r.URL.RawQuery = "from=" + fromValue
		if s := websiteSignup(r, "google"); s.Source != wantSource || s.Method != "google" {
			t.Fatalf("%s arrival: %+v", fromValue, s)
		}
	}
}
