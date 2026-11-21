package datahttp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vsriram/simple-host/host/web"
)

// Cloudflare's documented test keys: this site key always passes and this
// secret always passes. Siteverify itself is a local fake here, so the
// tests never reach Cloudflare and can answer every case.
const (
	turnstileTestSiteKey = "1x00000000000000000000AA"
	turnstileTestSecret  = "1x0000000000000000000000000000000AA"
	turnstileTestApp     = "https://app.simple-host.test"
)

// fakeSiteverify answers siteverify. answer picks the reply for a token;
// it sees every form the server sent.
type fakeSiteverify struct {
	mu    sync.Mutex
	forms []map[string][]string
	delay time.Duration
	srv   *httptest.Server
}

func newFakeSiteverify(t *testing.T, answer func(token string) map[string]any) *fakeSiteverify {
	t.Helper()
	f := &fakeSiteverify{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		f.mu.Lock()
		f.forms = append(f.forms, r.PostForm)
		delay := f.delay
		f.mu.Unlock()
		if delay > 0 {
			time.Sleep(delay)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(answer(r.PostForm.Get("response")))
	}))
	old := TurnstileVerifyURL
	TurnstileVerifyURL = f.srv.URL
	t.Cleanup(func() { TurnstileVerifyURL = old; f.srv.Close() })
	return f
}

// passing is siteverify's answer for a token solved on host for resource.
func passing(app, host, resource string) map[string]any {
	return map[string]any{"success": true, "action": TurnstileAction, "hostname": app,
		"cdata": TurnstileCData(host, resource), "challenge_ts": time.Now().UTC().Format(time.RFC3339)}
}

func TestTurnstileVerify(t *testing.T) {
	answers := map[string]map[string]any{}
	f := newFakeSiteverify(t, func(token string) map[string]any {
		if a, ok := answers[token]; ok {
			return a
		}
		return map[string]any{"success": false, "error-codes": []string{"invalid-input-response"}}
	})
	v := NewTurnstileVerifier(turnstileTestSiteKey, turnstileTestSecret, turnstileTestApp)
	host, res := "guest.ann.simple-host.test", "guestbook"
	ok := passing("app.simple-host.test", host, res)
	with := func(k string, val any) map[string]any {
		m := map[string]any{}
		for a, b := range ok {
			m[a] = b
		}
		m[k] = val
		return m
	}
	answers["good"] = ok
	answers["wrong-action"] = with("action", "login")
	answers["wrong-host"] = with("hostname", "evil.example")
	answers["site-host"] = with("hostname", host)
	answers["wrong-cdata"] = with("cdata", TurnstileCData(host, "other"))
	answers["other-site"] = with("cdata", TurnstileCData("other.ann.simple-host.test", res))
	answers["old"] = with("challenge_ts", time.Now().Add(-6*time.Minute).UTC().Format(time.RFC3339))
	answers["failed"] = with("success", false)
	answers["duplicate"] = map[string]any{"success": false, "error-codes": []string{"timeout-or-duplicate"}}
	ctx := context.Background()
	if got := v.verify(ctx, "good", host, res); got != "" {
		t.Fatalf("good token: %q", got)
	}
	if got := v.verify(ctx, "good", host, res); got != "spam_check_failed" {
		t.Fatalf("reused token: %q", got)
	}
	for _, tok := range []string{"wrong-action", "wrong-host", "site-host", "wrong-cdata", "other-site", "old", "failed", "duplicate", "unknown"} {
		if got := v.verify(ctx, tok, host, res); got != "spam_check_failed" {
			t.Fatalf("%s: want spam_check_failed, got %q", tok, got)
		}
	}
	if got := v.verify(ctx, strings.Repeat("x", 2049), host, res); got != "spam_check_failed" {
		t.Fatalf("long token: %q", got)
	}
	if got := v.verify(ctx, "", host, res); got != "spam_check_failed" {
		t.Fatalf("empty token: %q", got)
	}
	// The secret goes to siteverify; the visitor's address never does.
	f.mu.Lock()
	for _, form := range f.forms {
		if form["secret"][0] != turnstileTestSecret || form["remoteip"] != nil {
			t.Fatalf("siteverify form: secret sent %v, remoteip %v", form["secret"] != nil, form["remoteip"])
		}
	}
	f.mu.Unlock()
	// A timeout refuses (fail closed).
	v.client.Timeout = 200 * time.Millisecond
	f.mu.Lock()
	f.delay = 500 * time.Millisecond
	f.mu.Unlock()
	answers["slow"] = ok
	if got := v.verify(ctx, "slow", host, res); got != "spam_check_unavailable" {
		t.Fatalf("timeout: %q", got)
	}
	f.mu.Lock()
	f.delay = 0
	f.mu.Unlock()
	// Cloudflare unreachable refuses too.
	TurnstileVerifyURL = "http://127.0.0.1:1/siteverify"
	if got := v.verify(ctx, "unreachable", host, res); got != "spam_check_unavailable" {
		t.Fatalf("unreachable: %q", got)
	}
}

// TestTurnstileMessagesJS runs the frame's script and auth.js's spam check
// under node with a fake window, and checks the postMessage origin rules
// both ways.
func TestTurnstileMessagesJS(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not installed")
	}
	page := turnstileFramePage
	start := strings.LastIndex(page, "<script nonce=\"%s\">") + len("<script nonce=\"%s\">")
	end := strings.LastIndex(page, "</script>")
	frame := strings.Replace(strings.Replace(page[start:end], "%s", `"`+turnstileTestSiteKey+`"`, 1), "%s", `"`+TurnstileAction+`"`, 1)
	auth, err := web.EmbeddedTree.ReadFile("static/auth.js")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	fixture, _ := json.Marshal(map[string]string{
		"frame": frame, "auth": string(auth),
		"cdata": TurnstileCData("guest.ann.simple-host.app", "guestbook"),
	})
	fp := filepath.Join(dir, "fixture.json")
	if err := os.WriteFile(fp, fixture, 0600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("node", "testdata/turnstile_harness.js", fp)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("harness: %v\n%s", err, out)
	}
	t.Log(string(out))
}
