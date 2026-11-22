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

	appstorage "github.com/vsriram/simple-host/host/data"
	"github.com/vsriram/simple-host/host/httpx"
	rt "github.com/vsriram/simple-host/host/server/servertest/routetest"
)

// fakeSiteverify answers siteverify with answer(token), counting calls.
type fakeSiteverify struct {
	mu sync.Mutex
	n  int
}

func newFakeSiteverify(t *testing.T, answer func(token string) map[string]any) *fakeSiteverify {
	t.Helper()
	f := &fakeSiteverify{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		f.mu.Lock()
		f.n++
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(answer(r.PostForm.Get("response")))
	}))
	old := appstorage.TurnstileVerifyURL
	appstorage.TurnstileVerifyURL = srv.URL
	t.Cleanup(func() { appstorage.TurnstileVerifyURL = old; srv.Close() })
	return f
}

func (f *fakeSiteverify) calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.n
}

func TestTurnstileOffWithoutKeys(t *testing.T) {
	for _, c := range [][2]string{{"", ""}, {TurnstileTestSiteKey, ""}, {"", TurnstileTestSecret}} {
		if appstorage.NewTurnstileVerifier(c[0], c[1], TurnstileTestApp) != nil {
			t.Fatalf("on with key %v, secret %v", c[0] != "", c[1] != "")
		}
	}
	s := newShop(t)
	// auth.checked is not a binding while the spam check is off.
	if r := s.Must(t, "PUT", "/resources/guestbook", StorageRules("sqlite", RulesComments), s.Key, 400); r.Code() != "invalid_rules" {
		t.Fatalf("off: %s", r.Body)
	}
	if r := s.A.At(t, "GET", s.A.AppHost(), "/v1/turnstile/frame", nil, nil); r.Status != 404 {
		t.Fatalf("frame while off: %d", r.Status)
	}
}

// commentsSite has the spam check on, a comments-rules SQLite resource
// "guestbook" with table entries, and siteverify passing every token made
// by Shop.Token (for the host and resource in it).
func commentsSite(t *testing.T) (Shop, *fakeSiteverify) {
	t.Helper()
	s := newShop(t)
	app, _ := url.Parse(s.A.Srv.URL)
	f := newFakeSiteverify(t, func(token string) map[string]any {
		parts := strings.SplitN(strings.TrimPrefix(token, "ok-"), "|", 3)
		if !strings.HasPrefix(token, "ok-") || len(parts) < 2 {
			return map[string]any{"success": false, "error-codes": []string{"invalid-input-response"}}
		}
		return map[string]any{"success": true, "action": appstorage.TurnstileAction, "hostname": app.Hostname(),
			"cdata": appstorage.TurnstileCData(parts[0], parts[1]), "challenge_ts": time.Now().UTC().Format(time.RFC3339)}
	})
	s.A.Data.SetTurnstile(TurnstileTestSiteKey, TurnstileTestSecret, s.A.Srv.URL)
	s.A.Data.SpamIPLimiter = httpx.NewRateLimiter(100000, 0)
	s.A.Data.SpamSiteLimiter = httpx.NewRateLimiter(100000, 0)
	s.Must(t, "PUT", "/resources/guestbook", StorageRules("sqlite", RulesComments), s.Key, 201)
	s.Must(t, "POST", "/sqlite/guestbook/schema", map[string]string{"sql": "CREATE TABLE entries (id INTEGER PRIMARY KEY, name TEXT, message TEXT, created_at TEXT)"}, s.Key, 200)
	return s, f
}

func withToken(h map[string]string, token string) map[string]string {
	return rtCopy(h, appstorage.TurnstileHeader, token)
}

func TestCommentsRulesPolicy(t *testing.T) {
	s, f := commentsSite(t)
	anon := s.Callers["anon"]
	rows := "/sqlite/guestbook/tables/entries/rows"
	post := `{"name":"Ann","message":"Lovely shop"}`
	// No token: refused without calling siteverify.
	if r := s.Must(t, "POST", rows, post, anon, 403); r.Code() != "spam_check_required" || f.calls() != 0 {
		t.Fatalf("no token: %s (calls %d)", r.Body, f.calls())
	}
	s.Must(t, "POST", rows, post, withToken(anon, "bad-token"), 403)
	// A token solved for another resource or another site is refused.
	s.Must(t, "POST", rows, post, withToken(anon, "ok-"+s.Host+"|other|1"), 403)
	s.Must(t, "POST", rows, post, withToken(anon, "ok-other.ann."+rt.SiteDomain+"|guestbook|1"), 403)
	// A good token posts once only.
	tok := s.Token("guestbook")
	s.Must(t, "POST", rows, post, withToken(anon, tok), 200)
	s.Must(t, "POST", rows, post, withToken(anon, tok), 403)
	// A signed-in visitor needs the check too; the owner on the site does not.
	s.Must(t, "POST", rows, `{"name":"Alice","message":"hi"}`, s.Callers["alice"], 403)
	s.Must(t, "POST", rows, `{"name":"Owner","message":"thanks"}`, s.Callers["owner-site"], 200)
	list := s.Must(t, "GET", rows, nil, anon, 200)
	if !strings.Contains(string(list.Body), "Lovely shop") || strings.Contains(string(list.Body), s.AliceID) {
		t.Fatalf("anon read: %s", list.Body)
	}
	// Visitors never edit or delete, token or not; the owner deletes.
	s.Must(t, "DELETE", rows+"/1", nil, withToken(anon, s.Token("guestbook")), 403)
	s.Must(t, "PATCH", rows+"/1", `{"message":"spam"}`, withToken(anon, s.Token("guestbook")), 403)
	s.Must(t, "DELETE", rows+"/1", nil, s.Key, 200)
	// Post limits: size and links; the owner's own tools are not limited.
	big := `{"name":"Ann","message":"` + strings.Repeat("a", 5000) + `"}`
	s.Must(t, "POST", rows, big, withToken(anon, s.Token("guestbook")), 413)
	if r := s.Must(t, "POST", rows, `{"message":"see https:\/\/a.example and http://b.example and www.c.example"}`, withToken(anon, s.Token("guestbook")), 400); r.Code() != "too_many_links" {
		t.Fatalf("links: %s", r.Body)
	}
	s.Must(t, "POST", rows, big, s.Key, 200)
	// KV: a checked add of a new key; nobody overwrites one.
	s.Must(t, "PUT", "/resources/notes", StorageRules("kv", RulesComments), s.Key, 201)
	s.Must(t, "PUT", "/kv/notes/keys/k1", `{"value":"hello"}`, anon, 403)
	s.Must(t, "PUT", "/kv/notes/keys/k1", `{"value":"hello"}`, withToken(anon, s.Token("notes")), 200)
	if r := s.Do(t, "PUT", "/kv/notes/keys/k1", `{"value":"over"}`, withToken(anon, s.Token("notes"))); r.Status < 400 {
		t.Fatalf("overwrite: %d %s", r.Status, r.Body)
	}
	// Files never take checked.
	if r := s.Must(t, "PUT", "/resources/pics", StorageRules("files", RulesComments), s.Key, 400); r.Code() != "invalid_rules" {
		t.Fatalf("checked files: %s", r.Body)
	}
	// Siteverify down: refused, nothing saved.
	appstorage.TurnstileVerifyURL = "http://127.0.0.1:1/siteverify"
	if r := s.Must(t, "POST", rows, post, withToken(anon, s.Token("guestbook")), 403); r.Code() != "spam_check_unavailable" {
		t.Fatalf("down: %s", r.Body)
	}
}

func TestCommentsApproveFirst(t *testing.T) {
	s, _ := commentsSite(t)
	anon := s.Callers["anon"]
	rows := "/sqlite/guestbook/tables/entries/rows"
	has := func(h map[string]string, text string) bool {
		return strings.Contains(string(s.Must(t, "GET", rows, nil, h, 200).Body), text)
	}
	// Posts already there stay public when approve_first is turned on.
	s.Must(t, "POST", rows, `{"name":"Early","message":"first"}`, withToken(anon, s.Token("guestbook")), 200)
	s.Must(t, "PUT", "/resources/guestbook", StorageRules("sqlite", RulesCommentsApproveFirst), s.Key, 200)
	held := s.Must(t, "POST", rows, `{"name":"Late","message":"held"}`, withToken(anon, s.Token("guestbook")), 200).JSON(t)["last_insert_id"]
	if !has(anon, "first") || has(anon, "held") || !has(s.Callers["owner-site"], "held") {
		t.Fatal("held post visibility")
	}
	s.Must(t, "GET", rows+"/"+jsonString(held), nil, anon, 404)
	s.Must(t, "POST", "/sqlite/guestbook/execute", map[string]any{"sql": "UPDATE entries SET approved_at = datetime('now') WHERE id = ?", "params": []any{held}}, s.Key, 200)
	if !has(anon, "held") {
		t.Fatal("approved post not shown")
	}
	// Visitors cannot approve their own post; the owner on the site posts approved.
	s.Must(t, "POST", rows, `{"name":"Sneaky","message":"x","approved_at":"2026-01-01"}`, withToken(anon, s.Token("guestbook")), 200)
	s.Must(t, "POST", rows, `{"name":"Owner","message":"reply"}`, s.Callers["owner-site"], 200)
	if has(anon, "Sneaky") || !has(anon, "reply") {
		t.Fatal("approval stamps")
	}
}

func jsonString(v any) string { b, _ := json.Marshal(v); return string(b) }

// A checked add on one table's override needs the check; a rule that only
// mentions the text "auth.checked" does not.
func TestRulesSpamCheckHook(t *testing.T) {
	s, fake := commentsSite(t)
	s.Must(t, "PUT", "/resources/guestbook", `{"kind":"sqlite","rules":{"policies":[{"command":"read","using":"true"}]},"tables":{"entries":{"rules":{"policies":[{"command":"read","using":"true"},{"command":"add","check":"auth.checked"}]}}}}`, s.Key, 200)
	path := "/sqlite/guestbook/tables/entries/rows"
	if r := s.Must(t, "POST", path, `{"message":"unchecked"}`, s.Callers["anon"], 403); r.Code() != "spam_check_required" || fake.calls() != 0 {
		t.Fatalf("unchecked: %s calls=%d", r.Body, fake.calls())
	}
	s.Must(t, "POST", path, `{"message":"checked"}`, withToken(s.Callers["anon"], s.Token("guestbook")), 200)
	s.Must(t, "PUT", "/resources/guestbook", `{"kind":"sqlite","rules":{"policies":[{"command":"read","using":"true"},{"command":"add","check":"message == \"auth.checked\""}]},"tables":{}}`, s.Key, 200)
	s.Must(t, "POST", path, `{"message":"auth.checked"}`, withToken(s.Callers["anon"], s.Token("guestbook")), 200)
	if fake.calls() != 1 {
		t.Fatalf("spam check calls=%d", fake.calls())
	}
}

func TestTurnstileFrame(t *testing.T) {
	s, _ := commentsSite(t)
	r := s.A.At(t, "GET", s.A.AppHost(), "/v1/turnstile/frame", nil, nil)
	csp := r.Header.Get("Content-Security-Policy")
	if r.Status != 200 || !strings.Contains(csp, "frame-ancestors *") || !strings.Contains(csp, "https://challenges.cloudflare.com") {
		t.Fatalf("frame: %d %s", r.Status, csp)
	}
	if body := string(r.Body); !strings.Contains(body, TurnstileTestSiteKey) || strings.Contains(body, TurnstileTestSecret) {
		t.Fatal("frame must carry the site key and never the secret")
	}
	if r := s.A.At(t, "GET", s.Host, "/v1/turnstile/frame", nil, nil); r.Status != 404 {
		t.Fatalf("frame on a site host: %d", r.Status)
	}
}
