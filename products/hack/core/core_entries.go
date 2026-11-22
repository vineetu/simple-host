package core

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"net/url"
	"strings"
	"time"

	hostmodel "github.com/vsriram/simple-host/host/model"
	entryapp "github.com/vsriram/simple-host/products/hack/app/entries"

	entries "github.com/vsriram/simple-host/products/hack/app/entries"
	"github.com/vsriram/simple-host/products/hack/app/events"
	"github.com/vsriram/simple-host/products/hack/model"
)

func (h *Hack) JudgeSiteLinkGate(ctx context.Context, a Access) (func(teamID string) bool, error) {
	return h.Entries.JudgeSiteLinks(ctx, a.Event, a.User.ID, a.Admin || a.Member.Role == "organiser", h.ScoringStage(ctx, a.Event))
}

// RedactEntrySiteLinks drops an entry list item's preview and pinned links:
// the site block keeps its public address and host but can't be framed.
func RedactEntrySiteLinks(item map[string]any) {
	item["pinned_url"] = nil
	if site, ok := item["site"].(map[string]any); ok {
		site["preview_url"] = nil
		if site["frameable"] == true {
			site["frameable"] = false
			site["not_frameable_why"] = "not_ready"
		}
	}
}

func (h *Hack) EntryTrack(ctx context.Context, eventID, teamID string) (bool, *model.EventTrack, error) {
	tracks, err := h.Events.EventTracks(ctx, eventID)
	if err != nil {
		return false, nil, err
	}
	track, err := h.Events.TeamTrackLookup(ctx, teamID)
	return len(tracks) > 0, track, err
}

func EntryCardJSON(e model.EventEntry) map[string]any {
	var updated any
	if e.Exists {
		updated = e.UpdatedAt.UTC().Format(time.RFC3339Nano)
	}
	return map[string]any{
		"title": e.Title, "tagline": e.Tagline, "video_url": e.VideoURL, "code_url": e.CodeURL,
		"has_screenshot": e.HasScreenshot, "updated_at": updated,
		"problem": e.Problem, "audience": e.Audience, "tools": NonNilStrings(e.Tools), "maturity": e.Maturity,
	}
}

func TrackJSON(track *model.EventTrack, withID bool) any {
	if track == nil {
		return nil
	}
	out := map[string]any{"slug": track.Slug, "name": track.Name}
	if withID {
		out["id"] = track.ID
	}
	return out
}

func OrNil[T comparable](v T) any {
	var zero T
	if v == zero {
		return nil
	}
	return v
}

func NonNilStrings(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// EntryListItem is one team's entry as organisers and judges see it, with the video embed and whether the judge screen may frame the site.
func (h *Hack) EntryListItem(ctx context.Context, ev model.Event, team model.EventTeam, e model.EventEntry, fields map[string]string, hasTracks bool, tl TeamList) (map[string]any, error) {
	st := tl.States[team.ID]
	site := tl.Site(team.Slug)
	track, err := h.Events.TeamTrackLookup(ctx, team.ID)
	if err != nil {
		return nil, err
	}
	check := entries.Evaluate(fields, e, entries.TeamSiteLive(site, st), hasTracks, track)
	var pinned, pinnedURL any
	if st.PinnedVersion.Valid {
		pinned = st.PinnedVersion.Int64
		if u, ok := h.Links.pinnedLink(ev, team, tl, int(st.PinnedVersion.Int64)); ok {
			pinnedURL = u
		}
	}
	siteView, err := h.entrySiteView(ctx, ev, team, tl, pinnedURL)
	if err != nil {
		return nil, err
	}
	var excludedAt any
	if e.ExcludedAt.Valid {
		excludedAt = RFC3339(e.ExcludedAt)
	}
	item := EntryCardJSON(e)
	for k, v := range map[string]any{
		"team":            map[string]any{"id": team.ID, "slug": team.Slug, "name": team.Name},
		"description":     e.Description,
		"track":           TrackJSON(track, false),
		"status":          e.Status(),
		"submitted_at":    RFC3339(e.SubmittedAt),
		"submit_auto":     e.SubmitAuto,
		"incomplete":      e.Status() == "not_submitted",
		"excluded":        e.ExcludedAt.Valid,
		"excluded_at":     excludedAt,
		"complete":        check.Complete,
		"missing":         check.Missing,
		"video":           entryVideoJSON(e.VideoURL),
		"site":            siteView,
		"site_url":        h.Links.TeamSiteURL(ev.Slug, team.Slug),
		"site_exists":     site != nil,
		"site_taken_down": st.SiteTakenDown || st.EventTakenDown,
		"deadline":        RFC3339(st.Deadline),
		"frozen":          st.Frozen(),
		"pinned_version":  pinned,
		"pinned_url":      pinnedURL,
	} {
		item[k] = v
	}
	return item, nil
}

func entryVideoJSON(raw string) any {
	if raw == "" {
		return nil
	}
	return map[string]any{"url": raw, "host": URLHost(raw), "embed_url": OrNil(VideoEmbed(raw))}
}

func (h *Hack) entrySiteView(ctx context.Context, ev model.Event, team model.EventTeam, tl TeamList, pinnedURL any) (any, error) {
	site := tl.Site(team.Slug)
	if site == nil {
		return nil, nil
	}
	liveURL := h.Links.TeamSiteURL(ev.Slug, team.Slug)
	preview, _ := pinnedURL.(string)
	if preview == "" && site.ActiveVersion > 0 {
		if u, ok := h.Links.pinnedLink(ev, team, tl, site.ActiveVersion); ok {
			preview = u
		}
	}
	why, err := h.notFrameableWhy(ctx, site, tl.States[team.ID], tl.Ready && preview != "")
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	// A preview link carries no passcode, viewer or offline check of its own (servePreview checks only the token), so a site that can't be framed gets no preview link at all.
	var previewJSON any
	if why == "" {
		previewJSON = OrNil(preview)
	}
	return map[string]any{
		"url":               liveURL,
		"preview_url":       previewJSON,
		"host":              URLHost(liveURL),
		"frameable":         why == "",
		"not_frameable_why": OrNil(why),
	}, nil
}

// notFrameableWhy names the first reason the site can't be framed ("" when it can).
func (h *Hack) notFrameableWhy(ctx context.Context, site *hostmodel.Site, st model.TeamWriteState, previewReady bool) (string, error) {
	switch {
	case st.SiteTakenDown || st.EventTakenDown || site.Suspended():
		return "taken_down", nil
	case site.Offline:
		return "offline", nil
	case !previewReady:
		return "not_ready", nil
	}
	row, err := h.Events.SitePasscode(ctx, site.ID)
	switch {
	case err != nil:
		return "", err
	case row.Offline:
		return "offline", nil
	case row.Enc != nil:
		return "passcode", nil
	case row.NamedViewers:
		return "viewers", nil
	}
	return "", nil
}

func URLHost(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return u.Host
}

// SiteLiveCheck reports whether a team's site is live right now, loading
// the event's team list once, on first use.
func (h *Links) siteLiveCheck(ctx context.Context, ev model.Event) func(teamID, teamSlug string) (bool, error) {
	var tl *TeamList
	return func(teamID, teamSlug string) (bool, error) {
		if tl == nil {
			loaded, err := h.LoadTeamList(ctx, ev)
			if err != nil {
				return false, err
			}
			tl = &loaded
		}
		return entryapp.TeamSiteLive(tl.Site(teamSlug), tl.States[teamID]), nil
	}
}

// ResultSiteURL answers a ranked team's site address for its results card,
// "" unless the site is live right now (a passcode or named-viewer site
// still gets its public address).
func (h *Links) ResultSiteURL(ctx context.Context, ev model.Event) func(teamID, teamSlug string) (string, error) {
	live := h.siteLiveCheck(ctx, ev)
	return func(teamID, teamSlug string) (string, error) {
		ok, err := live(teamID, teamSlug)
		if !ok || err != nil {
			return "", err
		}
		return h.TeamSiteURL(ev.Slug, teamSlug), nil
	}
}

// LiveResultSites maps team id to the public address of every team site live right now, so /results and /public drop a card's site link once a site is taken down or goes offline after publish, without a republish.
func (h *Links) LiveResultSites(ctx context.Context, svc *events.Service, ev model.Event) map[string]string {
	out := map[string]string{}
	if h.Hooks == nil {
		return out
	}
	cards, err := svc.GalleryCards(ctx, ev.ID, ev.AccountID)
	if err != nil {
		log.Printf("hack: result sites %s: %v", ev.Slug, err)
		return out
	}
	for _, c := range cards {
		if link := GalleryLink(h.Hooks.TeamSiteURL, ev.Slug, c.Slug); link != "" {
			out[c.ID] = link
		}
	}
	return out
}

func (h *Links) SubmissionSiteURL() func(string, string) string {
	if h.Hooks == nil {
		return nil
	}
	return h.Hooks.TeamSiteURL
}

// GallerySlug is one label: lowercase letters, digits and hyphens, not
// starting or ending with a hyphen.
func GallerySlug(s string) bool {
	return entries.ValidVotingLabel(s, 30)
}

func eventLabel(s string) bool {
	return entries.ValidVotingLabel(s, 63)
}

// GalleryLink is the team site URL when both slugs are single labels and
// the builder returned an http(s) URL whose host starts with those labels —
// either the two-level <team>.<event>.
func GalleryLink(teamSiteURL func(string, string) string, eventSlug, teamSlug string) string {
	if teamSiteURL == nil || !eventLabel(eventSlug) || !GallerySlug(teamSlug) {
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
