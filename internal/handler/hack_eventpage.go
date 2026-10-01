package handler

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"html/template"
	"net/http"
	"sort"
	"strings"
	"time"
)

// hackEventPage is the public event page at <event>.simple-hack.app/. The
// integrator fills it from the database.
type hackEventPage struct {
	Slug, Title, Tagline, About, Rules, Prizes string
	OrganiserName, Organisation                string
	Stage                                      string    // draft|open|building|closed|judging|results|archived
	TimeZone                                   string    // IANA name
	StartsAt, EndsAt                           time.Time // zero = not set
	Participants, Teams                        int
	AppURL                                     string // "https://simple-hack.app"
	TakenDown                                  bool
	TakenDownReason                            string
	Gallery                                    []hackGalleryCard
	Tracks                                     []hackTrackView
	Results                                    *hackResultsView
	Content                                    hackContent
	Announcements                              []hackAnnouncementView
	Deadline                                   time.Time
	VoteStatus                                 string
}

type hackAnnouncementView struct{ Title, Body, When string }
type hackSponsorView struct {
	Name, Tier, URL string
	Logo            template.URL
}
type hackScheduleView struct{ Title, Description, When, Status string }
type hackTrackView struct{ Name, Challenge, Prize string }

// hackResultsRow is one row the public page shows: winners-only has just the
// rank-1 team(s), a full ranking has every ranked team.
type hackResultsRow struct {
	Rank        int
	TeamName    string
	Tied        bool
	TrackName   string
	TrackPrize  string
	TrackWinner bool
	Score       string
	ScoreMode   string
}

// hackResultsView is nil when nothing is published yet.
type hackResultsView struct {
	FullRanking bool
	Rows        []hackResultsRow
}

// hackGalleryCard is one project on the public event page. Team is set only
// when it differs from Title. Shot and URL are built from a validated slug.
type hackGalleryCard struct {
	Title, Tagline, Team string
	Shot, URL            string
	Initial              string // the title's first letter, for a card with no screenshot
}

var hackEventTmpl = template.Must(template.New("hack-event.html").Funcs(template.FuncMap{
	// html/template drops HTML comments, so the chrome markers in the file
	// never reach withChrome unless we write them back as trusted HTML.
	"hackMarker": func(which string) template.HTML {
		switch which {
		case "head":
			return markerHead
		case "header":
			return markerHeader
		case "footer":
			return markerFooter
		default:
			return ""
		}
	},
}).ParseFS(staticFiles, "static/hack-event.html"))

type hackEventView struct {
	Title, Tagline       string
	OrganisedBy          string
	DateLine             string
	Status               string
	About, Rules, Prizes [][]string
	Teams, Participants  int
	AppURL               string
	TakenDown            bool
	TakenDownReason      string
	Gallery              []hackGalleryCard
	Tracks               []hackTrackView
	Results              *hackResultsView
	Sponsors             []hackSponsorView
	FAQ                  []hackFAQ
	Schedule             []hackScheduleView
	Announcements        []hackAnnouncementView
	Deadline             string
	DeadlineLine         string
	VoteStatus           string
	VoteURL              string
}

// renderHackEventPage writes the whole response (status 200, or 410 when TakenDown).
func renderHackEventPage(w http.ResponseWriter, r *http.Request, p hackEventPage) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	nonce := base64.StdEncoding.EncodeToString(b[:])
	r = r.WithContext(context.WithValue(r.Context(), cspNonceKey{}, nonce))

	page, err := assembleHackEventPage(r, p)
	if err != nil {
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	app := strings.TrimRight(p.AppURL, "/")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", fmt.Sprintf(
		"default-src 'none'; script-src 'nonce-%s'; style-src %s 'unsafe-inline'; img-src 'self' %s data:; font-src %s data:; connect-src 'none'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'",
		nonce, app, app, app))
	if p.TakenDown || p.Stage == "draft" {
		w.Header().Set("X-Robots-Tag", "noindex")
	}
	status := http.StatusOK
	if p.TakenDown {
		status = http.StatusGone
	}
	w.WriteHeader(status)
	_, _ = w.Write(stampNonce(r, page))
}

func assembleHackEventPage(r *http.Request, p hackEventPage) ([]byte, error) {
	v := hackEventView{
		Title:           p.Title,
		Tagline:         p.Tagline,
		OrganisedBy:     hackOrganisedBy(p.OrganiserName, p.Organisation),
		DateLine:        hackEventDateLine(p.StartsAt, p.EndsAt, p.TimeZone),
		Status:          hackStageStatus(p.Stage),
		About:           hackTextParas(p.About),
		Rules:           hackTextParas(p.Rules),
		Prizes:          hackTextParas(p.Prizes),
		Teams:           p.Teams,
		Participants:    p.Participants,
		AppURL:          strings.TrimRight(p.AppURL, "/"),
		TakenDown:       p.TakenDown,
		TakenDownReason: p.TakenDownReason,
		Gallery:         p.Gallery,
		Tracks:          p.Tracks,
		Results:         p.Results,
		FAQ:             p.Content.FAQ,
		Announcements:   p.Announcements,
		VoteStatus:      p.VoteStatus,
		VoteURL:         strings.TrimRight(p.AppURL, "/") + "/e/" + p.Slug + "/vote",
	}
	loc, err := time.LoadLocation(p.TimeZone)
	if err != nil {
		loc = time.UTC
	}
	for _, s := range p.Content.Sponsors {
		sponsor := hackSponsorView{Name: s.Name, Tier: s.Tier, URL: s.URL}
		if cleanLogo(s.LogoData) {
			sponsor.Logo = template.URL(s.LogoData)
		}
		v.Sponsors = append(v.Sponsors, sponsor)
	}
	items := append([]hackScheduleItem(nil), p.Content.Schedule...)
	sort.Slice(items, func(i, j int) bool { return items[i].StartAt < items[j].StartAt })
	now := time.Now()
	next := -1
	ongoing := -1
	for i, item := range items {
		start, e1 := time.Parse(time.RFC3339, item.StartAt)
		end, e2 := time.Parse(time.RFC3339, item.EndAt)
		if e1 != nil || e2 != nil {
			continue
		}
		if ongoing < 0 && !now.Before(start) && now.Before(end) {
			ongoing = i
		}
		if next < 0 && now.Before(start) {
			next = i
		}
	}
	for i, item := range items {
		start, e1 := time.Parse(time.RFC3339, item.StartAt)
		end, e2 := time.Parse(time.RFC3339, item.EndAt)
		if e1 != nil || e2 != nil {
			continue
		}
		status := ""
		if i == ongoing {
			status = "Now"
		} else if i == next {
			status = "Next"
		}
		v.Schedule = append(v.Schedule, hackScheduleView{Title: item.Title, Description: item.Description,
			When: start.In(loc).Format("Mon 2 Jan, 15:04") + "–" + end.In(loc).Format("15:04 MST"), Status: status})
	}
	if !p.Deadline.IsZero() && p.Stage != "archived" {
		v.Deadline = p.Deadline.UTC().Format(time.RFC3339)
		v.DeadlineLine = p.Deadline.In(loc).Format("Mon 2 Jan 2006, 15:04 MST")
	}
	var buf bytes.Buffer
	if err := hackEventTmpl.ExecuteTemplate(&buf, "hack-event.html", v); err != nil {
		return nil, err
	}
	return withChrome(buf.Bytes(), chromeDataFor(r, v.AppURL))
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

func hackStageStatus(stage string) string {
	switch stage {
	case "draft":
		return "Not open yet."
	case "open":
		return "Sign-up is open. Ask the organisers for the join link."
	case "building":
		return "Teams are building."
	case "closed":
		return "Submissions are closed."
	case "judging":
		return "Judging is under way."
	case "results":
		return "Results are in."
	case "archived":
		return "This event has ended."
	default:
		return ""
	}
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

func hackEventDateLine(starts, ends time.Time, zone string) string {
	loc, err := time.LoadLocation(zone)
	if err != nil || loc == nil {
		loc = time.UTC
		zone = "UTC"
	} else if zone == "" {
		zone = loc.String()
	}
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
		return start.Format("2 January 2006") + " · " + zone
	}
	if start.IsZero() {
		return end.Format("2 January 2006") + " · " + zone
	}
	return hackDateRange(start, end) + " · " + zone
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
