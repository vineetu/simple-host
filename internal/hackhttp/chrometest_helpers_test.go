package hackhttp

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/vsriram/simple-host/internal/edition"
	"github.com/vsriram/simple-host/internal/handler"
)

// get, chromeTestHandler, chromeTestMux and assertStrictScriptCSP below are
// duplicated from internal/handler/{chrome_test.go,csp_test.go} (cleanup
// 3.2 slice 3). chromeTestHandler cannot be a straight copy: the original
// built a bare &SiteHandler{} composite literal (ed/limitSet/siteDomain/
// contentHost are all unexported fields), which only compiles inside
// package handler. This calls the exported production constructor
// (handler.NewSiteHandler) instead, with every parameter the original bare
// literal left at its zero value also left at its zero/empty value here
// ("" hackInstanceURL, nil database/disk, no preview accounts, no mailer);
// the one parameter the literal DID set (siteDomain/contentHost) is passed
// through unchanged. chromeTestHandlerWithBase additionally sets the asset
// pipeline's instance address, replacing the old two-line
// "sh := chromeTestHandler(kind); sh.assets = newAssetPipeline(sh.ed, base)"
// pattern several moved tests used (sh.assets is unexported, so that
// pattern cannot cross the package boundary either): NewSiteHandler already
// takes that address as a constructor argument.

func get(t *testing.T, h http.Handler, host, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if host != "" {
		req.Host = host
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func chromeTestHandler(kind edition.Kind) *handler.SiteHandler {
	return chromeTestHandlerWithBase(kind, "")
}

func chromeTestHandlerWithBase(kind edition.Kind, base string) *handler.SiteHandler {
	return handler.NewSiteHandler(edition.ForKind(kind), base, nil, nil, "simple-host.app", "sites.simple-host.app", "", "", "", "", nil, 0, "", "", nil, nil)
}

func chromeTestMux(t *testing.T, kind edition.Kind) *http.ServeMux {
	t.Helper()
	mux := http.NewServeMux()
	handler.RegisterUIRoutes(mux, "https://simple-host.app", chromeTestHandler(kind))
	return mux
}

var (
	chromeScriptTagRe  = regexp.MustCompile(`<script\b[^>]*>`)
	chromeScriptSrcRe  = regexp.MustCompile(`script-src ([^;]*)`)
	chromeCSPNonceRe   = regexp.MustCompile(`'nonce-([A-Za-z0-9+/=]+)'`)
	chromeInlineAttrRe = regexp.MustCompile(`<[a-zA-Z][^<>]*\son[a-z]+\s*=`)
)

// assertStrictScriptCSP checks one apex HTML response: script-src carries a
// nonce and no 'unsafe-inline', every inline <script> carries that nonce,
// and no element uses an on*= handler attribute (a nonce does not cover
// those).
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
