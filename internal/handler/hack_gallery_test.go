package handler

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	db "github.com/vsriram/simple-host/internal/db"
)

// shotShown is the PNG a gallery team stores. shotHidden is a different image
// that must never be served: it belongs to a team the gallery leaves out.
var (
	shotShown  = []byte("\x89PNG\r\n\x1a\nshown-team")
	shotHidden = []byte("\x89PNG\r\n\x1a\nhidden-team")
)

func TestHackScreenshotPath(t *testing.T) {
	for _, slug := range []string{"a", "ab", "a-b", "a0", "a--b", strings.Repeat("a", 30)} {
		got, ok := hackScreenshotPath("/screenshots/" + slug)
		if !ok || got != slug {
			t.Errorf("slug %q: got %q ok=%v", slug, got, ok)
		}
	}
	for _, path := range []string{
		"/screenshots",
		"/screenshots/",
		"/screenshot/ab",
		"/screenshots/ab/extra",
		"/screenshots/ab/",
		"/screenshots/../etc/passwd",
		"/screenshots/..",
		"/screenshots/ab/../../etc/passwd",
		"/screenshots/%2e%2e",
		"/screenshots/foo%2fbar",
		"/screenshots/Ab",
		"/screenshots/AB",
		"/screenshots/a_b",
		"/screenshots/a.b",
		"/screenshots/-ab",
		"/screenshots/ab-",
		"/screenshots/" + strings.Repeat("a", 31),
		"/screenshots/ab%00cd",
		"/other",
		"/",
	} {
		if _, ok := hackScreenshotPath(path); ok {
			t.Errorf("accepted %q", path)
		}
	}
}

func TestHackGalleryLink(t *testing.T) {
	ok := func(event, team string) string {
		return "https://" + team + "." + event + ".simple-hack.test/"
	}
	if got := hackGalleryLink(ok, "weekend", "alpha"); got != "https://alpha.weekend.simple-hack.test/" {
		t.Fatalf("link %q", got)
	}
	for _, c := range []struct {
		event, team string
		build       func(string, string) string
	}{
		{"Weekend", "alpha", ok},
		{"weekend", "Alpha", ok},
		{"weekend", "../x", ok},
		{"week.end", "alpha", ok},
		{"", "alpha", ok},
		{"weekend", "", ok},
		{"weekend", strings.Repeat("a", 31), ok},
		{"weekend", "alpha", func(string, string) string { return "javascript:alert(1)" }},
		{"weekend", "alpha", func(string, string) string { return "https://evil.example/" }},
		{"weekend", "alpha", func(string, string) string { return "https://user:pass@alpha.weekend.simple-hack.test/" }},
		{"weekend", "alpha", nil},
	} {
		if got := hackGalleryLink(c.build, c.event, c.team); got != "" {
			t.Errorf("event %q team %q built %q", c.event, c.team, got)
		}
	}
}

func TestHackEventGalleryMarkup(t *testing.T) {
	p := testHackEvent()
	p.Prizes = "Glory."
	p.Gallery = []hackGalleryCard{
		{
			Title:   "bravo <script>alert(1)</script>",
			Tagline: `"><img src=x onerror=1>`,
			Team:    "Qqq",
			Shot:    "/screenshots/qqq",
			URL:     "https://qqq.weekend.simple-hack.test/",
		},
		{Title: "Aaa", URL: "https://aaa.weekend.simple-hack.test/"},
		{Title: "Same", Tagline: "{{.Title}}", URL: "https://same.weekend.simple-hack.test/"},
	}
	rec := renderHackEvent(t, p)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	body := rec.Body.String()
	prizes := strings.Index(body, "<h2>Prizes</h2>")
	projects := strings.Index(body, "<h2>Projects</h2>")
	if prizes < 0 || projects < prizes {
		t.Fatalf("Projects is not after Prizes")
	}
	if strings.Contains(body, "<script>alert(1)</script>") || strings.Contains(body, `<img src=x onerror=1>`) {
		t.Fatal("gallery text was not escaped")
	}
	if !strings.Contains(body, "bravo &lt;script&gt;alert(1)&lt;/script&gt;") {
		t.Fatal("script title was not shown as text")
	}
	if !strings.Contains(body, "{{.Title}}") {
		t.Fatal("tagline braces were executed or dropped")
	}
	cards := galleryCardHTML(body)
	if len(cards) != 3 {
		t.Fatalf("cards: %d", len(cards))
	}
	if !strings.Contains(cards[0], `src="/screenshots/qqq"`) || !strings.Contains(cards[0], `alt=""`) || !strings.Contains(cards[0], `loading="lazy"`) {
		t.Fatalf("shot img: %s", cards[0])
	}
	if strings.Contains(cards[1], "<img") || strings.Contains(cards[1], "gcard-team") {
		t.Fatalf("team without a different title still has a shot or a team line: %s", cards[1])
	}
	if strings.Contains(cards[2], "gcard-team") {
		t.Fatalf("title equal to the team name still shows the team line: %s", cards[2])
	}
	if !strings.Contains(cards[0], `class="gcard-team">Qqq</p>`) {
		t.Fatalf("missing team line: %s", cards[0])
	}
	for i, card := range cards {
		if !strings.Contains(card, `target="_blank"`) || !strings.Contains(card, `rel="noopener"`) || !strings.Contains(card, ">Open project</a>") {
			t.Fatalf("card %d link: %s", i, card)
		}
	}
	if galleryCardHref(cards[0]) != "https://qqq.weekend.simple-hack.test/" {
		t.Fatalf("href %q", galleryCardHref(cards[0]))
	}
	style := pageStyle(body)
	for _, want := range []string{
		"grid-template-columns: 1fr",
		"grid-template-columns: 1fr 1fr",
		"min-width: 640px",
		"aspect-ratio: 16 / 10",
		"object-fit: cover",
		"min-height: 40px",
		"var(--surface)",
		"var(--border)",
		"var(--text)",
		"var(--text-secondary)",
		"var(--radius)",
		"var(--grid)",
		"var(--accent)",
	} {
		if !strings.Contains(style, want) {
			t.Errorf("page style missing %q", want)
		}
	}
	if regexp.MustCompile(`#[0-9a-fA-F]{3,8}`).MatchString(style) {
		t.Errorf("page style has a hard-coded colour:\n%s", style)
	}
	assertEventPageCSP(t, rec.Header())

	p.TakenDown = true
	gone := renderHackEvent(t, p)
	if gone.Code != http.StatusGone {
		t.Fatalf("taken down status %d", gone.Code)
	}
	if strings.Contains(gone.Body.String(), "Projects") || strings.Contains(gone.Body.String(), "Open project") {
		t.Fatal("taken-down page still shows the gallery")
	}
}

func TestHackEventGallery(t *testing.T) {
	a := newTeamSiteApp(t)
	org := a.newPerson(t, "org")
	slug := a.makeEvent(t, org)
	var eventID, accountID string
	if err := a.database.QueryRow(`SELECT id, account_id FROM events WHERE slug = $1`, slug).Scan(&eventID, &accountID); err != nil {
		t.Fatal(err)
	}
	names := []string{"Aaa", "Qqq", "Mmm", "Zzz", "Same", "Nnn", "Sss", "Ttt", "Ddd"}
	slugOf := map[string]string{}
	idOf := map[string]string{}
	for _, name := range names {
		p := a.newPerson(t, strings.ToLower(name))
		a.join(t, slug, p, org)
		teamSlug, _ := a.startTeam(t, slug, name, p)
		slugOf[name] = teamSlug
		var id string
		if err := a.database.QueryRow(`SELECT id FROM event_teams WHERE event_id = $1 AND slug = $2`, eventID, teamSlug).Scan(&id); err != nil {
			t.Fatal(err)
		}
		idOf[name] = id
	}
	live := []string{"Aaa", "Qqq", "Mmm", "Zzz", "Same", "Ttt"}
	for _, name := range live {
		if _, err := a.database.Exec(`INSERT INTO sites (user_id, name, active_version) VALUES ($1, $2, 1)`, accountID, slugOf[name]); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := a.database.Exec(`INSERT INTO sites (user_id, name, active_version, suspended_at) VALUES ($1, $2, 1, now())`, accountID, slugOf["Sss"]); err != nil {
		t.Fatal(err)
	}
	if _, err := a.database.Exec(`INSERT INTO sites (user_id, name, active_version, deleted_at) VALUES ($1, $2, 1, now())`, accountID, slugOf["Ddd"]); err != nil {
		t.Fatal(err)
	}
	if _, err := a.database.Exec(`UPDATE event_teams SET site_taken_down_at = now(), site_taken_down_reason = 'spam' WHERE id = $1`, idOf["Ttt"]); err != nil {
		t.Fatal(err)
	}
	insertEntry := func(name, title, tagline string, shot []byte, ctype string) {
		t.Helper()
		if _, err := a.database.Exec(`
			INSERT INTO event_entries (team_id, event_id, title, tagline, screenshot, screenshot_type)
			VALUES ($1, $2, $3, $4, $5, $6)`, idOf[name], eventID, title, tagline, shot, ctype); err != nil {
			t.Fatal(err)
		}
	}
	insertEntry("Qqq", "bravo <script>alert(1)</script>", `"><img src=x onerror=1>`, shotShown, "image/png")
	insertEntry("Mmm", "charlie", "A quiet line", nil, "")
	insertEntry("Zzz", "delta", "", nil, "")
	insertEntry("Same", "Same", "hello", nil, "")
	insertEntry("Nnn", "HIDDEN_NOSITE", "nope", nil, "")
	insertEntry("Sss", "HIDDEN_SUSPENDED", "nope", shotHidden, "image/png")
	insertEntry("Ttt", "HIDDEN_TAKEDOWN", "nope", shotHidden, "image/png")
	insertEntry("Ddd", "HIDDEN_DELETED", "nope", shotHidden, "image/png")

	markReady(t, a.certDir, slug)
	host := slug + "." + tsDomain
	wantOrder := []string{slugOf["Aaa"], slugOf["Qqq"], slugOf["Mmm"], slugOf["Zzz"], slugOf["Same"]}

	cards, err := db.ListGalleryCards(context.Background(), a.database, eventID, accountID)
	if err != nil {
		t.Fatal(err)
	}
	if got := gallerySlugs(cards); strings.Join(got, ",") != strings.Join(wantOrder, ",") {
		t.Fatalf("cards %v, want %v", got, wantOrder)
	}
	if cards[0].Title != "" || cards[0].Name != "Aaa" || cards[0].HasScreenshot {
		t.Fatalf("team without an entry: %+v", cards[0])
	}
	if cards[1].Title != "bravo <script>alert(1)</script>" || !cards[1].HasScreenshot || cards[1].Tagline != `"><img src=x onerror=1>` {
		t.Fatalf("script card: %+v", cards[1])
	}
	data, ctype, err := db.GalleryScreenshot(context.Background(), a.database, eventID, accountID, slugOf["Qqq"])
	if err != nil || ctype != "image/png" || !bytes.Equal(data, shotShown) {
		t.Fatalf("db screenshot: %q %v %v", ctype, err, data)
	}
	for _, name := range []string{"Aaa", "Nnn", "Sss", "Ttt", "Ddd"} {
		_, _, err := db.GalleryScreenshot(context.Background(), a.database, eventID, accountID, slugOf[name])
		if !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("%s screenshot err %v", name, err)
		}
	}

	// The rows exist, but the gallery switch is still off.
	page := a.on(t, "GET", host, "/", nil, "")
	if page.status != http.StatusOK || strings.Contains(string(page.body), "<h2>Projects</h2>") || bytes.Contains(page.body, shotShown) {
		t.Fatalf("gallery open while the switch is off: %d", page.status)
	}
	hidden := a.on(t, "GET", host, "/screenshots/"+slugOf["Qqq"], nil, "")
	if hidden.status != http.StatusNotFound || bytes.Contains(hidden.body, shotShown) {
		t.Fatalf("screenshot while the gallery is closed: %d %s", hidden.status, hidden.body)
	}

	if r := a.api(t, "PATCH", "/v1/hack/events/"+slug, map[string]any{"gallery_open": true, "prizes": "A trophy."}, org.key); r.status != 200 {
		t.Fatalf("open gallery: %d %s", r.status, r.body)
	}
	page = a.on(t, "GET", host, "/", nil, "")
	if page.status != http.StatusOK {
		t.Fatalf("page: %d %s", page.status, page.body)
	}
	body := string(page.body)
	if strings.Index(body, "<h2>Prizes</h2>") < 0 || strings.Index(body, "<h2>Prizes</h2>") > strings.Index(body, "<h2>Projects</h2>") {
		t.Fatal("Projects is not after Prizes")
	}
	assertGalleryPage(t, body, slug, slugOf)
	assertEventPageCSP(t, page.header)
	if page.header.Get("Cache-Control") != "no-store" {
		t.Errorf("page Cache-Control %q", page.header.Get("Cache-Control"))
	}
	if hit := inlineAttrRe.FindString(body); hit != "" {
		t.Errorf("inline handler: %s", hit)
	}

	shot := a.on(t, "GET", host, "/screenshots/"+slugOf["Qqq"], nil, "")
	assertGalleryShot(t, shot, shotShown)
	head := a.on(t, "HEAD", host, "/screenshots/"+slugOf["Qqq"], nil, "")
	assertGalleryShot(t, head, nil)
	if head.header.Get("Content-Length") != shot.header.Get("Content-Length") {
		t.Fatalf("HEAD length %q, GET %q", head.header.Get("Content-Length"), shot.header.Get("Content-Length"))
	}

	for _, name := range []string{"Aaa", "Nnn", "Sss", "Ttt", "Ddd"} {
		r := a.on(t, "GET", host, "/screenshots/"+slugOf[name], nil, "")
		if r.status != http.StatusNotFound || bytes.Contains(r.body, shotShown) || bytes.Contains(r.body, shotHidden) {
			t.Fatalf("screenshot %s: %d", name, r.status)
		}
	}
	for _, path := range []string{
		"/screenshots/" + strings.ToUpper(slugOf["Qqq"]),
		"/screenshots/" + slugOf["Qqq"] + "/extra",
		"/screenshots/" + slugOf["Qqq"] + "/",
		"/screenshots/../etc/passwd",
		"/screenshots/..",
		"/screenshots/" + slugOf["Qqq"] + "/../../etc/passwd",
		"/screenshots/%2e%2e",
		"/screenshots/%2e%2e/%2e%2e/etc/passwd",
		"/screenshots/foo%2fbar",
		"/screenshots/" + strings.Repeat("a", 31),
		"/screenshots/not-a-team",
		"/screenshots/-bad",
		"/screenshots/bad-",
		"/screenshots/bad_name",
		"/no-such-page",
	} {
		r := a.on(t, "GET", host, path, nil, "")
		if r.status != http.StatusNotFound || bytes.Contains(r.body, shotShown) || bytes.Contains(r.body, shotHidden) {
			t.Fatalf("%s: %d", path, r.status)
		}
	}
	if r := a.on(t, "POST", host, "/screenshots/"+slugOf["Qqq"], nil, ""); r.status != http.StatusMethodNotAllowed {
		t.Fatalf("POST screenshot: %d", r.status)
	}
	v1 := a.on(t, "GET", host, "/v1/sites/"+slugOf["Qqq"], nil, "")
	if v1.status != http.StatusNotFound || !strings.Contains(string(v1.body), "not_found") || strings.Contains(v1.header.Get("Content-Type"), "text/html") {
		t.Fatalf("event host /v1/: %d %s", v1.status, v1.body)
	}

	// Not ready: the rows stay, the page and the route do not show them.
	if err := os.Remove(filepath.Join(a.certDir, "ready", slug)); err != nil {
		t.Fatal(err)
	}
	if r := a.on(t, "GET", host, "/", nil, ""); r.status != 200 || strings.Contains(string(r.body), "<h2>Projects</h2>") {
		t.Fatalf("gallery while addresses are not ready: %d", r.status)
	}
	if r := a.on(t, "GET", host, "/screenshots/"+slugOf["Qqq"], nil, ""); r.status != http.StatusNotFound || bytes.Contains(r.body, shotShown) {
		t.Fatalf("screenshot while not ready: %d", r.status)
	}
	markReady(t, a.certDir, slug)
	if r := a.on(t, "GET", host, "/", nil, ""); !strings.Contains(string(r.body), "<h2>Projects</h2>") {
		t.Fatal("gallery did not return once addresses were ready")
	}

	// Closed still shows. Draft does not. Open does again.
	if _, err := a.database.Exec(`UPDATE events SET stage = 'closed' WHERE slug = $1`, slug); err != nil {
		t.Fatal(err)
	}
	if r := a.on(t, "GET", host, "/", nil, ""); !strings.Contains(string(r.body), "<h2>Projects</h2>") {
		t.Fatal("closed event hid the gallery")
	}
	if _, err := a.database.Exec(`UPDATE events SET stage = 'draft' WHERE slug = $1`, slug); err != nil {
		t.Fatal(err)
	}
	draft := a.on(t, "GET", host, "/", nil, "")
	if draft.status != http.StatusOK || strings.Contains(string(draft.body), "<h2>Projects</h2>") || strings.Contains(string(draft.body), "HIDDEN_") {
		t.Fatalf("draft showed the gallery: %d", draft.status)
	}
	if r := a.on(t, "GET", host, "/screenshots/"+slugOf["Qqq"], nil, ""); r.status != http.StatusNotFound {
		t.Fatalf("draft screenshot: %d", r.status)
	}
	if _, err := a.database.Exec(`UPDATE events SET stage = 'open' WHERE slug = $1`, slug); err != nil {
		t.Fatal(err)
	}

	if _, err := a.database.Exec(`UPDATE events SET taken_down_at = now(), taken_down_reason = 'nope' WHERE slug = $1`, slug); err != nil {
		t.Fatal(err)
	}
	down := a.on(t, "GET", host, "/", nil, "")
	if down.status != http.StatusGone || strings.Contains(string(down.body), "<h2>Projects</h2>") || strings.Contains(string(down.body), "bravo") || strings.Contains(string(down.body), "HIDDEN_") {
		t.Fatalf("taken down showed the gallery: %d", down.status)
	}
	if r := a.on(t, "GET", host, "/screenshots/"+slugOf["Qqq"], nil, ""); r.status != http.StatusNotFound || bytes.Contains(r.body, shotShown) {
		t.Fatalf("taken down screenshot: %d", r.status)
	}
	if _, err := a.database.Exec(`UPDATE events SET taken_down_at = NULL, taken_down_reason = '' WHERE slug = $1`, slug); err != nil {
		t.Fatal(err)
	}
	if r := a.on(t, "GET", host, "/", nil, ""); !strings.Contains(string(r.body), "<h2>Projects</h2>") {
		t.Fatal("gallery did not return after restore")
	}

	if _, err := a.database.Exec(`UPDATE users SET suspended_at = now() WHERE id = $1`, accountID); err != nil {
		t.Fatal(err)
	}
	susp, err := db.ListGalleryCards(context.Background(), a.database, eventID, accountID)
	if err != nil || len(susp) != 0 {
		t.Fatalf("suspended account cards: %v %v", susp, err)
	}
	if _, _, err := db.GalleryScreenshot(context.Background(), a.database, eventID, accountID, slugOf["Qqq"]); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("suspended account screenshot: %v", err)
	}
	held := a.on(t, "GET", host, "/", nil, "")
	if held.status != http.StatusOK || strings.Contains(string(held.body), "<h2>Projects</h2>") || strings.Contains(string(held.body), "HIDDEN_") {
		t.Fatalf("suspended account showed the gallery: %d", held.status)
	}
	if r := a.on(t, "GET", host, "/screenshots/"+slugOf["Qqq"], nil, ""); r.status != http.StatusNotFound {
		t.Fatalf("suspended account screenshot route: %d", r.status)
	}
	if _, err := a.database.Exec(`UPDATE users SET suspended_at = NULL WHERE id = $1`, accountID); err != nil {
		t.Fatal(err)
	}
	back, err := db.ListGalleryCards(context.Background(), a.database, eventID, accountID)
	if err != nil || strings.Join(gallerySlugs(back), ",") != strings.Join(wantOrder, ",") {
		t.Fatalf("after unsuspend: %v %v", gallerySlugs(back), err)
	}

	if r := a.api(t, "PATCH", "/v1/hack/events/"+slug, map[string]any{"gallery_open": false}, org.key); r.status != 200 {
		t.Fatalf("close gallery: %d %s", r.status, r.body)
	}
	if r := a.on(t, "GET", host, "/", nil, ""); strings.Contains(string(r.body), "<h2>Projects</h2>") {
		t.Fatal("gallery stayed up after the switch was turned off")
	}
	if r := a.on(t, "GET", host, "/screenshots/"+slugOf["Qqq"], nil, ""); r.status != http.StatusNotFound || bytes.Contains(r.body, shotShown) {
		t.Fatalf("screenshot after the switch was turned off: %d", r.status)
	}
}

func gallerySlugs(cards []db.GalleryCard) []string {
	out := make([]string, len(cards))
	for i, c := range cards {
		out[i] = c.Slug
	}
	return out
}

func assertGalleryPage(t *testing.T, body, event string, slugOf map[string]string) {
	t.Helper()
	for _, hidden := range []string{"HIDDEN_NOSITE", "HIDDEN_SUSPENDED", "HIDDEN_TAKEDOWN", "HIDDEN_DELETED", "<script>alert(1)</script>", `<img src=x onerror=1>`} {
		if strings.Contains(body, hidden) {
			t.Errorf("page contains %q", hidden)
		}
	}
	if !strings.Contains(body, "bravo &lt;script&gt;alert(1)&lt;/script&gt;") {
		t.Error("script title was not shown as text")
	}
	cards := galleryCardHTML(body)
	want := []struct {
		name, title, team, tagline, shot string
	}{
		{"Aaa", "Aaa", "", "", ""},
		{"Qqq", "bravo &lt;script&gt;alert(1)&lt;/script&gt;", "Qqq", "&gt;&lt;img src=x onerror=1&gt;", "/screenshots/" + slugOf["Qqq"]},
		{"Mmm", "charlie", "Mmm", "A quiet line", ""},
		{"Zzz", "delta", "Zzz", "", ""},
		{"Same", "Same", "", "hello", ""},
	}
	if len(cards) != len(want) {
		t.Fatalf("cards %d, want %d\n%s", len(cards), len(want), body)
	}
	for i, w := range want {
		card := cards[i]
		if !strings.Contains(card, "<h3>"+w.title+"</h3>") {
			t.Errorf("card %d title: %s", i, card)
		}
		if w.team == "" && strings.Contains(card, "gcard-team") {
			t.Errorf("card %d shows the team name: %s", i, card)
		}
		if w.team != "" && !strings.Contains(card, `class="gcard-team">`+w.team+`</p>`) {
			t.Errorf("card %d team line: %s", i, card)
		}
		if w.tagline == "" && strings.Contains(card, "<p>") {
			t.Errorf("card %d has an empty tagline: %s", i, card)
		}
		if w.tagline != "" && !strings.Contains(card, w.tagline) {
			t.Errorf("card %d tagline %q missing: %s", i, w.tagline, card)
		}
		if w.shot == "" && strings.Contains(card, "<img") {
			t.Errorf("card %d has a screenshot: %s", i, card)
		}
		if w.shot != "" && (!strings.Contains(card, `src="`+w.shot+`"`) || !strings.Contains(card, `alt=""`) || !strings.Contains(card, `loading="lazy"`)) {
			t.Errorf("card %d img: %s", i, card)
		}
		href := galleryCardHref(card)
		wantHref := "https://" + slugOf[w.name] + "." + event + "." + tsDomain + "/"
		if href != wantHref {
			t.Errorf("card %d href %q, want %q", i, href, wantHref)
		}
		if !strings.Contains(card, `target="_blank"`) || !strings.Contains(card, `rel="noopener"`) || !strings.Contains(card, ">Open project</a>") {
			t.Errorf("card %d link: %s", i, card)
		}
	}
	if strings.Count(body, "<img") != 1 {
		t.Errorf("img count %d", strings.Count(body, "<img"))
	}
}

func assertGalleryShot(t *testing.T, r resp, body []byte) {
	t.Helper()
	if r.status != http.StatusOK {
		t.Fatalf("screenshot status %d: %s", r.status, r.body)
	}
	if body == nil {
		if len(r.body) != 0 {
			t.Fatalf("HEAD body %q", r.body)
		}
	} else if !bytes.Equal(r.body, body) {
		t.Fatalf("screenshot bytes %q", r.body)
	}
	for k, want := range map[string]string{
		"Content-Type":                 "image/png",
		"X-Content-Type-Options":       "nosniff",
		"Content-Security-Policy":      "default-src 'none'; sandbox",
		"Cache-Control":                "public, max-age=300",
		"Cross-Origin-Resource-Policy": "same-origin",
		"Content-Length":               "18",
	} {
		if r.header.Get(k) != want {
			t.Errorf("%s %q, want %q", k, r.header.Get(k), want)
		}
	}
	if r.header.Get("Content-Disposition") != "" {
		t.Errorf("Content-Disposition %q", r.header.Get("Content-Disposition"))
	}
}

func assertEventPageCSP(t *testing.T, h http.Header) {
	t.Helper()
	csp := h.Get("Content-Security-Policy")
	for _, want := range []string{
		"default-src 'none'",
		"connect-src 'none'",
		"base-uri 'none'",
		"form-action 'none'",
		"frame-ancestors 'none'",
		"img-src 'self'",
	} {
		if !strings.Contains(csp, want) {
			t.Errorf("CSP missing %s: %s", want, csp)
		}
	}
	if strings.Contains(csp, "*") {
		t.Errorf("CSP has a wildcard: %s", csp)
	}
	m := scriptSrcRe.FindStringSubmatch(csp)
	if m == nil || strings.Contains(m[1], "unsafe-inline") || cspNonceRe.FindStringSubmatch(m[1]) == nil {
		t.Errorf("script-src is not strict: %s", csp)
	}
	img := regexp.MustCompile(`img-src ([^;]*)`).FindStringSubmatch(csp)
	if img == nil || !strings.Contains(img[1], "'self'") || strings.Contains(img[1], "*") {
		t.Errorf("img-src %q", csp)
	}
}

func galleryCardHTML(body string) []string {
	var out []string
	rest := body
	const open = `<li class="gcard">`
	for {
		i := strings.Index(rest, open)
		if i < 0 {
			return out
		}
		rest = rest[i:]
		j := strings.Index(rest, "</li>")
		if j < 0 {
			return out
		}
		out = append(out, rest[:j])
		rest = rest[j+5:]
	}
}

func galleryCardHref(card string) string {
	const key = `href="`
	i := strings.Index(card, key)
	if i < 0 {
		return ""
	}
	rest := card[i+len(key):]
	j := strings.Index(rest, `"`)
	if j < 0 {
		return ""
	}
	return rest[:j]
}

func pageStyle(body string) string {
	i := strings.Index(body, ".wrap {")
	if i < 0 {
		return ""
	}
	start := strings.LastIndex(body[:i], "<style>")
	end := strings.Index(body[i:], "</style>")
	if start < 0 || end < 0 {
		return ""
	}
	return body[start : i+end]
}
