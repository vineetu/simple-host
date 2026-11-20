package hackhttp

import (
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

func assertStrictScriptCSP(t *testing.T, label string, rec *httptest.ResponseRecorder) {
	t.Helper()
	csp := rec.Header().Get("Content-Security-Policy")
	m := chromeScriptSrcRe.FindStringSubmatch(csp)
	if m == nil {
		t.Errorf("%s: no script-src in CSP %q", label, csp)
		return
	}
	if strings.Contains(m[1], "unsafe-inline") {
		t.Errorf("%s: script-src allows 'unsafe-inline': %q", label, m[1])
	}
	n := chromeCSPNonceRe.FindStringSubmatch(m[1])
	if n == nil {
		t.Errorf("%s: script-src has no nonce: %q", label, m[1])
		return
	}
	body := rec.Body.String()
	inline := 0
	for _, tag := range chromeScriptTagRe.FindAllString(body, -1) {
		if strings.Contains(tag, " src=") || strings.Contains(tag, `type="application/json"`) {
			continue
		}
		inline++
		if !strings.Contains(tag, `nonce="`+n[1]+`"`) {
			t.Errorf("%s: inline script without the response nonce: %s", label, tag)
		}
	}
	if inline == 0 {
		t.Errorf("%s: no inline scripts found; the check is not looking at a real page", label)
	}
	if hit := chromeInlineAttrRe.FindString(body); hit != "" {
		t.Errorf("%s: inline event handler attribute, blocked by the CSP: %s", label, hit)
	}
}

var (
	chromeScriptTagRe  = regexp.MustCompile(`<script\b[^>]*>`)
	chromeScriptSrcRe  = regexp.MustCompile(`script-src ([^;]*)`)
	chromeCSPNonceRe   = regexp.MustCompile(`'nonce-([A-Za-z0-9+/=]+)'`)
	chromeInlineAttrRe = regexp.MustCompile(`<[a-zA-Z][^<>]*\son[a-z]+\s*=`)
)
