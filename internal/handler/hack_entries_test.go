package handler

import (
	"bytes"
	"context"
	"database/sql"
	"image"
	"image/jpeg"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	db "github.com/vsriram/simple-host/internal/db"
)

// Entries (M2): a participant's own entry and screenshot, and the organiser
// and judge list. newHackApp does not turn hack mode on; these routes do
// not need it.

type hackEntryWorld struct {
	a     *hackApp
	slug  string
	org   person
	p1    person // team Alpha
	p2    person // team zeta
	lone  person // participant on no team
	judge person
	alpha string
	zeta  string
}

func newHackEntryWorld(t *testing.T) *hackEntryWorld {
	t.Helper()
	a := newHackApp(t)
	org := a.newPerson(t, "eo")
	p1 := a.newPerson(t, "e1")
	p2 := a.newPerson(t, "e2")
	lone := a.newPerson(t, "e0")
	judge := a.newPerson(t, "ej")
	slug := uniqueSlug()
	if r := a.createEvent(t, org, slug, nil); r.status != http.StatusCreated {
		t.Fatalf("create: %d %s", r.status, r.body)
	}
	a.openEvent(t, org, slug)
	ev := a.at(t, "GET", "/v1/hack/events/"+slug, nil, a.key(org)).json(t)
	orgView := ev["organiser"].(map[string]any)
	join := orgView["join_code"].(string)
	jcode := orgView["judge_code"].(string)
	joinAs := func(p person, name string, asJudge bool) {
		t.Helper()
		path := "/v1/hack/join/" + join
		if asJudge {
			path = "/v1/hack/judge/" + jcode
		}
		r := a.at(t, "POST", path, map[string]any{"accept_coc": true, "display_name": name}, a.key(p))
		if r.status != 200 {
			t.Fatalf("join %s: %d %s", name, r.status, r.body)
		}
	}
	joinAs(p1, "Ada One", false)
	joinAs(p2, "Bea Two", false)
	joinAs(lone, "Lonely", false)
	joinAs(judge, "Jude", true)
	// zeta is created first so name order is not creation order.
	zeta := makeEntryTeam(t, a, slug, p2, "zeta")
	alpha := makeEntryTeam(t, a, slug, p1, "Alpha")
	return &hackEntryWorld{a, slug, org, p1, p2, lone, judge, alpha, zeta}
}

func makeEntryTeam(t *testing.T, a *hackApp, slug string, p person, name string) string {
	t.Helper()
	r := a.at(t, "POST", "/v1/hack/events/"+slug+"/teams", map[string]string{"name": name}, a.key(p))
	if r.status != http.StatusCreated {
		t.Fatalf("team %s: %d %s", name, r.status, r.body)
	}
	return r.json(t)["slug"].(string)
}

func (a *hackApp) rawBody(t *testing.T, method, path string, body []byte, contentType string, headers map[string]string) resp {
	t.Helper()
	req, err := http.NewRequest(method, a.srv.URL+path, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return resp{res.StatusCode, res.Header, b}
}

func (w *hackEntryWorld) entryPath() string {
	return "/v1/hack/events/" + w.slug + "/entry"
}

func jsonStrings(t *testing.T, v any) []string {
	t.Helper()
	raw, ok := v.([]any)
	if !ok {
		t.Fatalf("want array, got %#v", v)
	}
	out := make([]string, len(raw))
	for i, item := range raw {
		s, ok := item.(string)
		if !ok {
			t.Fatalf("want string, got %#v", item)
		}
		out[i] = s
	}
	return out
}

func wantJSONKeys(t *testing.T, what string, m map[string]any, keys ...string) {
	t.Helper()
	got := make([]string, 0, len(m))
	for k := range m {
		got = append(got, k)
	}
	slices.Sort(got)
	want := append([]string(nil), keys...)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("%s keys %v, want %v", what, got, want)
	}
}

func wantEntryShape(t *testing.T, body map[string]any) map[string]any {
	t.Helper()
	wantJSONKeys(t, "entry response", body, "entry", "required", "complete", "missing", "editable", "refusal", "deadline")
	e, ok := body["entry"].(map[string]any)
	if !ok {
		t.Fatalf("entry: %#v", body["entry"])
	}
	wantJSONKeys(t, "entry", e, "title", "tagline", "description", "video_url", "code_url", "has_screenshot", "updated_at", "updated_by")
	return e
}

func rfc3339Field(t *testing.T, v any) time.Time {
	t.Helper()
	s, ok := v.(string)
	if !ok || !strings.HasSuffix(s, "Z") {
		t.Fatalf("want RFC3339 UTC, got %#v", v)
	}
	tm, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatal(err)
	}
	return tm
}

var (
	pngMagic  = pngOfSize(16, 10)
	jpegMagic = tinyJPEG()
	webpMagic = []byte("RIFF\x16\x00\x00\x00WEBPVP8X\x0a\x00\x00\x00\x00\x00\x00\x00\x0f\x00\x00\x09\x00\x00")
	gifMagic  = []byte("GIF89a")
	svgBody   = []byte(`<svg xmlns="http://www.w3.org/2000/svg"><text>x</text></svg>`)
)

func wantImageHeaders(t *testing.T, r resp, mime string, body []byte) {
	t.Helper()
	if r.status != http.StatusOK {
		t.Fatalf("image: %d %s", r.status, r.body)
	}
	checks := []struct{ k, v string }{
		{"Content-Type", mime},
		{"X-Content-Type-Options", "nosniff"},
		{"Content-Security-Policy", "default-src 'none'; sandbox"},
		{"Cache-Control", "private, no-store"},
		{"Content-Disposition", "inline"},
	}
	for _, c := range checks {
		if got := r.header.Get(c.k); got != c.v {
			t.Fatalf("%s %q, want %q", c.k, got, c.v)
		}
	}
	if !bytes.Equal(r.body, body) {
		t.Fatalf("image body %d bytes, want %d", len(r.body), len(body))
	}
}

func TestHackEntryReadUpdateAndValidation(t *testing.T) {
	w := newHackEntryWorld(t)
	a := w.a

	r := a.at(t, "GET", w.entryPath(), nil, a.key(w.p1))
	if r.status != 200 {
		t.Fatalf("empty: %d %s", r.status, r.body)
	}
	body := r.json(t)
	e := wantEntryShape(t, body)
	if e["title"] != "" || e["tagline"] != "" || e["description"] != "" || e["video_url"] != "" || e["code_url"] != "" {
		t.Fatalf("empty text: %#v", e)
	}
	if e["has_screenshot"] != false || e["updated_at"] != nil || e["updated_by"] != "" {
		t.Fatalf("empty meta: %#v", e)
	}
	if !slices.Equal(jsonStrings(t, body["required"]), []string{"title"}) || body["complete"] != false ||
		!slices.Equal(jsonStrings(t, body["missing"]), []string{"title"}) {
		t.Fatalf("empty status: %#v", body)
	}
	if body["editable"] != true || body["refusal"] != nil || body["deadline"] != nil {
		t.Fatalf("empty write state: %#v", body)
	}

	// A person with no team, and roles that are not a participant, never get an entry.
	wantCode(t, "no team", a.at(t, "GET", w.entryPath(), nil, a.key(w.lone)), 409, "no_team")
	wantCode(t, "no team put", a.at(t, "PUT", w.entryPath(), map[string]string{"title": "X"}, a.key(w.lone)), 409, "no_team")
	wantCode(t, "organiser entry", a.at(t, "GET", w.entryPath(), nil, a.key(w.org)), 404, "event_not_found")
	wantCode(t, "judge entry", a.at(t, "PUT", w.entryPath(), map[string]string{"title": "X"}, a.key(w.judge)), 404, "event_not_found")
	wantCode(t, "admin write", a.at(t, "PUT", w.entryPath(), map[string]string{"title": "X"}, a.adminH()), 404, "event_not_found")
	stranger := a.newPerson(t, "ez")
	wantCode(t, "stranger", a.at(t, "GET", w.entryPath(), nil, a.key(stranger)), 404, "event_not_found")

	r = a.at(t, "PUT", w.entryPath(), map[string]string{"title": "  Hello   World  "}, a.key(w.p1))
	if r.status != 200 {
		t.Fatalf("put title: %d %s", r.status, r.body)
	}
	body = r.json(t)
	e = wantEntryShape(t, body)
	if e["title"] != "Hello World" || e["tagline"] != "" || e["updated_by"] != "Ada One" || e["has_screenshot"] != false {
		t.Fatalf("after title: %#v", e)
	}
	if e["updated_at"] == nil {
		t.Fatal("updated_at not set")
	}
	rfc3339Field(t, e["updated_at"])
	if body["complete"] != true || !slices.Equal(jsonStrings(t, body["missing"]), []string{}) {
		t.Fatalf("complete: %#v", body)
	}

	// A later partial write changes only the fields it sends.
	r = a.at(t, "PUT", w.entryPath(), map[string]any{
		"tagline":     "A short line",
		"description": "line1\nline2",
		"video_url":   "https://example.com/watch?v=1",
	}, a.key(w.p1))
	if r.status != 200 {
		t.Fatalf("partial: %d %s", r.status, r.body)
	}
	e = wantEntryShape(t, r.json(t))
	if e["title"] != "Hello World" || e["tagline"] != "A short line" || e["description"] != "line1\nline2" ||
		e["video_url"] != "https://example.com/watch?v=1" || e["code_url"] != "" {
		t.Fatalf("partial kept: %#v", e)
	}
	r = a.at(t, "PUT", w.entryPath(), map[string]string{"code_url": "http://code.example/repo"}, a.key(w.p1))
	e = wantEntryShape(t, r.json(t))
	if r.status != 200 || e["code_url"] != "http://code.example/repo" || e["video_url"] != "https://example.com/watch?v=1" {
		t.Fatalf("code url: %d %#v", r.status, e)
	}
	r = a.at(t, "PUT", w.entryPath(), map[string]string{"title": "", "video_url": ""}, a.key(w.p1))
	body = r.json(t)
	e = wantEntryShape(t, body)
	if e["title"] != "" || e["video_url"] != "" || e["tagline"] != "A short line" || body["complete"] != false ||
		!slices.Equal(jsonStrings(t, body["missing"]), []string{"title"}) {
		t.Fatalf("cleared: %#v %v", e, body["missing"])
	}

	longURL := "https://example.com/" + strings.Repeat("a", 500-len("https://example.com/"))
	if r := a.at(t, "PUT", w.entryPath(), map[string]string{"video_url": longURL}, a.key(w.p1)); r.status != 200 {
		t.Fatalf("500 char url: %d %s", r.status, r.body)
	}
	tooLongURL := longURL + "b"

	cases := []struct {
		name string
		body map[string]string
		code string
		msg  string
	}{
		{"title length", map[string]string{"title": strings.Repeat("a", 81)}, "invalid_title", ""},
		{"title line", map[string]string{"title": "a\nb"}, "invalid_title", ""},
		{"tagline length", map[string]string{"tagline": strings.Repeat("a", 161)}, "invalid_tagline", ""},
		{"description length", map[string]string{"description": strings.Repeat("a", 5001)}, "invalid_description", ""},
		{"video space", map[string]string{"video_url": "https://example.com/a b"}, "invalid_url", "video_url"},
		{"video scheme", map[string]string{"video_url": "javascript:alert(1)"}, "invalid_url", "video_url"},
		{"video ftp", map[string]string{"video_url": "ftp://example.com/a"}, "invalid_url", "video_url"},
		{"video relative", map[string]string{"video_url": "example.com"}, "invalid_url", "video_url"},
		{"video host", map[string]string{"video_url": "https://"}, "invalid_url", "video_url"},
		{"video length", map[string]string{"video_url": tooLongURL}, "invalid_url", "video_url"},
		{"video control", map[string]string{"video_url": "https://example.com/\x01"}, "invalid_url", "video_url"},
		{"code space", map[string]string{"code_url": "http://example.com/a b"}, "invalid_url", "code_url"},
	}
	for _, c := range cases {
		r := a.at(t, "PUT", w.entryPath(), c.body, a.key(w.p1))
		wantCode(t, c.name, r, 400, c.code)
		if c.msg != "" && !strings.Contains(r.json(t)["error"].(string), c.msg) {
			t.Fatalf("%s message %q does not name %s", c.name, r.json(t)["error"], c.msg)
		}
	}
	// A rejected write leaves the stored entry alone.
	e = wantEntryShape(t, a.at(t, "GET", w.entryPath(), nil, a.key(w.p1)).json(t))
	if e["tagline"] != "A short line" || e["video_url"] != longURL || e["title"] != "" {
		t.Fatalf("rejected write changed the entry: %#v", e)
	}
	// The boundary lengths that are still allowed.
	okBody := map[string]string{
		"title":       strings.Repeat("a", 80),
		"tagline":     strings.Repeat("b", 160),
		"description": strings.Repeat("c", 5000),
	}
	if r := a.at(t, "PUT", w.entryPath(), okBody, a.key(w.p1)); r.status != 200 {
		t.Fatalf("limits: %d %s", r.status, r.body)
	}
}

func TestHackEntryIsolation(t *testing.T) {
	w := newHackEntryWorld(t)
	a := w.a
	if r := a.at(t, "PUT", w.entryPath(), map[string]string{"title": "Secret Title"}, a.key(w.p1)); r.status != 200 {
		t.Fatalf("put: %d %s", r.status, r.body)
	}
	png := append([]byte(nil), pngMagic...)
	if r := a.rawBody(t, "PUT", w.entryPath()+"/screenshot", png, "image/svg+xml", a.key(w.p1)); r.status != 200 {
		t.Fatalf("png: %d %s", r.status, r.body)
	}

	// The other team only ever sees its own entry.
	r := a.at(t, "GET", w.entryPath(), nil, a.key(w.p2))
	e := wantEntryShape(t, r.json(t))
	if r.status != 200 || e["title"] != "" || e["has_screenshot"] != false || strings.Contains(string(r.body), "Secret Title") {
		t.Fatalf("other team saw the entry: %d %s", r.status, r.body)
	}
	if r := a.at(t, "PUT", w.entryPath(), map[string]string{"title": "Stolen"}, a.key(w.p2)); r.status != 200 || r.json(t)["entry"].(map[string]any)["title"] != "Stolen" {
		t.Fatalf("other put: %d %s", r.status, r.body)
	}
	e = wantEntryShape(t, a.at(t, "GET", w.entryPath(), nil, a.key(w.p1)).json(t))
	if e["title"] != "Secret Title" || e["has_screenshot"] != true || e["updated_by"] != "Ada One" {
		t.Fatalf("first team changed: %#v", e)
	}
	wantCode(t, "other screenshot", a.rawBody(t, "GET", w.entryPath()+"/screenshot", nil, "", a.key(w.p2)), 404, "no_screenshot")
	wantCode(t, "other team path", a.at(t, "GET", "/v1/hack/events/"+w.slug+"/teams/"+w.alpha+"/screenshot", nil, a.key(w.p2)), 404, "event_not_found")
	shot := a.rawBody(t, "GET", w.entryPath()+"/screenshot", nil, "", a.key(w.p1))
	wantImageHeaders(t, shot, "image/png", png)
	if strings.Contains(string(a.at(t, "GET", w.entryPath(), nil, a.key(w.p1)).body), w.p2.email) ||
		strings.Contains(string(a.at(t, "GET", w.entryPath(), nil, a.key(w.p1)).body), a.userID(t, w.p2)) {
		t.Fatal("entry leaked an email or account id")
	}
}

func TestHackEntryScreenshot(t *testing.T) {
	w := newHackEntryWorld(t)
	a := w.a
	a.hack.shotWrites = newRateLimiter(1000, 1000) // this test uploads many times in a row
	path := w.entryPath() + "/screenshot"
	if r := a.at(t, "PUT", w.entryPath(), map[string]string{"title": "Kept"}, a.key(w.p1)); r.status != 200 {
		t.Fatalf("title: %d %s", r.status, r.body)
	}
	wantCode(t, "none yet", a.rawBody(t, "GET", path, nil, "", a.key(w.p1)), 404, "no_screenshot")

	refused := []struct {
		name, ctype string
		body        []byte
		status      int
		code        string
	}{
		{"svg", "image/svg+xml", svgBody, 415, "unsupported_image"},
		{"gif", "image/gif", gifMagic, 415, "unsupported_image"},
		{"gif as png", "image/png", gifMagic, 415, "unsupported_image"},
		{"text", "text/plain", []byte("hello this is not an image"), 415, "unsupported_image"},
		{"empty", "application/octet-stream", []byte{}, 415, "unsupported_image"},
	}
	for _, c := range refused {
		r := a.rawBody(t, "PUT", path, c.body, c.ctype, a.key(w.p1))
		wantCode(t, c.name, r, c.status, c.code)
	}
	over := make([]byte, (2<<20)+1)
	copy(over, pngMagic)
	wantCode(t, "too big", a.rawBody(t, "PUT", path, over, "image/png", a.key(w.p1)), 413, "image_too_large")
	wantCode(t, "still none", a.rawBody(t, "GET", path, nil, "", a.key(w.p1)), 404, "no_screenshot")

	exact := make([]byte, 2<<20)
	copy(exact, pngMagic)
	r := a.rawBody(t, "PUT", path, exact, "image/svg+xml", a.key(w.p1))
	if r.status != 200 || r.json(t)["entry"].(map[string]any)["has_screenshot"] != true {
		t.Fatalf("exact png: %d %s", r.status, r.body)
	}
	wantImageHeaders(t, a.rawBody(t, "GET", path, nil, "", a.key(w.p1)), "image/png", exact)

	// A refused replacement does not remove the stored image.
	wantCode(t, "gif after", a.rawBody(t, "PUT", path, gifMagic, "image/png", a.key(w.p1)), 415, "unsupported_image")
	bigger := make([]byte, (2<<20)+2)
	copy(bigger, pngMagic)
	wantCode(t, "bigger after", a.rawBody(t, "PUT", path, bigger, "image/png", a.key(w.p1)), 413, "image_too_large")
	wantImageHeaders(t, a.rawBody(t, "GET", path, nil, "", a.key(w.p1)), "image/png", exact)

	jpeg := append([]byte(nil), jpegMagic...)
	if r := a.rawBody(t, "PUT", path, jpeg, "text/plain", a.key(w.p1)); r.status != 200 {
		t.Fatalf("jpeg: %d %s", r.status, r.body)
	}
	wantImageHeaders(t, a.rawBody(t, "GET", path, nil, "", a.key(w.p1)), "image/jpeg", jpeg)
	webp := append([]byte(nil), webpMagic...)
	if r := a.rawBody(t, "PUT", path, webp, "image/gif", a.key(w.p1)); r.status != 200 {
		t.Fatalf("webp: %d %s", r.status, r.body)
	}
	wantImageHeaders(t, a.rawBody(t, "GET", path, nil, "", a.key(w.p1)), "image/webp", webp)

	r = a.at(t, "DELETE", path, nil, a.key(w.p1))
	e := wantEntryShape(t, r.json(t))
	if r.status != 200 || e["has_screenshot"] != false || e["title"] != "Kept" {
		t.Fatalf("delete: %d %#v", r.status, e)
	}
	wantCode(t, "gone", a.rawBody(t, "GET", path, nil, "", a.key(w.p1)), 404, "no_screenshot")

	png := append([]byte(nil), pngMagic...)
	if r := a.rawBody(t, "PUT", path, png, "application/octet-stream", a.key(w.p1)); r.status != 200 {
		t.Fatalf("png again: %d %s", r.status, r.body)
	}
	teamPath := "/v1/hack/events/" + w.slug + "/teams/" + w.alpha + "/screenshot"
	wantImageHeaders(t, a.rawBody(t, "GET", teamPath, nil, "", a.key(w.org)), "image/png", png)
	wantImageHeaders(t, a.rawBody(t, "GET", teamPath, nil, "", a.key(w.judge)), "image/png", png)
	wantImageHeaders(t, a.rawBody(t, "GET", teamPath, nil, "", a.adminH()), "image/png", png)
	wantCode(t, "participant team path", a.at(t, "GET", teamPath, nil, a.key(w.p1)), 404, "event_not_found")
	wantCode(t, "other team path", a.at(t, "GET", teamPath, nil, a.key(w.p2)), 404, "event_not_found")
	wantCode(t, "missing team", a.at(t, "GET", "/v1/hack/events/"+w.slug+"/teams/no-such-team/screenshot", nil, a.key(w.org)), 404, "team_not_found")
	wantCode(t, "lone screenshot", a.rawBody(t, "PUT", path, png, "image/png", a.key(w.lone)), 409, "no_team")
	wantCode(t, "lone delete", a.at(t, "DELETE", path, nil, a.key(w.lone)), 409, "no_team")
}

func TestHackEntriesList(t *testing.T) {
	w := newHackEntryWorld(t)
	a := w.a
	if r := a.at(t, "PATCH", "/v1/hack/events/"+w.slug, map[string]any{
		"entry_required": []string{"screenshot", "title", "video_url", "tagline", "code_url", "description"},
	}, a.key(w.org)); r.status != 200 {
		t.Fatalf("required: %d %s", r.status, r.body)
	}
	if r := a.at(t, "PUT", w.entryPath(), map[string]string{
		"title": "Alpha title", "video_url": "https://example.com/a",
	}, a.key(w.p1)); r.status != 200 {
		t.Fatalf("alpha entry: %d %s", r.status, r.body)
	}
	var accountID string
	if err := a.database.QueryRow(`SELECT account_id FROM events WHERE slug = $1`, w.slug).Scan(&accountID); err != nil {
		t.Fatal(err)
	}
	if _, err := a.database.Exec(`INSERT INTO sites (user_id, name, site_url, active_version) VALUES ($1, $2, $3, $4)`,
		accountID, w.alpha, "https://example.test/", 4); err != nil {
		t.Fatal(err)
	}
	if _, err := a.database.Exec(`INSERT INTO sites (user_id, name, site_url, deleted_at) VALUES ($1, $2, $3, now())`,
		accountID, w.zeta, "https://example.test/old"); err != nil {
		t.Fatal(err)
	}

	wantCode(t, "participant list", a.at(t, "GET", "/v1/hack/events/"+w.slug+"/entries", nil, a.key(w.p1)), 404, "event_not_found")
	stranger := a.newPerson(t, "ez")
	wantCode(t, "stranger list", a.at(t, "GET", "/v1/hack/events/"+w.slug+"/entries", nil, a.key(stranger)), 404, "event_not_found")

	check := func(who string, headers map[string]string) {
		t.Helper()
		r := a.at(t, "GET", "/v1/hack/events/"+w.slug+"/entries", nil, headers)
		if r.status != 200 {
			t.Fatalf("%s list: %d %s", who, r.status, r.body)
		}
		if strings.Contains(string(r.body), w.p1.email) || strings.Contains(string(r.body), w.p2.email) ||
			strings.Contains(string(r.body), a.userID(t, w.p1)) {
			t.Fatalf("%s list leaked an email or account id: %s", who, r.body)
		}
		body := r.json(t)
		if !slices.Equal(jsonStrings(t, body["required"]), []string{"title", "tagline", "description", "video_url", "code_url", "screenshot"}) {
			t.Fatalf("%s required: %#v", who, body["required"])
		}
		rows, ok := body["entries"].([]any)
		if !ok || len(rows) != 2 {
			t.Fatalf("%s entries: %#v", who, body["entries"])
		}
		first := rows[0].(map[string]any)
		second := rows[1].(map[string]any)
		wantJSONKeys(t, "list item", first,
			"team", "title", "tagline", "description", "video_url", "code_url", "has_screenshot",
			"complete", "missing", "updated_at", "site_url", "site_exists", "site_taken_down", "deadline", "frozen",
			"pinned_version", "pinned_url")
		team := first["team"].(map[string]any)
		wantJSONKeys(t, "team", team, "id", "slug", "name")
		if team["slug"] != w.alpha || team["name"] != "Alpha" {
			t.Fatalf("%s first team: %#v", who, team)
		}
		if second["team"].(map[string]any)["name"] != "zeta" {
			t.Fatalf("%s second team: %#v", who, second["team"])
		}
		if first["title"] != "Alpha title" || first["video_url"] != "https://example.com/a" || first["complete"] != false ||
			!slices.Equal(jsonStrings(t, first["missing"]), []string{"tagline", "description", "code_url", "screenshot"}) {
			t.Fatalf("%s alpha: %#v", who, first)
		}
		if first["site_exists"] != true || first["site_url"] != "https://"+w.alpha+"."+w.slug+".simple-hack.test/" {
			t.Fatalf("%s site: %#v", who, first)
		}
		if second["title"] != "" || second["complete"] != false || second["site_exists"] != false ||
			!slices.Equal(jsonStrings(t, second["missing"]), []string{"title", "tagline", "description", "video_url", "code_url", "screenshot"}) {
			t.Fatalf("%s zeta: %#v", who, second)
		}
		if first["updated_at"] == nil || second["updated_at"] != nil {
			t.Fatalf("%s times: %#v %#v", who, first["updated_at"], second["updated_at"])
		}
		rfc3339Field(t, first["updated_at"])
		if first["frozen"] != false || first["pinned_version"] != nil || first["pinned_url"] != nil || first["deadline"] != nil {
			t.Fatalf("%s not due: %#v", who, first)
		}
	}
	check("organiser", a.key(w.org))
	check("judge", a.key(w.judge))
	check("admin", a.adminH())
}

func TestHackEntryDeadlineAndTakedown(t *testing.T) {
	w := newHackEntryWorld(t)
	a := w.a
	if r := a.at(t, "PUT", w.entryPath(), map[string]string{"title": "Before"}, a.key(w.p1)); r.status != 200 {
		t.Fatalf("before: %d %s", r.status, r.body)
	}
	png := append([]byte(nil), pngMagic...)
	if r := a.rawBody(t, "PUT", w.entryPath()+"/screenshot", png, "image/png", a.key(w.p1)); r.status != 200 {
		t.Fatalf("png: %d %s", r.status, r.body)
	}
	var pinnedAt sql.NullTime
	var pinnedVer sql.NullInt64
	qpin := `SELECT pinned_at, pinned_version FROM event_teams
		WHERE event_id = (SELECT id FROM events WHERE slug = $1) AND slug = $2`
	if err := a.database.QueryRow(qpin, w.slug, w.alpha).Scan(&pinnedAt, &pinnedVer); err != nil {
		t.Fatal(err)
	}
	if pinnedAt.Valid || pinnedVer.Valid {
		t.Fatalf("pinned early: %v %v", pinnedAt, pinnedVer)
	}
	var accountID string
	if err := a.database.QueryRow(`SELECT account_id FROM events WHERE slug = $1`, w.slug).Scan(&accountID); err != nil {
		t.Fatal(err)
	}
	if _, err := a.database.Exec(`INSERT INTO sites (user_id, name, site_url, active_version) VALUES ($1, $2, $3, 4)`,
		accountID, w.alpha, "https://example.test/"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.database.Exec(`INSERT INTO versions (site_id, version_number, disk_path, status)
		SELECT id, 4, 'x', 'active' FROM sites WHERE user_id = $1 AND name = $2`, accountID, w.alpha); err != nil {
		t.Fatal(err)
	}

	if _, err := a.database.Exec(`UPDATE events SET submission_deadline = clock_timestamp() - interval '1 minute' WHERE slug = $1`, w.slug); err != nil {
		t.Fatal(err)
	}
	wantCode(t, "put closed", a.at(t, "PUT", w.entryPath(), map[string]string{"title": "Late"}, a.key(w.p1)), 409, "submissions_closed")
	wantCode(t, "screenshot closed", a.rawBody(t, "PUT", w.entryPath()+"/screenshot", png, "image/png", a.key(w.p1)), 409, "submissions_closed")
	wantCode(t, "delete closed", a.at(t, "DELETE", w.entryPath()+"/screenshot", nil, a.key(w.p1)), 409, "submissions_closed")
	body := a.at(t, "GET", w.entryPath(), nil, a.key(w.p1)).json(t)
	e := wantEntryShape(t, body)
	if body["editable"] != false || body["refusal"] != "submissions_closed" || e["title"] != "Before" || e["has_screenshot"] != true {
		t.Fatalf("frozen view: %#v", body)
	}
	if rfc3339Field(t, body["deadline"]).After(time.Now().Add(time.Minute)) {
		t.Fatalf("deadline not past: %v", body["deadline"])
	}
	// A closed entry can still be read, including its screenshot.
	wantImageHeaders(t, a.rawBody(t, "GET", w.entryPath()+"/screenshot", nil, "", a.key(w.p1)), "image/png", png)

	a.hack.SetSites(entrySiteStub{})
	list := a.at(t, "GET", "/v1/hack/events/"+w.slug+"/entries", nil, a.key(w.org))
	if list.status != 200 {
		t.Fatalf("list: %d %s", list.status, list.body)
	}
	if err := a.database.QueryRow(qpin, w.slug, w.alpha).Scan(&pinnedAt, &pinnedVer); err != nil {
		t.Fatal(err)
	}
	if !pinnedAt.Valid || !pinnedVer.Valid || pinnedVer.Int64 != 4 {
		t.Fatalf("pin after list: at %v version %v", pinnedAt, pinnedVer)
	}
	if err := a.database.QueryRow(qpin, w.slug, w.zeta).Scan(&pinnedAt, &pinnedVer); err != nil {
		t.Fatal(err)
	}
	if !pinnedAt.Valid || pinnedVer.Valid {
		t.Fatalf("zeta pin: at %v version %v", pinnedAt, pinnedVer)
	}
	rows := list.json(t)["entries"].([]any)
	var alpha, zeta map[string]any
	for _, raw := range rows {
		item := raw.(map[string]any)
		switch item["team"].(map[string]any)["slug"] {
		case w.alpha:
			alpha = item
		case w.zeta:
			zeta = item
		}
	}
	if alpha == nil || zeta == nil {
		t.Fatalf("list teams: %s", list.body)
	}
	if alpha["frozen"] != true || alpha["pinned_version"] != float64(4) ||
		alpha["pinned_url"] != "https://preview.example/"+w.slug+"/"+w.alpha+"/v4" {
		t.Fatalf("alpha pin fields: %#v", alpha)
	}
	if zeta["frozen"] != true || zeta["pinned_version"] != nil || zeta["pinned_url"] != nil {
		t.Fatalf("zeta pin fields: %#v", zeta)
	}
	rfc3339Field(t, alpha["deadline"])

	// The judge's scoring screen uses the queue, not the organiser's list.
	// Display names ("Alpha") must not replace URL slugs ("alpha").
	queue := a.at(t, "GET", "/v1/hack/events/"+w.slug+"/judge/queue", nil, a.key(w.judge))
	if queue.status != http.StatusOK {
		t.Fatalf("judge queue: %d %s", queue.status, queue.body)
	}
	var queuedAlpha map[string]any
	for _, raw := range queue.json(t)["queue"].([]any) {
		item := raw.(map[string]any)
		if item["team_id"] == alpha["team"].(map[string]any)["id"] {
			queuedAlpha = item
		}
	}
	if queuedAlpha == nil || queuedAlpha["team"].(map[string]any)["slug"] != w.alpha ||
		queuedAlpha["site_exists"] != true || queuedAlpha["pinned_url"] != alpha["pinned_url"] {
		t.Fatalf("judge queue lost the team's real slug or deadline link: %#v", queuedAlpha)
	}

	if _, err := a.database.Exec(`UPDATE event_teams SET deadline_override = clock_timestamp() + interval '1 day'
		WHERE event_id = (SELECT id FROM events WHERE slug = $1) AND slug = $2`, w.slug, w.alpha); err != nil {
		t.Fatal(err)
	}
	r := a.at(t, "PUT", w.entryPath(), map[string]string{"title": "Extended"}, a.key(w.p1))
	body = r.json(t)
	if r.status != 200 || body["editable"] != true || body["refusal"] != nil || body["entry"].(map[string]any)["title"] != "Extended" {
		t.Fatalf("extension: %d %s", r.status, r.body)
	}
	if r := a.rawBody(t, "PUT", w.entryPath()+"/screenshot", jpegMagic, "image/jpeg", a.key(w.p1)); r.status != 200 {
		t.Fatalf("screenshot after extension: %d %s", r.status, r.body)
	}

	if _, err := a.database.Exec(`UPDATE event_teams SET site_taken_down_at = clock_timestamp()
		WHERE event_id = (SELECT id FROM events WHERE slug = $1) AND slug = $2`, w.slug, w.alpha); err != nil {
		t.Fatal(err)
	}
	wantCode(t, "takedown put", a.at(t, "PUT", w.entryPath(), map[string]string{"title": "Nope"}, a.key(w.p1)), 403, "team_site_taken_down")
	wantCode(t, "takedown screenshot", a.rawBody(t, "PUT", w.entryPath()+"/screenshot", png, "image/png", a.key(w.p1)), 403, "team_site_taken_down")
	wantCode(t, "takedown delete", a.at(t, "DELETE", w.entryPath()+"/screenshot", nil, a.key(w.p1)), 403, "team_site_taken_down")
	body = a.at(t, "GET", w.entryPath(), nil, a.key(w.p1)).json(t)
	if body["editable"] != false || body["refusal"] != "team_site_taken_down" || body["entry"].(map[string]any)["title"] != "Extended" {
		t.Fatalf("taken down view: %#v", body)
	}
}

// entrySiteStub is the site side the entries list needs for a deadline
// preview link. The real site handler is not wired in these tests.
type entrySiteStub struct{}

func (entrySiteStub) TeamSitesReady(string) bool { return true }
func (entrySiteStub) TeamSiteURL(eventSlug, teamSlug string) string {
	return "https://" + teamSlug + "." + eventSlug + ".simple-hack.test/"
}
func (entrySiteStub) TeamSiteInfo(context.Context, string, string) (TeamSite, db.Site, error) {
	return TeamSite{}, db.Site{}, nil
}
func (entrySiteStub) TeamPreviewLink(_ context.Context, _, eventSlug, teamSlug string, n int) (string, time.Time, bool) {
	return "https://preview.example/" + eventSlug + "/" + teamSlug + "/v" + strconv.Itoa(n), time.Time{}, true
}
func (entrySiteStub) SetTeamSiteTakenDown(context.Context, string, string, string, bool, string) error {
	return nil
}
func (entrySiteStub) TrashTeamSite(context.Context, string, string) error { return nil }
func (entrySiteStub) RequestSiteCert(string)                              {}
func (entrySiteStub) TeamPreviewLinkFor(site db.Site, eventSlug string, n int) (string, bool) {
	return "https://preview.example/" + eventSlug + "/" + site.Name + "/v" + strconv.Itoa(n), true
}
func (entrySiteStub) RemoveAccountFiles(string, string) error          { return nil }
func (entrySiteStub) SyncAccountMarkers(context.Context, string) error { return nil }

// tinyJPEG is a real 2x2 JPEG (DecodeConfig reads its header).
func tinyJPEG() []byte {
	var b bytes.Buffer
	_ = jpeg.Encode(&b, image.NewRGBA(image.Rect(0, 0, 2, 2)), nil)
	return b.Bytes()
}

func TestHackScreenshotWritesLimited(t *testing.T) {
	w := newHackEntryWorld(t)
	a := w.a
	path := w.entryPath() + "/screenshot"
	limited := false
	for i := 0; i < 8; i++ {
		if r := a.at(t, "PUT", path, string(pngMagic), a.key(w.p1)); r.status == 429 {
			limited = true
			break
		}
	}
	if !limited {
		t.Fatal("screenshot uploads not limited")
	}
}
