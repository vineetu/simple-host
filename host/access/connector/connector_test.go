package connector

import (
	"strings"
	"testing"
	"time"

	_ "github.com/lib/pq"

	model "github.com/vsriram/simple-host/host/model"
)

// ---- pure rules (no database) ---------------------------------------------------

func TestLoopbackPortMatch(t *testing.T) {
	cases := []struct {
		reg, pre string
		want     bool
	}{
		{"http://127.0.0.1:1234/cb", "http://127.0.0.1:5678/cb", true},
		{"http://127.0.0.1/cb", "http://127.0.0.1:5678/cb", true},
		{"http://localhost:1/cb", "http://localhost:2/cb", true},
		{"http://127.0.0.1:1/cb", "http://127.0.0.1:2/other", false},
		{"http://127.0.0.1:1/cb", "http://localhost:2/cb", false},
		{"http://127.0.0.1:1/cb", "https://127.0.0.1:2/cb", false},
		{"https://chat.example/cb", "https://chat.example:444/cb", false},
	}
	for _, c := range cases {
		if got := loopbackPortMatch(c.reg, c.pre); got != c.want {
			t.Errorf("loopbackPortMatch(%q, %q) = %v", c.reg, c.pre, got)
		}
	}
}

func TestNormalizeResource(t *testing.T) {
	h := &Handler{Issuer: "https://simple-host.app", mcpResource: "https://simple-host.app/mcp"}
	for in, want := range map[string]string{
		"":                            "https://simple-host.app",
		"https://simple-host.app":     "https://simple-host.app",
		"https://simple-host.app/mcp": "https://simple-host.app/mcp",
	} {
		if got, ok := h.normalizeResource(in); !ok || got != want {
			t.Errorf("%q -> %q %v", in, got, ok)
		}
	}
	for _, in := range []string{"https://evil.example/mcp", "https://simple-host.app/v1", "https://simple-host.app/mcp#x", "mcp"} {
		if _, ok := h.normalizeResource(in); ok {
			t.Errorf("accepted resource %q", in)
		}
	}
}

func TestNormalizeResourceMCPAlias(t *testing.T) {
	for _, issuer := range []string{"https://simple-host.app", "https://sites.example.org"} {
		t.Run(issuer, func(t *testing.T) {
			h := &Handler{Issuer: issuer, mcpResource: issuer + "/mcp"}
			canonical, ok := h.normalizeResource(h.mcpResource)
			if !ok {
				t.Fatal("canonical resource rejected")
			}
			got, ok := h.normalizeResource("https://mcp.simple-host.app/mcp")
			if strings.EqualFold(issuer, "https://simple-host.app") {
				if !ok || got != canonical {
					t.Fatalf("alias -> %q %v, want canonical audience %q", got, ok, canonical)
				}
			} else if ok {
				t.Fatal("alias accepted for non-canonical issuer")
			}
		})
	}
}

func TestConsentCSRF(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	h := &Handler{consentKey: []byte("k"), now: func() time.Time { return now }}
	req := authzRequest{Client: model.OAuthClient{ClientID: "c"}, RedirectURI: "https://a/cb", CodeChallenge: "x", State: "s", Resource: "r"}
	tok := h.consentCSRF(req, now.Unix())
	if !h.checkConsentCSRF(req, tok) {
		t.Fatal("fresh token refused")
	}
	other := req
	other.RedirectURI = "https://b/cb"
	if h.checkConsentCSRF(other, tok) {
		t.Fatal("token accepted for a different request")
	}
	h.now = func() time.Time { return now.Add(oauthConsentTTL + time.Second) }
	if h.checkConsentCSRF(req, tok) {
		t.Fatal("expired token accepted")
	}
}
