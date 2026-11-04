package hackhttp

import (
	"net/http"
	"regexp"
	"strings"
	"testing"
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
	p.Stage = "building" // "sites" is in the stage order from open on (gallery_open)
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
	sites := strings.Index(body, `id="sites"`)
	if prizes < 0 || sites < 0 || sites > prizes {
		t.Fatalf("Team sites is not before Prizes in the building order")
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
		t.Fatalf("cards: %d, body:\n%s", len(cards), body)
	}
	if !strings.Contains(cards[0], `src="/screenshots/qqq"`) || !strings.Contains(cards[0], `alt=""`) || !strings.Contains(cards[0], `loading="lazy"`) {
		t.Fatalf("shot img: %s", cards[0])
	}
	if !regexp.MustCompile(`<img[^>]*\bwidth="\d+"[^>]*\bheight="\d+"`).MatchString(cards[0]) {
		t.Fatalf("shot img missing width/height (fixes list: layout shift): %s", cards[0])
	}
	if strings.Contains(cards[1], "<img") || strings.Contains(cards[1], "site-team") {
		t.Fatalf("team without a different title still has a shot or a team line: %s", cards[1])
	}
	if strings.Contains(cards[2], "site-team") {
		t.Fatalf("title equal to the team name still shows the team line: %s", cards[2])
	}
	if !strings.Contains(cards[0], `class="site-team">by Qqq</span>`) {
		t.Fatalf("missing team line: %s", cards[0])
	}
	for i, card := range cards {
		if strings.Contains(card, `target="_blank"`) {
			t.Fatalf("card %d link: %s", i, card)
		}
	}
	// The link's accessible name comes from its visible content (the title,
	// tagline and team line), not an aria-label that would hide them from a
	// screen reader (review 2026-10-30 item 6).
	if strings.Contains(cards[0], "aria-label") {
		t.Fatalf("card 0 still has an aria-label hiding its visible content: %s", cards[0])
	}
	if !strings.Contains(cards[0], "<b>bravo &lt;script&gt;alert(1)&lt;/script&gt;</b>") {
		t.Fatalf("card 0 title not in the link's accessible name: %s", cards[0])
	}
	if galleryCardHref(cards[0]) != "https://qqq.weekend.simple-hack.test/" {
		t.Fatalf("href %q", galleryCardHref(cards[0]))
	}
	if !strings.Contains(body, `href="`+p.AppURL+`/hack/kit.css"`) {
		t.Error("page does not load the app kit")
	}
	if hit := regexp.MustCompile(`<style>[^<]*#[0-9a-fA-F]{3,8}`).FindString(body); hit != "" {
		t.Errorf("page's own inline style has a hard-coded colour: %s", hit)
	}
	assertEventPageCSP(t, rec.Header())

	p.TakenDown = true
	gone := renderHackEvent(t, p)
	if gone.Code != http.StatusGone {
		t.Fatalf("taken down status %d", gone.Code)
	}
	if strings.Contains(gone.Body.String(), `id="sites"`) || strings.Contains(gone.Body.String(), "site-card") {
		t.Fatal("taken-down page still shows the gallery")
	}
}
