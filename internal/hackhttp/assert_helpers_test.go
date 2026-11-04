package hackhttp

import (
	"net/http"
	"regexp"
	"strings"
	"testing"
)

func wantTS(t *testing.T, what string, r resp, status int, code string) {
	t.Helper()
	if r.status != status {
		t.Fatalf("%s: got %d want %d: %s", what, r.status, status, r.body)
	}
	if code != "" {
		if got, _ := r.json(t)["code"].(string); got != code {
			t.Fatalf("%s: code %q want %q: %s", what, got, code, r.body)
		}
	}
}

// galleryCardHTML extracts each gallery card's markup. The card's own
// element became a <div> in slice 3 (build-plan §4.6): the clickable site
// link is now a nested <a class="site-link">, since the card also carries
// the tool tags and a "See result" link outside that anchor (an <a> cannot
// nest another <a>). No gallery card nests a further <div>, so the first
// "</div>" after the opener still closes exactly this card.
func galleryCardHTML(body string) []string {
	var out []string
	rest := body
	const open = `<div class="site-card"`
	for {
		i := strings.Index(rest, open)
		if i < 0 {
			return out
		}
		rest = rest[i:]
		j := strings.Index(rest, "</div>")
		if j < 0 {
			return out
		}
		out = append(out, rest[:j])
		rest = rest[j+6:]
	}
}

func galleryCardHref(card string) string {
	const key = `href="`
	i := strings.Index(card, key)
	if i < 0 {
		return ""
	}
	rest := card[i+len(key):]
	j := strings.Index(rest, `"`)
	if j < 0 {
		return ""
	}
	return rest[:j]
}

func assertEventPageCSP(t *testing.T, h http.Header) {
	t.Helper()
	csp := h.Get("Content-Security-Policy")
	for _, want := range []string{
		"default-src 'none'",
		"connect-src http",
		"base-uri 'none'",
		"form-action 'none'",
		"frame-ancestors 'none'",
		"img-src 'self'",
	} {
		if !strings.Contains(csp, want) {
			t.Errorf("CSP missing %s: %s", want, csp)
		}
	}
	if strings.Contains(csp, "*") {
		t.Errorf("CSP has a wildcard: %s", csp)
	}
	m := chromeScriptSrcRe.FindStringSubmatch(csp)
	if m == nil || strings.Contains(m[1], "unsafe-inline") || chromeCSPNonceRe.FindStringSubmatch(m[1]) == nil {
		t.Errorf("script-src is not strict: %s", csp)
	}
	img := regexp.MustCompile(`img-src ([^;]*)`).FindStringSubmatch(csp)
	if img == nil || !strings.Contains(img[1], "'self'") || strings.Contains(img[1], "*") {
		t.Errorf("img-src %q", csp)
	}
}
