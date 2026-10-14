package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
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
	old := turnstileVerifyURL
	turnstileVerifyURL = f.srv.URL
	t.Cleanup(func() { turnstileVerifyURL = old; f.srv.Close() })
	return f
}

func (f *fakeSiteverify) calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.forms)
}

// passing is siteverify's answer for a token solved on host for resource.
func passing(app, host, resource string) map[string]any {
	return map[string]any{"success": true, "action": turnstileAction, "hostname": app,
		"cdata": turnstileCData(host, resource), "challenge_ts": time.Now().UTC().Format(time.RFC3339)}
}

func TestTurnstileVerify(t *testing.T) {
	answers := map[string]map[string]any{}
	f := newFakeSiteverify(t, func(token string) map[string]any {
		if a, ok := answers[token]; ok {
			return a
		}
		return map[string]any{"success": false, "error-codes": []string{"invalid-input-response"}}
	})
	v := newTurnstileVerifier(turnstileTestSiteKey, turnstileTestSecret, turnstileTestApp)
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
	answers["wrong-cdata"] = with("cdata", turnstileCData(host, "other"))
	answers["other-site"] = with("cdata", turnstileCData("other.ann.simple-host.test", res))
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
	turnstileVerifyURL = "http://127.0.0.1:1/siteverify"
	if got := v.verify(ctx, "unreachable", host, res); got != "spam_check_unavailable" {
		t.Fatalf("unreachable: %q", got)
	}
}

func TestTurnstileOffWithoutKeys(t *testing.T) {
	for _, c := range [][2]string{{"", ""}, {turnstileTestSiteKey, ""}, {"", turnstileTestSecret}} {
		if newTurnstileVerifier(c[0], c[1], turnstileTestApp) != nil {
			t.Fatalf("on with key %v, secret %v", c[0] != "", c[1] != "")
		}
	}
	s := newPresetSite(t)
	r := s.must(t, "PUT", "/resources/guestbook", map[string]string{"kind": "sqlite", "preset": "comments"}, s.key, 400)
	if !strings.Contains(string(r.body), "spam_check_unavailable") {
		t.Fatalf("off: %s", r.body)
	}
	s.must(t, "PUT", "/resources/guestbook", map[string]string{"kind": "sqlite", "preset": "inbox", "add": "checked"}, s.key, 400)
	if r := s.a.at(t, "GET", s.a.appHost(), "/v1/turnstile/frame", nil, nil); r.status != 404 {
		t.Fatalf("frame while off: %d", r.status)
	}
}

// commentsSite is a preset site with the spam check on, a comments SQLite
// resource "guestbook" with table entries, and siteverify answering
// passing() for every token that starts with "ok-".
func commentsSite(t *testing.T) (presetSite, *fakeSiteverify) {
	t.Helper()
	s := newPresetSite(t)
	app, _ := url.Parse(s.a.srv.URL)
	f := newFakeSiteverify(t, func(token string) map[string]any {
		if rest, ok := strings.CutPrefix(token, "ok-"); ok {
			parts := strings.SplitN(rest, "|", 3)
			if len(parts) < 2 {
				return map[string]any{"success": false}
			}
			return passing(app.Hostname(), parts[0], parts[1])
		}
		return map[string]any{"success": false, "error-codes": []string{"invalid-input-response"}}
	})
	s.a.sites.SetTurnstile(turnstileTestSiteKey, turnstileTestSecret, s.a.srv.URL)
	s.a.sites.spamIPLimiter = newRateLimiter(100000, 0)
	s.a.sites.spamSiteLimiter = newRateLimiter(100000, 0)
	put := s.must(t, "PUT", "/resources/guestbook", map[string]string{"kind": "sqlite", "preset": "comments"}, s.key, 201).json(t)
	if put["preset"] != "comments" {
		t.Fatalf("preset: %v", put["preset"])
	}
	s.must(t, "POST", "/sqlite/guestbook/schema", map[string]string{"sql": "CREATE TABLE entries (id INTEGER PRIMARY KEY, name TEXT, message TEXT, created_at TEXT)"}, s.key, 200)
	return s, f
}

var tokenSeq int

func (s presetSite) token(resource string) string {
	tokenSeq++
	return "ok-" + s.host + "|" + resource + "|" + strconv.Itoa(tokenSeq)
}

func withToken(h map[string]string, token string) map[string]string {
	out := map[string]string{}
	for k, v := range h {
		out[k] = v
	}
	out[turnstileHeader] = token
	return out
}

func TestCommentsPresetPolicy(t *testing.T) {
	s, f := commentsSite(t)
	anon := s.callers["anon"]
	rows := "/sqlite/guestbook/tables/entries/rows"
	post := `{"name":"Ann","message":"Lovely shop"}`
	// No token: refused, and siteverify is not called.
	r := s.must(t, "POST", rows, post, anon, 403)
	if !strings.Contains(string(r.body), "spam_check_required") || f.calls() != 0 {
		t.Fatalf("no token: %s (calls %d)", r.body, f.calls())
	}
	s.must(t, "POST", rows, post, withToken(anon, "bad-token"), 403)
	// A token solved for another resource, or another site, is refused.
	s.must(t, "POST", rows, post, withToken(anon, "ok-"+s.host+"|other|1"), 403)
	s.must(t, "POST", rows, post, withToken(anon, "ok-other.ann.simple-host.test|guestbook|1"), 403)
	// A good token posts once; the same token again is refused.
	tok := s.token("guestbook")
	s.must(t, "POST", rows, post, withToken(anon, tok), 200)
	s.must(t, "POST", rows, post, withToken(anon, tok), 403)
	// A signed-in visitor needs the check too; the owner on the site does not.
	s.must(t, "POST", rows, `{"name":"Alice","message":"hi"}`, s.callers["alice"], 403)
	s.must(t, "POST", rows, `{"name":"Alice","message":"hi"}`, withToken(s.callers["alice"], s.token("guestbook")), 200)
	s.must(t, "POST", rows, `{"name":"Owner","message":"thanks"}`, s.callers["owner-site"], 200)
	// Anyone reads.
	list := s.must(t, "GET", rows, nil, anon, 200)
	if !strings.Contains(string(list.body), "Lovely shop") || !strings.Contains(string(list.body), "thanks") {
		t.Fatalf("anon read: %s", list.body)
	}
	if strings.Contains(string(list.body), s.aliceID) {
		t.Fatalf("anon read leaks visitor_id: %s", list.body)
	}
	// Visitors never edit or delete, token or not; the owner deletes.
	id := itemsFirstID(t, list)
	s.must(t, "DELETE", rows+"/"+id, nil, withToken(anon, s.token("guestbook")), 403)
	s.must(t, "PATCH", rows+"/"+id, `{"message":"spam"}`, withToken(anon, s.token("guestbook")), 403)
	s.must(t, "DELETE", rows+"/"+id, nil, s.callers["alice"], 403)
	s.must(t, "DELETE", rows+"/"+id, nil, s.key, 200)
	// Post limits: size and links.
	big := `{"name":"Ann","message":"` + strings.Repeat("a", 5000) + `"}`
	s.must(t, "POST", rows, big, withToken(anon, s.token("guestbook")), 413)
	links := `{"name":"Ann","message":"see https:\/\/a.example and http://b.example and www.c.example"}`
	r = s.must(t, "POST", rows, links, withToken(anon, s.token("guestbook")), 400)
	if !strings.Contains(string(r.body), "too_many_links") {
		t.Fatalf("links: %s", r.body)
	}
	// The owner's own tools are not limited.
	s.must(t, "POST", rows, big, s.key, 200)
	// KV: anyone adds a new key with the check; nobody overwrites one.
	s.must(t, "PUT", "/resources/notes", map[string]string{"kind": "kv", "preset": "comments"}, s.key, 201)
	s.must(t, "PUT", "/kv/notes/keys/k1", `{"value":"hello"}`, anon, 403)
	s.must(t, "PUT", "/kv/notes/keys/k1", `{"value":"hello"}`, withToken(anon, s.token("notes")), 200)
	if r := s.do(t, "PUT", "/kv/notes/keys/k1", `{"value":"over"}`, withToken(anon, s.token("notes"))); r.status < 400 {
		t.Fatalf("overwrite: %d %s", r.status, r.body)
	}
	s.must(t, "GET", "/kv/notes/keys/k1", nil, anon, 200)
	s.must(t, "DELETE", "/kv/notes/keys/k1", nil, anon, 403)
	s.must(t, "DELETE", "/kv/notes/keys/k1", nil, s.key, 200)
	// Files never take checked; checked is add only; own never mixes with it.
	s.must(t, "PUT", "/resources/pics", map[string]string{"kind": "files", "preset": "comments"}, s.key, 400)
	s.must(t, "PUT", "/resources/x1", map[string]string{"kind": "kv", "preset": "custom", "read": "checked", "add": "checked", "edit": "owner", "delete": "owner"}, s.key, 400)
	s.must(t, "PUT", "/resources/x2", map[string]string{"kind": "kv", "preset": "comments", "read": "own"}, s.key, 400)
	// Siteverify down: refused, nothing saved.
	turnstileVerifyURL = "http://127.0.0.1:1/siteverify"
	r = s.must(t, "POST", rows, post, withToken(anon, s.token("guestbook")), 403)
	if !strings.Contains(string(r.body), "spam_check_unavailable") {
		t.Fatalf("down: %s", r.body)
	}
}

func itemsFirstID(t *testing.T, r resp) string {
	t.Helper()
	var out struct {
		Columns []string `json:"columns"`
		Rows    [][]any  `json:"rows"`
	}
	if err := json.Unmarshal(r.body, &out); err != nil || len(out.Rows) == 0 {
		t.Fatalf("rows: %s", r.body)
	}
	for i, c := range out.Columns {
		if c == "id" {
			return jsonString(out.Rows[0][i])
		}
	}
	t.Fatalf("no id column: %s", r.body)
	return ""
}

func jsonString(v any) string { b, _ := json.Marshal(v); return string(b) }

func TestCommentsApproveFirst(t *testing.T) {
	s, _ := commentsSite(t)
	anon := s.callers["anon"]
	rows := "/sqlite/guestbook/tables/entries/rows"
	// Posts already there stay public when approve_first is turned on.
	s.must(t, "POST", rows, `{"name":"Early","message":"first"}`, withToken(anon, s.token("guestbook")), 200)
	put := s.must(t, "PUT", "/resources/guestbook", map[string]any{"kind": "sqlite", "preset": "comments", "approve_first": true}, s.key, 200).json(t)
	if put["approve_first"] != true {
		t.Fatalf("approve_first: %v", put)
	}
	s.must(t, "POST", rows, `{"name":"Late","message":"held"}`, withToken(anon, s.token("guestbook")), 200)
	list := s.must(t, "GET", rows, nil, anon, 200)
	if !strings.Contains(string(list.body), "first") || strings.Contains(string(list.body), "held") {
		t.Fatalf("visitor sees: %s", list.body)
	}
	// The owner sees the held post and its id; a visitor cannot reach it.
	q := s.must(t, "POST", "/sqlite/guestbook/query", map[string]any{"sql": "SELECT id FROM entries WHERE approved_at IS NULL", "params": []any{}}, s.key, 200)
	var held struct {
		Rows [][]any `json:"rows"`
	}
	if json.Unmarshal(q.body, &held) != nil || len(held.Rows) != 1 {
		t.Fatalf("held rows: %s", q.body)
	}
	id := jsonString(held.Rows[0][0])
	s.must(t, "GET", rows+"/"+id, nil, anon, 404)
	if !strings.Contains(string(s.must(t, "GET", rows, nil, s.callers["owner-site"], 200).body), "held") {
		t.Fatal("owner on the site does not see the held post")
	}
	// The owner approves with their tools; then everyone sees it.
	s.must(t, "POST", "/sqlite/guestbook/execute", map[string]any{"sql": "UPDATE entries SET approved_at = datetime('now') WHERE id = ?", "params": []any{held.Rows[0][0]}}, s.key, 200)
	if !strings.Contains(string(s.must(t, "GET", rows, nil, anon, 200).body), "held") {
		t.Fatal("approved post not shown")
	}
	// Visitors cannot set approved_at themselves on a held post.
	s.must(t, "POST", rows, `{"name":"Sneaky","message":"x","approved_at":"2026-01-01"}`, withToken(anon, s.token("guestbook")), 200)
	if strings.Contains(string(s.must(t, "GET", rows, nil, anon, 200).body), "Sneaky") {
		t.Fatal("a visitor approved their own post")
	}
	// The owner signed in on the site posts approved.
	s.must(t, "POST", rows, `{"name":"Owner","message":"reply"}`, s.callers["owner-site"], 200)
	if !strings.Contains(string(s.must(t, "GET", rows, nil, anon, 200).body), "reply") {
		t.Fatal("owner's post held")
	}
	// approve_first needs a checked add, and SQLite.
	s.must(t, "PUT", "/resources/w", map[string]any{"kind": "sqlite", "preset": "wall", "approve_first": true}, s.key, 400)
	s.must(t, "PUT", "/resources/k", map[string]any{"kind": "kv", "preset": "comments", "approve_first": true}, s.key, 400)
}

func TestTurnstileFrame(t *testing.T) {
	s, _ := commentsSite(t)
	r := s.a.at(t, "GET", s.a.appHost(), "/v1/turnstile/frame", nil, nil)
	if r.status != 200 {
		t.Fatalf("frame: %d %s", r.status, r.body)
	}
	csp := r.header.Get("Content-Security-Policy")
	for _, want := range []string{"frame-ancestors *", "script-src 'nonce-", "https://challenges.cloudflare.com", "default-src 'none'"} {
		if !strings.Contains(csp, want) {
			t.Fatalf("frame CSP lacks %q: %s", want, csp)
		}
	}
	body := string(r.body)
	if !strings.Contains(body, turnstileTestSiteKey) || strings.Contains(body, turnstileTestSecret) {
		t.Fatal("frame must carry the site key and never the secret")
	}
	// Only on the app's own address.
	if r := s.a.at(t, "GET", s.host, "/v1/turnstile/frame", nil, nil); r.status != 404 {
		t.Fatalf("frame on a site host: %d", r.status)
	}
	// Pages other than the frame keep Cloudflare out of their CSP.
	if strings.Contains(s.a.at(t, "GET", s.a.appHost(), "/privacy.html", nil, nil).header.Get("Content-Security-Policy"), "cloudflare") {
		t.Fatal("privacy page allows Cloudflare")
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
	frame := strings.Replace(strings.Replace(page[start:end], "%s", `"`+turnstileTestSiteKey+`"`, 1), "%s", `"`+turnstileAction+`"`, 1)
	auth, err := os.ReadFile("static/auth.js")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	fixture, _ := json.Marshal(map[string]string{
		"frame": frame, "auth": string(auth),
		"cdata": turnstileCData("guest.ann.simple-host.app", "guestbook"),
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
