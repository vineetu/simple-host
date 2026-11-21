package siteshttp

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/vsriram/simple-host/host/httpx"
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

func newTestReport(m *fakeReportMailer) *ReportHandler {
	return &ReportHandler{
		mailer: m,
		to:     "support@simple-host.app",
		Hosted: func(_ context.Context, host string) (bool, error) {
			return isPlatformHost(host, "simple-host.app") || host == "recipes.example.com", nil
		},
		perIP:  httpx.NewRateLimiter(1000, 1000),
		global: httpx.NewRateLimiter(1000, 1000),
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
	for _, want := range []string{"support@simple-host.app|reporter@example.org|", "Page: https://scam.jane.simple-host.app/login", "Asks for bank logins."} {
		if !strings.Contains(got, want) {
			t.Errorf("email missing %q:\n%s", want, got)
		}
	}

}

func TestReportValidation(t *testing.T) {
	long := strings.Repeat("a", reportMaxDetails+1)
	cases := map[string]struct {
		body string
		code string
	}{
		"empty url":        {`{"url":"","reason":"spam"}`, "invalid_url"},
		"not hosted":       {`{"url":"https://example.com/","reason":"spam"}`, "not_hosted"},
		"lookalike host":   {`{"url":"https://evil-simple-host.app/","reason":"spam"}`, "not_hosted"},
		"unknown reason":   {`{"url":"https://x.simple-host.app/","reason":"boring"}`, "invalid_reason"},
		"details too long": {`{"url":"https://x.simple-host.app/","reason":"other","details":"` + long + `"}`, "details_too_long"},
		"bad email":        {`{"url":"https://x.simple-host.app/","reason":"other","email":"not-an-email"}`, "invalid_email"},
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
	rh.perIP = httpx.NewRateLimiter(2, 0.0001)
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
