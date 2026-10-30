package handler

import (
	"bytes"
	"fmt"
	"github.com/vsriram/simple-host/internal/db"
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
	if !strings.Contains(rec.Body.String(), `<span class="pill stage"><span class="d"></span>Not open yet</span>`) {
		t.Error("draft page missing stage pill")
	}
	if !strings.Contains(rec.Body.String(), "The organisers are still setting this up") {
		t.Error("draft page missing stage card")
	}
}

func TestHackEventPageTakenDown(t *testing.T) {
	p := testHackEvent()
	p.TakenDown = true
	p.TakenDownReason = "The organiser asked us to."
	p.IconURL = p.AppURL + "/v1/hack/events/" + p.Slug + "/icon"
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
	if strings.Contains(body, p.IconURL) || !strings.Contains(body, p.AppURL+"/favicon.svg") {
		t.Fatal("taken-down page requests a blocked icon instead of the platform favicon")
	}
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
	for _, want := range []string{`<span class="f">12–14 March 2027</span>`, `<span class="f">times in GMT</span>`} {
		if !strings.Contains(body, want) {
			t.Errorf("dates: want %q in body", want)
		}
	}

	p.EndsAt = time.Time{}
	body = renderHackEvent(t, p).Body.String()
	if !strings.Contains(body, `<span class="f">12 March 2027</span>`) {
		t.Error("start-only date missing")
	}

	p.StartsAt = time.Time{}
	body = renderHackEvent(t, p).Body.String()
	if strings.Contains(body, "March 2027") {
		t.Error("no dates set but a date line was shown")
	}
	if strings.Contains(body, "times in") {
		t.Error("zone note shown with no date line")
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
	for _, want := range []string{`<span class="f">12–14 March 2027</span>`, `<span class="f">times in UTC</span>`} {
		if !strings.Contains(body, want) {
			t.Errorf("bad zone: want %q in body", want)
		}
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
	if strings.Contains(body, `<b>-</b><span class="sh-logo-b">host</span>`) {
		t.Error("hack chrome: host logo still present")
	}
	for _, want := range []string{
		`href="https://simple-host.app/"`,
		`href="https://simple-host.app/hackathons"`,
		`href="/privacy.html"`,
		`href="/terms"`,
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
	if !strings.Contains(body, `<b>-</b><span class="sh-logo-b">host</span>`) {
		t.Error("host chrome: missing simple-host logo")
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
		"Simple Hack: hackathons, now for everyone.",
		`href="/events/new"`,
		"Want fresh ideas from your people? Run a hackathon. Free.",
		`href="/events"`,
		`id="film"`,
		`id="bigPlay"`,
		`id="againBtn"`,
		`id="playBtn"`,
		`id="seek"`,
		`id="muteBtn"`,
		`id="endCard"`,
		`class="sh-footer"`,
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
	for _, banned := range []string{"<h2>About</h2>", "<h2>Rules</h2>", "<h2>Prizes</h2>", "<h2>Projects</h2>", "Projects appear here", "teams ·"} {
		if strings.Contains(body, banned) {
			t.Errorf("empty field still rendered %q", banned)
		}
	}
	if !strings.Contains(body, "Building is under way") {
		t.Error("building status missing")
	}
}

// TestHackEventPageBuildingHeadingPointsAtGallery (audit Part B
// hack-event.html item 1): once there is something to see, the Building
// stage card says so instead of the generic placeholder, and the stage
// pill is no longer repeated by a kicker above the heading.
func TestHackEventPageBuildingHeadingPointsAtGallery(t *testing.T) {
	p := testHackEvent()
	p.Stage = "building"
	p.Gallery = []hackGalleryCard{{Title: "Alpha", URL: "https://alpha.example.com/"}}
	body := renderHackEvent(t, p).Body.String()
	if !strings.Contains(body, "<h2>See what teams are making</h2>") {
		t.Error("building heading did not point at the gallery")
	}
	// The stage pill still legitimately says "Teams are building"; only the
	// stage-card heading (and its kicker, which this removes outright) must
	// stop repeating it.
	if strings.Contains(body, "<h2>Teams are building</h2>") || strings.Contains(body, "<h2>Teams are building.</h2>") {
		t.Error("building stage card heading still says the generic placeholder with a gallery present")
	}
	if strings.Contains(body, `class="k"`) {
		t.Error("stage card kicker still rendered, repeating the stage pill")
	}
}

// TestHackEventPageNoCountsInDraft: the header's team/participant count line
// never shows in draft, whatever the numbers are (review 2026-10-30 item 7).
func TestHackEventPageNoCountsInDraft(t *testing.T) {
	p := testHackEvent()
	p.Stage = "draft"
	p.Teams, p.Participants = 4, 12
	body := renderHackEvent(t, p).Body.String()
	if strings.Contains(body, `class="ev-counts`) || strings.Contains(body, "participants") {
		t.Errorf("draft page shows a counts line: %s", body)
	}
}

// TestHackEventPageNoAnyoneCanJoin: fixes list — "'Anyone can join. No coding
// needed.' was shown on every open or building event, also on approval-only
// ones. Removed." Confirm it is gone for good, on every stage.
func TestHackEventPageNoAnyoneCanJoin(t *testing.T) {
	join := "Anyone can join. No coding needed."
	p := testHackEvent()
	for _, stage := range []string{"draft", "open", "building", "closed", "judging", "results", "archived"} {
		p.Stage = stage
		if strings.Contains(renderHackEvent(t, p).Body.String(), join) {
			t.Errorf("stage %s: join line still shown", stage)
		}
	}
}

func TestHackEventPageLayout(t *testing.T) {
	rec := renderHackEvent(t, testHackEvent())
	body := rec.Body.String()
	for _, want := range []string{
		"<h1>Spring hack</h1>",
		"Build something",
		`<span class="f">Organised by Ada (Example Club)</span>`,
		`<span class="pill stage"><span class="d"></span>Sign-up is open</span>`,
		"Join with the link from the organisers",
		"<h2>About</h2>",
		"A weekend of making.",
		"Bring a laptop.",
		"<h2>Rules</h2>",
		"<h2>Prizes</h2>",
		`<div class="ev-counts muted small">4 teams · 12 participants</div>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}
	if strings.Contains(body, "Projects") {
		t.Error("projects section still present")
	}
	stageAt := strings.Index(body, `class="ev-stage"`)
	countAt := strings.Index(body, "4 teams · 12 participants")
	aboutAt := strings.Index(body, "<h2>About</h2>")
	if stageAt < 0 || countAt < stageAt || (aboutAt >= 0 && countAt > aboutAt) {
		t.Error("team count is not a line under the stage pill, in the header")
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
		// Shared pages now name the existing favicon, versioned so a browser that
		// cached the pre-rebrand icon fetches the new one. The rest of the chrome
		// stays fixed.
		if name == "head.html" {
			out = append([]byte(`<link rel="icon" href="{{.Base}}/favicon.svg?v=c" type="image/svg+xml">`+"\n"+
				`<link rel="icon" type="image/png" sizes="32x32" href="{{.Base}}/favicon-32.png?v=c">`+"\n"+
				`<link rel="icon" href="{{.Base}}/favicon.ico?v=c" sizes="any">`+"\n"+
				`<link rel="apple-touch-icon" href="{{.Base}}/apple-touch-icon.png?v=c">`+"\n"), out...)
		}
		// The Host logo is two inks (2026-10-10), like Simple Hack's, with a dash
		// rather than a dot so it reads like the domain.
		if name == "header.html" {
			out = bytes.Replace(out, []byte(`">simple<b>·</b>host</a>`), []byte(`"><span class="sh-logo-a">simple</span><b>-</b><span class="sh-logo-b">host</span></a>`), 1)
		}
		// 2026-10-30: the footer gained a Cost and limits link next to Features
		// (SIM-190), a plain nav addition, not a step toward the hack chrome.
		if name == "footer.html" {
			before := `<a href="{{.Base}}/features"{{if eq .Current "features"}} aria-current="page"{{end}}>Features</a>` + "\n        "
			after := before + `<a href="{{.Base}}/pricing"{{if eq .Current "pricing"}} aria-current="page"{{end}}>Cost and limits</a>` + "\n        "
			out = bytes.Replace(out, []byte(before), []byte(after), 1)
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
			{Rank: 1, Overall: true, TeamName: "Team <Alpha>", URL: "https://alpha.example.com/"},
		},
	}
	rec := renderHackEvent(t, p)
	body := rec.Body.String()
	if !strings.Contains(body, `<h2 class="section-title">Winners</h2>`) {
		t.Error("winners heading missing")
	}
	if !strings.Contains(body, "Team &lt;Alpha&gt;") {
		t.Error("team name not escaped or missing")
	}
	if !strings.Contains(body, `href="https://alpha.example.com/"`) {
		t.Error("winner site link missing")
	}
	if !strings.Contains(body, `<span class="av accent">1</span>`) || !strings.Contains(body, `<span class="pill accent">Winner</span>`) {
		t.Error("winner avatar/pill missing")
	}
	if strings.Contains(body, `<h2 class="section-title">Results</h2>`) {
		t.Error("full-ranking heading shown for winners-only")
	}
}

// TestHackEventPageResultsWinnersOnlyTrackWinnerIsAlsoOverall (review
// 2026-10-30 item 1): markTrackWinners makes the rank-1 team the winner of
// its own track too, so a winners-only row can carry both the "Winner" pill
// (avatar "1") and a "<Track> winner" pill at once — it must not lose the
// Winner pill just because it is also a track winner.
func TestHackEventPageResultsWinnersOnlyTrackWinnerIsAlsoOverall(t *testing.T) {
	p := testHackEvent()
	p.Results = &hackResultsView{
		FullRanking: false,
		Rows: []hackResultsRow{
			{Rank: 1, Overall: true, TrackWinner: true, TrackName: "AI", TeamName: "Alpha", URL: "https://alpha.example.com/"},
			{TrackWinner: true, TrackName: "Climate", TeamName: "Beta", URL: "https://beta.example.com/"},
		},
	}
	body := renderHackEvent(t, p).Body.String()
	if !strings.Contains(body, `<span class="av accent">1</span>`) || !strings.Contains(body, `<span class="pill accent">Winner</span>`) {
		t.Error("overall-winner-and-track-winner row lost its Winner pill/avatar")
	}
	if !strings.Contains(body, `<span class="pill">AI winner</span>`) {
		t.Error("overall-winner-and-track-winner row lost its track pill")
	}
	if !strings.Contains(body, `<span class="pill">Climate winner</span>`) {
		t.Error("track-only winner missing its track pill")
	}
	if strings.Count(body, `<span class="pill accent">Winner</span>`) != 1 {
		t.Error("a non-overall track winner should not also get the Winner pill")
	}
}

func TestHackEventPageResultsFullRanking(t *testing.T) {
	p := testHackEvent()
	p.Results = &hackResultsView{
		FullRanking: true,
		Rows: []hackResultsRow{
			{Rank: 1, TeamName: "Alpha", Tied: true},
			{Rank: 1, TeamName: "Beta", Tied: true},
			{Rank: 3, TeamName: "Gamma", URL: "https://gamma.example.com/"},
		},
	}
	rec := renderHackEvent(t, p)
	body := rec.Body.String()
	if !strings.Contains(body, `<h2 class="section-title">Results</h2>`) {
		t.Error("full-ranking heading missing")
	}
	if strings.Count(body, `<span class="pill">Tied</span>`) != 2 {
		t.Errorf("expected 2 tie pills, body: %s", body)
	}
	if !strings.Contains(body, "Gamma") {
		t.Error("rank-3 team missing from full ranking")
	}
	if !strings.Contains(body, `<span class="rank">3</span>`) {
		t.Error("rank-3 number missing")
	}
	if !strings.Contains(body, `href="https://gamma.example.com/"`) {
		t.Error("ranked site link missing")
	}
}

func TestHackEventPageNoResultsSectionWhenUnpublished(t *testing.T) {
	p := testHackEvent()
	p.Stage = "results"
	p.Results = nil
	rec := renderHackEvent(t, p)
	body := rec.Body.String()
	// id="results" is the one marker that can't drift with a markup reorder;
	// ban that directly rather than a heading/class string the markup no
	// longer uses (review 2026-10-30 item 7: this test could not fail).
	if strings.Contains(body, `id="results"`) {
		t.Error("results section rendered with nothing published")
	}
	for _, banned := range []string{"Results are in.", "<h2>Winners</h2>", "<h2>Results</h2>"} {
		if strings.Contains(body, banned) {
			t.Errorf("results section rendered with nothing published: %q", banned)
		}
	}
}

func TestHackSchedulePreservesSavedOrder(t *testing.T) {
	p := testHackEvent()
	p.Content.Schedule = []hackScheduleItem{
		{Title: "Closing item", StartAt: "2027-03-14T16:00:00Z", EndAt: "2027-03-14T17:00:00Z"},
		{Title: "Opening item", StartAt: "2027-03-12T10:00:00Z", EndAt: "2027-03-12T11:00:00Z"},
	}
	r := renderHackEvent(t, p)
	body := r.Body.String()
	if r.Code != 200 || strings.Index(body, "Closing item") < 0 || strings.Index(body, "Closing item") > strings.Index(body, "Opening item") {
		t.Fatal("saved schedule order was lost")
	}
}

// TestHackScheduleMultiDayShowsEndDate (fixes list: "Multi-day schedule items
// showed only the end time ... The end date is shown when it is a different day").
func TestHackScheduleMultiDayShowsEndDate(t *testing.T) {
	p := testHackEvent()
	p.TimeZone = "Asia/Jakarta"
	p.Content.Schedule = []hackScheduleItem{
		{Title: "Build time", StartAt: "2027-03-12T03:00:00Z", EndAt: "2027-03-13T11:00:00Z"}, // spans a day in WIB
		{Title: "Opening", StartAt: "2027-03-12T02:00:00Z", EndAt: "2027-03-12T03:00:00Z"},    // same day in WIB
	}
	body := renderHackEvent(t, p).Body.String()
	if !strings.Contains(body, "Fri 12 Mar 10:00 am – Sat 13 Mar 6:00 pm WIB") {
		t.Errorf("multi-day item missing the end date, body:\n%s", body)
	}
	if !strings.Contains(body, "Fri 12 Mar, 9:00 am–10:00 am WIB") {
		t.Errorf("same-day item should not repeat the date, body:\n%s", body)
	}
}

// TestHackEventPageOGTags: tech_notes — title, meta description, og:title,
// og:description, og:type, og:image (when there is an icon and the event is
// not taken down) and twitter:card, all present.
func TestHackEventPageOGTags(t *testing.T) {
	p := testHackEvent()
	p.IconURL = p.AppURL + "/v1/hack/events/" + p.Slug + "/icon"
	body := renderHackEvent(t, p).Body.String()
	for _, want := range []string{
		"<title>Spring hack · Simple Hack</title>",
		`<meta name="description" content="Build something">`,
		`<meta property="og:title" content="Spring hack">`,
		`<meta property="og:description" content="Build something">`,
		`<meta property="og:type" content="website">`,
		`<meta property="og:image" content="` + p.IconURL + `">`,
		`<meta name="twitter:card" content="summary">`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}

	// No tagline: falls back to the stage words, not an empty description.
	p.Tagline = ""
	body = renderHackEvent(t, p).Body.String()
	if !strings.Contains(body, `<meta name="description" content="Sign-up is open">`) {
		t.Error("description did not fall back to the stage words")
	}

	// Taken down: no OG tags, no link-preview image, noindex instead.
	p.TakenDown = true
	body = renderHackEvent(t, p).Body.String()
	for _, banned := range []string{"og:title", "og:description", "og:image", "twitter:card"} {
		if strings.Contains(body, banned) {
			t.Errorf("taken-down page still has %q", banned)
		}
	}
	if !strings.Contains(body, `<meta name="robots" content="noindex">`) {
		t.Error("taken-down page missing noindex")
	}
}

// TestHackZoneAbbrFallsBackToIANAName: Go's tzdata does not give every zone a
// short code; a bare numeric offset ("+07") falls back to the IANA name
// instead of leaking something unreadable onto the page.
func TestHackZoneAbbrFallsBackToIANAName(t *testing.T) {
	numeric := time.Date(2026, 10, 28, 9, 0, 0, 0, time.FixedZone("+07", 7*3600))
	if got := hackZoneAbbr(numeric, "Asia/Jakarta"); got != "Asia/Jakarta" {
		t.Errorf("numeric offset: got %q, want the IANA fallback", got)
	}
	named := time.Date(2026, 10, 28, 9, 0, 0, 0, time.FixedZone("WIB", 7*3600))
	if got := hackZoneAbbr(named, "Asia/Jakarta"); got != "WIB" {
		t.Errorf("real abbreviation: got %q, want %q", got, "WIB")
	}
}

// TestHackEventPageSponsorLinkSaysWebsite (audit Part B hack-event.html
// item 9): the sponsor's name is already shown next to the logo, so the
// link just says "Website" instead of repeating "Visit <sponsor>" — each
// one still points at that sponsor's own URL and keeps an aria-label for
// anyone using a screen reader's link list.
func TestHackEventPageSponsorLinkSaysWebsite(t *testing.T) {
	p := testHackEvent()
	p.Content.Sponsors = []hackSponsor{
		{Name: "Northwind Analytics", Tier: "Gold", URL: "https://example.com/northwind"},
		{Name: "Riverside Cloud", Tier: "Silver", URL: "https://example.com/riverside"},
	}
	body := renderHackEvent(t, p).Body.String()
	for _, want := range []string{
		`href="https://example.com/northwind" aria-label="Northwind Analytics website">Website</a>`,
		`href="https://example.com/riverside" aria-label="Riverside Cloud website">Website</a>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}
	for _, banned := range []string{">Visit sponsor</a>", "Visit Northwind Analytics", "Visit Riverside Cloud"} {
		if strings.Contains(body, banned) {
			t.Errorf("old per-sponsor 'Visit <name>' link text still used: %q", banned)
		}
	}
}

// TestHackEventPageScoreMethodLine: the score-method line is said once, in
// plain words (open_questions: "Scores are the judges' average" / "Scores
// are adjusted so strict and generous judges count the same").
func TestHackEventPageScoreMethodLine(t *testing.T) {
	raw := "Scores are the judges&#39; average."
	normalised := "Scores are adjusted so strict and generous judges count the same."
	for _, c := range []struct {
		mode, want, banned string
	}{
		{"raw", raw, normalised},
		{"normalised", normalised, raw},
	} {
		p := testHackEvent()
		p.Results = &hackResultsView{Rows: []hackResultsRow{
			{Rank: 1, TeamName: "Alpha", Score: "80.00", ScoreMode: c.mode},
		}}
		body := renderHackEvent(t, p).Body.String()
		if !strings.Contains(body, c.want) {
			t.Errorf("mode %s: missing %q", c.mode, c.want)
		}
		if strings.Contains(body, c.banned) {
			t.Errorf("mode %s: wrong line %q shown", c.mode, c.banned)
		}
		if strings.Count(body, "Scores are") != 1 {
			t.Errorf("mode %s: score line said more than once", c.mode)
		}
	}
}

// TestHackEventPageChipsFollowStageOrder: "On this page" chips list only the
// sections that have content, in the stage's own order (spec "On this page").
func TestHackEventPageChipsFollowStageOrder(t *testing.T) {
	p := testHackEvent()
	p.Stage = "building"
	p.Gallery = []hackGalleryCard{{Title: "Alpha", URL: "https://alpha.example.com/"}}
	p.Tracks = []hackTrackView{{Name: "Education"}}
	p.Content.Schedule = []hackScheduleItem{
		{Title: "Opening", StartAt: "2027-03-12T10:00:00Z", EndAt: "2027-03-12T11:00:00Z"},
	}
	p.Content.Sponsors = []hackSponsor{{Name: "Acme"}}
	p.Announcements = []hackAnnouncementView{{Title: "Welcome", Body: "Hi", CreatedAt: time.Now()}}
	p.Content.FAQ = nil // leave FAQ empty: its chip must be left out entirely
	body := renderHackEvent(t, p).Body.String()
	chips := regexp.MustCompile(`<a class="chip" href="#(\w+)">([^<]*)</a>`).FindAllStringSubmatch(body, -1)
	var ids []string
	for _, m := range chips {
		ids = append(ids, m[1])
	}
	// building's order (hackStageSectionOrder): announcements, schedule,
	// sites, tracks, about, prizes, rules, faq, sponsors — minus faq.
	want := []string{"announcements", "schedule", "sites", "tracks", "about", "prizes", "rules", "sponsors"}
	if strings.Join(ids, ",") != strings.Join(want, ",") {
		t.Errorf("chip order %v, want %v", ids, want)
	}
	for _, id := range ids {
		if id == "faq" {
			t.Error("FAQ chip present with no FAQ content")
		}
	}
}

// TestHackEventPageEachStageRendersItsMainBlock walks every stage and checks
// the one thing that stage must show (spec "primary_action_by_stage").
func TestHackEventPageEachStageRendersItsMainBlock(t *testing.T) {
	for _, c := range []struct {
		stage, want string
	}{
		{"draft", "The organisers are still setting this up"},
		{"open", "Join with the link from the organisers"},
		{"building", "Sign-up is still open. Join with the link from the organisers."},
		{"closed", "Teams have handed in their work"},
		{"judging", "The judges are scoring every entry"},
		{"results", "Winners will be posted here"},
		{"archived", "Thanks to everyone who took part"},
	} {
		p := testHackEvent()
		p.Stage = c.stage
		body := renderHackEvent(t, p).Body.String()
		if !strings.Contains(body, c.want) {
			t.Errorf("stage %s: missing %q, body:\n%s", c.stage, c.want, body)
		}
	}

	// Results and ended, once published, replace the stage card.
	for _, stage := range []string{"results", "archived"} {
		p := testHackEvent()
		p.Stage = stage
		p.Results = &hackResultsView{Rows: []hackResultsRow{{Rank: 1, TeamName: "Alpha"}}}
		body := renderHackEvent(t, p).Body.String()
		if !strings.Contains(body, `id="results"`) {
			t.Errorf("stage %s: published results did not replace the stage card", stage)
		}
		if strings.Contains(body, "Winners will be posted here") || strings.Contains(body, "Thanks to everyone") {
			t.Errorf("stage %s: stage card still shown alongside published results", stage)
		}
	}
}

// TestHackEventPageCountdownStrictCSP: the one inline script (the countdown)
// carries the response nonce and the page has no inline event handlers,
// whether or not a deadline/countdown is actually on the page.
func TestHackEventPageCountdownStrictCSP(t *testing.T) {
	for _, stage := range []string{"open", "building", "judging", "results"} {
		p := testHackEvent()
		p.Stage = stage
		p.Deadline = time.Now().Add(49 * time.Hour)
		rec := renderHackEvent(t, p)
		assertStrictScriptCSP(t, "event page stage "+stage, rec)
	}
}

// TestHackEventPagePeoplesChoiceDowngradesStagePrimary: decision — "the stage
// card's gallery button becomes secondary so there is still one main action"
// once People's choice is open.
func TestHackEventPagePeoplesChoiceDowngradesStagePrimary(t *testing.T) {
	p := testHackEvent()
	p.Stage = "building"
	p.Gallery = []hackGalleryCard{{Title: "Alpha", URL: "https://alpha.example.com/"}}
	p.VoteStatus = "open"
	body := renderHackEvent(t, p).Body.String()
	if !strings.Contains(body, `<a class="btn btn-block primary" href="`+p.AppURL+`/e/`+p.Slug+`/vote">Vote for your favourite</a>`) {
		t.Error("people's choice vote button missing or not primary")
	}
	if !strings.Contains(body, `<a class="btn" href="#sites">See team sites (1)</a>`) {
		t.Error("stage card's gallery button did not downgrade to secondary styling")
	}
	if strings.Contains(body, `<a class="btn primary" href="#sites">`) {
		t.Error("stage card's gallery button is still primary alongside an open vote")
	}
}

// TestHackEventPageNoCountdownOutsideOpenAndBuilding (fixes list: the
// deadline countdown kept running after Building; review 2026-10-30 item 7,
// one of the listed gaps in per-stage coverage).
func TestHackEventPageNoCountdownOutsideOpenAndBuilding(t *testing.T) {
	for _, stage := range []string{"closed", "judging", "results", "archived"} {
		p := testHackEvent()
		p.Stage = stage
		p.Deadline = time.Now().Add(49 * time.Hour)
		body := renderHackEvent(t, p).Body.String()
		if strings.Contains(body, `id="countdown"`) {
			t.Errorf("stage %s: countdown rendered outside open/building", stage)
		}
	}
}

// TestHackEventPageScheduleAndRulesCollapseOnlyAfterResults: Schedule and
// Rules are a plain <section> everywhere except once results/archived has
// them collapsed into a <details> (review 2026-10-30 item 7).
func TestHackEventPageScheduleAndRulesCollapseOnlyAfterResults(t *testing.T) {
	for _, c := range []struct {
		stage     string
		collapsed bool
	}{
		{"draft", false}, {"open", false}, {"building", false},
		{"closed", false}, {"judging", false},
		{"results", true}, {"archived", true},
	} {
		p := testHackEvent()
		p.Stage = c.stage
		p.Content.Schedule = []hackScheduleItem{
			{Title: "Opening", StartAt: "2027-03-12T10:00:00Z", EndAt: "2027-03-12T11:00:00Z"},
		}
		body := renderHackEvent(t, p).Body.String()
		wantSchedule := `<section class="card" id="schedule">`
		wantRules := `<section class="card" id="rules">`
		if c.collapsed {
			wantSchedule = `<section class="card collapsed" id="schedule">`
			wantRules = `<section class="card collapsed" id="rules">`
		}
		if !strings.Contains(body, wantSchedule) {
			t.Errorf("stage %s: schedule collapsed=%v not found, body:\n%s", c.stage, c.collapsed, body)
		}
		if !strings.Contains(body, wantRules) {
			t.Errorf("stage %s: rules collapsed=%v not found, body:\n%s", c.stage, c.collapsed, body)
		}
	}
}

// TestHackEventPageOlderAnnouncementsFoldIntoDetails: more than 3
// announcements put the rest behind a <details> fold (review 2026-10-30
// item 7).
func TestHackEventPageOlderAnnouncementsFoldIntoDetails(t *testing.T) {
	p := testHackEvent()
	for i := 0; i < 5; i++ {
		p.Announcements = append(p.Announcements, hackAnnouncementView{
			Title: fmt.Sprintf("Update %d", i), Body: "Body", CreatedAt: time.Now(),
		})
	}
	body := renderHackEvent(t, p).Body.String()
	for i := 0; i < 3; i++ {
		if !strings.Contains(body, fmt.Sprintf("Update %d", i)) {
			t.Errorf("announcement %d should show without opening the fold", i)
		}
	}
	foldAt := strings.Index(body, "Show 2 older announcements")
	if foldAt < 0 {
		t.Fatalf("missing the fold summary, body:\n%s", body)
	}
	for i := 3; i < 5; i++ {
		at := strings.Index(body, fmt.Sprintf("Update %d", i))
		if at < 0 || at < foldAt {
			t.Errorf("announcement %d should be inside the fold, after its summary", i)
		}
	}
	if !strings.Contains(body, "<details>") {
		t.Error("older announcements are not behind a <details> element")
	}
}

// TestHackEventPageAnnouncementDatesInEventZone (fixes list: "Announcement
// dates were UTC days. They now use the event's time zone.").
func TestHackEventPageAnnouncementDatesInEventZone(t *testing.T) {
	p := testHackEvent()
	p.TimeZone = "Asia/Jakarta" // UTC+7
	// 23:30 UTC on 11 March is already 12 March in Jakarta.
	p.Announcements = []hackAnnouncementView{
		{Title: "Late one", Body: "Body", CreatedAt: time.Date(2027, 3, 11, 23, 30, 0, 0, time.UTC)},
	}
	body := renderHackEvent(t, p).Body.String()
	if !strings.Contains(body, "12 Mar 2027") {
		t.Errorf("announcement date was not shifted into the event's own zone, body:\n%s", body)
	}
	if strings.Contains(body, "11 Mar 2027") {
		t.Error("announcement date still shown in UTC")
	}
}

// TestHackEventPagePeoplesChoiceCopyByStatus (review 2026-10-30 item 7):
// upcoming and closed each have their own copy, and the card is absent
// entirely when voting is disabled.
func TestHackEventPagePeoplesChoiceCopyByStatus(t *testing.T) {
	p := testHackEvent()
	p.VoteStatus = "upcoming"
	body := renderHackEvent(t, p).Body.String()
	if !strings.Contains(body, "Voting opens soon.") {
		t.Error("upcoming: missing copy")
	}
	if strings.Contains(body, `href="`+p.AppURL+`/e/`+p.Slug+`/vote"`) {
		t.Error("upcoming: should have no vote link yet")
	}

	p.VoteStatus = "closed"
	body = renderHackEvent(t, p).Body.String()
	if !strings.Contains(body, "Voting has closed.") || !strings.Contains(body, "See the vote results") {
		t.Error("closed: missing copy or results link")
	}

	for _, status := range []string{"disabled", ""} {
		p.VoteStatus = status
		body = renderHackEvent(t, p).Body.String()
		if strings.Contains(body, "People&#39;s choice") || strings.Contains(body, "People's choice") {
			t.Errorf("status %q: People's choice card should not render", status)
		}
	}
}

// TestHackEventPageNoGalleryCTAWhenGalleryEmpty: the stage card's "See what
// teams built" / "See team sites" button only appears once the gallery has
// cards (review 2026-10-30 item 7).
func TestHackEventPageNoGalleryCTAWhenGalleryEmpty(t *testing.T) {
	for _, stage := range []string{"building", "closed", "judging", "archived"} {
		p := testHackEvent()
		p.Stage = stage
		p.Gallery = nil
		body := renderHackEvent(t, p).Body.String()
		if strings.Contains(body, `href="#sites"`) {
			t.Errorf("stage %s: gallery CTA shown with an empty gallery", stage)
		}
	}
}

// TestHackEventPageTakenDownNoColourOrIcon: the taken-down page drops the
// event colour and the event icon entirely, not just the OG/meta tags
// (review 2026-10-30 item 7).
func TestHackEventPageTakenDownNoColourOrIcon(t *testing.T) {
	p := testHackEvent()
	p.TakenDown = true
	p.Color = "#267b79"
	p.IconURL = p.AppURL + "/v1/hack/events/" + p.Slug + "/icon"
	body := renderHackEvent(t, p).Body.String()
	for _, banned := range []string{"--ev:", "--ev-ink:", `class="ev-icon"`, `class="ev-header"`, p.Color} {
		if strings.Contains(body, banned) {
			t.Errorf("taken-down page still has %q", banned)
		}
	}
}

// TestHackEventPageSectionOrderClosedStage: the chip order follows
// hackStageSectionOrder for a stage other than building too (review
// 2026-10-30 item 7 — only building had this coverage).
func TestHackEventPageSectionOrderClosedStage(t *testing.T) {
	p := testHackEvent()
	p.Stage = "closed"
	p.Gallery = []hackGalleryCard{{Title: "Alpha", URL: "https://alpha.example.com/"}}
	p.Tracks = []hackTrackView{{Name: "Education"}}
	p.Content.Sponsors = []hackSponsor{{Name: "Acme"}}
	body := renderHackEvent(t, p).Body.String()
	chips := regexp.MustCompile(`<a class="chip" href="#(\w+)">`).FindAllStringSubmatch(body, -1)
	var ids []string
	for _, m := range chips {
		ids = append(ids, m[1])
	}
	// closed's order (hackStageSectionOrder): sites, announcements,
	// schedule, tracks, about, prizes, rules, faq, sponsors — minus the
	// sections this event has no content for.
	want := []string{"sites", "tracks", "about", "prizes", "rules", "sponsors"}
	if strings.Join(ids, ",") != strings.Join(want, ",") {
		t.Errorf("chip order %v, want %v", ids, want)
	}
}

// TestHackEventPageDeadlinePassedShowsEntriesClosed (review 2026-10-30 item
// 9): once the deadline has passed, Open and Building show "Entries are
// closed." instead of a countdown or an "Entries close <date>" line, even
// though the stage itself has not moved on yet.
func TestHackEventPageDeadlinePassedShowsEntriesClosed(t *testing.T) {
	for _, stage := range []string{"open", "building"} {
		p := testHackEvent()
		p.Stage = stage
		p.Deadline = time.Now().Add(-time.Hour)
		body := renderHackEvent(t, p).Body.String()
		if strings.Contains(body, `id="countdown"`) {
			t.Errorf("stage %s: countdown still rendered past the deadline", stage)
		}
		if !strings.Contains(body, "Entries are closed") {
			t.Errorf("stage %s: missing the static closed line, body:\n%s", stage, body)
		}
		if strings.Contains(body, "Entries close ") {
			t.Errorf("stage %s: still shows the future-tense deadline text", stage)
		}
	}
}

// TestHackEventPageResultsPublishedBeforeResultsStage (review 2026-10-30
// item 8): publishResults does not check the stage, so results can land
// while the event is still closed or judging. The pill and section order
// must follow the results layout, not the stale stage.
func TestHackEventPageResultsPublishedBeforeResultsStage(t *testing.T) {
	for _, stage := range []string{"closed", "judging"} {
		p := testHackEvent()
		p.Stage = stage
		p.Gallery = []hackGalleryCard{{Title: "Alpha", URL: "https://alpha.example.com/"}}
		p.Results = &hackResultsView{Rows: []hackResultsRow{{Rank: 1, Overall: true, TeamName: "Alpha"}}}
		body := renderHackEvent(t, p).Body.String()
		if !strings.Contains(body, "Results are out") {
			t.Errorf("stage %s: pill did not follow the published results", stage)
		}
		if strings.Contains(body, "Judging is under way") || strings.Contains(body, "Teams have handed in their work") {
			t.Errorf("stage %s: pill/stage-card copy still shows the stale stage", stage)
		}
		sitesIdx, resultsIdx := strings.Index(body, `id="sites"`), strings.Index(body, `id="results"`)
		if sitesIdx < 0 || resultsIdx < 0 || resultsIdx > sitesIdx {
			t.Errorf("stage %s: results-stage section order not used (results then sites)", stage)
		}
	}
}

// TestHackEventPageZoneNoteFallsBackToEndsAt (review 2026-10-30 item 10):
// the zone note uses the end date, not today's date, when only EndsAt is
// set — otherwise a London event viewed outside BST would wrongly say GMT.
func TestHackEventPageZoneNoteFallsBackToEndsAt(t *testing.T) {
	p := testHackEvent()
	p.StartsAt = time.Time{}
	p.EndsAt = time.Date(2027, 7, 14, 18, 0, 0, 0, time.UTC) // July: BST, not GMT
	body := renderHackEvent(t, p).Body.String()
	if !strings.Contains(body, `<span class="f">times in BST</span>`) {
		t.Errorf("zone note did not use the end date's own offset, body:\n%s", body)
	}
}

// TestHackEventPageFAQAndAnnouncementsKeepLineBreaks (review 2026-10-30 item
// 2): FAQ answers and announcement bodies are multi-line free text too, not
// just About/Rules/Prizes, and lost their line breaks.
func TestHackEventPageFAQAndAnnouncementsKeepLineBreaks(t *testing.T) {
	p := testHackEvent()
	p.Content.FAQ = []hackFAQ{{Question: "Can I join?", Answer: "Yes.\nJust sign up."}}
	p.Announcements = []hackAnnouncementView{{Title: "Update", Body: "Line one.\nLine two.", CreatedAt: time.Now()}}
	body := renderHackEvent(t, p).Body.String()
	// The newline is in the HTML either way; what keeps it visible is the
	// pre-wrap rule for these two blocks.
	if !regexp.MustCompile(`\.announcement-body,#faq details p\{white-space:pre-wrap\}`).MatchString(body) {
		t.Error("FAQ answers and announcement bodies lost their pre-wrap rule")
	}
	if !strings.Contains(body, "Yes.\nJust sign up.") {
		t.Error("FAQ answer lost its line break")
	}
	if !strings.Contains(body, "Line one.\nLine two.") {
		t.Error("announcement body lost its line break")
	}
}

// TestHackEventPageEventInkOnBackground (review 2026-10-30 item 4): the ink
// colour matches the icon endpoint's own rule — white only for teal and
// indigo, dark ink for every other crayon.
func TestHackEventPageEventInkOnBackground(t *testing.T) {
	for _, c := range []struct{ color, ink string }{
		{"#f2c94c", "#141413"}, // yellow: dark ink
		{"#267b79", "#fff"},    // teal: white ink
		{"#4a49a8", "#fff"},    // indigo: white ink
	} {
		p := testHackEvent()
		p.Color = c.color
		body := renderHackEvent(t, p).Body.String()
		if !strings.Contains(body, "--ev-ink:"+c.ink) {
			t.Errorf("colour %s: want ink %s, body:\n%s", c.color, c.ink, body)
		}
	}
}

// TestHackEventPageResultRowNoAriaLabel (review 2026-10-30 item 6): the
// result row's link names itself from its visible content (rank/avatar,
// team name, pills, score), not an aria-label that would hide them.
func TestHackEventPageResultRowNoAriaLabel(t *testing.T) {
	p := testHackEvent()
	p.Results = &hackResultsView{
		FullRanking: true,
		Rows:        []hackResultsRow{{Rank: 1, TeamName: "Alpha", URL: "https://alpha.example.com/"}},
	}
	body := renderHackEvent(t, p).Body.String()
	resultsAt := strings.Index(body, `id="results"`)
	sectionsAt := strings.Index(body[resultsAt:], "</section>")
	resultsSection := body[resultsAt : resultsAt+sectionsAt]
	if strings.Contains(resultsSection, "aria-label") {
		t.Errorf("result row still has an aria-label: %s", resultsSection)
	}
	if !strings.Contains(resultsSection, "<b>Alpha</b>") {
		t.Error("result row's visible team name missing")
	}
}

// TestHackEventPageSitesCountGrammar (review 2026-10-30 item 11): "1 team
// has", not "1 team have".
func TestHackEventPageSitesCountGrammar(t *testing.T) {
	p := testHackEvent()
	p.Stage = "building"
	p.Gallery = []hackGalleryCard{{Title: "Alpha", URL: "https://alpha.example.com/"}}
	body := renderHackEvent(t, p).Body.String()
	if !strings.Contains(body, "1 team has a live site") {
		t.Errorf("singular grammar wrong, body:\n%s", body)
	}
	if strings.Contains(body, "1 team have") {
		t.Error("still says '1 team have'")
	}

	p.Gallery = append(p.Gallery, hackGalleryCard{Title: "Beta", URL: "https://beta.example.com/"})
	body = renderHackEvent(t, p).Body.String()
	if !strings.Contains(body, "2 teams have a live site") {
		t.Errorf("plural grammar wrong, body:\n%s", body)
	}
}

// TestHackEventPageHeaderFooterLandmarksOutsideMain (review 2026-10-30 item
// 14): the top bar and footer are their own banner/contentinfo landmarks,
// not swallowed into <main>.
func TestHackEventPageHeaderFooterLandmarksOutsideMain(t *testing.T) {
	for _, takenDown := range []bool{false, true} {
		p := testHackEvent()
		p.TakenDown = takenDown
		body := renderHackEvent(t, p).Body.String()
		mainAt := strings.Index(body, "<main")
		mainEnd := strings.Index(body, "</main>")
		topAt := strings.Index(body, `<header class="pub-top">`)
		footAt := strings.Index(body, `<footer class="pub-foot">`)
		if mainAt < 0 || mainEnd < 0 || topAt < 0 || footAt < 0 {
			t.Fatalf("takenDown=%v: missing a landmark, body:\n%s", takenDown, body)
		}
		if topAt > mainAt {
			t.Errorf("takenDown=%v: top bar is inside <main>", takenDown)
		}
		if footAt < mainEnd {
			t.Errorf("takenDown=%v: footer is inside <main>", takenDown)
		}
	}
}

// TestHackEventPageCollapsedHeadingOutsideSummary (review 2026-10-30 item
// 14): Schedule/Rules once collapsed still expose an <h2> for heading
// navigation, and it is not nested inside <summary> (which many screen
// readers flatten to a plain button).
func TestHackEventPageCollapsedHeadingOutsideSummary(t *testing.T) {
	p := testHackEvent()
	p.Stage = "archived"
	p.Content.Schedule = []hackScheduleItem{
		{Title: "Opening", StartAt: "2027-03-12T10:00:00Z", EndAt: "2027-03-12T11:00:00Z"},
	}
	body := renderHackEvent(t, p).Body.String()
	for _, id := range []string{"rules", "schedule"} {
		open := `<section class="card collapsed" id="` + id + `">`
		at := strings.Index(body, open)
		if at < 0 {
			t.Fatalf("missing collapsed %s, body:\n%s", id, body)
		}
		section := body[at : at+strings.Index(body[at:], "</section>")]
		// The heading must sit before <details>: a closed <details> hides all
		// but its summary from assistive tech.
		h, d := strings.Index(section, "<h2"), strings.Index(section, "<details>")
		if h < 0 || d < 0 || h > d {
			t.Errorf("%s: heading must come before <details>: %s", id, section)
		}
	}
}

// TestHackResultsFromSnapshotOverallWithRanksHidden (review 2026-10-30 N5):
// the overall winner is marked from the real snapshot even when ranks are
// hidden and it is also its track's winner.
func TestHackResultsFromSnapshotOverallWithRanksHidden(t *testing.T) {
	snap := []byte(`[
		{"rank":1,"team_id":"a","team_name":"Alpha","track_winner":true,"track":{"name":"Health"},"total":9},
		{"rank":2,"team_id":"b","team_name":"Beta","track_winner":true,"track":{"name":"Education"},"total":8},
		{"rank":3,"team_id":"c","team_name":"Gamma","total":7}
	]`)
	v := hackResultsFromSnapshot(db.EventResultsRow{Published: true, Snapshot: snap}, false, false)
	if v == nil || len(v.Rows) != 2 {
		t.Fatalf("winners-only rows: %+v", v)
	}
	if !v.Rows[0].Overall || v.Rows[0].Rank != 0 {
		t.Errorf("Alpha must be the overall winner with its rank hidden: %+v", v.Rows[0])
	}
	if v.Rows[1].Overall {
		t.Errorf("Beta is a track winner only: %+v", v.Rows[1])
	}
}

// TestHackEventPageDraftKeepsDraftLayoutWithResults (review 2026-10-30 N4):
// published results only switch building, closed and judging to the results
// layout; a draft keeps its own pill.
func TestHackEventPageDraftKeepsDraftLayoutWithResults(t *testing.T) {
	p := testHackEvent()
	p.Stage = "draft"
	p.Results = &hackResultsView{Rows: []hackResultsRow{{TeamName: "Alpha", Overall: true}}}
	body := renderHackEvent(t, p).Body.String()
	if strings.Contains(body, "Results are out") {
		t.Error("a draft event must not be labelled Results are out")
	}
}

// TestHackScoreTextDropsTrailingZero (Grok SIM-346 Part B hack-event.html
// item 4): "100.00" should read "100", "60" or "42.5" — at most one
// decimal, no trailing ".0".
func TestHackScoreTextDropsTrailingZero(t *testing.T) {
	for _, c := range []struct {
		score float64
		want  string
	}{
		{100, "100"}, {60, "60"}, {42.5, "42.5"}, {42.56, "42.6"}, {0, "0"},
	} {
		if got := hackScoreText(c.score); got != c.want {
			t.Errorf("hackScoreText(%v) = %q, want %q", c.score, got, c.want)
		}
	}
}

// TestHackResultsFromSnapshotScoreFormatting: the public page's own score
// text (via hackResultsFromSnapshot, publicScores=true) follows the same
// rule as hackScoreText, not the raw "%.2f" that produced "100.00".
func TestHackResultsFromSnapshotScoreFormatting(t *testing.T) {
	snap := []byte(`[{"rank":1,"team_id":"a","team_name":"Alpha","total":100},{"rank":2,"team_id":"b","team_name":"Beta","total":42.5}]`)
	v := hackResultsFromSnapshot(db.EventResultsRow{Published: true, FullRanking: true, Snapshot: snap}, true, true)
	if v == nil || len(v.Rows) != 2 {
		t.Fatalf("rows: %+v", v)
	}
	if v.Rows[0].Score != "100" {
		t.Errorf("Alpha score = %q, want %q", v.Rows[0].Score, "100")
	}
	if v.Rows[1].Score != "42.5" {
		t.Errorf("Beta score = %q, want %q", v.Rows[1].Score, "42.5")
	}
}

// TestHackEventPageFooterSaysMadeWith (audit Part B hack-event.html item
// 10): "Run on Simple Hack" reads oddly for a page the organiser built
// themselves, so the footer says "Made with Simple Hack" instead — on the
// live page and on the taken-down and not-found states, which share the
// same footer markup.
func TestHackEventPageFooterSaysMadeWith(t *testing.T) {
	body := renderHackEvent(t, testHackEvent()).Body.String()
	if !strings.Contains(body, "<span>Made with Simple Hack</span>") {
		t.Error("live page footer missing 'Made with Simple Hack'")
	}
	if strings.Contains(body, "Run on Simple Hack") {
		t.Error("live page footer still says 'Run on Simple Hack'")
	}
	p := testHackEvent()
	p.TakenDown = true
	down := renderHackEvent(t, p).Body.String()
	if !strings.Contains(down, "<span>Made with Simple Hack</span>") {
		t.Error("taken-down page footer missing 'Made with Simple Hack'")
	}
}

// TestHackEventPageNotFoundIsFriendly (Grok SIM-346 S15): a slug that no
// longer resolves to an event gets the same top bar and footer as a live
// event page, not a bare message or the generic site chrome.
func TestHackEventPageNotFoundIsFriendly(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/e/missing", nil)
	writeHackEventNotFound(rec, req, "https://simple-hack.app")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status %d", rec.Code)
	}
	if rec.Header().Get("X-Robots-Tag") != "noindex" {
		t.Error("not-found page should be noindex")
	}
	body := rec.Body.String()
	for _, want := range []string{
		`<title>Event not found · Simple Hack</title>`,
		`<a class="wordmark" href="https://simple-hack.app/">simple<b>·</b>hack</a>`,
		`<a href="https://simple-hack.app/directory">Find a hackathon</a>`,
		"<h1>Event not found</h1>",
		`<a class="btn quiet" href="https://simple-hack.app/directory">Find another hackathon</a>`,
		`<footer class="pub-foot">`,
		"<span>Made with Simple Hack</span>",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q, body:\n%s", want, body)
		}
	}
	assertStrictScriptCSP(t, "event not found", rec)
}
