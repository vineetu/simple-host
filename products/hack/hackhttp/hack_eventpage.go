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
	"strings"
	"sync"
	"time"

	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/products/hack/app/events"
	"github.com/vsriram/simple-host/products/hack/model"
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
	ApplyURL string
	IconURL  string
	Color    string
	// Theme is the Look setting (SIM-353, 0011-hack-event-theme.sql): "" is
	// Classic, today's look, with no themes.css link and no data-theme
	// attribute at all, so Classic renders byte-identical to before this
	// field existed. Otherwise one of events.Themes.
	Theme           string
	TakenDown       bool
	TakenDownReason string
	Submissions     []hackSubmission
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
	Pinned      bool
	BodyHTML    string
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
	TeamID       string
	URL          string
	Rank         int
	TeamName     string
	Tied         bool
	TrackName    string
	TrackPrize   string
	TrackWinner  bool
	OverallPrize string
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
	Title         string            `json:"title"`
	Tagline       string            `json:"tagline"`
	Problem       string            `json:"problem"`
	Audience      string            `json:"audience"`
	Tools         []string          `json:"tools"`
	Maturity      string            `json:"maturity"`
	Track         *model.EventTrack `json:"track"`
	VideoURL      string            `json:"video_url"`
	VideoHost     string            `json:"video_host"`
	CodeURL       string            `json:"code_url"`
	CodeHost      string            `json:"code_host"`
	SiteURL       string            `json:"site_url"`
	HasScreenshot bool              `json:"has_screenshot"`
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
// response all render from. It parses lazily, once, from the caller's fs.FS
// (the product's own embed in production; the test fixtures do the same).
type HackEventPages struct {
	fsys fs.FS
	once sync.Once
	tmpl *template.Template
	err  error
}

// NewHackEventPages builds a template cache for hack-event.html, read from
// fsys (the product's embed, products/hack/webassets.Static) on first use.
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
	Avatar     string // team initials when no screenshot is available
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
	// above). ShotLetter is the team-initials fallback when Shot is empty.
	ShotLetter    string
	MaturityLabel string
}

type hackSubmissionView struct {
	hackSubmission
	MaturityLabel string
}

type hackEventView struct {
	Title, Tagline, OrganisedBy string
	DateRange, ZoneNote         string
	StagePill                   string
	IconURL, Initial, Color     string
	Ink                         string // text colour that reads on --ev (hackEventInk)
	// Theme (SIM-353): "" is Classic, never rendered as an attribute or a
	// themes.css link, so a classic event's HTML is unchanged from before
	// this field existed.
	Theme string
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
	Submissions           []hackSubmissionView
	Gallery               []hackGalleryCard
	VoteURL               string
}

// hackAnnouncementLine is one announcement as the page shows it: the date
// already formatted in the event's own time zone.
type hackAnnouncementLine struct {
	Title, Body, When string
	// BodyHTML is the body rendered by rules.AnnouncementHTML, which escapes
	// every character of the organiser's text, so it is safe to trust here.
	BodyHTML template.HTML
	Pinned   bool
}

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
		"default-src 'none'; script-src 'nonce-%s'; style-src %s 'unsafe-inline'; img-src 'self' %s data: https://i.ytimg.com; font-src %s data:; connect-src %s; base-uri 'none'; form-action 'none'; frame-ancestors 'none'",
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
	"judging":  {"submissions", "sites", "announcements", "schedule", "tracks", "about", "prizes", "rules", "faq", "sponsors"},
	"results":  {"submissions", "sites", "sponsors", "tracks", "about", "prizes", "announcements", "schedule", "rules", "faq"},
	"archived": {"submissions", "sites", "sponsors", "tracks", "about", "prizes", "announcements", "schedule", "rules", "faq"},
}

var hackSectionChipLabel = map[string]string{
	"about": "About", "tracks": "Tracks", "prizes": "Prizes", "schedule": "Schedule",
	"faq": "FAQ", "rules": "Rules", "sponsors": "Sponsors", "announcements": "Announcements",
	"sites":       "Team sites",
	"submissions": "Submissions",
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
		Theme:           p.Theme,
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
	v.ScheduleCollapsed = false
	v.RulesCollapsed = effStage == "results" || effStage == "archived"

	for i, a := range p.Announcements {
		view := hackAnnouncementLine{Title: a.Title, Body: a.Body, When: a.CreatedAt.In(loc).Format("2 Jan 2006"), Pinned: a.Pinned,
			BodyHTML: template.HTML(events.RenderAnnouncement(a.Body))}
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

	for _, c := range p.Submissions {
		v.Submissions = append(v.Submissions, hackSubmissionView{c, hackMaturityLabel(c.Maturity)})
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
		"submissions":   len(v.Submissions) > 0,
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
