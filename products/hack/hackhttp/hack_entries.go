package hackhttp

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"log"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/vsriram/simple-host/host/httpx"
	hostmodel "github.com/vsriram/simple-host/host/model"
	entries "github.com/vsriram/simple-host/products/hack/app/entries"
	"github.com/vsriram/simple-host/products/hack/model"
)

// Entries: the text, links and screenshot a team submits, kept apart from the team site. A participant reads and writes only their own team's entry; organisers and judges read every team's entry and screenshots.

const hackScreenshotMax = 2 << 20

const hackScreenshotMaxSide = 8000

func (h *participationHTTP) getEntry(w http.ResponseWriter, r *http.Request) {
	a, ok := loadMember(w, r, h.members, r.PathValue("slug"), false, "participant")
	if !ok {
		return
	}
	team, ok := participantTeam(w, r, h.teams, a)
	if !ok {
		return
	}
	h.writeOwnEntry(w, r, a.event, team.ID, a.user.ID)
}

func (h *participationHTTP) putEntry(w http.ResponseWriter, r *http.Request) {
	a, ok := loadMember(w, r, h.members, r.PathValue("slug"), true, "participant")
	if !ok {
		return
	}
	if !h.entryWrites.AllowHTTP(w, a.user.ID) {
		httpx.TooManyRequests(w)
		return
	}
	if _, ok := participantTeam(w, r, h.teams, a); !ok {
		return
	}
	var in struct {
		UpdatedAt   *string   `json:"updated_at"`
		Title       *string   `json:"title"`
		Tagline     *string   `json:"tagline"`
		Description *string   `json:"description"`
		VideoURL    *string   `json:"video_url"`
		CodeURL     *string   `json:"code_url"`
		Problem     *string   `json:"problem"`
		Audience    *string   `json:"audience"`
		Tools       *[]string `json:"tools"`
		Maturity    *string   `json:"maturity"`
		Track       *string   `json:"track"`
	}
	if !httpx.DecodeJSON(w, r, hackMaxBody, &in) {
		return
	}
	req, err := entries.TextWrite(entries.TextInput(in))
	if err != nil {
		writeHackError(w, err)
		return
	}
	teamID, ok := h.entryWrite(w, r, a, req)
	if !ok {
		return
	}
	h.writeOwnEntry(w, r, a.event, teamID, a.user.ID)
}

// submitEntry moves the entry from draft to submitted, enforced against the event's rules.
func (h *participationHTTP) submitEntry(w http.ResponseWriter, r *http.Request) {
	a, ok := loadMember(w, r, h.members, r.PathValue("slug"), true, "participant")
	if !ok {
		return
	}
	if _, ok := participantTeam(w, r, h.teams, a); !ok {
		return
	}
	// The body is optional: a bodyless POST submits unconditionally.
	var req struct {
		UpdatedAt *string `json:"updated_at"`
	}
	if r.ContentLength != 0 {
		raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, hackMaxBody))
		if err != nil {
			httpx.WriteJSON(w, http.StatusBadRequest, httpx.ErrorResponse{Error: "invalid JSON body", Code: "invalid_request"})
			return
		}
		if len(bytes.TrimSpace(raw)) > 0 {
			if err := json.Unmarshal(raw, &req); err != nil {
				httpx.WriteJSON(w, http.StatusBadRequest, httpx.ErrorResponse{Error: "invalid JSON body", Code: "invalid_request"})
				return
			}
		}
	}
	teamID, ok := h.entryWrite(w, r, a, entries.WriteRequest{Kind: "submit", UpdatedAt: req.UpdatedAt})
	if !ok {
		return
	}
	h.events.QueueReceipt(r.Context(), a.event, teamID, a.user.ID)
	h.writeOwnEntry(w, r, a.event, teamID, a.user.ID)
}

func (h *participationHTTP) unsubmitEntry(w http.ResponseWriter, r *http.Request) {
	a, ok := loadMember(w, r, h.members, r.PathValue("slug"), true, "participant")
	if !ok {
		return
	}
	if _, ok := participantTeam(w, r, h.teams, a); !ok {
		return
	}
	teamID, ok := h.entryWrite(w, r, a, entries.WriteRequest{Kind: "unsubmit"})
	if !ok {
		return
	}
	h.writeOwnEntry(w, r, a.event, teamID, a.user.ID)
}

// excludeEntry and includeEntry are the organiser's manual exclusion of an entry from judging (the client confirms).
func (h *participationHTTP) excludeEntry(w http.ResponseWriter, r *http.Request) {
	h.setEntryExcluded(w, r, true)
}
func (h *participationHTTP) includeEntry(w http.ResponseWriter, r *http.Request) {
	h.setEntryExcluded(w, r, false)
}

func (h *participationHTTP) setEntryExcluded(w http.ResponseWriter, r *http.Request, excluded bool) {
	a, ok := loadMember(w, r, h.members, r.PathValue("slug"), true, "organiser")
	if !ok {
		return
	}
	team, err := h.entries.SetExcluded(r.Context(), a.event.ID, a.user.ID, strings.ToLower(strings.TrimSpace(r.PathValue("team"))), excluded)
	if err != nil {
		writeHackError(w, err)
		return
	}
	tl, err := h.links.loadTeamList(r.Context(), a.event)
	if err != nil {
		log.Printf("hack: entry exclude %s: %v", a.event.Slug, err)
		httpx.WriteInternal(w)
		return
	}
	e, err := h.entries.Entry(r.Context(), team.ID)
	if err != nil {
		log.Printf("hack: entry exclude %s: %v", a.event.Slug, err)
		httpx.WriteInternal(w)
		return
	}
	tracks, err := h.events.EventTracks(r.Context(), a.event.ID)
	if err != nil {
		log.Printf("hack: entry exclude %s: %v", a.event.Slug, err)
		httpx.WriteInternal(w)
		return
	}
	item, err := h.views.entryListItem(r.Context(), a.event, team, e, entries.LoadRules(a.event.EntryRules), len(tracks) > 0, tl)
	if err != nil {
		log.Printf("hack: entry exclude %s: %v", a.event.Slug, err)
		httpx.WriteInternal(w)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, item)
}

func (h *participationHTTP) putEntryScreenshot(w http.ResponseWriter, r *http.Request) {
	a, ok := loadMember(w, r, h.members, r.PathValue("slug"), true, "participant")
	if !ok {
		return
	}
	if !h.entryWrites.AllowHTTP(w, a.user.ID) {
		httpx.TooManyRequests(w)
		return
	}
	if _, ok := participantTeam(w, r, h.teams, a); !ok {
		return
	}
	if !h.shotWrites.AllowHTTP(w, a.user.ID) {
		httpx.TooManyRequests(w)
		return
	}
	data, mime, ok := readEntryImage(w, r)
	if !ok {
		return
	}
	teamID, ok := h.entryWrite(w, r, a, entries.WriteRequest{Kind: "screenshot", Data: data, MIME: mime})
	if !ok {
		return
	}
	h.writeOwnEntry(w, r, a.event, teamID, a.user.ID)
}

func (h *participationHTTP) deleteEntryScreenshot(w http.ResponseWriter, r *http.Request) {
	a, ok := loadMember(w, r, h.members, r.PathValue("slug"), true, "participant")
	if !ok {
		return
	}
	if !h.entryWrites.AllowHTTP(w, a.user.ID) {
		httpx.TooManyRequests(w)
		return
	}
	if _, ok := participantTeam(w, r, h.teams, a); !ok {
		return
	}
	teamID, ok := h.entryWrite(w, r, a, entries.WriteRequest{Kind: "screenshot"})
	if !ok {
		return
	}
	h.writeOwnEntry(w, r, a.event, teamID, a.user.ID)
}

func (h *participationHTTP) getEntryScreenshot(w http.ResponseWriter, r *http.Request) {
	a, ok := loadMember(w, r, h.members, r.PathValue("slug"), false, "participant")
	if !ok {
		return
	}
	team, ok := participantTeam(w, r, h.teams, a)
	if !ok {
		return
	}
	h.serveTeamScreenshot(w, r, team.ID)
}

func (h *participationHTTP) getTeamScreenshot(w http.ResponseWriter, r *http.Request) {
	a, ok := loadMember(w, r, h.members, r.PathValue("slug"), false, "organiser", "judge")
	if !ok {
		return
	}
	team, err := h.teams.TeamBySlugLookup(r.Context(), a.event.ID, strings.ToLower(strings.TrimSpace(r.PathValue("team"))))
	if errors.Is(err, sql.ErrNoRows) {
		httpx.WriteError(w, http.StatusNotFound, "team_not_found", "team not found")
		return
	}
	if err != nil {
		log.Printf("hack: team screenshot %s: %v", a.event.Slug, err)
		httpx.WriteInternal(w)
		return
	}
	// A judge sees screenshots only for teams in their queue (assignment mode, conflicts, entry judgeability).
	if a.member.Role == "judge" {
		eligible, err := h.entries.JudgeTeamEligible(r.Context(), a.event, a.user.ID, team.ID)
		if err != nil {
			log.Printf("hack: team screenshot %s: %v", a.event.Slug, err)
			httpx.WriteInternal(w)
			return
		}
		if !eligible {
			httpx.WriteError(w, http.StatusNotFound, "team_not_found", "team not found")
			return
		}
	}
	h.serveTeamScreenshot(w, r, team.ID)
}

func (h *participationHTTP) listEntries(w http.ResponseWriter, r *http.Request) {
	a, ok := loadMember(w, r, h.members, r.PathValue("slug"), false, "organiser", "judge")
	if !ok {
		return
	}
	out, counts, err := h.entryList(r.Context(), a)
	if err != nil {
		log.Printf("hack: entries %s: %v", a.event.Slug, err)
		httpx.WriteInternal(w)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"required": entryRequiredList(a.event.EntryRequired), "entries": out, "counts": counts})
}

func (h *participationHTTP) entryList(ctx context.Context, a hackAccess) ([]map[string]any, entries.EntryCounts, error) {
	ev := a.event
	tl, err := h.links.loadTeamList(ctx, ev)
	if err != nil {
		return nil, nil, err
	}
	teams, err := h.teams.EventTeams(ctx, ev.ID)
	if err != nil {
		return nil, nil, err
	}
	sort.Slice(teams, func(i, j int) bool { return strings.ToLower(teams[i].Name) < strings.ToLower(teams[j].Name) })
	byTeam, err := h.entriesByTeam(ctx, ev.ID)
	if err != nil {
		return nil, nil, err
	}
	tracks, err := h.events.EventTracks(ctx, ev.ID)
	if err != nil {
		return nil, nil, err
	}
	mayLink, err := h.views.judgeSiteLinkGate(ctx, a)
	if err != nil {
		return nil, nil, err
	}
	fields := entries.LoadRules(ev.EntryRules)
	out := make([]map[string]any, 0, len(teams))
	for _, team := range teams {
		item, err := h.views.entryListItem(ctx, ev, team, byTeam[team.ID], fields, len(tracks) > 0, tl)
		if err != nil {
			return nil, nil, fmt.Errorf("%s: %w", team.Slug, err)
		}
		if !mayLink(team.ID) {
			redactEntrySiteLinks(item)
		}
		out = append(out, item)
	}
	live := map[string]bool{}
	for _, t := range teams {
		site := tl.site(t.Slug)
		live[t.ID] = site != nil && site.ActiveVersion > 0
	}
	counts := entries.CountEntries(teams, byTeam, live)
	return out, counts, nil
}

func (h *participationHTTP) entriesByTeam(ctx context.Context, eventID string) (map[string]model.EventEntry, error) {
	stored, err := h.entries.Entries(ctx, eventID)
	byTeam := make(map[string]model.EventEntry, len(stored))
	for _, e := range stored {
		byTeam[e.TeamID] = e
	}
	return byTeam, err
}

func (h *views) judgeSiteLinkGate(ctx context.Context, a hackAccess) (func(teamID string) bool, error) {
	return h.entries.JudgeSiteLinks(ctx, a.event, a.user.ID, a.admin || a.member.Role == "organiser", h.scoringStage(ctx, a.event))
}

// redactEntrySiteLinks drops an entry list item's preview and pinned links:
// the site block keeps its public address and host but can't be framed.
func redactEntrySiteLinks(item map[string]any) {
	item["pinned_url"] = nil
	if site, ok := item["site"].(map[string]any); ok {
		site["preview_url"] = nil
		if site["frameable"] == true {
			site["frameable"] = false
			site["not_frameable_why"] = "not_ready"
		}
	}
}

func (h *participationHTTP) entryWrite(w http.ResponseWriter, r *http.Request, a hackAccess, req entries.WriteRequest) (string, bool) {
	req.Event, req.UserID = a.event, a.user.ID
	id, missing, err := h.entries.Write(r.Context(), req)
	if err == nil {
		return id, true
	}
	var refusal *entries.Error
	if errors.As(err, &refusal) {
		switch refusal.Code {
		case "entry_changed":
			view, loadErr := h.ownEntryJSON(r.Context(), a.event, id, a.user.ID)
			if loadErr != nil {
				httpx.WriteInternal(w)
				return "", false
			}
			view["code"] = "entry_changed"
			view["error"] = "A teammate changed this since you opened it"
			httpx.WriteJSON(w, 409, view)
		case "entry_incomplete", "entry_would_be_incomplete":
			writeHackErrMissing(w, http.StatusUnprocessableEntity, refusal.Code, "Add these first: "+entries.MissingMessage(missing, entries.LoadRules(a.event.EntryRules)), missing)
		default:
			writeHackError(w, err)
		}
		return "", false
	}
	writeHackError(w, err)
	return "", false
}

func (h *participationHTTP) writeOwnEntry(w http.ResponseWriter, r *http.Request, ev model.Event, teamID, userID string) {
	body, err := h.ownEntryJSON(r.Context(), ev, teamID, userID)
	if err != nil {
		log.Printf("hack: entry %s: %v", ev.Slug, err)
		httpx.WriteInternal(w)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, body)
}

// ownEntryJSON is the participant's view of their team's entry: summary card, submit state, rules, completeness and the Home progress checklist.
func (h *participationHTTP) ownEntryJSON(ctx context.Context, ev model.Event, teamID, userID string) (map[string]any, error) {
	e, err := h.entries.Entry(ctx, teamID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	team, err := h.teams.TeamByID(ctx, teamID)
	if err != nil {
		return nil, err
	}
	st, err := h.teams.TeamWriteStateForLookup(ctx, ev.ID, teamID, false)
	if err != nil {
		return nil, err
	}
	hasTracks, track, err := h.views.entryTrack(ctx, ev.ID, teamID)
	if err != nil {
		return nil, err
	}
	progress, err := h.views.teamProgressJSON(ctx, ev, team, userID, st)
	if err != nil {
		return nil, err
	}
	fields := entries.LoadRules(ev.EntryRules)
	siteExists, siteLiveVersion, siteLive := h.links.entrySiteState(ctx, ev, team.Slug, st.SiteTakenDown)
	check := entries.Evaluate(fields, e, siteLive, hasTracks, track)
	card := entryCardJSON(e)
	card["description"], card["updated_by"] = e.Description, e.UpdatedByName
	card["track"] = trackJSON(track, true)
	var submittedBy any
	if e.SubmittedAt.Valid {
		submittedBy = orNil(e.SubmittedByName)
	}
	return map[string]any{
		"entry":  card,
		"status": e.Status(), "submitted_at": rfc3339UTC(e.SubmittedAt), "submitted_by": submittedBy, "submit_auto": e.SubmitAuto,
		"rules": map[string]any{
			"preset": ev.EntryPreset, "fields": fields, "judge_first": ev.JudgeFirst, "summary": entries.RulesSummary(fields),
		},
		"required": entryRequiredList(ev.EntryRequired),
		"complete": check.Complete, "missing": check.Missing,
		"site":                  map[string]any{"exists": siteExists, "live_version": siteLiveVersion},
		"editable":              st.WriteRefusal() == "",
		"refusal":               orNil(st.WriteRefusal()),
		"deadline":              rfc3339UTC(st.Deadline),
		"deadline_is_extension": st.HasOverride,
		"progress":              progress,
	}, nil
}

func (h *views) entryTrack(ctx context.Context, eventID, teamID string) (bool, *model.EventTrack, error) {
	tracks, err := h.events.EventTracks(ctx, eventID)
	if err != nil {
		return false, nil, err
	}
	track, err := h.events.TeamTrackLookup(ctx, teamID)
	return len(tracks) > 0, track, err
}

func (h *views) teamProgressJSON(ctx context.Context, ev model.Event, team model.EventTeam, userID string, st model.TeamWriteState) (map[string]any, error) {
	people, err := h.teams.TeamPeople(ctx, team.ID)
	if err != nil {
		return nil, err
	}
	var teammates []string
	for _, p := range people {
		if p.UserID != userID {
			teammates = append(teammates, p.DisplayName)
		}
	}
	return h.entryProgressJSON(ctx, ev, team, teammates, st)
}

func entryCardJSON(e model.EventEntry) map[string]any {
	var updated any
	if e.Exists {
		updated = e.UpdatedAt.UTC().Format(time.RFC3339Nano)
	}
	return map[string]any{
		"title": e.Title, "tagline": e.Tagline, "video_url": e.VideoURL, "code_url": e.CodeURL,
		"has_screenshot": e.HasScreenshot, "updated_at": updated,
		"problem": e.Problem, "audience": e.Audience, "tools": nonNilStrings(e.Tools), "maturity": e.Maturity,
	}
}

func trackJSON(track *model.EventTrack, withID bool) any {
	if track == nil {
		return nil
	}
	out := map[string]any{"slug": track.Slug, "name": track.Name}
	if withID {
		out["id"] = track.ID
	}
	return out
}

func orNil[T comparable](v T) any {
	var zero T
	if v == zero {
		return nil
	}
	return v
}

func nonNilStrings(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// entryListItem is one team's entry as organisers and judges see it, with the video embed and whether the judge screen may frame the site.
func (h *views) entryListItem(ctx context.Context, ev model.Event, team model.EventTeam, e model.EventEntry, fields map[string]string, hasTracks bool, tl teamList) (map[string]any, error) {
	st := tl.states[team.ID]
	site := tl.site(team.Slug)
	track, err := h.events.TeamTrackLookup(ctx, team.ID)
	if err != nil {
		return nil, err
	}
	check := entries.Evaluate(fields, e, entries.TeamSiteLive(site, st), hasTracks, track)
	var pinned, pinnedURL any
	if st.PinnedVersion.Valid {
		pinned = st.PinnedVersion.Int64
		if u, ok := h.links.pinnedLink(ev, team, tl, int(st.PinnedVersion.Int64)); ok {
			pinnedURL = u
		}
	}
	siteView, err := h.entrySiteView(ctx, ev, team, tl, pinnedURL)
	if err != nil {
		return nil, err
	}
	var excludedAt any
	if e.ExcludedAt.Valid {
		excludedAt = rfc3339UTC(e.ExcludedAt)
	}
	item := entryCardJSON(e)
	for k, v := range map[string]any{
		"team":            map[string]any{"id": team.ID, "slug": team.Slug, "name": team.Name},
		"description":     e.Description,
		"track":           trackJSON(track, false),
		"status":          e.Status(),
		"submitted_at":    rfc3339UTC(e.SubmittedAt),
		"submit_auto":     e.SubmitAuto,
		"incomplete":      e.Status() == "not_submitted",
		"excluded":        e.ExcludedAt.Valid,
		"excluded_at":     excludedAt,
		"complete":        check.Complete,
		"missing":         check.Missing,
		"video":           entryVideoJSON(e.VideoURL),
		"site":            siteView,
		"site_url":        h.links.teamSiteURL(ev.Slug, team.Slug),
		"site_exists":     site != nil,
		"site_taken_down": st.SiteTakenDown || st.EventTakenDown,
		"deadline":        rfc3339UTC(st.Deadline),
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
	return map[string]any{"url": raw, "host": hackURLHost(raw), "embed_url": orNil(videoEmbed(raw))}
}

func (h *views) entrySiteView(ctx context.Context, ev model.Event, team model.EventTeam, tl teamList, pinnedURL any) (any, error) {
	site := tl.site(team.Slug)
	if site == nil {
		return nil, nil
	}
	liveURL := h.links.teamSiteURL(ev.Slug, team.Slug)
	preview, _ := pinnedURL.(string)
	if preview == "" && site.ActiveVersion > 0 {
		if u, ok := h.links.pinnedLink(ev, team, tl, site.ActiveVersion); ok {
			preview = u
		}
	}
	why, err := h.notFrameableWhy(ctx, site, tl.states[team.ID], tl.ready && preview != "")
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	// A preview link carries no passcode, viewer or offline check of its own (servePreview checks only the token), so a site that can't be framed gets no preview link at all.
	var previewJSON any
	if why == "" {
		previewJSON = orNil(preview)
	}
	return map[string]any{
		"url":               liveURL,
		"preview_url":       previewJSON,
		"host":              hackURLHost(liveURL),
		"frameable":         why == "",
		"not_frameable_why": orNil(why),
	}, nil
}

// notFrameableWhy names the first reason the site can't be framed ("" when it can).
func (h *views) notFrameableWhy(ctx context.Context, site *hostmodel.Site, st model.TeamWriteState, previewReady bool) (string, error) {
	switch {
	case st.SiteTakenDown || st.EventTakenDown || site.Suspended():
		return "taken_down", nil
	case site.Offline:
		return "offline", nil
	case !previewReady:
		return "not_ready", nil
	}
	row, err := h.events.SitePasscode(ctx, site.ID)
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

func hackURLHost(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return u.Host
}

func readEntryImage(w http.ResponseWriter, r *http.Request) ([]byte, string, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, hackScreenshotMax+1)
	data, err := io.ReadAll(r.Body)
	var tooBig *http.MaxBytesError
	if errors.As(err, &tooBig) || len(data) > hackScreenshotMax {
		httpx.WriteError(w, http.StatusRequestEntityTooLarge, "image_too_large", "the screenshot must be 2 MB or smaller")
		return nil, "", false
	}
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "could not read the screenshot")
		return nil, "", false
	}
	mime, ok := entryImageType(data)
	if !ok {
		httpx.WriteError(w, http.StatusUnsupportedMediaType, "unsupported_image", "the screenshot must be a PNG, JPEG or WebP image")
		return nil, "", false
	}
	// A small file can declare an enormous picture; refuse one no screen needs.
	wd, ht, ok := 0, 0, false
	if mime == "image/webp" {
		wd, ht, ok = webpSize(data)
	} else if cfg, _, err := image.DecodeConfig(bytes.NewReader(data)); err == nil {
		wd, ht, ok = cfg.Width, cfg.Height, true
	}
	if !ok || wd < 1 || ht < 1 || wd > hackScreenshotMaxSide || ht > hackScreenshotMaxSide {
		httpx.WriteError(w, http.StatusUnsupportedMediaType, "unsupported_image", "the screenshot must be a PNG, JPEG or WebP image at most 8000 pixels on a side")
		return nil, "", false
	}
	return data, mime, true
}

func entryImageType(b []byte) (string, bool) {
	if len(b) >= 8 && bytes.Equal(b[:8], []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}) {
		return "image/png", true
	}
	if len(b) >= 3 && b[0] == 0xff && b[1] == 0xd8 && b[2] == 0xff {
		return "image/jpeg", true
	}
	if len(b) >= 12 && bytes.Equal(b[:4], []byte("RIFF")) && bytes.Equal(b[8:12], []byte("WEBP")) {
		return "image/webp", true
	}
	return "", false
}

func entryImageAllowed(mime string) bool {
	switch mime {
	case "image/png", "image/jpeg", "image/webp":
		return true
	default:
		return false
	}
}

func (h *participationHTTP) serveTeamScreenshot(w http.ResponseWriter, r *http.Request, teamID string) {
	data, mime, err := h.entries.Screenshot(r.Context(), teamID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			httpx.WriteError(w, http.StatusNotFound, "no_screenshot", "this team has no screenshot")
			return
		}
		log.Printf("hack: screenshot: %v", err)
		httpx.WriteInternal(w)
		return
	}
	if len(data) == 0 || !entryImageAllowed(mime) {
		httpx.WriteError(w, http.StatusNotFound, "no_screenshot", "this team has no screenshot")
		return
	}
	w.Header().Set("Content-Type", mime)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Content-Disposition", "inline")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

// webpSize reads a WebP's canvas size from its first chunk: VP8X (extended),
// VP8L (lossless) or VP8 (lossy). ok=false for anything else.
func webpSize(b []byte) (w, h int, ok bool) {
	if len(b) < 30 {
		return 0, 0, false
	}
	switch string(b[12:16]) {
	case "VP8X":
		w = 1 + (int(b[24]) | int(b[25])<<8 | int(b[26])<<16)
		h = 1 + (int(b[27]) | int(b[28])<<8 | int(b[29])<<16)
		return w, h, true
	case "VP8L":
		if b[20] != 0x2f {
			return 0, 0, false
		}
		v := uint32(b[21]) | uint32(b[22])<<8 | uint32(b[23])<<16 | uint32(b[24])<<24
		return int(v&0x3fff) + 1, int(v>>14&0x3fff) + 1, true
	case "VP8 ":
		if b[23] != 0x9d || b[24] != 0x01 || b[25] != 0x2a {
			return 0, 0, false
		}
		return int(uint16(b[26])|uint16(b[27])<<8) & 0x3fff, int(uint16(b[28])|uint16(b[29])<<8) & 0x3fff, true
	}
	return 0, 0, false
}
