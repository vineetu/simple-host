package handler

import (
	"bytes"
	"html/template"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"regexp"
	"strings"
	"testing"
	"time"
)

func testHackEvent() hackEventPage {
	return hackEventPage{
		Slug:            "weekend",
		Title:           "Spring hack",
		Tagline:         "Build something",
		About:           "A weekend of making.\n\nBring a laptop.",
		Rules:           "Be kind.",
		Prizes:          "Glory.",
		OrganiserName:   "Ada",
		Organisation:    "Example Club",
		Stage:           "open",
		TimeZone:        "Europe/London",
		StartsAt:        time.Date(2027, 3, 12, 10, 0, 0, 0, time.UTC),
		EndsAt:          time.Date(2027, 3, 14, 18, 0, 0, 0, time.UTC),
		Participants:    12,
		Teams:           4,
		AppURL:          "https://simple-hack.app",
		TakenDown:       false,
		TakenDownReason: "",
	}
}

func renderHackEvent(t *testing.T, p hackEventPage) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	renderHackEventPage(rec, req, p)
	return rec
}

func TestHackEventPageEscapesOrganiserText(t *testing.T) {
	payload := `<script>alert(1)</script>"><img src=x onerror=1>{{.}}`
	p := hackEventPage{
		Slug:            payload,
		Title:           payload,
		Tagline:         payload,
		About:           payload,
		Rules:           payload,
		Prizes:          payload,
		OrganiserName:   payload,
		Organisation:    payload,
		Stage:           payload,
		TimeZone:        payload,
		TakenDownReason: payload,
		AppURL:          "https://simple-hack.app",
		Participants:    1,
		Teams:           1,
	}
	rec := renderHackEvent(t, p)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	body := rec.Body.String()
	for _, raw := range []string{
		`<script>alert(1)</script>`,
		`"><img src=x onerror=1>`,
		`<img src=x onerror=1>`,
	} {
		if strings.Contains(body, raw) {
			t.Errorf("unescaped payload %q", raw)
		}
	}
	if !strings.Contains(body, "&lt;script&gt;") {
		t.Error("script payload was not escaped")
	}
	// {{.}} must not have been executed as a template action.
	if strings.Contains(body, "hackEventPage") || strings.Contains(body, "hackEventView") {
		t.Error("{{.}} expanded as a template")
	}
}

func TestHackEventPageCSPNonce(t *testing.T) {
	rec := renderHackEvent(t, testHackEvent())
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	csp := rec.Header().Get("Content-Security-Policy")
	if csp == "" {
		t.Fatal("missing Content-Security-Policy")
	}
	m := regexp.MustCompile(`'nonce-([A-Za-z0-9+/=]+)'`).FindStringSubmatch(csp)
	if m == nil {
		t.Fatalf("CSP has no nonce: %q", csp)
	}
	nonce := m[1]
	body := rec.Body.String()
	n := 0
	for _, tag := range regexp.MustCompile(`<script\b[^>]*>`).FindAllString(body, -1) {
		n++
		if !strings.Contains(tag, `nonce="`+nonce+`"`) {
			t.Errorf("script without the response nonce: %s", tag)
		}
	}
	if n == 0 {
		t.Error("no <script> tags; the check is not looking at a real page")
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("Cache-Control %q", rec.Header().Get("Cache-Control"))
	}
	if rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("X-Content-Type-Options %q", rec.Header().Get("X-Content-Type-Options"))
	}
}

func TestHackEventPageDraftNoindex(t *testing.T) {
	p := testHackEvent()
	p.Stage = "draft"
	rec := renderHackEvent(t, p)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	if rec.Header().Get("X-Robots-Tag") != "noindex" {
		t.Errorf("X-Robots-Tag %q, want noindex", rec.Header().Get("X-Robots-Tag"))
	}
	if !strings.Contains(rec.Body.String(), "Not open yet.") {
		t.Error("draft page missing status")
	}
}

func TestHackEventPageTakenDown(t *testing.T) {
	p := testHackEvent()
	p.TakenDown = true
	p.TakenDownReason = "The organiser asked us to."
	p.About = "SECRET_ABOUT_SHOULD_NOT_APPEAR"
	p.Rules = "SECRET_RULES_SHOULD_NOT_APPEAR"
	p.Prizes = "SECRET_PRIZES_SHOULD_NOT_APPEAR"
	p.Tagline = "SECRET_TAGLINE_SHOULD_NOT_APPEAR"
	rec := renderHackEvent(t, p)
	if rec.Code != http.StatusGone {
		t.Fatalf("status %d, want 410", rec.Code)
	}
	if rec.Header().Get("X-Robots-Tag") != "noindex" {
		t.Errorf("X-Robots-Tag %q, want noindex", rec.Header().Get("X-Robots-Tag"))
	}
	body := rec.Body.String()
	if !strings.Contains(body, "This event has been taken down.") {
		t.Error("missing take-down copy")
	}
	if !strings.Contains(body, "The organiser asked us to.") {
		t.Error("missing take-down reason")
	}
	if !strings.Contains(body, "Spring hack") {
		t.Error("missing title")
	}
	for _, secret := range []string{
		"SECRET_ABOUT_SHOULD_NOT_APPEAR",
		"SECRET_RULES_SHOULD_NOT_APPEAR",
		"SECRET_PRIZES_SHOULD_NOT_APPEAR",
		"SECRET_TAGLINE_SHOULD_NOT_APPEAR",
		"A weekend of making",
	} {
		if strings.Contains(body, secret) {
			t.Errorf("taken-down page still shows %q", secret)
		}
	}
}

func TestHackEventPageDatesInEventZone(t *testing.T) {
	p := testHackEvent()
	rec := renderHackEvent(t, p)
	body := rec.Body.String()
	want := "12–14 March 2027 · Europe/London"
	if !strings.Contains(body, want) {
		t.Errorf("dates: want %q in body", want)
	}

	p.EndsAt = time.Time{}
	body = renderHackEvent(t, p).Body.String()
	if !strings.Contains(body, "12 March 2027 · Europe/London") {
		t.Error("start-only date missing")
	}

	p.StartsAt = time.Time{}
	body = renderHackEvent(t, p).Body.String()
	if strings.Contains(body, "March 2027") {
		t.Error("no dates set but a date line was shown")
	}
}

func TestHackEventPageBadZoneFallsBackToUTC(t *testing.T) {
	p := testHackEvent()
	p.TimeZone = "Not/A Zone"
	rec := renderHackEvent(t, p)
	if rec.Code != http.StatusOK {
		t.Fatalf("bad zone status %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "12–14 March 2027 · UTC") {
		t.Errorf("bad zone: want UTC dates, got body without them")
	}
	if strings.Contains(body, "Not/A Zone") {
		t.Error("invalid zone name leaked onto the page")
	}
}

func TestHackChromeHeader(t *testing.T) {
	orig := hackChrome
	t.Cleanup(func() { SetHackChrome(orig) })

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	page := []byte("<!--sh:header--><!--sh:footer-->")

	SetHackChrome(true)
	on, err := withChrome(page, chromeDataFor(r, ""))
	if err != nil {
		t.Fatal(err)
	}
	body := string(on)
	if !strings.Contains(body, `simple<b>·</b>hack`) {
		t.Error("hack chrome: missing simple·hack logo")
	}
	if !strings.Contains(body, `href="/signin"`) {
		t.Error("hack chrome: sign-in does not link /signin")
	}
	if !strings.Contains(body, `data-signed-in="Your events"`) {
		t.Error("hack chrome: sign-in label is not Your events")
	}
	if strings.Contains(body, `simple<b>·</b>host`) {
		t.Error("hack chrome: host logo still present")
	}
	for _, want := range []string{
		`href="https://simple-host.app/"`,
		`href="https://simple-host.app/hackathons"`,
		`href="https://simple-host.app/privacy.html"`,
		`href="https://simple-host.app/terms"`,
		`href="/report"`,
		">Self-host an event<",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("hack footer missing %s", want)
		}
	}
	if strings.Contains(body, "With thanks to Jacob Cole") {
		t.Error("hack footer still has the credit line")
	}
	headerPart := body
	if i := strings.Index(body, "<footer"); i >= 0 {
		headerPart = body[:i]
	}
	if strings.Contains(headerPart, "Self-host") {
		t.Error("hack header still has a Self-host link")
	}
	if !strings.Contains(body, `class="sh-header sh-hack"`) {
		t.Error("hack header missing sh-hack class")
	}

	SetHackChrome(false)
	off, err := withChrome(page, chromeDataFor(r, ""))
	if err != nil {
		t.Fatal(err)
	}
	body = string(off)
	if !strings.Contains(body, `simple<b>·</b>host`) {
		t.Error("host chrome: missing simple·host logo")
	}
	if strings.Contains(body, `simple<b>·</b>hack`) {
		t.Error("host chrome: hack logo leaked")
	}
}

func TestHackEventPageKeepsLineBreaks(t *testing.T) {
	p := testHackEvent()
	p.About = "First line.\nSecond line.\n\nNew paragraph."
	body := renderHackEvent(t, p).Body.String()
	if !strings.Contains(body, "First line.<br>Second line.") {
		t.Error("single newlines were not turned into <br>")
	}
	if !strings.Contains(body, "<p>New paragraph.</p>") {
		t.Error("blank line did not start a new paragraph")
	}
}

func TestHackHomePage(t *testing.T) {
	orig := hackChrome
	t.Cleanup(func() { SetHackChrome(orig) })
	SetHackChrome(true)

	mux := http.NewServeMux()
	RegisterHackHome(mux)
	rec := get(t, mux, "simple-hack.app", "/")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{
		"Run your hackathon here.",
		`href="/events/new"`,
		"Already running one?",
		`href="/events"`,
		`id="film"`,
		`sh-film-seen`,
		`id="replayBtn"`,
		`class="footlinks"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("hack home missing %q", want)
		}
	}
	if rec.Header().Get("X-Robots-Tag") == "noindex" {
		t.Error("product page should be indexable")
	}
}

func TestHackEventPageOmitsEmptySections(t *testing.T) {
	p := testHackEvent()
	p.About, p.Rules, p.Prizes = "", "", ""
	p.Teams = 0
	p.Stage = "building"
	rec := renderHackEvent(t, p)
	if rec.Header().Get("X-Robots-Tag") != "" {
		t.Errorf("open-stage-like page is noindex: %q", rec.Header().Get("X-Robots-Tag"))
	}
	body := rec.Body.String()
	for _, banned := range []string{"<h2>About</h2>", "<h2>Rules</h2>", "<h2>Prizes</h2>", "<h2>Projects</h2>", "Projects appear here", "teams, "} {
		if strings.Contains(body, banned) {
			t.Errorf("empty field still rendered %q", banned)
		}
	}
	if !strings.Contains(body, "Teams are building.") {
		t.Error("building status missing")
	}
}

func TestHackEventPageLayout(t *testing.T) {
	rec := renderHackEvent(t, testHackEvent())
	body := rec.Body.String()
	for _, want := range []string{
		"<h1>Spring hack</h1>",
		"Build something",
		"Organised by Ada (Example Club)",
		"Sign-up is open. Ask the organisers for the join link.",
		"<h2>About</h2>",
		"A weekend of making.",
		"Bring a laptop.",
		"<h2>Rules</h2>",
		"<h2>Prizes</h2>",
		"4 teams, 12 participants",
		`class="count"`,
		`class="sh-header"`,
		`class="sh-footer"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}
	if strings.Contains(body, "Projects") {
		t.Error("projects section still present")
	}
	statusAt := strings.Index(body, "Sign-up is open. Ask the organisers for the join link.")
	countAt := strings.Index(body, "4 teams, 12 participants")
	aboutAt := strings.Index(body, "<h2>About</h2>")
	if statusAt < 0 || countAt < statusAt || (aboutAt >= 0 && countAt > aboutAt) {
		t.Error("team count is not a line under the status")
	}
}

// TestNonHackPartialsMatchBaseline renders header, footer and head with Hack
// false and compares them to the same partials at ea2c6ac. simple-host.app must
// not pick up the hack chrome.
func TestNonHackPartialsMatchBaseline(t *testing.T) {
	names := []string{"header.html", "footer.html", "head.html"}
	old := template.New("baseline")
	for _, name := range names {
		out, err := exec.Command("git", "show", "ea2c6ac:internal/handler/static/partials/"+name).Output()
		if err != nil {
			t.Skipf("baseline not available (needs git and commit ea2c6ac): %s: %v", name, err)
		}
		if _, err := old.New(name).Parse(string(out)); err != nil {
			t.Fatalf("parse baseline %s: %v", name, err)
		}
	}
	for _, name := range []string{"theme.html", "dialog.html"} {
		b, err := staticFiles.ReadFile("static/partials/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := old.New(name).Parse(string(b)); err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
	}
	samples := []chromeData{
		{},
		{Current: "home"},
		{HackHome: true, Current: "home"},
		{Base: "https://simple-host.app", Current: "enterprise"},
		{Base: "https://simple-host.app", Current: "install"},
		{Current: "report"},
		{Current: "hackathons"},
		{Current: "dashboard"},
		{CSSVersion: "abc"},
	}
	for _, d := range samples {
		for _, name := range names {
			var got, want bytes.Buffer
			if err := chromeTemplates.ExecuteTemplate(&got, name, d); err != nil {
				t.Fatalf("current %s %+v: %v", name, d, err)
			}
			if err := old.ExecuteTemplate(&want, name, d); err != nil {
				t.Fatalf("baseline %s %+v: %v", name, d, err)
			}
			if got.String() != want.String() {
				t.Errorf("%s mismatch for %+v\n--- got ---\n%s\n--- want ---\n%s", name, d, got.String(), want.String())
			}
		}
	}
	var hackHead bytes.Buffer
	if err := chromeTemplates.ExecuteTemplate(&hackHead, "head.html", chromeData{Hack: true}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(hackHead.String(), `<meta name="sh-hack" content="1">`) {
		t.Error("hack head missing sh-hack meta")
	}
}

func TestHackEventPageResultsWinnersOnly(t *testing.T) {
	p := testHackEvent()
	p.Results = &hackResultsView{
		FullRanking: false,
		Rows: []hackResultsRow{
			{Rank: 1, TeamName: "Team <Alpha>"},
		},
	}
	rec := renderHackEvent(t, p)
	body := rec.Body.String()
	if !strings.Contains(body, "<h2>Winners</h2>") {
		t.Error("winners heading missing")
	}
	if !strings.Contains(body, "Team &lt;Alpha&gt;") {
		t.Error("team name not escaped or missing")
	}
	if strings.Contains(body, "<h2>Results</h2>") {
		t.Error("full-ranking heading shown for winners-only")
	}
}

func TestHackEventPageResultsFullRanking(t *testing.T) {
	p := testHackEvent()
	p.Results = &hackResultsView{
		FullRanking: true,
		Rows: []hackResultsRow{
			{Rank: 1, TeamName: "Alpha", Tied: true},
			{Rank: 1, TeamName: "Beta", Tied: true},
			{Rank: 3, TeamName: "Gamma"},
		},
	}
	rec := renderHackEvent(t, p)
	body := rec.Body.String()
	if !strings.Contains(body, "<h2>Results</h2>") {
		t.Error("full-ranking heading missing")
	}
	if strings.Count(body, `class="results-tie-note"`) != 2 {
		t.Errorf("expected 2 tie notes, body: %s", body)
	}
	if !strings.Contains(body, "Gamma") {
		t.Error("rank-3 team missing from full ranking")
	}
}

func TestHackEventPageNoResultsSectionWhenUnpublished(t *testing.T) {
	p := testHackEvent()
	p.Results = nil
	rec := renderHackEvent(t, p)
	body := rec.Body.String()
	for _, banned := range []string{"<h2>Winners</h2>", "<h2>Results</h2>", `class="results-list"`} {
		if strings.Contains(body, banned) {
			t.Errorf("results section rendered with nothing published: %q", banned)
		}
	}
}
