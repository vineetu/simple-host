package entries

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
	"sort"
	"strings"

	core "github.com/vsriram/simple-host/products/hack/core"

	"github.com/vsriram/simple-host/host/httpx"
	entries "github.com/vsriram/simple-host/products/hack/app/entries"
	"github.com/vsriram/simple-host/products/hack/model"
)

// Entries: the text, links and screenshot a team submits, kept apart from the team site. A participant reads and writes only their own team's entry; organisers and judges read every team's entry and screenshots.

const hackScreenshotMax = 2 << 20

const hackScreenshotMaxSide = 8000

func (h *handler) getEntry(w http.ResponseWriter, r *http.Request) {
	a, ok := core.LoadMember(w, r, h.Members, r.PathValue("slug"), false, "participant")
	if !ok {
		return
	}
	team, ok := core.ParticipantTeam(w, r, h.Teams, a)
	if !ok {
		return
	}
	h.writeOwnEntry(w, r, a.Event, team.ID, a.User.ID)
}

func (h *handler) putEntry(w http.ResponseWriter, r *http.Request) {
	a, ok := core.LoadMember(w, r, h.Members, r.PathValue("slug"), true, "participant")
	if !ok {
		return
	}
	if !h.EntryWrites.AllowHTTP(w, a.User.ID) {
		httpx.TooManyRequests(w)
		return
	}
	if _, ok := core.ParticipantTeam(w, r, h.Teams, a); !ok {
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
	if !httpx.DecodeJSON(w, r, core.MaxBody, &in) {
		return
	}
	req, err := TextWrite(TextInput(in))
	if err != nil {
		core.WriteError(w, err)
		return
	}
	teamID, ok := h.entryWrite(w, r, a, req)
	if !ok {
		return
	}
	h.writeOwnEntry(w, r, a.Event, teamID, a.User.ID)
}

// submitEntry moves the entry from draft to submitted, enforced against the event's rules.
func (h *handler) submitEntry(w http.ResponseWriter, r *http.Request) {
	a, ok := core.LoadMember(w, r, h.Members, r.PathValue("slug"), true, "participant")
	if !ok {
		return
	}
	if _, ok := core.ParticipantTeam(w, r, h.Teams, a); !ok {
		return
	}
	// The body is optional: a bodyless POST submits unconditionally.
	var req struct {
		UpdatedAt *string `json:"updated_at"`
	}
	if r.ContentLength != 0 {
		raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, core.MaxBody))
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
	h.Events.QueueReceipt(r.Context(), a.Event, teamID, a.User.ID)
	h.writeOwnEntry(w, r, a.Event, teamID, a.User.ID)
}

func (h *handler) unsubmitEntry(w http.ResponseWriter, r *http.Request) {
	a, ok := core.LoadMember(w, r, h.Members, r.PathValue("slug"), true, "participant")
	if !ok {
		return
	}
	if _, ok := core.ParticipantTeam(w, r, h.Teams, a); !ok {
		return
	}
	teamID, ok := h.entryWrite(w, r, a, entries.WriteRequest{Kind: "unsubmit"})
	if !ok {
		return
	}
	h.writeOwnEntry(w, r, a.Event, teamID, a.User.ID)
}

// excludeEntry and includeEntry are the organiser's manual exclusion of an entry from judging (the client confirms).
func (h *handler) excludeEntry(w http.ResponseWriter, r *http.Request) {
	h.setEntryExcluded(w, r, true)
}
func (h *handler) includeEntry(w http.ResponseWriter, r *http.Request) {
	h.setEntryExcluded(w, r, false)
}

func (h *handler) setEntryExcluded(w http.ResponseWriter, r *http.Request, excluded bool) {
	a, ok := core.LoadMember(w, r, h.Members, r.PathValue("slug"), true, "organiser")
	if !ok {
		return
	}
	team, err := h.Entries.SetExcluded(r.Context(), a.Event.ID, a.User.ID, strings.ToLower(strings.TrimSpace(r.PathValue("team"))), excluded)
	if err != nil {
		core.WriteError(w, err)
		return
	}
	tl, err := h.Links.LoadTeamList(r.Context(), a.Event)
	if err != nil {
		log.Printf("hack: entry exclude %s: %v", a.Event.Slug, err)
		httpx.WriteInternal(w)
		return
	}
	e, err := h.Entries.Entry(r.Context(), team.ID)
	if err != nil {
		log.Printf("hack: entry exclude %s: %v", a.Event.Slug, err)
		httpx.WriteInternal(w)
		return
	}
	tracks, err := h.Events.EventTracks(r.Context(), a.Event.ID)
	if err != nil {
		log.Printf("hack: entry exclude %s: %v", a.Event.Slug, err)
		httpx.WriteInternal(w)
		return
	}
	item, err := h.EntryListItem(r.Context(), a.Event, team, e, entries.LoadRules(a.Event.EntryRules), len(tracks) > 0, tl)
	if err != nil {
		log.Printf("hack: entry exclude %s: %v", a.Event.Slug, err)
		httpx.WriteInternal(w)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, item)
}

func (h *handler) putEntryScreenshot(w http.ResponseWriter, r *http.Request) {
	a, ok := core.LoadMember(w, r, h.Members, r.PathValue("slug"), true, "participant")
	if !ok {
		return
	}
	if !h.EntryWrites.AllowHTTP(w, a.User.ID) {
		httpx.TooManyRequests(w)
		return
	}
	if _, ok := core.ParticipantTeam(w, r, h.Teams, a); !ok {
		return
	}
	if !h.ShotWrites.AllowHTTP(w, a.User.ID) {
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
	h.writeOwnEntry(w, r, a.Event, teamID, a.User.ID)
}

func (h *handler) deleteEntryScreenshot(w http.ResponseWriter, r *http.Request) {
	a, ok := core.LoadMember(w, r, h.Members, r.PathValue("slug"), true, "participant")
	if !ok {
		return
	}
	if !h.EntryWrites.AllowHTTP(w, a.User.ID) {
		httpx.TooManyRequests(w)
		return
	}
	if _, ok := core.ParticipantTeam(w, r, h.Teams, a); !ok {
		return
	}
	teamID, ok := h.entryWrite(w, r, a, entries.WriteRequest{Kind: "screenshot"})
	if !ok {
		return
	}
	h.writeOwnEntry(w, r, a.Event, teamID, a.User.ID)
}

func (h *handler) getEntryScreenshot(w http.ResponseWriter, r *http.Request) {
	a, ok := core.LoadMember(w, r, h.Members, r.PathValue("slug"), false, "participant")
	if !ok {
		return
	}
	team, ok := core.ParticipantTeam(w, r, h.Teams, a)
	if !ok {
		return
	}
	h.ServeTeamScreenshot(w, r, team.ID)
}

func (h *handler) getTeamScreenshot(w http.ResponseWriter, r *http.Request) {
	a, ok := core.LoadMember(w, r, h.Members, r.PathValue("slug"), false, "organiser", "judge")
	if !ok {
		return
	}
	team, err := h.Teams.TeamBySlugLookup(r.Context(), a.Event.ID, strings.ToLower(strings.TrimSpace(r.PathValue("team"))))
	if errors.Is(err, sql.ErrNoRows) {
		httpx.WriteError(w, http.StatusNotFound, "team_not_found", "team not found")
		return
	}
	if err != nil {
		log.Printf("hack: team screenshot %s: %v", a.Event.Slug, err)
		httpx.WriteInternal(w)
		return
	}
	// A judge sees screenshots only for teams in their queue (assignment mode, conflicts, entry judgeability).
	if a.Member.Role == "judge" {
		eligible, err := h.Entries.JudgeTeamEligible(r.Context(), a.Event, a.User.ID, team.ID)
		if err != nil {
			log.Printf("hack: team screenshot %s: %v", a.Event.Slug, err)
			httpx.WriteInternal(w)
			return
		}
		if !eligible {
			httpx.WriteError(w, http.StatusNotFound, "team_not_found", "team not found")
			return
		}
	}
	h.ServeTeamScreenshot(w, r, team.ID)
}

func (h *handler) listEntries(w http.ResponseWriter, r *http.Request) {
	a, ok := core.LoadMember(w, r, h.Members, r.PathValue("slug"), false, "organiser", "judge")
	if !ok {
		return
	}
	out, counts, err := h.entryList(r.Context(), a)
	if err != nil {
		log.Printf("hack: entries %s: %v", a.Event.Slug, err)
		httpx.WriteInternal(w)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"required": core.EntryRequiredList(a.Event.EntryRequired), "entries": out, "counts": counts})
}

func (h *handler) entryList(ctx context.Context, a core.Access) ([]map[string]any, entries.EntryCounts, error) {
	ev := a.Event
	tl, err := h.Links.LoadTeamList(ctx, ev)
	if err != nil {
		return nil, nil, err
	}
	teams, err := h.Teams.EventTeams(ctx, ev.ID)
	if err != nil {
		return nil, nil, err
	}
	sort.Slice(teams, func(i, j int) bool { return strings.ToLower(teams[i].Name) < strings.ToLower(teams[j].Name) })
	byTeam, err := h.entriesByTeam(ctx, ev.ID)
	if err != nil {
		return nil, nil, err
	}
	tracks, err := h.Events.EventTracks(ctx, ev.ID)
	if err != nil {
		return nil, nil, err
	}
	mayLink, err := h.JudgeSiteLinkGate(ctx, a)
	if err != nil {
		return nil, nil, err
	}
	fields := entries.LoadRules(ev.EntryRules)
	out := make([]map[string]any, 0, len(teams))
	for _, team := range teams {
		item, err := h.EntryListItem(ctx, ev, team, byTeam[team.ID], fields, len(tracks) > 0, tl)
		if err != nil {
			return nil, nil, fmt.Errorf("%s: %w", team.Slug, err)
		}
		if !mayLink(team.ID) {
			core.RedactEntrySiteLinks(item)
		}
		out = append(out, item)
	}
	live := map[string]bool{}
	for _, t := range teams {
		site := tl.Site(t.Slug)
		live[t.ID] = site != nil && site.ActiveVersion > 0
	}
	counts := entries.CountEntries(teams, byTeam, live)
	return out, counts, nil
}

func (h *handler) entriesByTeam(ctx context.Context, eventID string) (map[string]model.EventEntry, error) {
	stored, err := h.Entries.Entries(ctx, eventID)
	byTeam := make(map[string]model.EventEntry, len(stored))
	for _, e := range stored {
		byTeam[e.TeamID] = e
	}
	return byTeam, err
}

func (h *handler) entryWrite(w http.ResponseWriter, r *http.Request, a core.Access, req entries.WriteRequest) (string, bool) {
	req.Event, req.UserID = a.Event, a.User.ID
	id, missing, err := h.Entries.Write(r.Context(), req)
	if err == nil {
		return id, true
	}
	var refusal *entries.Error
	if errors.As(err, &refusal) {
		switch refusal.Code {
		case "entry_changed":
			view, loadErr := h.ownEntryJSON(r.Context(), a.Event, id, a.User.ID)
			if loadErr != nil {
				httpx.WriteInternal(w)
				return "", false
			}
			view["code"] = "entry_changed"
			view["error"] = "A teammate changed this since you opened it"
			httpx.WriteJSON(w, 409, view)
		case "entry_incomplete", "entry_would_be_incomplete":
			writeHackErrMissing(w, http.StatusUnprocessableEntity, refusal.Code, "Add these first: "+entries.MissingMessage(missing, entries.LoadRules(a.Event.EntryRules)), missing)
		default:
			core.WriteError(w, err)
		}
		return "", false
	}
	core.WriteError(w, err)
	return "", false
}

func (h *handler) writeOwnEntry(w http.ResponseWriter, r *http.Request, ev model.Event, teamID, userID string) {
	body, err := h.ownEntryJSON(r.Context(), ev, teamID, userID)
	if err != nil {
		log.Printf("hack: entry %s: %v", ev.Slug, err)
		httpx.WriteInternal(w)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, body)
}

// ownEntryJSON is the participant's view of their team's entry: summary card, submit state, rules, completeness and the Home progress checklist.
func (h *handler) ownEntryJSON(ctx context.Context, ev model.Event, teamID, userID string) (map[string]any, error) {
	e, err := h.Entries.Entry(ctx, teamID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	team, err := h.Teams.TeamByID(ctx, teamID)
	if err != nil {
		return nil, err
	}
	st, err := h.Teams.TeamWriteStateForLookup(ctx, ev.ID, teamID, false)
	if err != nil {
		return nil, err
	}
	hasTracks, track, err := h.EntryTrack(ctx, ev.ID, teamID)
	if err != nil {
		return nil, err
	}
	progress, err := h.TeamProgressJSON(ctx, ev, team, userID, st)
	if err != nil {
		return nil, err
	}
	fields := entries.LoadRules(ev.EntryRules)
	siteExists, siteLiveVersion, siteLive := h.Links.EntrySiteState(ctx, ev, team.Slug, st.SiteTakenDown)
	check := entries.Evaluate(fields, e, siteLive, hasTracks, track)
	card := core.EntryCardJSON(e)
	card["description"], card["updated_by"] = e.Description, e.UpdatedByName
	card["track"] = core.TrackJSON(track, true)
	var submittedBy any
	if e.SubmittedAt.Valid {
		submittedBy = core.OrNil(e.SubmittedByName)
	}
	return map[string]any{
		"entry":  card,
		"status": e.Status(), "submitted_at": core.RFC3339(e.SubmittedAt), "submitted_by": submittedBy, "submit_auto": e.SubmitAuto,
		"rules": map[string]any{
			"preset": ev.EntryPreset, "fields": fields, "judge_first": ev.JudgeFirst, "summary": entries.RulesSummary(fields),
		},
		"required": core.EntryRequiredList(ev.EntryRequired),
		"complete": check.Complete, "missing": check.Missing,
		"site":                  map[string]any{"exists": siteExists, "live_version": siteLiveVersion},
		"editable":              st.WriteRefusal() == "",
		"refusal":               core.OrNil(st.WriteRefusal()),
		"deadline":              core.RFC3339(st.Deadline),
		"deadline_is_extension": st.HasOverride,
		"progress":              progress,
	}, nil
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
