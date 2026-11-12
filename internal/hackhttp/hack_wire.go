package hackhttp

import (
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

	"github.com/vsriram/simple-host/internal/app/hack/entries"
	"github.com/vsriram/simple-host/internal/app/hack/events"
	"github.com/vsriram/simple-host/internal/httpx"
	"github.com/vsriram/simple-host/internal/model"
)

// hackGallerySlug is one label: lowercase letters, digits and hyphens, not
// starting or ending with a hyphen. A team slug is at most 30 characters; an
// event slug used inside a team-site URL is at most one DNS label.
func hackGallerySlug(s string) bool {
	return entries.ValidVotingLabel(s, 30)
}

// hackScreenshotPath is the team slug of /screenshots/<slug> when the path is
// exactly one team slug. Extra segments, traversal and any other shape are not
// this route.
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
// serve their screenshots. ready is false until the event's team addresses
// have a certificate (and asking reports that, the same as a deploy).
func hackGalleryPublic(ev model.Event, ready func(string) bool) bool {
	return ev.GalleryOpen && ev.Stage != "draft" && !ev.TakenDown() && ready != nil && ready(ev.Slug)
}

// HackEventPage is the SiteHandler hook that renders <event>.<SITE_DOMAIN>/
// (hack_mode.go): user is the host's account; false when it holds no event.
// teamSiteURL and ready are SiteHandler.TeamSiteURL and TeamSitesReady.
// pages is the hack-event.html template cache (HackEventPages), built once
// by the caller and passed in explicitly (cleanup 3.2 slice 4).
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
		participants, teams, _, err := events.New(database).CountEventMembersLookup(r.Context(), ev.ID)
		if err != nil {
			log.Printf("event page %s: counts: %v", ev.Slug, err)
		}
		p := hackEventPage{
			Slug: ev.Slug, Title: ev.Title, Tagline: ev.Tagline, About: ev.About, Rules: ev.Rules, Prizes: ev.Prizes,
			OrganiserName: ev.OrganiserName, Organisation: ev.Organisation,
			Stage: ev.Stage, TimeZone: ev.TimeZone,
			Participants: participants, Teams: teams,
			AppURL:    appURL,
			TakenDown: ev.TakenDown(), TakenDownReason: ev.TakenDownReason,
		}
		if events.ApplicationsOpenNow(ev.ApplicationsOpen, ev.DirectoryListed, ev.Stage, ev.TakenDown()) {
			p.ApplyURL = strings.TrimRight(appURL, "/") + "/e/" + ev.Slug + "/apply"
		}
		if ev.SubmissionsPublic {
			p.Submissions, err = visibleSubmissions(r.Context(), database, ev, teamSiteURL, strings.TrimRight(appURL, "/")+"/e/"+ev.Slug+"/submissions")
			if err != nil {
				httpx.WriteInternal(w)
				return true
			}
		}
		p.Color = hackEventAccent(ev)
		p.Theme = ev.Theme
		p.IconURL = strings.TrimRight(appURL, "/") + "/v1/hack/events/" + ev.Slug + "/icon"
		if tracks, terr := events.New(database).EventTracks(r.Context(), ev.ID); terr == nil {
			for _, track := range tracks {
				p.Tracks = append(p.Tracks, hackTrackView{Name: track.Name, Challenge: track.Challenge, Prize: track.Prize})
			}
		} else {
			log.Printf("event page %s: tracks: %v", ev.Slug, terr)
		}
		if ev.StartsAt.Valid {
			p.StartsAt = ev.StartsAt.Time
		}
		if ev.EndsAt.Valid {
			p.EndsAt = ev.EndsAt.Time
		}
		if ev.SubmissionDeadline.Valid {
			p.Deadline = ev.SubmissionDeadline.Time
		}
		if content, cerr := readHackContent(r.Context(), database, ev.ID); cerr != nil {
			log.Printf("event page %s: content: %v", ev.Slug, cerr)
		} else {
			p.Content = content
		}
		if announcements, aerr := events.New(database).EventAnnouncements(r.Context(), ev.ID); aerr != nil {
			log.Printf("event page %s: announcements: %v", ev.Slug, aerr)
		} else {
			for _, announcement := range announcements {
				p.Announcements = append(p.Announcements, hackAnnouncementView{
					Title: announcement.Title, Body: announcement.Body,
					CreatedAt: announcement.CreatedAt, Pinned: announcement.Pinned, BodyHTML: announcement.BodyHTML,
				})
			}
		}
		fixedIn := hackFixedInputs{Event: ev, Now: time.Now()}
		if voting, verr := entries.New(database, nil).VotingSettings(r.Context(), ev.ID); verr == nil {
			p.VoteStatus = votingStatus(ev, voting, time.Now())
			fixedIn.Voting = &voting
		}
		if res, rerr := entries.New(database, nil).EventResults(r.Context(), ev.ID); rerr == nil {
			fixedIn.Results = res
		}
		p.Fixed = hackFixedSchedule(fixedIn)
		if hackGalleryPublic(ev, ready) {
			cards, gerr := events.New(database).GalleryCards(r.Context(), ev.ID, ev.AccountID)
			if gerr != nil {
				log.Printf("event page %s: gallery: %v", ev.Slug, gerr)
			} else {
				p.Gallery = hackGalleryCards(ev.Slug, cards, teamSiteURL)
				for i := range p.Gallery {
					if p.Gallery[i].Shot != "" {
						p.Gallery[i].Shot = strings.TrimRight(appURL, "/") + "/e/" + ev.Slug + p.Gallery[i].Shot
					}
				}
			}
		}
		if !ev.TakenDown() {
			if res, rerr := entries.New(database, nil).EventResults(r.Context(), ev.ID); rerr != nil {
				log.Printf("event page %s: results: %v", ev.Slug, rerr)
			} else if res.Published {
				p.Results = hackResultsFromSnapshot(res, ev.PublicScores, ev.PublicRanks, ev.OverallPrizes)
				if p.Results != nil {
					galleryPublic := hackGalleryPublic(ev, ready)
					cards, err := events.New(database).GalleryCards(r.Context(), ev.ID, ev.AccountID)
					if err != nil {
						log.Printf("event page %s: result sites: %v", ev.Slug, err)
					}
					resultTeamIDs := make(map[string]bool, len(p.Results.Rows))
					for i := range p.Results.Rows {
						resultTeamIDs[p.Results.Rows[i].TeamID] = true
						// Default to "no live site" every render: a card's
						// SiteURL/Shot reflect this exact query, run fresh on
						// every page load, not whatever the snapshot captured
						// at publish time -- a team taken down or whose site
						// went offline since publish drops its link and shot
						// here without needing a republish (build-plan §4.6
						// render-time filtering, extended to the card).
						if p.Results.Rows[i].Card != nil {
							p.Results.Rows[i].Card.SiteURL = ""
						}
						for _, card := range cards {
							if card.ID != p.Results.Rows[i].TeamID {
								continue
							}
							link := hackGalleryLink(teamSiteURL, ev.Slug, card.Slug)
							p.Results.Rows[i].URL = link
							if p.Results.Rows[i].Card != nil {
								p.Results.Rows[i].Card.SiteURL = link
								if galleryPublic && card.HasScreenshot {
									p.Results.Rows[i].Shot = strings.TrimRight(appURL, "/") + "/e/" + ev.Slug + "/screenshots/" + card.Slug
								}
							}
						}
					}
					for i := range p.Gallery {
						if resultTeamIDs[p.Gallery[i].TeamID] {
							p.Gallery[i].ResultAnchor = "#result-" + p.Gallery[i].TeamID
						}
					}
				}
			}
		}
		renderHackEventPage(w, r, p, pages)
		return true
	}
}

// hackResultsFromSnapshot is the public page's view of a published snapshot:
// Winners-only shows rank 1, places with an overall prize, and track winners;
// a full ranking shows every ranked team. Public score and rank visibility
// are applied here. Prize labels use the snapshot rank even when it is hidden.
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
		if !res.FullRanking && !shownInWinnersOnly(int(rank), trackWinner, prizes) {
			continue
		}
		name, _ := row["team_name"].(string)
		tied, _ := row["tied"].(bool)
		teamID, _ := row["team_id"].(string)
		view := hackResultsRow{TeamID: teamID, TeamName: name, TrackWinner: trackWinner, Overall: rank == 1, OverallPrize: overallPrizeLabel(int(rank), prizes)}
		if publicRanks {
			view.Rank = int(rank)
			view.Tied = tied
		}
		if track, ok := row["track"].(map[string]any); ok {
			view.TrackName, _ = track["name"].(string)
			view.TrackPrize, _ = track["prize"].(string)
		}
		if publicScores {
			// #8: this is also the public JSON API's score field
			// (hack_public_json.go) -- BUILD.md goal 4 says that API does not
			// change, so it keeps exactly the pre-231f769d full precision
			// (two decimals, untrimmed: "81.25", "100.00"). hackScoreText's
			// one-decimal rounding belongs to the HTML template's display
			// only, applied separately in buildHackResultsBlock
			// (hack_eventpage.go), never baked into this shared string.
			if score, ok := row["total"].(float64); ok {
				view.Score = strconv.FormatFloat(score, 'f', 2, 64)
			}
			view.ScoreMode, _ = row["deciding_mode"].(string)
		}
		// build-plan §4.6: a card is copied only into a row this function
		// already decided to emit, so a winners-only view never carries a
		// card for a team it does not otherwise show, even though the
		// snapshot holds every ranked team's card. A snapshot published
		// before slice 3 has no "entry" key on any row, which round-trips to
		// Card staying nil here -- "old snapshots must keep rendering, no
		// card".
		if raw, ok := row["entry"]; ok && raw != nil {
			if b, merr := json.Marshal(raw); merr == nil {
				var c hackResultCard
				if uerr := json.Unmarshal(b, &c); uerr == nil {
					view.Card = &c
				}
			}
		}
		v.Rows = append(v.Rows, view)
	}
	if !publicRanks {
		sort.Slice(v.Rows, func(i, j int) bool { return v.Rows[i].TeamName < v.Rows[j].TeamName })
	}
	return v
}

func hackGalleryCards(eventSlug string, cards []model.GalleryCard, teamSiteURL func(string, string) string) []hackGalleryCard {
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
// either the two-level <team>.<event>. or, under TEAM_SITE_FLAT_HOSTS, the
// flat <team>--<event>. single label. Accepting either shape here, rather
// than asking the builder which mode it is in, keeps this a pure sanity
// check: whatever teamSiteURL built, it must still visibly be this team's
// site before the gallery links to it.
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
// the event host. It writes the image only while that team is on the public
// gallery; otherwise false, and the caller renders the 404.
func HackScreenshot(database *sql.DB, ready func(string) bool, owners ...*HackHandler) func(w http.ResponseWriter, r *http.Request, user model.User, team string) bool {
	// Public and unauthenticated, each hit reads up to 2 MB from the
	// database: metered per network address.
	perIP := httpx.NewRateLimiter(120, 5)
	if len(owners) > 0 {
		owners[0].addLimiter("http.hack.screenshots", perIP)
	}
	return func(w http.ResponseWriter, r *http.Request, user model.User, team string) bool {
		if !hackGallerySlug(team) {
			return false
		}
		if !perIP.AllowHTTP(w, httpx.ClientIP(r)) {
			http.Error(w, "Too many requests. Try again in a minute.", http.StatusTooManyRequests)
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

func overallPrizeLabel(rank int, prizes []string) string {
	if rank < 1 || rank > 3 || rank > len(prizes) || prizes[rank-1] == "" {
		return ""
	}
	return []string{"1st", "2nd", "3rd"}[rank-1] + " · " + prizes[rank-1]
}
func shownInWinnersOnly(rank int, trackWinner bool, prizes []string) bool {
	return rank > 0 && (rank == 1 || trackWinner || overallPrizeLabel(rank, prizes) != "")
}
