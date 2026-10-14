package handler

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/vsriram/simple-host/internal/config"
)

type importRoundTrip func(*http.Request) (*http.Response, error)

func (f importRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestImportFetcherRefusals(t *testing.T) {
	f := newImportFetcher(map[string]bool{"old.example": true, "127.0.0.1": true, "169.254.169.254": true}, "https://old.example/", true, config.DefaultLimits())
	defer f.client.CloseIdleConnections()
	for _, raw := range []string{"http://127.0.0.1/", "http://169.254.169.254/latest/meta-data/", "http://[::ffff:127.0.0.1]/", "file:///etc/passwd", "https://unknown.example/", "http://100.64.0.1/", "http://0.1.2.3/", "http://[fd00::1]/"} {
		if _, err := f.Fetch(context.Background(), raw); err == nil {
			t.Fatal("fetch accepted", raw)
		}
	}
	// Exercise the real dialer as well: an allowed DNS name resolving locally is refused.
	f.hosts["localhost"] = true
	if _, err := f.Fetch(context.Background(), "http://localhost/"); err == nil {
		t.Fatal("local DNS accepted")
	}
	private, _ := url.Parse("http://127.0.0.1/secret")
	r := &http.Request{URL: private}
	if err := f.client.CheckRedirect(r, []*http.Request{{}}); err == nil {
		t.Fatal("private redirect accepted")
	}
	if err := f.client.CheckRedirect(&http.Request{URL: mustImportURL(t, "https://old.example/")}, make([]*http.Request, 4)); err == nil {
		t.Fatal("too many redirects")
	}
}
func mustImportURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u
}
func TestImportFetcherBudgetsAndPhotoOptOut(t *testing.T) {
	f := newImportFetcher(map[string]bool{"old.example": true}, "https://old.example/", false, config.DefaultLimits())
	defer f.client.CloseIdleConnections()
	calls := 0
	f.client.Transport = importRoundTrip(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Header.Get("User-Agent") != "SimpleHostImporter/1.0 (+https://simple-host.app)" {
			t.Fatal("user agent")
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/css"}}, Body: io.NopCloser(strings.NewReader("12345")), Request: r}, nil
	})
	if _, err := f.Fetch(context.Background(), "https://old.example/photo.jpg"); err == nil || calls != 0 {
		t.Fatal("media opt-out")
	}
	for _, raw := range []string{f.theme, "https://old.example/style.css"} {
		if _, err := f.Fetch(context.Background(), raw); err != nil {
			t.Fatal(err)
		}
	}
	f.max = 4
	if _, err := f.Fetch(context.Background(), "https://old.example/style.css"); err == nil {
		t.Fatal("per-file cap")
	}
	f.max = 40
	f.total = 4
	if _, err := f.Fetch(context.Background(), f.theme); err == nil {
		t.Fatal("total cap")
	}
	f.total = 40
	f.deadline = time.Now().Add(-time.Second)
	f.client.Transport = http.DefaultTransport
	if _, err := f.Fetch(context.Background(), f.theme); err == nil {
		t.Fatal("deadline")
	}
}

func TestImportFetcherRedirectsAndAllowedHosts(t *testing.T) {
	f := newImportFetcher(map[string]bool{"old.example": true}, "", true, config.DefaultLimits())
	for _, raw := range []string{"https://old.example/x", "https://a.files.wordpress.com/photo.jpg", "https://i0.wp.com/photo.jpg", "https://fonts.googleapis.com/css?family=example", "https://fonts.gstatic.com/font.woff2"} {
		if err := f.allowed(mustImportURL(t, raw)); err != nil {
			t.Fatal(raw, err)
		}
	}
	calls := 0
	f.client.Transport = importRoundTrip(func(r *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 302, Header: http.Header{"Location": []string{"http://169.254.169.254/latest/meta-data/"}}, Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
	})
	if _, err := f.Fetch(context.Background(), "https://old.example/photo.jpg"); err == nil || calls != 1 {
		t.Fatal("private redirect was dialed", calls, err)
	}
}
