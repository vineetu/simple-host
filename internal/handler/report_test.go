package handler

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// fakeReportMailer records what POST /report sends.
type fakeReportMailer struct {
	mu   sync.Mutex
	sent []string // "to|replyTo|subject|text"
	fail bool
}

func (m *fakeReportMailer) SendNoticeReplyTo(to, replyTo, subject, text string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.fail {
		return errors.New("down")
	}
	m.sent = append(m.sent, to+"|"+replyTo+"|"+subject+"|"+text)
	return nil
}

func newTestReport(m *fakeReportMailer) *reportHandler {
	return &reportHandler{
		mailer: m,
		to:     "support@simple-host.app",
		hosted: func(_ context.Context, host string) (bool, error) {
			return isPlatformHost(host, "simple-host.app") || host == "recipes.example.com", nil
		},
		perIP:  newRateLimiter(1000, 1000),
		global: newRateLimiter(1000, 1000),
	}
}

func postReport(t *testing.T, h http.Handler, body string, mod func(*http.Request)) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "https://simple-host.app/report", strings.NewReader(body))
	req.Host = "simple-host.app"
	req.Header.Set("X-Forwarded-Proto", "https")
	req.Header.Set("Origin", "https://simple-host.app")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.Header.Set("Content-Type", "application/json")
	if mod != nil {
		mod(req)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestReportSendsEmailToSupport(t *testing.T) {
	m := &fakeReportMailer{}
	rec := postReport(t, newTestReport(m), `{"url":"https://scam.jane.simple-host.app/login","reason":"phishing","details":"Asks for bank logins.","email":"reporter@example.org"}`, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if len(m.sent) != 1 {
		t.Fatalf("sent %d emails, want 1", len(m.sent))
	}
	got := m.sent[0]
	for _, want := range []string{"support@simple-host.app|reporter@example.org|Report: Phishing or scam — scam.jane.simple-host.app|", "Page: https://scam.jane.simple-host.app/login", "Asks for bank logins.", "Reporter's email: reporter@example.org"} {
		if !strings.Contains(got, want) {
			t.Errorf("email missing %q:\n%s", want, got)
		}
	}

	// No email and no details are fine; a custom domain the service hosts is too.
	rec = postReport(t, newTestReport(m), `{"url":"https://recipes.example.com/","reason":"csam"}`, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("custom domain: status %d: %s", rec.Code, rec.Body)
	}
	if !strings.HasPrefix(m.sent[1], "support@simple-host.app||") || !strings.Contains(m.sent[1], "(not given)") {
		t.Errorf("anonymous report: %s", m.sent[1])
	}
}

func TestReportValidation(t *testing.T) {
	long := strings.Repeat("a", reportMaxDetails+1)
	cases := map[string]struct {
		body string
		code string
	}{
		"empty url":        {`{"url":"","reason":"spam"}`, "invalid_url"},
		"not a url":        {`{"url":"hello there","reason":"spam"}`, "invalid_url"},
		"javascript url":   {`{"url":"javascript:alert(1)","reason":"spam"}`, "invalid_url"},
		"credentials":      {`{"url":"https://a:b@x.simple-host.app/","reason":"spam"}`, "invalid_url"},
		"url too long":     {`{"url":"https://x.simple-host.app/` + strings.Repeat("a", reportMaxURL) + `","reason":"spam"}`, "invalid_url"},
		"not hosted":       {`{"url":"https://example.com/","reason":"spam"}`, "not_hosted"},
		"lookalike host":   {`{"url":"https://evil-simple-host.app/","reason":"spam"}`, "not_hosted"},
		"unknown reason":   {`{"url":"https://x.simple-host.app/","reason":"boring"}`, "invalid_reason"},
		"missing reason":   {`{"url":"https://x.simple-host.app/"}`, "invalid_reason"},
		"details too long": {`{"url":"https://x.simple-host.app/","reason":"other","details":"` + long + `"}`, "details_too_long"},
		"bad email":        {`{"url":"https://x.simple-host.app/","reason":"other","email":"not-an-email"}`, "invalid_email"},
		"email with name":  {`{"url":"https://x.simple-host.app/","reason":"other","email":"Eve <eve@example.org>"}`, "invalid_email"},
		"not json":         {`url=https://x.simple-host.app/`, "invalid_body"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			m := &fakeReportMailer{}
			rec := postReport(t, newTestReport(m), c.body, nil)
			if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), `"code":"`+c.code+`"`) {
				t.Fatalf("got %d %s, want 400 %s", rec.Code, rec.Body, c.code)
			}
			if len(m.sent) != 0 {
				t.Fatal("an invalid report was emailed")
			}
		})
	}
	// A body over the cap is refused without being read in full.
	m := &fakeReportMailer{}
	rec := postReport(t, newTestReport(m), `{"url":"https://x.simple-host.app/","reason":"other","details":"`+strings.Repeat("a", reportMaxBody)+`"}`, nil)
	if rec.Code != http.StatusBadRequest || len(m.sent) != 0 {
		t.Fatalf("oversized body: %d %s", rec.Code, rec.Body)
	}
}

func TestReportSameOriginOnly(t *testing.T) {
	ok := `{"url":"https://x.simple-host.app/","reason":"spam"}`
	for name, mod := range map[string]func(*http.Request){
		"other origin":     func(r *http.Request) { r.Header.Set("Origin", "https://evil.example") },
		"cross-site fetch": func(r *http.Request) { r.Header.Set("Sec-Fetch-Site", "cross-site") },
		"a hosted site": func(r *http.Request) {
			r.Header.Set("Origin", "https://x.simple-host.app")
			r.Header.Set("Sec-Fetch-Site", "same-site")
		},
		"no origin at all": func(r *http.Request) { r.Header.Del("Origin"); r.Header.Del("Sec-Fetch-Site") },
	} {
		t.Run(name, func(t *testing.T) {
			m := &fakeReportMailer{}
			rec := postReport(t, newTestReport(m), ok, mod)
			if rec.Code != http.StatusForbidden || len(m.sent) != 0 {
				t.Fatalf("got %d %s, want 403 and nothing sent", rec.Code, rec.Body)
			}
		})
	}
}

func TestReportRateLimitedPerIP(t *testing.T) {
	m := &fakeReportMailer{}
	rh := newTestReport(m)
	rh.perIP = newRateLimiter(2, 0.0001)
	ok := `{"url":"https://x.simple-host.app/","reason":"spam"}`
	from := func(ip string) func(*http.Request) {
		return func(r *http.Request) { r.RemoteAddr = ip + ":1234" }
	}
	for i := 0; i < 2; i++ {
		if rec := postReport(t, rh, ok, from("203.0.113.9")); rec.Code != http.StatusOK {
			t.Fatalf("report %d: %d", i, rec.Code)
		}
	}
	if rec := postReport(t, rh, ok, from("203.0.113.9")); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("third report: %d, want 429", rec.Code)
	}
	if rec := postReport(t, rh, ok, from("198.51.100.4")); rec.Code != http.StatusOK {
		t.Fatalf("another address: %d, want 200", rec.Code)
	}
	if len(m.sent) != 3 {
		t.Fatalf("sent %d, want 3", len(m.sent))
	}
}

func TestReportMailFailures(t *testing.T) {
	ok := `{"url":"https://x.simple-host.app/","reason":"spam"}`
	rec := postReport(t, newTestReport(&fakeReportMailer{fail: true}), ok, nil)
	if rec.Code != http.StatusBadGateway || !strings.Contains(rec.Body.String(), "support@simple-host.app") {
		t.Fatalf("send failure: %d %s", rec.Code, rec.Body)
	}
	rh := newTestReport(nil)
	rh.mailer = nil
	rec = postReport(t, rh, ok, nil)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("no mailer: %d %s", rec.Code, rec.Body)
	}
}

// The page is served with the chrome, and the footer links to it everywhere.
func TestReportPageAndLinks(t *testing.T) {
	mux := chromeTestMux(t)
	rec := get(t, mux, "simple-host.app", "/report")
	if rec.Code != http.StatusOK {
		t.Fatalf("/report: %d", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{"Report a page", "emergency services", "Do not include, attach or link the material itself", `aria-current="page">Report a page`} {
		if !strings.Contains(body, want) {
			t.Errorf("/report missing %q", want)
		}
	}
	for _, path := range []string{"/", "/terms", "/support"} {
		if b := get(t, mux, "simple-host.app", path).Body.String(); !strings.Contains(b, `href="/report"`) {
			t.Errorf("%s does not link to /report", path)
		}
	}
}
