package web

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
		{"", "", "", ""},
		{"", "", "<script>alert(1)</script>/1", "scriptalert"},
		{"My Agent\t\t v2", "", "curl/8", "My Agent v2"},
		{long, "", "", strings.Repeat("a", 40)},
		{"", "0.27.3", "curl/8", "skill 0.27.3"},
		{"", "", "10.0.0.1 x", ""},
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
