package entries

import (
	"bytes"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	hacktest "github.com/vsriram/simple-host/products/hack/hacktest"

	"github.com/vsriram/simple-host/host/httpx"
)

// Entries (M2): a participant's own entry and screenshot, and the organiser
// and judge list. newHackApp does not turn hack mode on; these routes do
// not need it.

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
	e, ok := body["entry"].(map[string]any)
	if !ok {
		t.Fatalf("entry: %#v", body["entry"])
	}
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
	pngMagic  = hacktest.PNGOfSize(16, 10)
	jpegMagic = hacktest.TinyJPEG()
	webpMagic = []byte("RIFF\x16\x00\x00\x00WEBPVP8X\x0a\x00\x00\x00\x00\x00\x00\x00\x0f\x00\x00\x09\x00\x00")
	gifMagic  = []byte("GIF89a")
	svgBody   = []byte(`<svg xmlns="http://www.w3.org/2000/svg"><text>x</text></svg>`)
)

func wantImageHeaders(t *testing.T, r hacktest.Resp, mime string, body []byte) {
	t.Helper()
	if r.Status != http.StatusOK {
		t.Fatalf("image: %d %s", r.Status, r.Body)
	}
	checks := []struct{ k, v string }{
		{"Content-Type", mime},
		{"X-Content-Type-Options", "nosniff"},
		{"Content-Security-Policy", "default-src 'none'; sandbox"},
		{"Cache-Control", "private, no-store"},
		{"Content-Disposition", "inline"},
	}
	for _, c := range checks {
		if got := r.Header.Get(c.k); got != c.v {
			t.Fatalf("%s %q, want %q", c.k, got, c.v)
		}
	}
	if !bytes.Equal(r.Body, body) {
		t.Fatalf("image body %d bytes, want %d", len(r.Body), len(body))
	}
}

func TestHackEntryReadUpdateAndValidation(t *testing.T) {
	w := newHackEntryWorld(t)
	a := w.a

	r := a.At(t, "GET", w.EntryPath(), nil, a.Key(w.P1))
	if r.Status != 200 {
		t.Fatalf("empty: %d %s", r.Status, r.Body)
	}
	body := r.JSON(t)
	e := wantEntryShape(t, body)
	if e["title"] != "" || e["tagline"] != "" || e["description"] != "" || e["video_url"] != "" || e["code_url"] != "" {
		t.Fatalf("empty text: %#v", e)
	}
	if e["has_screenshot"] != false || e["updated_at"] != nil || e["updated_by"] != "" {
		t.Fatalf("empty meta: %#v", e)
	}
	if !slices.Equal(hacktest.JSONStrings(t, body["required"]), []string{"title"}) || body["complete"] != false ||
		!slices.Equal(hacktest.JSONStrings(t, body["missing"]), []string{"title"}) {
		t.Fatalf("empty status: %#v", body)
	}
	if body["editable"] != true || body["refusal"] != nil || body["deadline"] != nil {
		t.Fatalf("empty write state: %#v", body)
	}

	// A person with no team, and roles that are not a participant, never get an entry.
	hacktest.WantCode(t, "no team", a.At(t, "GET", w.EntryPath(), nil, a.Key(w.Lone)), 409, "no_team")
	hacktest.WantCode(t, "no team put", a.At(t, "PUT", w.EntryPath(), map[string]string{"title": "X"}, a.Key(w.Lone)), 409, "no_team")
	hacktest.WantCode(t, "organiser entry", a.At(t, "GET", w.EntryPath(), nil, a.Key(w.Org)), 404, "event_not_found")
	hacktest.WantCode(t, "judge entry", a.At(t, "PUT", w.EntryPath(), map[string]string{"title": "X"}, a.Key(w.Judge)), 404, "event_not_found")
	hacktest.WantCode(t, "admin write", a.At(t, "PUT", w.EntryPath(), map[string]string{"title": "X"}, a.AdminH()), 404, "event_not_found")
	stranger := a.NewPerson(t, "ez")
	hacktest.WantCode(t, "stranger", a.At(t, "GET", w.EntryPath(), nil, a.Key(stranger)), 404, "event_not_found")

	r = a.At(t, "PUT", w.EntryPath(), map[string]string{"title": "  Hello   World  "}, a.Key(w.P1))
	if r.Status != 200 {
		t.Fatalf("put title: %d %s", r.Status, r.Body)
	}
	body = r.JSON(t)
	e = wantEntryShape(t, body)
	if e["title"] != "Hello World" || e["tagline"] != "" || e["updated_by"] != "Ada One" || e["has_screenshot"] != false {
		t.Fatalf("after title: %#v", e)
	}
	if e["updated_at"] == nil {
		t.Fatal("updated_at not set")
	}
	rfc3339Field(t, e["updated_at"])
	if body["complete"] != true || !slices.Equal(hacktest.JSONStrings(t, body["missing"]), []string{}) {
		t.Fatalf("complete: %#v", body)
	}

	// A later partial write changes only the fields it sends.
	r = a.At(t, "PUT", w.EntryPath(), map[string]any{
		"tagline":     "A short line",
		"description": "line1\nline2",
		"video_url":   "https://example.com/watch?v=1",
	}, a.Key(w.P1))
	if r.Status != 200 {
		t.Fatalf("partial: %d %s", r.Status, r.Body)
	}
	e = wantEntryShape(t, r.JSON(t))
	if e["title"] != "Hello World" || e["tagline"] != "A short line" || e["description"] != "line1\nline2" ||
		e["video_url"] != "https://example.com/watch?v=1" || e["code_url"] != "" {
		t.Fatalf("partial kept: %#v", e)
	}
	r = a.At(t, "PUT", w.EntryPath(), map[string]string{"code_url": "http://code.example/repo"}, a.Key(w.P1))
	e = wantEntryShape(t, r.JSON(t))
	if r.Status != 200 || e["code_url"] != "http://code.example/repo" || e["video_url"] != "https://example.com/watch?v=1" {
		t.Fatalf("code url: %d %#v", r.Status, e)
	}
	r = a.At(t, "PUT", w.EntryPath(), map[string]string{"title": "", "video_url": ""}, a.Key(w.P1))
	body = r.JSON(t)
	e = wantEntryShape(t, body)
	if e["title"] != "" || e["video_url"] != "" || e["tagline"] != "A short line" || body["complete"] != false ||
		!slices.Equal(hacktest.JSONStrings(t, body["missing"]), []string{"title"}) {
		t.Fatalf("cleared: %#v %v", e, body["missing"])
	}

	longURL := "https://example.com/" + strings.Repeat("a", 500-len("https://example.com/"))
	if r := a.At(t, "PUT", w.EntryPath(), map[string]string{"video_url": longURL}, a.Key(w.P1)); r.Status != 200 {
		t.Fatalf("500 char url: %d %s", r.Status, r.Body)
	}

	cases := []struct {
		name string
		body map[string]string
		code string
	}{
		{"title length", map[string]string{"title": strings.Repeat("a", 81)}, "invalid_title"},
		{"video scheme", map[string]string{"video_url": "javascript:alert(1)"}, "invalid_url"},
		{"video relative", map[string]string{"video_url": "example.com"}, "invalid_url"},
		{"code space", map[string]string{"code_url": "http://example.com/a b"}, "invalid_url"},
	}
	for _, c := range cases {
		r := a.At(t, "PUT", w.EntryPath(), c.body, a.Key(w.P1))
		hacktest.WantCode(t, c.name, r, 400, c.code)
	}
	// A rejected write leaves the stored entry alone.
	e = wantEntryShape(t, a.At(t, "GET", w.EntryPath(), nil, a.Key(w.P1)).JSON(t))
	if e["tagline"] != "A short line" || e["video_url"] != longURL || e["title"] != "" {
		t.Fatalf("rejected write changed the entry: %#v", e)
	}
}

func TestHackEntryIsolation(t *testing.T) {
	w := newHackEntryWorld(t)
	a := w.a
	if r := a.At(t, "PUT", w.EntryPath(), map[string]string{"title": "Secret Title"}, a.Key(w.P1)); r.Status != 200 {
		t.Fatalf("put: %d %s", r.Status, r.Body)
	}
	png := append([]byte(nil), pngMagic...)
	if r := a.RawBody(t, "PUT", w.EntryPath()+"/screenshot", png, "image/svg+xml", a.Key(w.P1)); r.Status != 200 {
		t.Fatalf("png: %d %s", r.Status, r.Body)
	}

	// The other team only ever sees its own entry.
	r := a.At(t, "GET", w.EntryPath(), nil, a.Key(w.P2))
	e := wantEntryShape(t, r.JSON(t))
	if r.Status != 200 || e["title"] != "" || e["has_screenshot"] != false || strings.Contains(string(r.Body), "Secret Title") {
		t.Fatalf("other team saw the entry: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "PUT", w.EntryPath(), map[string]string{"title": "Stolen"}, a.Key(w.P2)); r.Status != 200 || r.JSON(t)["entry"].(map[string]any)["title"] != "Stolen" {
		t.Fatalf("other put: %d %s", r.Status, r.Body)
	}
	e = wantEntryShape(t, a.At(t, "GET", w.EntryPath(), nil, a.Key(w.P1)).JSON(t))
	if e["title"] != "Secret Title" || e["has_screenshot"] != true || e["updated_by"] != "Ada One" {
		t.Fatalf("first team changed: %#v", e)
	}
	hacktest.WantCode(t, "other screenshot", a.RawBody(t, "GET", w.EntryPath()+"/screenshot", nil, "", a.Key(w.P2)), 404, "no_screenshot")
	hacktest.WantCode(t, "other team path", a.At(t, "GET", "/v1/hack/events/"+w.Slug+"/teams/"+w.Alpha+"/screenshot", nil, a.Key(w.P2)), 404, "event_not_found")
	shot := a.RawBody(t, "GET", w.EntryPath()+"/screenshot", nil, "", a.Key(w.P1))
	wantImageHeaders(t, shot, "image/png", png)
	if strings.Contains(string(a.At(t, "GET", w.EntryPath(), nil, a.Key(w.P1)).Body), w.P2.Email) ||
		strings.Contains(string(a.At(t, "GET", w.EntryPath(), nil, a.Key(w.P1)).Body), a.UserID(t, w.P2)) {
		t.Fatal("entry leaked an email or account id")
	}
}

func TestHackEntryScreenshot(t *testing.T) {
	w := newHackEntryWorld(t)
	a := w.a
	a.hack.ShotWrites = httpx.NewRateLimiter(1000, 1000) // this test uploads many times in a row
	path := w.EntryPath() + "/screenshot"
	if r := a.At(t, "PUT", w.EntryPath(), map[string]string{"title": "Kept"}, a.Key(w.P1)); r.Status != 200 {
		t.Fatalf("title: %d %s", r.Status, r.Body)
	}
	hacktest.WantCode(t, "none yet", a.RawBody(t, "GET", path, nil, "", a.Key(w.P1)), 404, "no_screenshot")

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
		r := a.RawBody(t, "PUT", path, c.body, c.ctype, a.Key(w.P1))
		hacktest.WantCode(t, c.name, r, c.status, c.code)
	}
	over := make([]byte, (2<<20)+1)
	copy(over, pngMagic)
	hacktest.WantCode(t, "too big", a.RawBody(t, "PUT", path, over, "image/png", a.Key(w.P1)), 413, "image_too_large")
	hacktest.WantCode(t, "still none", a.RawBody(t, "GET", path, nil, "", a.Key(w.P1)), 404, "no_screenshot")

	exact := make([]byte, 2<<20)
	copy(exact, pngMagic)
	r := a.RawBody(t, "PUT", path, exact, "image/svg+xml", a.Key(w.P1))
	if r.Status != 200 || r.JSON(t)["entry"].(map[string]any)["has_screenshot"] != true {
		t.Fatalf("exact png: %d %s", r.Status, r.Body)
	}
	wantImageHeaders(t, a.RawBody(t, "GET", path, nil, "", a.Key(w.P1)), "image/png", exact)

	// A refused replacement does not remove the stored image.
	hacktest.WantCode(t, "gif after", a.RawBody(t, "PUT", path, gifMagic, "image/png", a.Key(w.P1)), 415, "unsupported_image")
	bigger := make([]byte, (2<<20)+2)
	copy(bigger, pngMagic)
	hacktest.WantCode(t, "bigger after", a.RawBody(t, "PUT", path, bigger, "image/png", a.Key(w.P1)), 413, "image_too_large")
	wantImageHeaders(t, a.RawBody(t, "GET", path, nil, "", a.Key(w.P1)), "image/png", exact)

	jpeg := append([]byte(nil), jpegMagic...)
	if r := a.RawBody(t, "PUT", path, jpeg, "text/plain", a.Key(w.P1)); r.Status != 200 {
		t.Fatalf("jpeg: %d %s", r.Status, r.Body)
	}
	wantImageHeaders(t, a.RawBody(t, "GET", path, nil, "", a.Key(w.P1)), "image/jpeg", jpeg)
	webp := append([]byte(nil), webpMagic...)
	if r := a.RawBody(t, "PUT", path, webp, "image/gif", a.Key(w.P1)); r.Status != 200 {
		t.Fatalf("webp: %d %s", r.Status, r.Body)
	}
	wantImageHeaders(t, a.RawBody(t, "GET", path, nil, "", a.Key(w.P1)), "image/webp", webp)

	r = a.At(t, "DELETE", path, nil, a.Key(w.P1))
	e := wantEntryShape(t, r.JSON(t))
	if r.Status != 200 || e["has_screenshot"] != false || e["title"] != "Kept" {
		t.Fatalf("delete: %d %#v", r.Status, e)
	}
	hacktest.WantCode(t, "gone", a.RawBody(t, "GET", path, nil, "", a.Key(w.P1)), 404, "no_screenshot")

	png := append([]byte(nil), pngMagic...)
	if r := a.RawBody(t, "PUT", path, png, "application/octet-stream", a.Key(w.P1)); r.Status != 200 {
		t.Fatalf("png again: %d %s", r.Status, r.Body)
	}
	// A judge only sees a team whose entry is submitted or settled (section
	// 4.5); the organiser and admin views below are unaffected by this.
	if r := a.At(t, "POST", w.EntryPath()+"/submit", map[string]any{}, a.Key(w.P1)); r.Status != 200 {
		t.Fatalf("submit: %d %s", r.Status, r.Body)
	}
	teamPath := "/v1/hack/events/" + w.Slug + "/teams/" + w.Alpha + "/screenshot"
	wantImageHeaders(t, a.RawBody(t, "GET", teamPath, nil, "", a.Key(w.Org)), "image/png", png)
	wantImageHeaders(t, a.RawBody(t, "GET", teamPath, nil, "", a.Key(w.Judge)), "image/png", png)
	wantImageHeaders(t, a.RawBody(t, "GET", teamPath, nil, "", a.AdminH()), "image/png", png)
	hacktest.WantCode(t, "participant team path", a.At(t, "GET", teamPath, nil, a.Key(w.P1)), 404, "event_not_found")
	hacktest.WantCode(t, "other team path", a.At(t, "GET", teamPath, nil, a.Key(w.P2)), 404, "event_not_found")
	hacktest.WantCode(t, "missing team", a.At(t, "GET", "/v1/hack/events/"+w.Slug+"/teams/no-such-team/screenshot", nil, a.Key(w.Org)), 404, "team_not_found")
	hacktest.WantCode(t, "lone screenshot", a.RawBody(t, "PUT", path, png, "image/png", a.Key(w.Lone)), 409, "no_team")
	hacktest.WantCode(t, "lone delete", a.At(t, "DELETE", path, nil, a.Key(w.Lone)), 409, "no_team")
}

func TestHackScreenshotWritesLimited(t *testing.T) {
	w := newHackEntryWorld(t)
	a := w.a
	path := w.EntryPath() + "/screenshot"
	limited := false
	for i := 0; i < 8; i++ {
		if r := a.At(t, "PUT", path, string(pngMagic), a.Key(w.P1)); r.Status == 429 {
			limited = true
			break
		}
	}
	if !limited {
		t.Fatal("screenshot uploads not limited")
	}
}
