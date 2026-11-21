package accesshttp

import (
	"bytes"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"
)

const testPasscodeKey = "q83vEjRWeJq83vEjRWeJq83vEjRWeJq83vEjRWeJq80="

func TestNormalizePasscode(t *testing.T) {
	for in, want := range map[string]string{
		"123456":                 "123456",
		"  123456  ":             "123456",
		"Trip 2026 Goa!":         "Trip 2026 Goa!",
		"ಬೆಂಗಳೂರು":               "ಬೆಂಗಳೂರು",
		"12345":                  "",
		"     1234   ":           "",
		"12345\n6":               "",
		"abc\tdef":               "",
		strings.Repeat("x", 128): strings.Repeat("x", 128),
		strings.Repeat("x", 129): "",
	} {
		got, err := normalizePasscode(in, 6)
		if want == "" {
			if err == nil {
				t.Errorf("%q: accepted as %q", in, got)
			}
			continue
		}
		if err != nil || got != want {
			t.Errorf("%q: got %q, %v; want %q", in, got, err, want)
		}
	}
	if _, err := normalizePasscode("123456789", 10); err == nil {
		t.Fatal("PASSCODE_MIN_LENGTH=10 accepted 9 characters")
	}
	for i := 0; i < 20; i++ {
		g, err := generatePasscode(6)
		if err != nil || !regexp.MustCompile(`^[0-9]{6}$`).MatchString(g) {
			t.Fatalf("generated %q, %v", g, err)
		}
	}
}

func TestPasscodeSealAndCookie(t *testing.T) {
	h := &AccessHandler{}
	if err := h.SetPasscodeKey("not base64!"); err == nil {
		t.Fatal("bad key accepted")
	}
	if err := h.SetPasscodeKey(base64.StdEncoding.EncodeToString([]byte("short"))); err == nil {
		t.Fatal("short key accepted")
	}
	if err := h.SetPasscodeKey(testPasscodeKey); err != nil {
		t.Fatal(err)
	}
	enc, err := h.Passcode.Seal("site-a", "123456")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(enc, []byte("123456")) {
		t.Fatal("sealed value holds the passcode")
	}
	if got, err := h.Passcode.open("site-a", enc); err != nil || got != "123456" {
		t.Fatalf("open: %q %v", got, err)
	}
	if _, err := h.Passcode.open("site-b", enc); err == nil {
		t.Fatal("sealed value opened for another site")
	}
	if !passcodeMatches("123456", "123456") || passcodeMatches("123457", "123456") || passcodeMatches("1234567", "123456") {
		t.Fatal("passcodeMatches")
	}

	now := time.Unix(1_800_000_000, 0)
	h.Passcode.Now = func() time.Time { return now }
	mk := func(host string) *http.Request {
		r := httptest.NewRequest("GET", "https://"+host+"/", nil)
		r.Host = host
		r.Header.Set("X-Forwarded-Proto", "https")
		return r
	}
	rec := httptest.NewRecorder()
	h.setUnlockCookie(rec, mk("shop.olive.example.app"), "site-a", 3)
	c := rec.Result().Cookies()[0]
	if !strings.HasPrefix(c.Name, "__Host-sh_pass_") || !c.HttpOnly || !c.Secure || c.SameSite != http.SameSiteLaxMode || c.Path != "/" || c.Domain != "" {
		t.Fatalf("cookie attributes: %+v", c)
	}
	if c.MaxAge < 399*24*3600 {
		t.Fatalf("unlock should last ~400 days, Max-Age %d", c.MaxAge)
	}
	with := func(host, name, value string) *http.Request {
		r := mk(host)
		r.AddCookie(&http.Cookie{Name: name, Value: value})
		return r
	}
	if !h.unlocked(with("shop.olive.example.app", c.Name, c.Value), "site-a", 3) {
		t.Fatal("valid cookie refused")
	}
	if h.unlocked(with("blog.olive.example.app", c.Name, c.Value), "site-a", 3) {
		t.Fatal("cookie accepted on another host")
	}
	if h.unlocked(with("shop.olive.example.app", c.Name, c.Value), "site-b", 3) {
		t.Fatal("cookie accepted for another site")
	}
	if h.unlocked(with("shop.olive.example.app", c.Name, c.Value), "site-a", 4) {
		t.Fatal("cookie of an old generation accepted")
	}
	tampered := strings.Replace(c.Value, ".3.", ".4.", 1)
	if h.unlocked(with("shop.olive.example.app", c.Name, tampered), "site-a", 4) {
		t.Fatal("tampered cookie accepted")
	}
	plain := strings.TrimPrefix(c.Name, "__Host-")
	if h.unlocked(with("shop.olive.example.app", plain, c.Value), "site-a", 3) {
		t.Fatal("plain-named cookie accepted over HTTPS")
	}
	now = now.Add(401 * 24 * time.Hour)
	if h.unlocked(with("shop.olive.example.app", c.Name, c.Value), "site-a", 3) {
		t.Fatal("expired cookie accepted")
	}
	// Another key: every cookie made under the old one stops working.
	now = time.Unix(1_800_000_000, 0)
	h2 := &AccessHandler{}
	_ = h2.SetPasscodeKey(base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32)))
	h2.Passcode.Now = h.Passcode.Now
	if h2.unlocked(with("shop.olive.example.app", c.Name, c.Value), "site-a", 3) {
		t.Fatal("cookie accepted under another key")
	}
}

func TestSafeNext(t *testing.T) {
	for in, want := range map[string]string{
		"/":              "/",
		"/a/b?x=1":       "/a/b?x=1",
		"//evil.com/x":   "/",
		"/\\evil.com":    "/",
		"https://evil":   "/",
		"":               "/",
		"/a\r\nSet-X: 1": "/",
		"javascript:x":   "/",
		// nginx's and Caddy's marker rewrite: back to what the visitor asked for.
		"/internal/passcode/some/page?a=1": "/some/page?a=1",
		"/internal/passcode/":              "/",
		"/internal/passcode":               "/",
		"/internal/passcode?x=1":           "/?x=1",
		"/internal/passcode//evil.com":     "/",
		"/internal/passcodex/a":            "/",
		// Never one of the app's internal pages.
		"/internal/offline":             "/",
		"/internal/family/a":            "/",
		"/internal":                     "/",
		"/%69nternal/passcode/x":        "/",
		"/internal/passcode/internal/x": "/",
		"/internals/a":                  "/internals/a",
	} {
		if got := safeNext(in); got != want {
			t.Errorf("%q: %q want %q", in, got, want)
		}
	}
}
