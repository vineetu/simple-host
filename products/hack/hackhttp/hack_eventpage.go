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
	// Theme is the Look setting (SIM-353, 0011-hack-event-theme.sql): "" is
	// Classic, today's look, with no themes.css link and no data-theme attribute
	// at all, so Classic renders byte-identical to before this field existed.
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

// hackAnnouncementView carries the raw time so the public page can format it in
// the event's own time zone (tech_notes: "Announcement dates ...
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
	// Card is this team's public summary card (build-plan §4.6), nil for a row
	// from a snapshot published before slice 3, or for a team whose snapshot entry
	// was somehow never written.
	Card *hackResultCard
	// Shot is the template-ready absolute screenshot URL (hack_wire.go's
	// HackEventPage), set only when the gallery is public and this team's
	// site is live right now -- the same freshness gate the Team sites
	// section already applies, reused here instead of trusting the
	// publish-time has_screenshot forever.
	Shot string
}

// hackResultCard is one ranked team's public entry summary (build-plan §4.6),
// round-tripped through JSON from the snapshot's per-row "entry" field (see
// resultsSnapshotJSON/resultsEntryCards in hack_judging.go) so its field tags
// double as the wire shape for GET .../results, GET .../public and the MCP
// reads that proxy them.
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
func hackMaturityLabel(m string) string { return hackMaturityLabels[m] }

var hackMaturityLabels = map[string]string{"working": "Demo-ready", "partial": "Partly working", "concept": "Concept"}

// hackResultsView is nil when nothing is published yet.
type hackResultsView struct {
	FullRanking bool
	Rows        []hackResultsRow
}

// hackGalleryCard is one project on the public event page.
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

// HackEventPages owns the parsed hack-event.html template that the public event
// page, HackEventPage's SiteHandler hook and their shared not-found response
// all render from.
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

// template parses hack-event.html once, lazily, from p.fsys.
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
	// HasCards is true once any row carries a public summary card (build-plan
	// §4.6).
	HasCards bool
}

type hackResultsRowView struct {
	hackResultsRow
	Avatar     string // team initials when no screenshot is available
	WinnerPill bool
	TrackPill  string
	// Score shadows hackResultsRow.Score (same name, outer field wins over the
	// embedded one): the template only ever sees this trimmed-for- display form,
	// set by buildHackResultsBlock.
	Score string
	// ShotLetter and MaturityLabel are template-only strings the Go template
	// itself cannot compute (hackEventTmpl registers no custom funcs, so every bit
	// of formatting happens here first, same as Score above).
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
	// Slug (Grok retest, Cosmetic #3): the stage card's own script reads it to ask
	// the app's API, cross-origin, whether the signed-in visitor already belongs
	// to this event, and swap out "Join with the link from the organisers...
	Slug            string
	AppURL          string
	TakenDown       bool
	TakenDownReason string
	// NotFound renders the same .pub shell's own "not found" state, for a slug
	// that no longer resolves to an event (deleted, or never existed): the same
	// top bar and footer as a live event page, instead of generic site chrome
	// (Grok SIM-346 S15: "a deleted event shows a participant only a bare 'event
	// not found'").
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

// writeHackEventNotFound renders the public event page's own "not found"
// state at /e/{slug} when the slug no longer resolves to an event (deleted,
// or never existed): the same top bar and footer as a live event page,
// instead of generic site chrome (Grok SIM-346 S15: "a deleted event shows
// a participant only a bare 'event not found'").
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

// hackEventPageCSP is the public event page's Content-Security-Policy.
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

// resultsPublished is whether there are published results to show.
func (p hackEventPage) resultsPublished() bool {
	return p.Results != nil && len(p.Results.Rows) > 0
}

// hackSectionPresent is whether a section has anything to show.
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

// hackEventPageView decides everything the page shows, before any section
// renders: header, stage card, voting, results and the section data.
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

// hackEventHeader is the header's part of the view: title, organiser,
// stage pill, colours, dates and the share-preview fields.
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

// hackFirstTime is the first of ts that is set.
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

// hackAnnouncementLines are the first three announcements and the rest,
// dated in the event's zone.
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

// hackSubmissionViews adds each submission's maturity wording.
func hackSubmissionViews(subs []hackSubmission) []hackSubmissionView {
	var out []hackSubmissionView
	for _, c := range subs {
		out = append(out, hackSubmissionView{c, hackMaturityLabel(c.Maturity)})
	}
	return out
}

// execute renders one named template of hack-event.html.
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
