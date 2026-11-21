package hackhttp

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/model"
	"github.com/vsriram/simple-host/products/hack/app/entries"
	"github.com/vsriram/simple-host/products/hack/app/events"
	hackmodel "github.com/vsriram/simple-host/products/hack/model"
)

// hackGallerySlug is one label: lowercase letters, digits and hyphens, not
// starting or ending with a hyphen.
func hackGallerySlug(s string) bool {
	return entries.ValidVotingLabel(s, 30)
}

// hackScreenshotPath is the team slug of /screenshots/<slug> when the path is
// exactly one team slug.
func hackScreenshotPath(p string) (string, bool) {
	const prefix = "/screenshots/"
	if !strings.HasPrefix(p, prefix) {
		return "", false
	}
	slug := p[len(prefix):]
	if !hackGallerySlug(slug) {
		return "", false
	}
	return slug, true
}

func hackEventLabel(s string) bool {
	return entries.ValidVotingLabel(s, 63)
}

func hackGalleryImageType(s string) bool {
	switch s {
	case "image/png", "image/jpeg", "image/webp":
		return true
	default:
		return false
	}
}

// hackGalleryPublic: the event page may list projects, and the event host may
// serve their screenshots.
func hackGalleryPublic(ev hackmodel.Event, ready func(string) bool) bool {
	return ev.GalleryOpen && ev.Stage != "draft" && !ev.TakenDown() && ready != nil && ready(ev.Slug)
}

// HackEventPage is the SiteHandler hook that renders <event>.<SITE_DOMAIN>/
// (hack_mode.go): user is the host's account; false when it holds no event.
func HackEventPage(database *sql.DB, appURL string, teamSiteURL func(eventSlug, teamSlug string) string, ready func(eventSlug string) bool, pages *HackEventPages) func(w http.ResponseWriter, r *http.Request, user model.User) bool {
	return func(w http.ResponseWriter, r *http.Request, user model.User) bool {
		ev, err := events.New(database).EventByAccount(r.Context(), user.ID)
		if errors.Is(err, sql.ErrNoRows) {
			return false
		}
		if err != nil {
			log.Printf("event page %s: %v", user.ID, err)
			http.Error(w, "Something went wrong. Try again in a minute.", http.StatusInternalServerError)
			return true
		}
		src := hackEventPageSource{database: database, appURL: strings.TrimRight(appURL, "/"), teamSiteURL: teamSiteURL, galleryPublic: hackGalleryPublic(ev, ready)}
		p, err := src.load(r.Context(), ev)
		if err != nil {
			httpx.WriteInternal(w)
			return true
		}
		renderHackEventPage(w, r, p, pages)
		return true
	}
}

// hackEventPageSource is what the public event page is read with.
type hackEventPageSource struct {
	database      *sql.DB
	appURL        string // no trailing slash
	teamSiteURL   func(eventSlug, teamSlug string) string
	galleryPublic bool
}

// load reads everything the page shows.
func (s hackEventPageSource) load(ctx context.Context, ev hackmodel.Event) (hackEventPage, error) {
	p := hackEventPageBasics(ev, s.appURL)
	if ev.SubmissionsPublic {
		var err error
		if p.Submissions, err = visibleSubmissions(ctx, s.database, ev, s.teamSiteURL, s.appURL+"/e/"+ev.Slug+"/submissions"); err != nil {
			return p, err
		}
	}
	evs, ent := events.New(s.database), entries.New(s.database, nil)
	logErr := func(part string, err error) bool {
		if err != nil {
			log.Printf("event page %s: %s: %v", ev.Slug, part, err)
		}
		return err == nil
	}
	participants, teams, _, err := evs.CountEventMembersLookup(ctx, ev.ID)
	logErr("counts", err)
	p.Participants, p.Teams = participants, teams
	if tracks, err := evs.EventTracks(ctx, ev.ID); logErr("tracks", err) {
		p.Tracks = hackTrackViews(tracks)
	}
	if content, err := events.ReadEventContent(ctx, s.database, ev.ID); logErr("content", err) {
		p.Content = content
	}
	if anns, err := evs.EventAnnouncements(ctx, ev.ID); logErr("announcements", err) {
		p.Announcements = hackAnnouncementViews(anns)
	}
	now := time.Now()
	fixedIn := hackFixedInputs{Event: ev, Now: now}
	if voting, err := ent.VotingSettings(ctx, ev.ID); err == nil {
		p.VoteStatus, fixedIn.Voting = votingStatus(ev, voting, now), &voting
	}
	res, err := ent.EventResults(ctx, ev.ID)
	if logErr("results", err) {
		fixedIn.Results = res
	}
	p.Fixed = hackFixedSchedule(fixedIn)
	if res.Published && !ev.TakenDown() {
		p.Results = hackResultsFromSnapshot(res, ev.PublicScores, ev.PublicRanks, ev.OverallPrizes)
	}
	if !s.galleryPublic && p.Results == nil {
		return p, nil
	}
	cards, err := evs.GalleryCards(ctx, ev.ID, ev.AccountID)
	logErr("gallery", err)
	s.placeTeamSites(&p, ev.Slug, cards)
	return p, nil
}

// hackEventPageBasics is the page's fields that come straight from the event.
func hackEventPageBasics(ev hackmodel.Event, appURL string) hackEventPage {
	p := hackEventPage{
		Slug: ev.Slug, Title: ev.Title, Tagline: ev.Tagline, About: ev.About, Rules: ev.Rules, Prizes: ev.Prizes,
		OrganiserName: ev.OrganiserName, Organisation: ev.Organisation,
		Stage: ev.Stage, TimeZone: ev.TimeZone, AppURL: appURL,
		TakenDown: ev.TakenDown(), TakenDownReason: ev.TakenDownReason,
		Color: hackEventAccent(ev), Theme: ev.Theme, IconURL: appURL + "/v1/hack/events/" + ev.Slug + "/icon",
		StartsAt: hackSetTime(ev.StartsAt), EndsAt: hackSetTime(ev.EndsAt), Deadline: hackSetTime(ev.SubmissionDeadline),
	}
	if events.ApplicationsOpenNow(ev.ApplicationsOpen, ev.DirectoryListed, ev.Stage, ev.TakenDown()) {
		p.ApplyURL = appURL + "/e/" + ev.Slug + "/apply"
	}
	return p
}

// hackSetTime is t's time, or zero when it is not set.
func hackSetTime(t sql.NullTime) time.Time {
	if !t.Valid {
		return time.Time{}
	}
	return t.Time
}

// hackTrackViews are the tracks as the page lists them.
func hackTrackViews(tracks []hackmodel.EventTrack) []hackTrackView {
	var out []hackTrackView
	for _, t := range tracks {
		out = append(out, hackTrackView{Name: t.Name, Challenge: t.Challenge, Prize: t.Prize})
	}
	return out
}

// hackAnnouncementViews are the announcements as the page lists them.
func hackAnnouncementViews(anns []hackmodel.EventAnnouncement) []hackAnnouncementView {
	var out []hackAnnouncementView
	for _, a := range anns {
		out = append(out, hackAnnouncementView{Title: a.Title, Body: a.Body, CreatedAt: a.CreatedAt, Pinned: a.Pinned, BodyHTML: a.BodyHTML})
	}
	return out
}

// placeTeamSites links the gallery and the results to the team sites that are
// live right now.
func (s hackEventPageSource) placeTeamSites(p *hackEventPage, eventSlug string, cards []hackmodel.GalleryCard) {
	shotBase := s.appURL + "/e/" + eventSlug
	if s.galleryPublic {
		p.Gallery = hackGalleryCards(eventSlug, cards, s.teamSiteURL)
		for i := range p.Gallery {
			if p.Gallery[i].Shot != "" {
				p.Gallery[i].Shot = shotBase + p.Gallery[i].Shot
			}
		}
	}
	if p.Results == nil {
		return
	}
	byTeam := make(map[string]hackmodel.GalleryCard, len(cards))
	for _, c := range cards {
		byTeam[c.ID] = c
	}
	inResults := make(map[string]bool, len(p.Results.Rows))
	for i := range p.Results.Rows {
		row := &p.Results.Rows[i]
		inResults[row.TeamID] = true
		c, live := byTeam[row.TeamID]
		if live {
			row.URL = hackGalleryLink(s.teamSiteURL, eventSlug, c.Slug)
		}
		if row.Card == nil {
			continue
		}
		row.Card.SiteURL = row.URL
		if live && s.galleryPublic && c.HasScreenshot {
			row.Shot = shotBase + "/screenshots/" + c.Slug
		}
	}
	for i := range p.Gallery {
		if inResults[p.Gallery[i].TeamID] {
			p.Gallery[i].ResultAnchor = "#result-" + p.Gallery[i].TeamID
		}
	}
}

// hackResultsFromSnapshot is the public page's view of a published snapshot:
// Winners-only shows rank 1, places with an overall prize, and track winners; a
// full ranking shows every ranked team.
func hackResultsFromSnapshot(res entries.EventResultsRow, publicScores, publicRanks bool, prizes []string) *hackResultsView {
	var rows []map[string]any
	if err := json.Unmarshal(res.Snapshot, &rows); err != nil {
		log.Printf("event page: results snapshot: %v", err)
		return nil
	}
	v := &hackResultsView{FullRanking: res.FullRanking}
	for _, row := range rows {
		rank, ok := row["rank"].(float64)
		if !ok {
			continue // unscored team (total is null): never shown publicly
		}
		trackWinner, _ := row["track_winner"].(bool)
		if res.FullRanking || entries.ShownInWinnersOnly(int(rank), trackWinner, prizes) {
			v.Rows = append(v.Rows, hackSnapshotRow(row, int(rank), publicScores, publicRanks, prizes))
		}
	}
	if !publicRanks {
		sort.Slice(v.Rows, func(i, j int) bool { return v.Rows[i].TeamName < v.Rows[j].TeamName })
	}
	return v
}

// hackSnapshotRow is one ranked snapshot row as the public sees it.
func hackSnapshotRow(row map[string]any, rank int, publicScores, publicRanks bool, prizes []string) hackResultsRow {
	view := hackResultsRow{Overall: rank == 1, OverallPrize: entries.OverallPrizeLabel(rank, prizes)}
	view.TeamID, _ = row["team_id"].(string)
	view.TeamName, _ = row["team_name"].(string)
	view.TrackWinner, _ = row["track_winner"].(bool)
	if publicRanks {
		view.Rank = rank
		view.Tied, _ = row["tied"].(bool)
	}
	if track, ok := row["track"].(map[string]any); ok {
		view.TrackName, _ = track["name"].(string)
		view.TrackPrize, _ = track["prize"].(string)
	}
	if publicScores {
		if score, ok := row["total"].(float64); ok {
			view.Score = strconv.FormatFloat(score, 'f', 2, 64)
		}
		view.ScoreMode, _ = row["deciding_mode"].(string)
	}
	if raw, ok := row["entry"]; ok && raw != nil {
		var c hackResultCard
		if b, err := json.Marshal(raw); err == nil && json.Unmarshal(b, &c) == nil {
			view.Card = &c
		}
	}
	return view
}

func hackGalleryCards(eventSlug string, cards []hackmodel.GalleryCard, teamSiteURL func(string, string) string) []hackGalleryCard {
	var out []hackGalleryCard
	for _, c := range cards {
		link := hackGalleryLink(teamSiteURL, eventSlug, c.Slug)
		if link == "" {
			continue
		}
		title := c.Title
		if title == "" {
			title = c.Name
		}
		card := hackGalleryCard{Title: title, Tagline: c.Tagline, URL: link, Tools: c.Tools, TeamID: c.ID}
		if rs := []rune(strings.TrimSpace(title)); len(rs) > 0 {
			card.Initial = strings.ToUpper(string(rs[0]))
		}
		if title != c.Name {
			card.Team = c.Name
		}
		if c.HasScreenshot {
			card.Shot = "/screenshots/" + c.Slug
		}
		out = append(out, card)
	}
	return out
}

// hackGalleryLink is the team site URL when both slugs are single labels and
// the builder returned an http(s) URL whose host starts with those labels —
// either the two-level <team>.<event>.
func hackGalleryLink(teamSiteURL func(string, string) string, eventSlug, teamSlug string) string {
	if teamSiteURL == nil || !hackEventLabel(eventSlug) || !hackGallerySlug(teamSlug) {
		return ""
	}
	raw := teamSiteURL(eventSlug, teamSlug)
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil {
		return ""
	}
	host := strings.ToLower(u.Hostname())
	if !strings.HasPrefix(host, teamSlug+"."+eventSlug+".") && !strings.HasPrefix(host, teamSlug+"--"+eventSlug+".") {
		return ""
	}
	return raw
}

// HackScreenshot is the SiteHandler hook for GET/HEAD /screenshots/<team> on
// the event host.
func HackScreenshot(database *sql.DB, ready func(string) bool, owners ...*HackHandler) func(w http.ResponseWriter, r *http.Request, user model.User, team string) bool {
	// Public and unauthenticated, each hit reads up to 2 MB from the
	// database: metered per network address.
	perIP := screenshotLimiter(owners)
	return func(w http.ResponseWriter, r *http.Request, user model.User, team string) bool {
		if !hackGallerySlug(team) {
			return false
		}
		if !perIP.AllowHTTP(w, httpx.ClientIP(r)) {
			httpx.TooManyRequests(w)
			return true
		}
		ev, err := events.New(database).EventByAccount(r.Context(), user.ID)
		if err != nil {
			if !errors.Is(err, sql.ErrNoRows) {
				log.Printf("event screenshot %s: %v", user.ID, err)
			}
			return false
		}
		if !hackGalleryPublic(ev, ready) {
			return false
		}
		data, ctype, err := events.New(database).GalleryScreenshotLookup(r.Context(), ev.ID, ev.AccountID, team)
		if err != nil {
			if !errors.Is(err, sql.ErrNoRows) {
				log.Printf("event screenshot %s/%s: %v", ev.Slug, team, err)
			}
			return false
		}
		if !hackGalleryImageType(ctype) || len(data) == 0 {
			return false
		}
		writeHackScreenshot(w, r, data, ctype)
		return true
	}
}

func writeHackScreenshot(w http.ResponseWriter, r *http.Request, data []byte, contentType string) {
	writeHackScreenshotPolicy(w, r, data, contentType, false)
}

func writeHackScreenshotPolicy(w http.ResponseWriter, r *http.Request, data []byte, contentType string, crossOrigin bool) {
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	// Short, and revalidated by its hash: a take-down or a closed gallery
	// shows within a minute.
	w.Header().Set("Cache-Control", "public, max-age=60")
	w.Header().Set("Cross-Origin-Resource-Policy", "same-origin")
	if crossOrigin {
		// Public submissions: custom event websites load these from their own
		// host, and every load revalidates so switching the setting off, an
		// exclusion or a stage move back takes effect at once.
		w.Header().Set("Cross-Origin-Resource-Policy", "cross-origin")
		w.Header().Set("Cache-Control", "no-cache")
	}
	sum := sha256.Sum256(data)
	etag := `"` + hex.EncodeToString(sum[:12]) + `"`
	w.Header().Set("ETag", etag)
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write(data)
	}
}
