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

// hackEventPage is the public event page at <event>.simple-hack.app/.
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
	// Theme is the Look setting: "" is Classic, rendered with no themes.css link and no data-theme attribute.
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

// hackAnnouncementView carries the raw time so the page can format it in the event's own time zone.
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
	// Overall is true for the rank-1 team(s), independent of whether ranks are
	// shown publicly (publicRanks zeroes Rank, not this).
	Overall   bool
	Score     string
	ScoreMode string
	// Card is this team's public summary card; nil for a row from an older snapshot or a team whose snapshot entry was never written.
	Card *hackResultCard
	// Shot is the absolute screenshot URL, set only when the gallery is public and the team's site is live right now (the same freshness gate as the Team sites section).
	Shot string
}

// hackResultCard is one ranked team's public entry summary, round-tripped through JSON from the snapshot's per-row "entry" field (resultsSnapshotJSON/resultsEntryCards in hack_judging.go); its tags are the wire shape for GET .../results, .../public and the MCP reads.
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

// hackMaturityLabel is the plain word for what works today; keep in step with ui.js's MATURITY table (this server-rendered page has no script to share it).
func hackMaturityLabel(m string) string { return hackMaturityLabels[m] }

var hackMaturityLabels = map[string]string{"working": "Demo-ready", "partial": "Partly working", "concept": "Concept"}

// hackResultsView is nil when nothing is published yet.
type hackResultsView struct {
	FullRanking bool
	Rows        []hackResultsRow
}

type hackGalleryCard struct {
	Title, Tagline, Team string
	Shot, URL            string
	Initial              string // the title's first letter, for a card with no screenshot
	Tools                []string
	ResultAnchor         string // "#result-<team id>" when this team has a visible result card, else ""
	// TeamID is never rendered by the template; HackEventPage (hack_wire.go) uses
	// it to match a gallery card to its results row when filling in ResultAnchor.
	TeamID string
}

// HackEventPages owns the parsed hack-event.html template.
type HackEventPages struct {
	fsys fs.FS
	once sync.Once
	tmpl *template.Template
	err  error
}

// NewHackEventPages reads hack-event.html from fsys on first use.
func NewHackEventPages(fsys fs.FS) *HackEventPages {
	return &HackEventPages{fsys: fsys}
}

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

type hackChip struct{ ID, Label string }

// hackStageCardView is the one stage-specific card under the header
// (spec: "primary_action_by_stage"). Nil when the Results block replaces it.
type hackStageCardView struct {
	Kicker, Heading, Body, Note string
	Primary, Secondary          *hackCTA
	// Extra is a third button between Primary and Secondary, used only for Building + live gallery + applications open.
	Extra *hackCTA
	// The live countdown (an inline <span id="countdown">, enhanced by the one
	// nonce script) sits in Note for Open and in Body for Building — never both.
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
	// HasCards is true once any row carries a public summary card.
	HasCards bool
}

type hackResultsRowView struct {
	hackResultsRow
	Avatar     string // team initials when no screenshot is available
	WinnerPill bool
	TrackPill  string
	// Score shadows hackResultsRow.Score: the template only sees this trimmed display form.
	Score string
	// ShotLetter and MaturityLabel are formatted here because the template has no custom funcs.
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
	// Theme: "" is Classic, rendered with no attribute or themes.css link.
	Theme string
	// Slug: the stage card's script reads it to ask the app's API (cross-origin) whether the signed-in visitor already belongs to this event.
	Slug            string
	AppURL          string
	TakenDown       bool
	TakenDownReason string
	// NotFound renders the .pub shell's not-found state for a slug that no longer resolves (deleted or never existed), with the same top bar and footer as a live page.
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

	// Fields below are internal build state: each holds one section's data long
	// enough for its "sec-*" partial to render, then that rendering is appended to
	// Sections in the stage's order.
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

type hackAnnouncementLine struct {
	Title, Body, When string
	// BodyHTML is the body rendered by rules.AnnouncementHTML, which escapes
	// every character of the organiser's text, so it is safe to trust here.
	BodyHTML template.HTML
	Pinned   bool
}

// renderHackEventPage writes the whole response (status 200, or 410 when TakenDown).
func renderHackEventPage(w http.ResponseWriter, r *http.Request, p hackEventPage, pages *HackEventPages) {
	page, err := assembleHackEventPage(p, pages)
	if err != nil {
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	status := http.StatusOK
	if p.TakenDown {
		status = http.StatusGone
	}
	writeHackEventHTML(w, r, p.AppURL, status, p.TakenDown || p.Stage == "draft", page)
}

// writeHackEventNotFound renders the not-found state at /e/{slug} for a slug that no longer resolves.
func writeHackEventNotFound(w http.ResponseWriter, r *http.Request, appURL string, pages *HackEventPages) {
	v := hackEventView{NotFound: true, Title: "Event not found", AppURL: strings.TrimRight(appURL, "/")}
	page, err := pages.execute("hack-event.html", v)
	if err != nil {
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	writeHackEventHTML(w, r, appURL, http.StatusNotFound, true, page)
}

// writeHackEventHTML writes a rendered event page with a fresh script nonce
// stamped into it and the page's own security headers.
func writeHackEventHTML(w http.ResponseWriter, r *http.Request, appURL string, status int, noindex bool, page []byte) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	nonce := base64.StdEncoding.EncodeToString(b[:])
	r = r.WithContext(context.WithValue(r.Context(), httpx.NonceKey{}, nonce))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", hackEventPageCSP(strings.TrimRight(appURL, "/"), nonce))
	if noindex {
		w.Header().Set("X-Robots-Tag", "noindex")
	}
	w.WriteHeader(status)
	_, _ = w.Write(httpx.StampNonce(r, page))
}

func hackEventPageCSP(app, nonce string) string {
	return fmt.Sprintf(
		"default-src 'none'; script-src 'nonce-%s'; style-src %s 'unsafe-inline'; img-src 'self' %s data: https://i.ytimg.com; font-src %s data:; connect-src %s; base-uri 'none'; form-action 'none'; frame-ancestors 'none'",
		nonce, app, app, app, app)
}

// hackStageSectionOrder is layout_by_stage's generic content blocks, minus the
// stage card / results / people's choice (those are placed separately, right
// under the header). "sites" only renders when the gallery has cards.
var hackStageSectionOrder = map[string][]string{
	"draft": {"about", "tracks", "prizes", "schedule", "rules", "faq", "sponsors", "announcements"},
	// "sites" sits right after "tracks", as in building and every later stage: team sites show from sign-up on whenever the gallery switch is on.
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

func (p hackEventPage) resultsPublished() bool {
	return p.Results != nil && len(p.Results.Rows) > 0
}

var hackSectionPresent = map[string]func(v *hackEventView) bool{
	"about":         func(v *hackEventView) bool { return len(v.About) > 0 },
	"tracks":        func(v *hackEventView) bool { return len(v.Tracks) > 0 },
	"prizes":        func(v *hackEventView) bool { return len(v.Prizes) > 0 },
	"schedule":      func(v *hackEventView) bool { return len(v.Schedule) > 0 },
	"faq":           func(v *hackEventView) bool { return len(v.FAQ) > 0 },
	"rules":         func(v *hackEventView) bool { return len(v.Rules) > 0 },
	"sponsors":      func(v *hackEventView) bool { return len(v.Sponsors) > 0 },
	"announcements": func(v *hackEventView) bool { return len(v.AnnouncementLines) > 0 },
	"sites":         func(v *hackEventView) bool { return len(v.Gallery) > 0 },
	"submissions":   func(v *hackEventView) bool { return len(v.Submissions) > 0 },
}

// assembleHackEventPage renders the whole public event page: the view is
// decided first, then each present section renders in the stage's order.
func assembleHackEventPage(p hackEventPage, pages *HackEventPages) ([]byte, error) {
	v := hackEventPageView(p, time.Now())
	if v.Results != nil {
		v.Chips = append(v.Chips, hackChip{ID: "results", Label: v.Results.Heading})
	}
	for _, key := range hackStageSectionOrder[events.PageStage(p.Stage, p.resultsPublished())] {
		if !hackSectionPresent[key](&v) {
			continue
		}
		html, err := pages.execute("sec-"+key, &v)
		if err != nil {
			return nil, err
		}
		v.Sections = append(v.Sections, template.HTML(html))
		v.Chips = append(v.Chips, hackChip{ID: key, Label: hackSectionChipLabel[key]})
	}
	return pages.execute("hack-event.html", v)
}

// hackEventPageView decides everything the page shows before any section renders.
func hackEventPageView(p hackEventPage, now time.Time) hackEventView {
	app := strings.TrimRight(p.AppURL, "/")
	loc, zoneFallback := hackEventLocation(p.TimeZone)
	resultsPublished := p.resultsPublished()
	pageStage := events.PageStage(p.Stage, resultsPublished)
	v := hackEventHeader(p, pageStage, resultsPublished, loc, zoneFallback, now)
	v.About, v.Rules, v.Prizes = hackTextParas(p.About), hackTextParas(p.Rules), hackTextParas(p.Prizes)
	v.Tracks, v.FAQ, v.Gallery = p.Tracks, p.Content.FAQ, p.Gallery
	v.VoteURL = app + "/e/" + p.Slug + "/vote"
	v.Sponsors = hackSponsorViews(p.Content.Sponsors)
	v.Schedule = hackScheduleViews(p.Content.Schedule, p.Fixed, loc, zoneFallback)
	v.RulesCollapsed = pageStage == "results" || pageStage == "archived"
	v.AnnouncementLines, v.AnnouncementLinesMore = hackAnnouncementLines(p.Announcements, loc)
	v.Submissions = hackSubmissionViews(p.Submissions)

	card := hackStageCardInput{Stage: p.Stage, AppURL: app, Who: hackOrganiserWho(p.OrganiserName, p.Organisation),
		ApplyURL: p.ApplyURL, GalleryCount: len(p.Gallery), ResultsPublished: resultsPublished, VotingOpen: p.VoteStatus == "open"}
	if !p.Deadline.IsZero() {
		dl := p.Deadline.In(loc)
		abbr := hackZoneAbbr(dl, zoneFallback)
		card.DeadlineNote = dl.Format("Mon 2 Jan 2006, 3:04 pm") + " " + abbr
		card.DeadlineHeading = dl.Format("Mon 2 Jan, 3:04 pm") + " " + abbr
		v.Deadline = p.Deadline.UTC().Format(time.RFC3339)
	}
	card.Countdown, card.DeadlinePassed = events.EntryDeadlineState(p.Stage, p.Deadline, now)
	if card.Countdown {
		v.CountdownText = hackCountdownText(now, p.Deadline)
	}
	v.StageCard = buildHackStageCard(card)
	v.PeoplesChoice = buildHackPeoplesChoice(p.VoteStatus, v.VoteURL)
	if resultsPublished {
		v.Results = buildHackResultsBlock(p.Results)
	}
	return v
}

func hackEventHeader(p hackEventPage, pageStage string, resultsPublished bool, loc *time.Location, zoneFallback string, now time.Time) hackEventView {
	color := strings.TrimSpace(p.Color)
	if color == "" {
		color = "#d15d48" // the kit's own brand-red fallback (kit.css .pub-event)
	}
	v := hackEventView{
		Slug: p.Slug, Title: p.Title, Tagline: p.Tagline,
		OrganisedBy: hackOrganisedBy(p.OrganiserName, p.Organisation),
		StagePill:   hackStagePill(pageStage, resultsPublished),
		IconURL:     p.IconURL, Initial: hackInitials(p.Title), Color: color, Ink: hackEventInk(color),
		Theme: p.Theme, AppURL: strings.TrimRight(p.AppURL, "/"),
		TakenDown: p.TakenDown, TakenDownReason: p.TakenDownReason,
		Teams: p.Teams, Participants: p.Participants,
		DateRange:       hackEventDateRange(p.StartsAt, p.EndsAt, loc),
		MetaDescription: p.Tagline,
	}
	if v.DateRange != "" {
		v.ZoneNote = "times in " + hackZoneAbbr(hackFirstTime(p.StartsAt, p.EndsAt, now).In(loc), zoneFallback)
	}
	if p.Teams > 0 && p.Stage != "draft" {
		v.CountLine = hackPlural(p.Teams, "team") + " · " + hackPlural(p.Participants, "participant")
	}
	if v.MetaDescription == "" {
		v.MetaDescription = v.StagePill
	}
	if !p.TakenDown {
		v.OGImage = p.IconURL
	}
	return v
}

func hackFirstTime(ts ...time.Time) time.Time {
	for _, t := range ts {
		if !t.IsZero() {
			return t
		}
	}
	return time.Time{}
}

// hackSponsorViews are the sponsors with a logo when it is a clean image,
// otherwise their initials.
func hackSponsorViews(sponsors []hackSponsor) []hackSponsorView {
	var out []hackSponsorView
	for _, s := range sponsors {
		sponsor := hackSponsorView{Name: s.Name, Tier: s.Tier, URL: s.URL, Avatar: hackInitials(s.Name)}
		if events.LogoValid(s.LogoData) {
			sponsor.Logo = template.URL(s.LogoData)
		}
		out = append(out, sponsor)
	}
	return out
}

func hackAnnouncementLines(anns []hackAnnouncementView, loc *time.Location) (first, more []hackAnnouncementLine) {
	for i, a := range anns {
		line := hackAnnouncementLine{Title: a.Title, Body: a.Body, When: a.CreatedAt.In(loc).Format("2 Jan 2006"), Pinned: a.Pinned,
			BodyHTML: template.HTML(events.RenderAnnouncement(a.Body))}
		if i < 3 {
			first = append(first, line)
		} else {
			more = append(more, line)
		}
	}
	return first, more
}

func hackSubmissionViews(subs []hackSubmission) []hackSubmissionView {
	var out []hackSubmissionView
	for _, c := range subs {
		out = append(out, hackSubmissionView{c, hackMaturityLabel(c.Maturity)})
	}
	return out
}

func (p *HackEventPages) execute(name string, data any) ([]byte, error) {
	tmpl, err := p.template()
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, name, data); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
