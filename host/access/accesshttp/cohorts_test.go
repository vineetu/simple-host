package accesshttp

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRequestArrival(t *testing.T) {
	r := httptest.NewRequest("POST", "/v1/auth/verify?from=vercel-ship&utm_source=poll&utm_campaign=%3Cscript%3E&referrer=ship.vineetsriram.com", nil)
	d, ok := requestArrival(r)
	if !ok || d.From != "vercel-ship" || d.UTMSource != "poll" || d.UTMCampaign != "script" || d.Referrer != "ship.vineetsriram.com" || d.LandedAt.IsZero() {
		t.Fatalf("detail: %+v %v", d, ok)
	}
	for _, bad := range []string{"", "%3Cscript%3E", "-x", strings.Repeat("a", 41)} {
		r := httptest.NewRequest("POST", "/v1/auth/verify?from="+bad, nil)
		if _, ok := requestArrival(r); ok {
			t.Errorf("accepted %q", bad)
		}
	}
	for _, ref := range []string{"10.0.0.1", "simple-host.app"} {
		r := httptest.NewRequest("POST", "https://simple-host.app/v1/auth/verify?from=x&referrer="+ref, nil)
		if d, _ := requestArrival(r); d.Referrer != "" {
			t.Errorf("kept referrer %s", ref)
		}
	}
	r = httptest.NewRequest("POST", "/v1/auth/verify", strings.NewReader("from=agentcon&utm_medium=web"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if d, ok := requestArrival(r); !ok || d.From != "agentcon" || d.UTMMedium != "web" {
		t.Fatalf("form: %+v", d)
	}
}
