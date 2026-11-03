package hackhttp

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	db "github.com/vsriram/simple-host/internal/db"
	"github.com/vsriram/simple-host/internal/httpx"
)

// hackEventPage is the public event page at <event>.simple-hack.app/. The
// integrator fills it from the database.
//
// Redesigned per docs/ux-atlas/spec.json public_pages "public-event-page"
// (INTENT 2026-10-30: the atlas-driven rebuild). The page is now built on the
// app kit (static/hack/kit.css) instead of the site chrome and hack-ink.css —
// see assembleHackEventPage and static/hack-event.html.
type hackEventPage struct {
	Slug, Title, Tagline, About, Rules, Prizes string
	OrganiserName, Organisation                string
	Stage                                      string    // draft|open|building|closed|judging|results|archived
	TimeZone                                   string    // IANA name
	StartsAt, EndsAt                           time.Time // zero = not set
	Participants, Teams                        int
	AppURL                                     string // "https://simple-hack.app"
	// ApplyURL is the public apply page's URL, built from AppURL; empty
	// when applications are off (zp-hack-applications.sql, hackApplicationsOpenNow).
	ApplyURL        string
	IconURL         string
	Color           string
	TakenDown       bool
	TakenDownReason string
	Gallery         []hackGalleryCard
	Tracks          []hackTrackView
	Results         *hackResultsView
	Content         hackContent
	Announcements   []hackAnnouncementView
	Deadline        time.Time
	VoteStatus      string
	// Fixed is the event's real times as schedule rows (hackFixedSchedule,
	// public view: no prompts, no team rows).
	Fixed []hackFixedRow
}

// hackAnnouncementView carries the raw time so the public page can format it
// in the event's own time zone (tech_notes: "Announcement dates ... use the
// event's time zone, like every other time on the page").
type hackAnnouncementView struct {
	Title, Body string
	CreatedAt   time.Time
}
type hackSponsorView struct {
	Name, Tier, URL string
	Logo            template.URL
	Avatar          string // letters shown when there is no logo
}
type hackScheduleView struct {
	Title, Description, When, Status string
	Fixed                            bool // from the event's settings, not typed into Schedule
}
type hackTrackView struct{ Name, Challenge, Prize string }

// hackResultsRow is one row the public page shows: winners-only has just the
// rank-1 team(s), a full ranking has every ranked team.
type hackResultsRow struct {
	TeamID      string
	URL         string
	Rank        int
	TeamName    string
	Tied        bool
	TrackName   string
	TrackPrize  string
	TrackWinner bool
	// Overall is true for the rank-1 team(s), independent of whether ranks
	// are shown publicly (publicRanks zeroes Rank, not this). A row can be
	// both Overall and a TrackWinner: markTrackWinners makes the top-of-the-
	// board team the winner of its own track too.
	Overall   bool
	Score     string
	ScoreMode string
	// Card is this team's public summary card (build-plan §4.6), nil for a
	// row from a snapshot published before slice 3, or for a team whose
	// snapshot entry was somehow never written. hackResultsFromSnapshot
	// fills it in only for a row it already decided to emit, so a winners-
	// only view never carries a card for a team it does not otherwise show.
	Card *hackResultCard
	// Shot is the template-ready absolute screenshot URL (hack_wire.go's
	// HackEventPage), set only when the gallery is public and this team's
	// site is live right now -- the same freshness gate the Team sites
	// section already applies, reused here instead of trusting the
	// publish-time has_screenshot forever.
	Shot string
}

// hackResultCard is one ranked team's public entry summary (build-plan
// §4.6), round-tripped through JSON from the snapshot's per-row "entry"
// field (see resultsSnapshotJSON/resultsEntryCards in hack_judging.go) so
// its field tags double as the wire shape for GET .../results, GET
// .../public and the MCP reads that proxy them. SiteURL is re-derived at
// render time on the HTML page (HackEventPage, hack_wire.go) from the same
// live gallery-cards query the Team sites section already runs, so a site
// taken down after publish drops its link without a republish; the public
// JSON routes do the same through liveResultSites (hack_judging.go).
type hackResultCard struct {
	Title         string         `json:"title"`
	Tagline       string         `json:"tagline"`
	Problem       string         `json:"problem"`
	Audience      string         `json:"audience"`
	Tools         []string       `json:"tools"`
	Maturity      string         `json:"maturity"`
	Track         *db.EventTrack `json:"track"`
	VideoURL      string         `json:"video_url"`
	VideoHost     string         `json:"video_host"`
	CodeURL       string         `json:"code_url"`
	CodeHost      string         `json:"code_host"`
	SiteURL       string         `json:"site_url"`
	HasScreenshot bool           `json:"has_screenshot"`
}

// hackMaturityLabel is the plain word for what works today, kept in step by
// hand with ui.js's MATURITY table (build-plan §6 point "Maturity wording
// must match what slice 1 uses elsewhere") since this is a server-rendered
// page with no script of its own to share that table with.
func hackMaturityLabel(m string) string {
	switch m {
	case "working":
		return "Demo-ready"
	case "partial":
		return "Partly working"
	case "concept":
		return "Concept"
	default:
		return ""
	}
}

// hackResultsView is nil when nothing is published yet.
type hackResultsView struct {
	FullRanking bool
	Rows        []hackResultsRow
}

// hackGalleryCard is one project on the public event page. Team is set only
// when it differs from Title. Shot and URL are built from a validated slug.
// Tools and ResultAnchor are set only once results are published (build-plan
// §4.6: "hackGalleryCard gains Tools []string and ResultAnchor ... for teams
// shown in the results").
type hackGalleryCard struct {
	Title, Tagline, Team string
	Shot, URL            string
	Initial              string // the title's first letter, for a card with no screenshot
	Tools                []string
	ResultAnchor         string // "#result-<team id>" when this team has a visible result card, else ""
	// TeamID is never rendered by the template; HackEventPage (hack_wire.go)
	// uses it to match a gallery card to its results row when filling in
	// ResultAnchor.
	TeamID string
}

// HackEventPages owns the parsed hack-event.html template that the public
// event page, HackEventPage's SiteHandler hook and their shared not-found
// response all render from. Build one with NewHackEventPages and pass it to
// HackEventPage and RegisterHackPublic; it parses lazily, once, on first
// use (its template method), same as the package-level sync.Once this
// replaces (cleanup 3.2 slice 4: hackhttp may not import handler, so this
// is an explicit value built from the caller's fs.FS instead of a global
// embeddedStaticFS set through SetEmbeddedStatic — same narrow-value
// pattern as SetHackEventPage/SetHackScreenshot, now a constructor
// argument instead of a setter). main.go builds one from
// handler.EmbeddedStatic(); test fixtures (hack_test.go, hack_m2_test.go,
// main_test.go) build one the same way, from their test-only handler
// import.
type HackEventPages struct {
	fsys fs.FS
	once sync.Once
	tmpl *template.Template
	err  error
}

// NewHackEventPages builds a template cache for hack-event.html, read from
// fsys (ui.go's staticFiles in internal/handler, reached through assets.go's
// exported EmbeddedStatic()) on first use.
func NewHackEventPages(fsys fs.FS) *HackEventPages {
	return &HackEventPages{fsys: fsys}
}

// template parses hack-event.html once, lazily, from p.fsys. Lazy rather
// than parsing eagerly in NewHackEventPages only because the original
// (package-level sync.Once) was lazy too; behaviour at the first real call
// is identical either way.
func (p *HackEventPages) template() (*template.Template, error) {
	p.once.Do(func() {
		p.tmpl, p.err = template.New("hack-event.html").ParseFS(p.fsys, "static/hack-event.html")
	})
	return p.tmpl, p.err
}

// hackCTA is one button or text link: Strong renders it as the kit's
// "primary" button; otherwise it is a plain or quiet action.
type hackCTA struct {
	Label, URL string
	Strong     bool
}

// hackChip is one "on this page" jump link.
type hackChip struct{ ID, Label string }

// hackStageCardView is the one stage-specific card under the header
// (spec: "primary_action_by_stage"). Nil when the Results block replaces it.
type hackStageCardView struct {
	Kicker, Heading, Body, Note string
	Primary, Secondary          *hackCTA
	// Extra is a third button, shown between Primary and Secondary: today
	// only Building + a live gallery + applications open, where "See team
	// sites" already holds Primary and "Already joined? Sign in" holds
	// Secondary, so Apply gets this third slot instead of displacing either.
	Extra *hackCTA
	// The live countdown (an inline <span id="countdown">, enhanced by the
	// one nonce script) sits in Note for Open and in Body for Building —
	// never both. hackEventView.CountdownText/Deadline carry the text.
	CountdownInNote, CountdownInBody bool
}

// hackPeoplesChoiceView is the voting card, shown whenever VoteStatus is
// upcoming, open or closed (never for "disabled").
type hackPeoplesChoiceView struct {
	Body string
	CTA  *hackCTA
}

// hackResultsBlockView is the template-ready form of hackResultsView: avatars,
// pills and the one score-method line are all decided here, in Go, not in the
// template.
type hackResultsBlockView struct {
	Heading         string // "Winners" or "Results"
	ScoreNote       string // one plain-English line, or "" when scores are private
	AnyRanked       bool
	ShowScoreHeader bool
	Rows            []hackResultsRowView
	// HasCards is true once any row carries a public summary card
	// (build-plan §4.6). A snapshot is built in one pass, so in practice
	// this is all-or-nothing across the rows: every ranked team gets a card
	// together (a publish after this slice shipped), or none do (a
	// snapshot published before it, never republished). The template
	// switches its whole rendering on this one flag rather than per row, so
	// a pre-slice-3 snapshot keeps the exact markup it always had.
	HasCards bool
}

type hackResultsRowView struct {
	hackResultsRow
	Avatar     string // winners-only: "1" for the overall winner, else initials
	WinnerPill bool
	TrackPill  string
	// Score shadows hackResultsRow.Score (same name, outer field wins over
	// the embedded one): the template only ever sees this trimmed-for-
	// display form, set by buildHackResultsBlock. #8: the embedded one is
	// also the public JSON API's score field (hack_public_json.go) and must
	// keep its full, untrimmed precision -- the two can't be the same
	// string any more now that display trims and JSON doesn't.
	Score string
	// ShotLetter and MaturityLabel are template-only strings the Go
	// template itself cannot compute (hackEventTmpl registers no custom
	// funcs, so every bit of formatting happens here first, same as Score
	// above). Set only for a row with a Card. ShotLetter is the fallback
	// initials shown in the shot box when there is no Shot image -- Avatar
	// already carries this for a winners-only row, but a full-ranking row's
	// Avatar is only ever set for a track winner (buildHackResultsBlock), so
	// a plain ranked row with a card still needs its own fallback letter.
	ShotLetter    string
	MaturityLabel string
}

type hackEventView struct {
	Title, Tagline, OrganisedBy string
	DateRange, ZoneNote         string
	StagePill                   string
	IconURL, Initial, Color     string
	Ink                         string // text colour that reads on --ev (hackEventInk)
	// Slug (Grok retest, Cosmetic #3): the stage card's own script reads it
	// to ask the app's API, cross-origin, whether the signed-in visitor
	// already belongs to this event, and swap out "Join with the link from
	// the organisers... Already joined? Sign in" for a signed-in member.
	Slug            string
	AppURL          string
	TakenDown       bool
	TakenDownReason string
	// NotFound renders the same .pub shell's own "not found" state, for a
	// slug that no longer resolves to an event (deleted, or never existed):
	// the same top bar and footer as a live event page, instead of generic
	// site chrome (Grok SIM-346 S15: "a deleted event shows a participant
	// only a bare 'event not found'"). Set only by writeHackEventNotFound,
	// never alongside TakenDown.
	NotFound            bool
	Teams, Participants int
	CountLine           string
	MetaDescription     string
	OGImage             string

	Chips         []hackChip
	StageCard     *hackStageCardView
	PeoplesChoice *hackPeoplesChoiceView
	Results       *hackResultsBlockView
	Sections      []template.HTML

	Deadline      string // RFC3339, for the countdown script's data attribute
	CountdownText string // server-rendered initial value; see hackCountdownText

	// Fields below are internal build state: each holds one section's data
	// long enough for its "sec-*" partial to render, then that rendering is
	// appended to Sections in the stage's order. The template never ranges
	// over these directly except from inside the matching partial.
	About, Rules, Prizes  [][]string
	Tracks                []hackTrackView
	FAQ                   []hackFAQ
	Schedule              []hackScheduleView
	ScheduleCollapsed     bool
	RulesCollapsed        bool
	Sponsors              []hackSponsorView
	AnnouncementLines     []hackAnnouncementLine
	AnnouncementLinesMore []hackAnnouncementLine
	Gallery               []hackGalleryCard
	VoteURL               string
}

// hackAnnouncementLine is one announcement as the page shows it: the date
// already formatted in the event's own time zone.
type hackAnnouncementLine struct{ Title, Body, When string }

// renderHackEventPage writes the whole response (status 200, or 410 when TakenDown).
func renderHackEventPage(w http.ResponseWriter, r *http.Request, p hackEventPage, pages *HackEventPages) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	nonce := base64.StdEncoding.EncodeToString(b[:])
	r = r.WithContext(context.WithValue(r.Context(), httpx.NonceKey{}, nonce))

	page, err := assembleHackEventPage(p, pages)
	if err != nil {
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	app := strings.TrimRight(p.AppURL, "/")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", hackEventPageCSP(app, nonce))
	if p.TakenDown || p.Stage == "draft" {
		w.Header().Set("X-Robots-Tag", "noindex")
	}
	status := http.StatusOK
	if p.TakenDown {
		status = http.StatusGone
	}
	w.WriteHeader(status)
	_, _ = w.Write(httpx.StampNonce(r, page))
}

// hackEventPageCSP is the public event page's Content-Security-Policy,
// shared by renderHackEventPage and writeHackEventNotFound so the two
// responses look like the same product to a browser's network panel.
func hackEventPageCSP(app, nonce string) string {
	return fmt.Sprintf(
		"default-src 'none'; script-src 'nonce-%s'; style-src %s 'unsafe-inline'; img-src 'self' %s data:; font-src %s data:; connect-src %s; base-uri 'none'; form-action 'none'; frame-ancestors 'none'",
		nonce, app, app, app, app)
}

// writeHackEventNotFound renders the public event page's own "not found"
// state at /e/{slug} when the slug no longer resolves to an event (deleted,
// or never existed): the same top bar and footer as a live event page,
// instead of generic site chrome (Grok SIM-346 S15: "a deleted event shows
// a participant only a bare 'event not found'"). hack_public.go's load()
// calls this instead of writeMessagePage for that one case.
func writeHackEventNotFound(w http.ResponseWriter, r *http.Request, appURL string, pages *HackEventPages) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	nonce := base64.StdEncoding.EncodeToString(b[:])
	r = r.WithContext(context.WithValue(r.Context(), httpx.NonceKey{}, nonce))

	app := strings.TrimRight(appURL, "/")
	v := hackEventView{NotFound: true, Title: "Event not found", AppURL: app}
	tmpl, err := pages.template()
	if err != nil {
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "hack-event.html", v); err != nil {
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Robots-Tag", "noindex")
	w.Header().Set("Content-Security-Policy", hackEventPageCSP(app, nonce))
	w.WriteHeader(http.StatusNotFound)
	_, _ = w.Write(httpx.StampNonce(r, buf.Bytes()))
}

// hackStageSectionOrder is layout_by_stage's generic content blocks, minus the
// stage card / results / people's choice (those are placed separately, right
// under the header). "sites" only renders when the gallery has cards.
var hackStageSectionOrder = map[string][]string{
	"draft": {"about", "tracks", "prizes", "schedule", "rules", "faq", "sponsors", "announcements"},
	// "sites" sits right after "tracks", the same adjacency building (and
	// every later stage) keeps between the two (review 2026-10-30 item 3):
	// the old page showed team sites from sign-up on, whenever the gallery
	// switch is on, and this restores that for the open stage too.
	"open":     {"about", "tracks", "sites", "prizes", "schedule", "faq", "rules", "sponsors", "announcements"},
	"building": {"announcements", "schedule", "sites", "tracks", "about", "prizes", "rules", "faq", "sponsors"},
	"closed":   {"sites", "announcements", "schedule", "tracks", "about", "prizes", "rules", "faq", "sponsors"},
	"judging":  {"sites", "announcements", "schedule", "tracks", "about", "prizes", "rules", "faq", "sponsors"},
	"results":  {"sites", "sponsors", "tracks", "about", "prizes", "announcements", "schedule", "rules", "faq"},
	"archived": {"sites", "sponsors", "tracks", "about", "prizes", "announcements", "schedule", "rules", "faq"},
}

var hackSectionChipLabel = map[string]string{
	"about": "About", "tracks": "Tracks", "prizes": "Prizes", "schedule": "Schedule",
	"faq": "FAQ", "rules": "Rules", "sponsors": "Sponsors", "announcements": "Announcements",
	"sites": "Team sites",
}

func assembleHackEventPage(p hackEventPage, pages *HackEventPages) ([]byte, error) {
	app := strings.TrimRight(p.AppURL, "/")
	color := strings.TrimSpace(p.Color)
	if color == "" {
		color = "#d15d48" // the kit's own brand-red fallback (kit.css .pub-event)
	}
	loc, zoneFallback := hackEventLocation(p.TimeZone)

	resultsPublished := p.Results != nil && len(p.Results.Rows) > 0
	galleryCount := len(p.Gallery)
	peoplesChoiceOpen := p.VoteStatus == "open"

	// Published results win regardless of stage (publishResults does not
	// check the stage): the pill, the section order and the Schedule/Rules
	// collapse all follow the results layout once there is anything to
	// show, not the raw stage the organiser happens to still be on
	// (review 2026-10-30 item 8). "archived" already shows its own
	// "This event has ended" pill with results in place either way, so it
	// is left alone.
	effStage := p.Stage
	switch p.Stage {
	case "building", "closed", "judging":
		if resultsPublished {
			effStage = "results"
		}
	}

	v := hackEventView{
		Slug:            p.Slug,
		Title:           p.Title,
		Tagline:         p.Tagline,
		OrganisedBy:     hackOrganisedBy(p.OrganiserName, p.Organisation),
		StagePill:       hackStagePill(effStage, resultsPublished),
		IconURL:         p.IconURL,
		Initial:         hackInitials(p.Title),
		Color:           color,
		Ink:             hackEventInk(color),
		AppURL:          app,
		TakenDown:       p.TakenDown,
		TakenDownReason: p.TakenDownReason,
		Teams:           p.Teams,
		Participants:    p.Participants,
		About:           hackTextParas(p.About),
		Rules:           hackTextParas(p.Rules),
		Prizes:          hackTextParas(p.Prizes),
		Tracks:          p.Tracks,
		FAQ:             p.Content.FAQ,
		Gallery:         p.Gallery,
		VoteURL:         app + "/e/" + p.Slug + "/vote",
	}
	v.DateRange = hackEventDateRange(p.StartsAt, p.EndsAt, loc)
	if v.DateRange != "" {
		zt := p.StartsAt
		if zt.IsZero() {
			zt = p.EndsAt
		}
		if zt.IsZero() {
			zt = time.Now()
		}
		v.ZoneNote = "times in " + hackZoneAbbr(zt.In(loc), zoneFallback)
	}
	if p.Teams > 0 && p.Stage != "draft" {
		v.CountLine = hackPlural(p.Teams, "team") + " · " + hackPlural(p.Participants, "participant")
	}
	v.MetaDescription = p.Tagline
	if v.MetaDescription == "" {
		v.MetaDescription = v.StagePill
	}
	if !p.TakenDown {
		v.OGImage = p.IconURL
	}

	for _, s := range p.Content.Sponsors {
		sponsor := hackSponsorView{Name: s.Name, Tier: s.Tier, URL: s.URL, Avatar: hackInitials(s.Name)}
		if cleanLogo(s.LogoData) {
			sponsor.Logo = template.URL(s.LogoData)
		}
		v.Sponsors = append(v.Sponsors, sponsor)
	}

	v.Schedule = hackScheduleViews(p.Content.Schedule, p.Fixed, loc, zoneFallback)
	v.ScheduleCollapsed = effStage == "results" || effStage == "archived"
	v.RulesCollapsed = v.ScheduleCollapsed

	for i, a := range p.Announcements {
		view := hackAnnouncementLine{Title: a.Title, Body: a.Body, When: a.CreatedAt.In(loc).Format("2 Jan 2006")}
		if i < 3 {
			v.AnnouncementLines = append(v.AnnouncementLines, view)
		} else {
			v.AnnouncementLinesMore = append(v.AnnouncementLinesMore, view)
		}
	}

	var deadlineNote, deadlineHeading string
	hasCountdown, deadlinePassed := false, false
	if !p.Deadline.IsZero() {
		dl := p.Deadline.In(loc)
		abbr := hackZoneAbbr(dl, zoneFallback)
		deadlineNote = dl.Format("Mon 2 Jan 2006, 3:04 pm") + " " + abbr
		deadlineHeading = dl.Format("Mon 2 Jan, 3:04 pm") + " " + abbr
		v.Deadline = p.Deadline.UTC().Format(time.RFC3339)
		if p.Stage == "open" || p.Stage == "building" {
			// Once the deadline has passed, the stage card says so plainly
			// (review 2026-10-30 item 9): no countdown and no "Entries
			// close <date>" text, even while the stage itself has not
			// moved on yet.
			if time.Now().Before(p.Deadline) {
				hasCountdown = true
				v.CountdownText = hackCountdownText(time.Now(), p.Deadline)
			} else {
				deadlinePassed = true
			}
		}
	}

	v.StageCard = buildHackStageCard(p.Stage, app, p.OrganiserName, p.Organisation,
		galleryCount, deadlineNote, deadlineHeading, hasCountdown, deadlinePassed, resultsPublished, p.ApplyURL)
	if v.StageCard != nil && v.StageCard.Primary != nil {
		v.StageCard.Primary.Strong = !peoplesChoiceOpen
	}
	v.PeoplesChoice = buildHackPeoplesChoice(p.VoteStatus, v.VoteURL)
	if resultsPublished {
		v.Results = buildHackResultsBlock(p.Results)
	}

	order := hackStageSectionOrder[effStage]
	present := map[string]bool{
		"about":         len(v.About) > 0,
		"tracks":        len(v.Tracks) > 0,
		"prizes":        len(v.Prizes) > 0,
		"schedule":      len(v.Schedule) > 0,
		"faq":           len(v.FAQ) > 0,
		"rules":         len(v.Rules) > 0,
		"sponsors":      len(v.Sponsors) > 0,
		"announcements": len(v.AnnouncementLines) > 0,
		"sites":         len(v.Gallery) > 0,
	}
	if v.Results != nil {
		v.Chips = append(v.Chips, hackChip{ID: "results", Label: v.Results.Heading})
	}
	for _, key := range order {
		if !present[key] {
			continue
		}
		html, err := hackRenderSection(key, &v, pages)
		if err != nil {
			return nil, err
		}
		v.Sections = append(v.Sections, html)
		v.Chips = append(v.Chips, hackChip{ID: key, Label: hackSectionChipLabel[key]})
	}

	tmpl, err := pages.template()
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "hack-event.html", v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func hackRenderSection(key string, v *hackEventView, pages *HackEventPages) (template.HTML, error) {
	tmpl, err := pages.template()
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "sec-"+key, v); err != nil {
		return "", err
	}
	return template.HTML(buf.String()), nil
}

func hackOrganisedBy(name, org string) string {
	name = strings.TrimSpace(name)
	org = strings.TrimSpace(org)
	switch {
	case name != "" && org != "":
		return "Organised by " + name + " (" + org + ")"
	case name != "":
		return "Organised by " + name
	case org != "":
		return "Organised by " + org
	default:
		return ""
	}
}

// hackStagePill is the short phrase in the event header's stage pill
// (spec sections "Event header": "[stage pill] Sign-up is open").
func hackStagePill(stage string, published bool) string {
	switch stage {
	case "draft":
		return "Not open yet"
	case "open":
		return "Sign-up is open"
	case "building":
		return "Teams are building"
	case "closed":
		return "Entries are closed"
	case "judging":
		return "Judging is under way"
	case "results":
		if published {
			return "Results are out"
		}
		return "Results coming soon"
	case "archived":
		return "This event has ended"
	default:
		return ""
	}
}

// buildHackStageCard is the one card under the header (spec
// "primary_action_by_stage" and "sections": "Stage card: ..."). Nil for
// results/archived once results are published: the Results block replaces it.
// applyURL is empty when applications are off (D4/§4: then the card is
// exactly what it would be without this feature).
func buildHackStageCard(stage, appURL, organiserName, organisation string,
	galleryCount int, deadlineNote, deadlineHeading string, hasCountdown, deadlinePassed bool, resultsPublished bool, applyURL string) *hackStageCardView {
	signIn := &hackCTA{Label: "Already joined? Sign in", URL: appURL + "/events"}
	switch stage {
	case "draft":
		return &hackStageCardView{
			Kicker:    "Not open yet",
			Heading:   "The organisers are still setting this up",
			Body:      "Details may change before sign-up opens. Check back here later.",
			Secondary: &hackCTA{Label: "Find another hackathon", URL: appURL + "/directory"},
		}
	case "open":
		who := strings.TrimSpace(organisation)
		if who == "" {
			who = strings.TrimSpace(organiserName)
		}
		if who == "" {
			who = "The organisers"
		}
		note := ""
		switch {
		case deadlinePassed:
			note = "Entries are closed"
		case deadlineNote != "":
			note = "Entries close " + deadlineNote
		}
		if applyURL != "" {
			return &hackStageCardView{
				Kicker:  "Sign-up is open",
				Heading: "Apply to join",
				Body: "Sign in with your email and fill in a short form. The organisers decide who joins, and you'll see their answer on your Simple Hack Home. " +
					"Have a join link from " + who + "? Open that instead.",
				Primary:         &hackCTA{Label: "Apply", URL: applyURL, Strong: true},
				Secondary:       signIn,
				Note:            note,
				CountdownInNote: hasCountdown && note != "",
			}
		}
		return &hackStageCardView{
			Kicker:  "Sign-up is open",
			Heading: "Join with the link from the organisers",
			Body: who + " shares a join link with you, for example in a WhatsApp group or by email. " +
				"Open it and sign in with your email. Some events ask a few questions first.",
			Note:            note,
			Secondary:       signIn,
			CountdownInNote: hasCountdown && note != "",
		}
	case "building":
		heading := "Building is under way"
		if galleryCount > 0 {
			heading = "See what teams are making"
		}
		switch {
		case deadlinePassed:
			heading = "Entries are closed"
		case deadlineHeading != "":
			heading = "Entries close " + deadlineHeading
		}
		body := "Sign-up is still open. Join with the link from the organisers."
		if applyURL != "" {
			body = "Sign-up is still open. Apply, or join with the link from the organisers."
		}
		card := &hackStageCardView{Kicker: "Teams are building", Heading: heading, Body: body, Secondary: signIn,
			CountdownInBody: hasCountdown}
		if galleryCount > 0 {
			card.Primary = &hackCTA{Label: fmt.Sprintf("See team sites (%d)", galleryCount), URL: "#sites"}
		}
		if applyURL != "" {
			apply := &hackCTA{Label: "Apply", URL: applyURL}
			if card.Primary == nil {
				apply.Strong = true
				card.Primary = apply
			} else {
				card.Extra = apply
			}
		}
		return card
	case "closed":
		body := "Winners will be posted on this page."
		if deadlineNote != "" {
			body = "Entries closed " + deadlineNote + ". " + body
		}
		card := &hackStageCardView{Kicker: "Entries are closed", Heading: "Teams have handed in their work", Body: body}
		if galleryCount > 0 {
			card.Primary = &hackCTA{Label: "See what teams built", URL: "#sites"}
		}
		return card
	case "judging":
		card := &hackStageCardView{
			Kicker: "Judging is under way", Heading: "The judges are scoring every entry",
			Body: "Winners will be posted on this page.",
		}
		if galleryCount > 0 {
			card.Primary = &hackCTA{Label: "See what teams built", URL: "#sites"}
		}
		return card
	case "results":
		if resultsPublished {
			return nil
		}
		card := &hackStageCardView{
			Kicker: "Results coming soon", Heading: "Winners will be posted here",
			Body: "The organisers publish the results on this page.",
		}
		if galleryCount > 0 {
			card.Primary = &hackCTA{Label: "See what teams built", URL: "#sites"}
		}
		return card
	case "archived":
		if resultsPublished {
			return nil
		}
		card := &hackStageCardView{Kicker: "This event has ended", Heading: "Thanks to everyone who took part"}
		if galleryCount > 0 {
			card.Primary = &hackCTA{Label: "See what teams built", URL: "#sites"}
		}
		return card
	default:
		return nil
	}
}

func buildHackPeoplesChoice(status, voteURL string) *hackPeoplesChoiceView {
	switch status {
	case "open":
		return &hackPeoplesChoiceView{
			Body: "Pick the team you like best. One vote each. You can change it until voting closes.",
			CTA:  &hackCTA{Label: "Vote for your favourite", URL: voteURL, Strong: true},
		}
	case "upcoming":
		return &hackPeoplesChoiceView{Body: "Voting opens soon."}
	case "closed":
		return &hackPeoplesChoiceView{Body: "Voting has closed.", CTA: &hackCTA{Label: "See the vote results", URL: voteURL}}
	default:
		return nil
	}
}

// buildHackResultsBlock turns the DB-facing hackResultsView into the
// template-ready form: avatars, the "Winner" pill and the one score-method
// line are all decided here (spec "Results" section, decision on score wording).
func buildHackResultsBlock(res *hackResultsView) *hackResultsBlockView {
	out := &hackResultsBlockView{Heading: "Winners"}
	if res.FullRanking {
		out.Heading = "Results"
	}
	for _, row := range res.Rows {
		rv := hackResultsRowView{hackResultsRow: row, Score: trimScoreZeros(row.Score)}
		if row.Rank > 0 {
			out.AnyRanked = true
		}
		if row.Score != "" {
			out.ShowScoreHeader = out.ShowScoreHeader || res.FullRanking
			if out.ScoreNote == "" {
				switch row.ScoreMode {
				case "normalised":
					out.ScoreNote = "Scores are adjusted so strict and generous judges count the same."
				default:
					out.ScoreNote = "Scores are the judges' average."
				}
			}
		}
		trackPill := ""
		if row.TrackWinner {
			trackPill = "Track winner"
			if row.TrackName != "" {
				trackPill = row.TrackName + " winner"
			}
			if row.TrackPrize != "" {
				trackPill += " · " + row.TrackPrize
			}
		}
		if !res.FullRanking {
			// Winners-only: a row can be the overall winner, a track
			// winner, or both — markTrackWinners makes the rank-1 team the
			// winner of its own track too, so the two pills are independent
			// (review 2026-10-30 item 1; Overall is set from the snapshot
			// rank, not from publicRanks, so this holds with ranks hidden).
			if row.Overall {
				rv.Avatar = "1"
				rv.WinnerPill = true
			} else if row.TrackWinner {
				rv.Avatar = hackInitials(row.TeamName)
			}
			if row.TrackWinner {
				rv.TrackPill = trackPill
			}
		} else {
			rv.TrackPill = trackPill
		}
		if row.Card != nil {
			rv.ShotLetter = rv.Avatar
			if rv.ShotLetter == "" {
				name := row.Card.Title
				if name == "" {
					name = row.TeamName
				}
				rv.ShotLetter = hackInitials(name)
			}
			rv.MaturityLabel = hackMaturityLabel(row.Card.Maturity)
			out.HasCards = true
		}
		out.Rows = append(out.Rows, rv)
	}
	return out
}

func hackTextParas(s string) [][]string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	var paras [][]string
	for _, p := range strings.Split(s, "\n\n") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		paras = append(paras, strings.Split(p, "\n"))
	}
	return paras
}

// hackEventLocation loads the event's IANA zone, falling back to UTC (and
// reporting "UTC" as the fallback name) when it is empty or invalid.
func hackEventLocation(zone string) (*time.Location, string) {
	loc, err := time.LoadLocation(zone)
	if err != nil || loc == nil {
		return time.UTC, "UTC"
	}
	name := zone
	if name == "" {
		name = loc.String()
	}
	return loc, name
}

var hackNumericZoneRe = regexp.MustCompile(`^[+-]\d`)

// hackZoneAbbrFamiliar is the same fixed map ui.zoneAbbr (internal/handler/
// static/hack/ui.js) and the vote page's own copy of it use -- kept in step
// by hand, since Go's tzdata and Intl don't always agree on which zones get
// a real short code. Go already gets North America, the UK and Central
// Europe (CET/CEST) and Australia (AEDT/AEST) right from tzdata directly;
// this map only covers the zones tzdata leaves as a bare numeric offset.
var hackZoneAbbrFamiliar = map[string]string{
	"Asia/Jakarta": "WIB", "Asia/Pontianak": "WIB",
	"Asia/Makassar":  "WITA",
	"Asia/Jayapura":  "WIT",
	"Asia/Tokyo":     "JST",
	"Asia/Seoul":     "KST",
	"Asia/Singapore": "SGT",
	"Asia/Kolkata":   "IST", "Asia/Calcutta": "IST",
}

// hackZoneAbbr is the short zone name for a moment in time ("WIB", "GMT"),
// the way tech_notes asks for ("times in WIB"). Go's tzdata does not assign
// every zone a short code; when what comes back looks like a bare UTC offset
// ("+07") this checks the familiar-zone map first (SGT, WIB, WITA, WIT, JST,
// KST, IST -- same table and same decision as the app's ui.zoneAbbr and the
// vote page), then falls back to a GMT±N label built from the numeric
// offset itself, never the IANA zone name ("Asia/Singapore") -- consistent
// with what a browser without a named abbreviation for that zone shows.
func hackZoneAbbr(t time.Time, ianaFallback string) string {
	abbr, offsetSeconds := t.Zone()
	if abbr != "" && !hackNumericZoneRe.MatchString(abbr) {
		return abbr
	}
	if a, ok := hackZoneAbbrFamiliar[ianaFallback]; ok {
		return a
	}
	return hackGMTOffsetLabel(offsetSeconds)
}

// hackGMTOffsetLabel formats a UTC offset in seconds as "GMT", "GMT+9" or
// "GMT+5:30" -- the same shape ui.zoneAbbr's gmtOffsetLabel builds.
func hackGMTOffsetLabel(offsetSeconds int) string {
	if offsetSeconds == 0 {
		return "GMT"
	}
	sign := "+"
	mins := offsetSeconds / 60
	if mins < 0 {
		sign = "-"
		mins = -mins
	}
	h, m := mins/60, mins%60
	if m == 0 {
		return fmt.Sprintf("GMT%s%d", sign, h)
	}
	return fmt.Sprintf("GMT%s%d:%02d", sign, h, m)
}

func hackEventDateRange(starts, ends time.Time, loc *time.Location) string {
	var start, end time.Time
	if !starts.IsZero() {
		start = starts.In(loc)
	}
	if !ends.IsZero() {
		end = ends.In(loc)
	}
	if start.IsZero() && end.IsZero() {
		return ""
	}
	if end.IsZero() {
		return start.Format("2 January 2006")
	}
	if start.IsZero() {
		return end.Format("2 January 2006")
	}
	return hackDateRange(start, end)
}

func hackDateRange(a, b time.Time) string {
	if a.Year() == b.Year() && a.Month() == b.Month() && a.Day() == b.Day() {
		return a.Format("2 January 2006")
	}
	if a.Year() == b.Year() && a.Month() == b.Month() {
		return fmt.Sprintf("%d–%d %s %d", a.Day(), b.Day(), a.Month().String(), a.Year())
	}
	if a.Year() == b.Year() {
		return fmt.Sprintf("%d %s–%d %s %d", a.Day(), a.Month().String(), b.Day(), b.Month().String(), a.Year())
	}
	return a.Format("2 January 2006") + "–" + b.Format("2 January 2006")
}

// hackScheduleViews sorts rows by start time (Grok re-test cosmetic #4: rows
// used to list in whatever order the organiser entered them, so "Demos"
// could print above "Kickoff" and the Next badge landed on the wrong row),
// marks Now/Next, and shows the end date too when an item spans more than one
// day (tech_notes/fixes: "Multi-day schedule items showed only the end time").
// A row with an unparseable time sorts last and is dropped below, same as
// before. The fixed rows (hack_schedule.go: the event's real start, end,
// entry deadline, voting window and results) are merged in: a moment shows
// one time, a date-only event start or end shows just the date and is never
// Now or Next.
func hackScheduleViews(items []hackScheduleItem, fixed []hackFixedRow, loc *time.Location, zoneFallback string) []hackScheduleView {
	type row struct {
		title, desc         string
		start, end, sortAt  time.Time
		hasEnd, allDay, fix bool
	}
	var rows []row
	for _, item := range items {
		start, e1 := time.Parse(time.RFC3339, item.StartAt)
		end, e2 := time.Parse(time.RFC3339, item.EndAt)
		if e1 != nil || e2 != nil {
			continue
		}
		rows = append(rows, row{title: item.Title, desc: item.Description, start: start, end: end, sortAt: start, hasEnd: true})
	}
	for _, f := range fixed {
		start, err := time.Parse(time.RFC3339, f.StartAt)
		if err != nil {
			continue
		}
		r := row{title: f.Title, desc: f.Description, start: start, sortAt: hackFixedSortTime(f, loc), allDay: f.AllDay, fix: true}
		if f.EndAt != nil {
			if end, err := time.Parse(time.RFC3339, *f.EndAt); err == nil {
				r.end, r.hasEnd = end, true
			}
		}
		rows = append(rows, r)
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].sortAt.Before(rows[j].sortAt) })
	now := time.Now()
	next, ongoing := -1, -1
	for i, r := range rows {
		if r.allDay {
			continue
		}
		if ongoing < 0 && r.hasEnd && !now.Before(r.start) && now.Before(r.end) {
			ongoing = i
		}
		if now.Before(r.start) && (next < 0 || r.start.Before(rows[next].start)) {
			next = i
		}
	}
	var out []hackScheduleView
	for i, r := range rows {
		ls := r.start.In(loc)
		var when string
		switch {
		case r.allDay:
			when = ls.Format("Mon 2 Jan")
		case !r.hasEnd:
			when = ls.Format("Mon 2 Jan, 3:04 pm") + " " + hackZoneAbbr(ls, zoneFallback)
		default:
			le := r.end.In(loc)
			abbr := hackZoneAbbr(le, zoneFallback)
			if ls.Year() == le.Year() && ls.Month() == le.Month() && ls.Day() == le.Day() {
				when = ls.Format("Mon 2 Jan, 3:04 pm") + "–" + le.Format("3:04 pm") + " " + abbr
			} else {
				when = ls.Format("Mon 2 Jan 3:04 pm") + " – " + le.Format("Mon 2 Jan 3:04 pm") + " " + abbr
			}
		}
		status := ""
		if i == ongoing {
			status = "Now"
		} else if i == next {
			status = "Next"
		}
		out = append(out, hackScheduleView{Title: r.title, Description: r.desc, When: when, Status: status, Fixed: r.fix})
	}
	return out
}

// hackCountdownText is the server-rendered, non-live value the countdown
// script enhances in place (tech_notes: "the date alone without it").
func hackCountdownText(now, deadline time.Time) string {
	if !now.Before(deadline) {
		return "Entries are closed."
	}
	left := deadline.Sub(now)
	days := int(left.Hours()) / 24
	hours := int(left.Hours()) % 24
	minutes := int(left.Minutes()) % 60
	switch {
	case days > 0:
		return hackPlural(days, "day") + " " + hackPlural(hours, "hour") + " left"
	case hours > 0:
		return hackPlural(hours, "hour") + " " + hackPlural(minutes, "minute") + " left"
	case minutes > 0:
		return hackPlural(minutes, "minute") + " left"
	default:
		return "less than a minute left"
	}
}

func hackPlural(n int, unit string) string {
	if n == 1 {
		return "1 " + unit
	}
	return fmt.Sprintf("%d %ss", n, unit)
}

// hackScoreText formats a score for display: full precision (two decimals,
// matching the data everywhere else stores it), with trailing zeros trimmed
// so "100.00" reads "100" and "42.50" reads "42.5" (Grok SIM-346 Part B
// hack-event.html item 4) -- but, after review #8, never rounded to fewer
// decimals than it actually has: "81.25" must stay "81.25", not become
// "81.3" or "81.2". The old one-decimal-with-rounding version did exactly
// that, and for two teams a hundredth apart it made both read the same
// score. hackResultsFromSnapshot (hack_wire.go) keeps the untrimmed,
// unrounded string for the public JSON API (BUILD.md goal 4: that API does
// not change); buildHackResultsBlock trims it for the HTML template only,
// via trimScoreZeros below, so display and API no longer have to be the
// same string.
func hackScoreText(score float64) string {
	return trimScoreZeros(strconv.FormatFloat(score, 'f', 2, 64))
}

// trimScoreZeros trims trailing zeros (then a bare trailing ".") off an
// already-formatted decimal string, for display -- it never rounds, so it
// can't drop a real digit the way reformatting at fewer decimal places can.
func trimScoreZeros(s string) string {
	if s == "" || !strings.Contains(s, ".") {
		return s
	}
	s = strings.TrimRight(s, "0")
	return strings.TrimSuffix(s, ".")
}

// hackInitials is one or two upper-case letters for an avatar with no image:
// the first letter of up to the first two words, or just the first letter of
// a single word.
func hackInitials(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	var out []rune
	for _, f := range strings.Fields(name) {
		r := []rune(f)
		if len(r) == 0 {
			continue
		}
		out = append(out, []rune(strings.ToUpper(string(r[0])))...)
		if len(out) >= 2 {
			break
		}
	}
	if len(out) == 0 {
		r := []rune(name)
		return strings.ToUpper(string(r[0]))
	}
	return string(out)
}
