package handler

import (
	"net/http"
	"net/http/httptest"
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
	page := []byte("<!--sh:header-->")

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
	if strings.Contains(body, `simple<b>·</b>host`) {
		t.Error("hack chrome: host logo still present")
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
		"<h2>Projects</h2>",
		"Projects appear here once teams publish.",
		"4 teams, 12 participants",
		`class="sh-header"`,
		`class="sh-footer"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}
}
