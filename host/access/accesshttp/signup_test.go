package accesshttp

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSignupAgentName(t *testing.T) {
	long := strings.Repeat("a", 100)
	for _, c := range []struct {
		client, skill, ua, want string
	}{
		{"", "", "curl/8.5.0", "curl"},
		{"", "", "python-requests/2.31.0", "python-requests"},
		{"", "", "Go-http-client/1.1", "Go-http-client"},
		{"", "", "Mozilla/5.0 (Macintosh)", "browser"},
		{"", "", "", ""},
		{"", "", "<script>alert(1)</script>/1", "scriptalert"},
		{"My Agent\t\t v2", "", "curl/8", "My Agent v2"},
		{"<b>", "", "node", "b"},
		{long, "", "", strings.Repeat("a", 40)},
		{"", "0.27.3", "curl/8", "skill 0.27.3"},
		{"", "0.27.3\"><img>", "", "skill 0.27.3img"},
		{"", "", "10.0.0.1 x", ""},
		{"203.0.113.9", "", "curl/8", "curl"},
		{"", "", "::1", ""},
		{"10.0.0.1:8080", "", "curl/8", "curl"},
		{"", "", "10.0.0.1:8080/x", ""},
		{"", "", "[2001:db8::1]:443", ""},
	} {
		r := httptest.NewRequest("POST", "/v1/auth/verify", nil)
		if c.client != "" {
			r.Header.Set("X-Simple-Host-Client", c.client)
		}
		if c.skill != "" {
			r.Header.Set("X-Skill-Version", c.skill)
		}
		r.Header.Set("User-Agent", c.ua)
		if got := signupAgentName(r); got != c.want {
			t.Errorf("client=%q skill=%q ua=%q: got %q, want %q", c.client, c.skill, c.ua, got, c.want)
		}
		if len(signupAgentName(r)) > signupAgentMax {
			t.Errorf("over the cap: %q", signupAgentName(r))
		}
	}
}
