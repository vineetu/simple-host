package datatest

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vsriram/simple-host/host/data/datahttp"
	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/server/servertest"
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
	old := datahttp.TurnstileVerifyURL
	datahttp.TurnstileVerifyURL = f.srv.URL
	t.Cleanup(func() { datahttp.TurnstileVerifyURL = old; f.srv.Close() })
	return f
}

func (f *fakeSiteverify) calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.forms)
}

// passing is siteverify's answer for a token solved on host for resource.
func passing(app, host, resource string) map[string]any {
	return map[string]any{"success": true, "action": datahttp.TurnstileAction, "hostname": app,
		"cdata": datahttp.TurnstileCData(host, resource), "challenge_ts": time.Now().UTC().Format(time.RFC3339)}
}

func TestTurnstileOffWithoutKeys(t *testing.T) {
	for _, c := range [][2]string{{"", ""}, {servertest.TurnstileTestSiteKey, ""}, {"", servertest.TurnstileTestSecret}} {
		if datahttp.NewTurnstileVerifier(c[0], c[1], servertest.TurnstileTestApp) != nil {
			t.Fatalf("on with key %v, secret %v", c[0] != "", c[1] != "")
		}
	}
	s := servertest.NewStorageTestSite(t)
	r := s.Must(t, "PUT", "/resources/guestbook", servertest.StorageRules("sqlite", servertest.RulesComments), s.Key, 400)
	// Rules: auth.checked is not a binding while the spam check is off.
	if r.JSON(t)["code"] != "invalid_rules" || !strings.Contains(string(r.Body), "auth.checked") {
		t.Fatalf("off: %s", r.Body)
	}
	if r := s.A.At(t, "GET", s.A.AppHost(), "/v1/turnstile/frame", nil, nil); r.Status != 404 {
		t.Fatalf("frame while off: %d", r.Status)
	}
}

// commentsSite is a site with the spam check on, a comments-rules SQLite
// resource "guestbook" with table entries, and siteverify answering
// passing() for every token that starts with "ok-".
func commentsSite(t *testing.T) (servertest.StorageTestSite, *fakeSiteverify) {
	t.Helper()
	s := servertest.NewStorageTestSite(t)
	app, _ := url.Parse(s.A.Srv.URL)
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
	s.A.Data.SetTurnstile(servertest.TurnstileTestSiteKey, servertest.TurnstileTestSecret, s.A.Srv.URL)
	s.A.Data.SpamIPLimiter = httpx.NewRateLimiter(100000, 0)
	s.A.Data.SpamSiteLimiter = httpx.NewRateLimiter(100000, 0)
	put := s.Must(t, "PUT", "/resources/guestbook", servertest.StorageRules("sqlite", servertest.RulesComments), s.Key, 201).JSON(t)
	if put["rules_status"] != "rules" {
		t.Fatalf("comments rules: %v", put)
	}
	s.Must(t, "POST", "/sqlite/guestbook/schema", map[string]string{"sql": "CREATE TABLE entries (id INTEGER PRIMARY KEY, name TEXT, message TEXT, created_at TEXT)"}, s.Key, 200)
	return s, f
}

func withToken(h map[string]string, token string) map[string]string {
	out := map[string]string{}
	for k, v := range h {
		out[k] = v
	}
	out[datahttp.TurnstileHeader] = token
	return out
}

func TestCommentsRulesPolicy(t *testing.T) {
	s, f := commentsSite(t)
	anon := s.Callers["anon"]
	rows := "/sqlite/guestbook/tables/entries/rows"
	post := `{"name":"Ann","message":"Lovely shop"}`
	// No token: refused, and siteverify is not called.
	r := s.Must(t, "POST", rows, post, anon, 403)
	if !strings.Contains(string(r.Body), "spam_check_required") || f.calls() != 0 {
		t.Fatalf("no token: %s (calls %d)", r.Body, f.calls())
	}
	s.Must(t, "POST", rows, post, withToken(anon, "bad-token"), 403)
	// A token solved for another resource, or another site, is refused.
	s.Must(t, "POST", rows, post, withToken(anon, "ok-"+s.Host+"|other|1"), 403)
	s.Must(t, "POST", rows, post, withToken(anon, "ok-other.ann.simple-host.test|guestbook|1"), 403)
	// A good token posts once; the same token again is refused.
	tok := s.Token("guestbook")
	s.Must(t, "POST", rows, post, withToken(anon, tok), 200)
	s.Must(t, "POST", rows, post, withToken(anon, tok), 403)
	// A signed-in visitor needs the check too; the owner on the site does not.
	s.Must(t, "POST", rows, `{"name":"Alice","message":"hi"}`, s.Callers["alice"], 403)
	s.Must(t, "POST", rows, `{"name":"Alice","message":"hi"}`, withToken(s.Callers["alice"], s.Token("guestbook")), 200)
	s.Must(t, "POST", rows, `{"name":"Owner","message":"thanks"}`, s.Callers["owner-site"], 200)
	// Anyone reads.
	list := s.Must(t, "GET", rows, nil, anon, 200)
	if !strings.Contains(string(list.Body), "Lovely shop") || !strings.Contains(string(list.Body), "thanks") {
		t.Fatalf("anon read: %s", list.Body)
	}
	if strings.Contains(string(list.Body), s.AliceID) {
		t.Fatalf("anon read leaks visitor_id: %s", list.Body)
	}
	// Visitors never edit or delete, token or not; the owner deletes.
	id := itemsFirstID(t, list)
	s.Must(t, "DELETE", rows+"/"+id, nil, withToken(anon, s.Token("guestbook")), 403)
	s.Must(t, "PATCH", rows+"/"+id, `{"message":"spam"}`, withToken(anon, s.Token("guestbook")), 403)
	s.Must(t, "DELETE", rows+"/"+id, nil, s.Callers["alice"], 403)
	s.Must(t, "DELETE", rows+"/"+id, nil, s.Key, 200)
	// Post limits: size and links.
	big := `{"name":"Ann","message":"` + strings.Repeat("a", 5000) + `"}`
	s.Must(t, "POST", rows, big, withToken(anon, s.Token("guestbook")), 413)
	links := `{"name":"Ann","message":"see https:\/\/a.example and http://b.example and www.c.example"}`
	r = s.Must(t, "POST", rows, links, withToken(anon, s.Token("guestbook")), 400)
	if !strings.Contains(string(r.Body), "too_many_links") {
		t.Fatalf("links: %s", r.Body)
	}
	// The owner's own tools are not limited.
	s.Must(t, "POST", rows, big, s.Key, 200)
	// KV: anyone adds a new key with the check; nobody overwrites one.
	s.Must(t, "PUT", "/resources/notes", servertest.StorageRules("kv", servertest.RulesComments), s.Key, 201)
	s.Must(t, "PUT", "/kv/notes/keys/k1", `{"value":"hello"}`, anon, 403)
	s.Must(t, "PUT", "/kv/notes/keys/k1", `{"value":"hello"}`, withToken(anon, s.Token("notes")), 200)
	if r := s.Do(t, "PUT", "/kv/notes/keys/k1", `{"value":"over"}`, withToken(anon, s.Token("notes"))); r.Status < 400 {
		t.Fatalf("overwrite: %d %s", r.Status, r.Body)
	}
	s.Must(t, "GET", "/kv/notes/keys/k1", nil, anon, 200)
	s.Must(t, "DELETE", "/kv/notes/keys/k1", nil, anon, 403)
	s.Must(t, "DELETE", "/kv/notes/keys/k1", nil, s.Key, 200)
	// Files never take checked: uploads are never spam-checked.
	if r := s.Must(t, "PUT", "/resources/pics", servertest.StorageRules("files", servertest.RulesComments), s.Key, 400); r.JSON(t)["code"] != "invalid_rules" {
		t.Fatalf("checked files: %s", r.Body)
	}
	// Siteverify down: refused, nothing saved.
	datahttp.TurnstileVerifyURL = "http://127.0.0.1:1/siteverify"
	r = s.Must(t, "POST", rows, post, withToken(anon, s.Token("guestbook")), 403)
	if !strings.Contains(string(r.Body), "spam_check_unavailable") {
		t.Fatalf("down: %s", r.Body)
	}
}

func itemsFirstID(t *testing.T, r servertest.Resp) string {
	t.Helper()
	var out struct {
		Columns []string `json:"columns"`
		Rows    [][]any  `json:"rows"`
	}
	if err := json.Unmarshal(r.Body, &out); err != nil || len(out.Rows) == 0 {
		t.Fatalf("rows: %s", r.Body)
	}
	for i, c := range out.Columns {
		if c == "id" {
			return jsonString(out.Rows[0][i])
		}
	}
	t.Fatalf("no id column: %s", r.Body)
	return ""
}

func jsonString(v any) string { b, _ := json.Marshal(v); return string(b) }

func TestCommentsApproveFirst(t *testing.T) {
	s, _ := commentsSite(t)
	anon := s.Callers["anon"]
	rows := "/sqlite/guestbook/tables/entries/rows"
	// Posts already there stay public when approve_first is turned on.
	s.Must(t, "POST", rows, `{"name":"Early","message":"first"}`, withToken(anon, s.Token("guestbook")), 200)
	put := s.Must(t, "PUT", "/resources/guestbook", servertest.StorageRules("sqlite", servertest.RulesCommentsApproveFirst), s.Key, 200).JSON(t)
	if put["rules_status"] != "rules" {
		t.Fatalf("approve_first: %v", put)
	}
	s.Must(t, "POST", rows, `{"name":"Late","message":"held"}`, withToken(anon, s.Token("guestbook")), 200)
	list := s.Must(t, "GET", rows, nil, anon, 200)
	if !strings.Contains(string(list.Body), "first") || strings.Contains(string(list.Body), "held") {
		t.Fatalf("visitor sees: %s", list.Body)
	}
	// The owner sees the held post and its id; a visitor cannot reach it.
	q := s.Must(t, "POST", "/sqlite/guestbook/query", map[string]any{"sql": "SELECT id FROM entries WHERE approved_at IS NULL", "params": []any{}}, s.Key, 200)
	var held struct {
		Rows [][]any `json:"rows"`
	}
	if json.Unmarshal(q.Body, &held) != nil || len(held.Rows) != 1 {
		t.Fatalf("held rows: %s", q.Body)
	}
	id := jsonString(held.Rows[0][0])
	s.Must(t, "GET", rows+"/"+id, nil, anon, 404)
	if !strings.Contains(string(s.Must(t, "GET", rows, nil, s.Callers["owner-site"], 200).Body), "held") {
		t.Fatal("owner on the site does not see the held post")
	}
	// The owner approves with their tools; then everyone sees it.
	s.Must(t, "POST", "/sqlite/guestbook/execute", map[string]any{"sql": "UPDATE entries SET approved_at = datetime('now') WHERE id = ?", "params": []any{held.Rows[0][0]}}, s.Key, 200)
	if !strings.Contains(string(s.Must(t, "GET", rows, nil, anon, 200).Body), "held") {
		t.Fatal("approved post not shown")
	}
	// Visitors cannot set approved_at themselves on a held post.
	s.Must(t, "POST", rows, `{"name":"Sneaky","message":"x","approved_at":"2026-01-01"}`, withToken(anon, s.Token("guestbook")), 200)
	if strings.Contains(string(s.Must(t, "GET", rows, nil, anon, 200).Body), "Sneaky") {
		t.Fatal("a visitor approved their own post")
	}
	// The owner signed in on the site posts approved.
	s.Must(t, "POST", rows, `{"name":"Owner","message":"reply"}`, s.Callers["owner-site"], 200)
	if !strings.Contains(string(s.Must(t, "GET", rows, nil, anon, 200).Body), "reply") {
		t.Fatal("owner's post held")
	}
}

func TestTurnstileFrame(t *testing.T) {
	s, _ := commentsSite(t)
	r := s.A.At(t, "GET", s.A.AppHost(), "/v1/turnstile/frame", nil, nil)
	if r.Status != 200 {
		t.Fatalf("frame: %d %s", r.Status, r.Body)
	}
	csp := r.Header.Get("Content-Security-Policy")
	for _, want := range []string{"frame-ancestors *", "script-src 'nonce-", "https://challenges.cloudflare.com", "default-src 'none'"} {
		if !strings.Contains(csp, want) {
			t.Fatalf("frame CSP lacks %q: %s", want, csp)
		}
	}
	body := string(r.Body)
	if !strings.Contains(body, servertest.TurnstileTestSiteKey) || strings.Contains(body, servertest.TurnstileTestSecret) {
		t.Fatal("frame must carry the site key and never the secret")
	}
	// Only on the app's own address.
	if r := s.A.At(t, "GET", s.Host, "/v1/turnstile/frame", nil, nil); r.Status != 404 {
		t.Fatalf("frame on a site host: %d", r.Status)
	}
	// Pages other than the frame keep Cloudflare out of their CSP.
	if strings.Contains(s.A.At(t, "GET", s.A.AppHost(), "/privacy.html", nil, nil).Header.Get("Content-Security-Policy"), "cloudflare") {
		t.Fatal("privacy page allows Cloudflare")
	}
}
