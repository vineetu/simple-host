package handler

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

var (
	scriptTagRe  = regexp.MustCompile(`<script\b[^>]*>`)
	scriptSrcRe  = regexp.MustCompile(`script-src ([^;]*)`)
	cspNonceRe   = regexp.MustCompile(`'nonce-([A-Za-z0-9+/=]+)'`)
	inlineAttrRe = regexp.MustCompile(`<[a-zA-Z][^<>]*\son[a-z]+\s*=`)
)

// assertStrictScriptCSP checks one apex HTML response: script-src carries a
// nonce and no 'unsafe-inline', every inline <script> carries that nonce, and
// no element uses an on*= handler attribute (a nonce does not cover those).
func assertStrictScriptCSP(t *testing.T, label string, rec *httptest.ResponseRecorder) {
	t.Helper()
	csp := rec.Header().Get("Content-Security-Policy")
	m := scriptSrcRe.FindStringSubmatch(csp)
	if m == nil {
		t.Errorf("%s: no script-src in CSP %q", label, csp)
		return
	}
	if strings.Contains(m[1], "unsafe-inline") {
		t.Errorf("%s: script-src allows 'unsafe-inline': %q", label, m[1])
	}
	n := cspNonceRe.FindStringSubmatch(m[1])
	if n == nil {
		t.Errorf("%s: script-src has no nonce: %q", label, m[1])
		return
	}
	body := rec.Body.String()
	inline := 0
	for _, tag := range scriptTagRe.FindAllString(body, -1) {
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
	if hit := inlineAttrRe.FindString(body); hit != "" {
		t.Errorf("%s: inline event handler attribute, blocked by the CSP: %s", label, hit)
	}
}

func TestApexPagesStrictScriptCSP(t *testing.T) {
	mux := chromeTestMux(t)
	for _, path := range []string{
		"/", "/install.html", "/docs.html", "/architecture.html", "/privacy.html",
		"/features.html", "/enterprise.html", "/hackathons.html",
		"/dashboard", "/features", "/enterprise", "/enterprise/brief",
		"/enterprise/architecture", "/hackathons", "/terms", "/support",
		"/admin", "/analytics/my-site",
	} {
		rec := get(t, mux, "simple-host.app", path)
		if rec.Code != http.StatusOK {
			t.Errorf("%s: status %d", path, rec.Code)
			continue
		}
		assertStrictScriptCSP(t, path, rec)
	}

	// Two responses never share a nonce.
	a := get(t, mux, "simple-host.app", "/dashboard").Header().Get("Content-Security-Policy")
	b := get(t, mux, "simple-host.app", "/dashboard").Header().Get("Content-Security-Policy")
	if a == b {
		t.Error("the CSP nonce repeats across responses")
	}

	// The showcase and the 404, rendered by handlers rather than the file
	// server, behind the same middleware as on the apex.
	h := chromeTestHandler()
	data := showcaseData{Handle: "jane", SitesBaseURL: "https://sites.simple-host.app", MainURL: "https://simple-host.app", Sites: []showcaseSite{}}
	sc := adminUICSP(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		page, err := showcasePage(chromeDataFor(r, ""), data)
		if err != nil {
			t.Fatal(err)
		}
		w.Write(stampNonce(r, page))
	}))
	assertStrictScriptCSP(t, "showcase", get(t, sc, "simple-host.app", "/jane"))
	nf := adminUICSP(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { h.renderNotFound(w, r, "/nothing-here.html") }))
	assertStrictScriptCSP(t, "notfound", get(t, nf, "simple-host.app", "/nothing-here.html"))
}

// Off the apex no policy is set, and pages pass through unstamped.
func TestStampNonceWithoutPolicy(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	if got := string(stampNonce(r, []byte("<script>x</script>"))); got != "<script>x</script>" {
		t.Errorf("stamped without a nonce: %s", got)
	}
}
