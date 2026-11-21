package hackhttp

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/vsriram/simple-host/products/hack/app/events"

	"github.com/vsriram/simple-host/products/hack/app/entries"
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
	renderHackEventPage(rec, req, p, testHackPages)
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

// TestNonHackPartialsMatchBaseline renders header, footer and head with Hack
// false and compares them to the same partials at ea2c6ac. simple-host.app must
// not pick up the hack chrome.
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
	if !strings.Contains(body, `<span class="rank">#1</span>`) || !strings.Contains(body, `<span class="pill accent">Winner</span>`) {
		t.Error("winner rank/pill missing")
	}
	if strings.Contains(body, `<h2 class="section-title">Results</h2>`) {
		t.Error("full-ranking heading shown for winners-only")
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

// Apply to a hackathon without a join link (build-plan.md §4,
// buildHackStageCard): the public event page's stage card switches to the
// Apply CTA once ApplyURL is set, and is byte-for-byte the old card when it
// is empty (applications off keeps exactly today's card).

// TestHackResultsFromSnapshotOverallWithRanksHidden (review 2026-10-30 N5):
// the overall winner is marked from the real snapshot even when ranks are
// hidden and it is also its track's winner.
func TestHackResultsFromSnapshotOverallWithRanksHidden(t *testing.T) {
	snap := []byte(`[
		{"rank":1,"team_id":"a","team_name":"Alpha","track_winner":true,"track":{"name":"Health"},"total":9},
		{"rank":2,"team_id":"b","team_name":"Beta","track_winner":true,"track":{"name":"Education"},"total":8},
		{"rank":3,"team_id":"c","team_name":"Gamma","total":7}
	]`)
	v := hackResultsFromSnapshot(entries.EventResultsRow{Published: true, Snapshot: snap}, false, false, nil)
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

func TestOverallPrizesValidation(t *testing.T) {
	for _, tc := range []struct {
		in, want []string
		invalid  bool
	}{
		{nil, []string{}, false}, {[]string{}, []string{}, false},
		{[]string{" $300 ", "", " "}, []string{"$300"}, false},
		{[]string{"", "$200"}, []string{"", "$200"}, false},
		{[]string{strings.Repeat("賞", 60)}, []string{strings.Repeat("賞", 60)}, false},
		{[]string{"a", "b", "c", ""}, nil, true},
		{[]string{"a\nb"}, nil, true}, {[]string{"a\rb"}, nil, true},
		{[]string{strings.Repeat("賞", 61)}, nil, true},
	} {
		got, err := events.EventOverallPrizes(tc.in)
		if tc.invalid {
			var coded *events.Error
			if !errors.As(err, &coded) || coded.Code != "invalid_overall_prizes" {
				t.Errorf("%q: error = %v", tc.in, err)
			}
		} else if err != nil || !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%q: got %q, %v; want %q", tc.in, got, err, tc.want)
		}
	}
}

func TestOverallPrizesWinners(t *testing.T) {
	prizes := []string{"", "$200", "$100"}
	snap := []byte(`[{"rank":1,"team_id":"a","team_name":"Alpha","entry":{}},{"rank":2,"team_id":"b","team_name":"Beta","tied":true,"entry":{"title":"Beta entry"}},{"rank":2,"team_id":"c","team_name":"Gamma","tied":true,"entry":{}},{"rank":4,"team_name":"Delta","entry":{}},{"team_name":"Unranked","track_winner":true}]`)
	for _, full := range []bool{false, true} {
		for _, ranks := range []bool{false, true} {
			v := hackResultsFromSnapshot(entries.EventResultsRow{Published: true, FullRanking: full, Snapshot: snap}, false, ranks, prizes)
			want := 3
			if full {
				want = 4
			}
			if len(v.Rows) != want {
				t.Fatalf("rows: %+v", v.Rows)
			}
			count := 0
			for _, row := range v.Rows {
				if row.TeamID == "b" || row.TeamID == "c" {
					count++
					if row.OverallPrize != "2nd · $200" {
						t.Errorf("tied prize row: %+v", row)
					}
					if !ranks && row.Rank != 0 {
						t.Errorf("rank leaked: %+v", row)
					}
				}
			}
			if count != 2 {
				t.Errorf("tie rows = %d", count)
			}
		}
	}
}

func TestAnnouncementPublicPageSafeCards(t *testing.T) {
	p := testHackEvent()
	p.Announcements = []hackAnnouncementView{{Title: "Kickoff", Body: "<script>alert(1)</script>\nhttps://zoom.us/j/123", Pinned: true}}
	r := renderHackEvent(t, p)
	body := r.Body.String()
	if strings.Contains(body, "<script>alert(1)</script>") || !strings.Contains(body, "&lt;script&gt;") || !strings.Contains(body, "ann-card ann-call") || !strings.Contains(body, ">Pinned</span>") {
		t.Fatal(body)
	}
}
