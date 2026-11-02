package handler

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
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
	"unicode"
	"unicode/utf8"

	"github.com/vsriram/simple-host/internal/auth"
	"github.com/vsriram/simple-host/internal/db"
)

// Entries: the text, links and screenshot a team submits, kept apart from
// the team site (hosted events, M2; docs/designs/simple-hack-platform.md).
// A participant reads and writes only their own team's entry. Organisers
// and judges read every team's entry and screenshots.

var errEntryChanged = errors.New("entry changed")

// hackScreenshotMax is the largest screenshot stored: 2 MiB.
const hackScreenshotMax = 2 << 20

// hackScreenshotMaxSide is the largest width or height a screenshot may
// declare.
const hackScreenshotMaxSide = 8000

func (h *HackHandler) registerEntries(mux *http.ServeMux, wrap func(http.HandlerFunc) http.Handler) {
	mux.Handle("GET /v1/hack/events/{slug}/entry", wrap(h.getEntry))
	mux.Handle("PUT /v1/hack/events/{slug}/entry", wrap(h.putEntry))
	mux.Handle("POST /v1/hack/events/{slug}/entry/submit", wrap(h.submitEntry))
	mux.Handle("POST /v1/hack/events/{slug}/entry/unsubmit", wrap(h.unsubmitEntry))
	mux.Handle("GET /v1/hack/events/{slug}/entry/screenshot", wrap(h.getEntryScreenshot))
	mux.Handle("PUT /v1/hack/events/{slug}/entry/screenshot", wrap(h.putEntryScreenshot))
	mux.Handle("DELETE /v1/hack/events/{slug}/entry/screenshot", wrap(h.deleteEntryScreenshot))
	mux.Handle("GET /v1/hack/events/{slug}/entry-rules", wrap(h.getEntryRules))
	mux.Handle("PUT /v1/hack/events/{slug}/entry-rules", wrap(h.putEntryRules))
	mux.Handle("GET /v1/hack/events/{slug}/teams/{team}/screenshot", wrap(h.getTeamScreenshot))
	mux.Handle("GET /v1/hack/events/{slug}/entries", wrap(h.listEntries))
	mux.Handle("POST /v1/hack/events/{slug}/teams/{team}/entry/exclude", wrap(h.excludeEntry))
	mux.Handle("POST /v1/hack/events/{slug}/teams/{team}/entry/include", wrap(h.includeEntry))
}

func (h *HackHandler) getEntry(w http.ResponseWriter, r *http.Request) {
	a, ok := h.loadMember(w, r, r.PathValue("slug"), false, "participant")
	if !ok {
		return
	}
	team, ok := h.participantTeam(w, r, a)
	if !ok {
		return
	}
	h.writeOwnEntry(w, r, a.event, team.ID, a.user.ID)
}

func (h *HackHandler) putEntry(w http.ResponseWriter, r *http.Request) {
	a, ok := h.loadMember(w, r, r.PathValue("slug"), true, "participant")
	if !ok {
		return
	}
	if !h.entryWrites.allow(a.user.ID) {
		writeJSON(w, http.StatusTooManyRequests, errorResponse{Error: "rate limit exceeded, slow down", Code: "rate_limited"})
		return
	}
	if _, ok := h.participantTeam(w, r, a); !ok {
		return
	}
	var req struct {
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
	if !decodeHackJSON(w, r, &req) {
		return
	}
	fields := map[string]string{}
	if req.Title != nil {
		s, ok := checkHackLine(w, *req.Title, "title", 0, 80)
		if !ok {
			return
		}
		fields["title"] = s
	}
	if req.Tagline != nil {
		s, ok := checkHackLine(w, *req.Tagline, "tagline", 0, 160)
		if !ok {
			return
		}
		fields["tagline"] = s
	}
	if req.Description != nil {
		s, ok := checkHackText(w, *req.Description, "description", 0, 5000)
		if !ok {
			return
		}
		fields["description"] = s
	}
	if req.VideoURL != nil {
		s, ok := checkEntryURL(w, *req.VideoURL, "video_url")
		if !ok {
			return
		}
		fields["video_url"] = s
	}
	if req.CodeURL != nil {
		s, ok := checkEntryURL(w, *req.CodeURL, "code_url")
		if !ok {
			return
		}
		fields["code_url"] = s
	}
	if req.Problem != nil {
		s, ok := checkHackText(w, *req.Problem, "problem", 0, 140)
		if !ok {
			return
		}
		fields["problem"] = s
	}
	if req.Audience != nil {
		s, ok := checkHackText(w, *req.Audience, "audience", 0, 100)
		if !ok {
			return
		}
		fields["audience"] = s
	}
	if req.Maturity != nil {
		if *req.Maturity != "" && *req.Maturity != "working" && *req.Maturity != "partial" && *req.Maturity != "concept" {
			writeHackErr(w, http.StatusBadRequest, "invalid_maturity", "maturity must be working, partial, concept or empty")
			return
		}
		fields["maturity"] = *req.Maturity
	}
	var tools *[]string
	if req.Tools != nil {
		cleaned, ok := checkEntryTools(w, *req.Tools)
		if !ok {
			return
		}
		tools = &cleaned
	}
	var trackSlug string
	if req.Track != nil {
		trackSlug = strings.TrimSpace(*req.Track)
		if trackSlug != "" && !hackTrackSlug.MatchString(trackSlug) {
			writeHackErr(w, http.StatusBadRequest, "invalid_track", "choose a listed track")
			return
		}
	}
	teamID, ok := h.entryWrite(w, r, a, func(ctx context.Context, tx *sql.Tx, teamID string, st db.TeamWriteState) error {
		if req.UpdatedAt != nil {
			current, err := db.GetEventEntry(ctx, tx, teamID)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			stamp := ""
			if current.Exists {
				stamp = current.UpdatedAt.UTC().Format(time.RFC3339Nano)
			}
			if *req.UpdatedAt != stamp {
				return errEntryChanged
			}
		}
		wasSubmitted := false
		if before, err := db.GetEventEntry(ctx, tx, teamID); err == nil {
			wasSubmitted = before.SubmittedAt.Valid
		} else if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if req.Track != nil {
			if !participantTeamStages(st.Stage) {
				return &hackWriteRefusalError{status: http.StatusConflict, code: "team_choice_closed", msg: "team choices are closed"}
			}
			if _, err := db.SetTeamTrack(ctx, tx, a.event.ID, teamID, trackSlug); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					return &hackWriteRefusalError{status: http.StatusNotFound, code: "track_not_found", msg: "track not found"}
				}
				return err
			}
		}
		if err := db.UpsertEventEntryText(ctx, tx, a.event.ID, teamID, a.user.ID, fields, tools); err != nil {
			return err
		}
		return h.keepSubmittedComplete(ctx, tx, a.event, st, teamID, wasSubmitted)
	})
	if !ok {
		return
	}
	h.writeOwnEntry(w, r, a.event, teamID, a.user.ID)
}

// submitEntry is POST .../entry/submit (section 4.1): the explicit move from
// draft to submitted, enforced against the event's rules. Idempotent -- a
// second call on an already-submitted entry answers 200 and keeps the first
// submitted_at.
func (h *HackHandler) submitEntry(w http.ResponseWriter, r *http.Request) {
	a, ok := h.loadMember(w, r, r.PathValue("slug"), true, "participant")
	if !ok {
		return
	}
	if _, ok := h.participantTeam(w, r, a); !ok {
		return
	}
	// The body is optional (section 4.1: "an empty body, or {updated_at}"):
	// a bodyless POST submits unconditionally.
	var req struct {
		UpdatedAt *string `json:"updated_at"`
	}
	if r.ContentLength != 0 {
		raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, hackMaxBody))
		if err != nil {
			writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid JSON body", Code: "invalid_request"})
			return
		}
		if len(bytes.TrimSpace(raw)) > 0 {
			if err := json.Unmarshal(raw, &req); err != nil {
				writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid JSON body", Code: "invalid_request"})
				return
			}
		}
	}
	teamID, ok := h.entryWrite(w, r, a, func(ctx context.Context, tx *sql.Tx, teamID string, st db.TeamWriteState) error {
		e, err := db.GetEventEntryForUpdate(ctx, tx, teamID)
		if errors.Is(err, sql.ErrNoRows) {
			e = db.EventEntry{TeamID: teamID, EventID: a.event.ID}
		} else if err != nil {
			return err
		}
		if req.UpdatedAt != nil {
			stamp := ""
			if e.Exists {
				stamp = e.UpdatedAt.UTC().Format(time.RFC3339Nano)
			}
			if *req.UpdatedAt != stamp {
				return errEntryChanged
			}
		}
		if e.SubmittedAt.Valid {
			return nil // idempotent: already submitted, keep it as it is
		}
		check, err := h.evaluateEntryTx(ctx, tx, a.event, st, teamID, e)
		if err != nil {
			return err
		}
		if !check.Complete {
			return &entryWouldBeIncompleteError{Missing: check.Missing, Submit: true}
		}
		_, err = db.SubmitEventEntry(ctx, tx, teamID, a.user.ID)
		return err
	})
	if !ok {
		return
	}
	h.queueReceipt(r.Context(), a.event, teamID, a.user.ID)
	h.writeOwnEntry(w, r, a.event, teamID, a.user.ID)
}

// unsubmitEntry is POST .../entry/unsubmit: any current member can put the
// entry back to draft before the deadline. entryWrite's own WriteRefusal
// already answers 409 submissions_closed once the team is frozen.
func (h *HackHandler) unsubmitEntry(w http.ResponseWriter, r *http.Request) {
	a, ok := h.loadMember(w, r, r.PathValue("slug"), true, "participant")
	if !ok {
		return
	}
	if _, ok := h.participantTeam(w, r, a); !ok {
		return
	}
	teamID, ok := h.entryWrite(w, r, a, func(ctx context.Context, tx *sql.Tx, teamID string, st db.TeamWriteState) error {
		_, err := db.UnsubmitEventEntry(ctx, tx, teamID)
		return err
	})
	if !ok {
		return
	}
	h.writeOwnEntry(w, r, a.event, teamID, a.user.ID)
}

// excludeEntry and includeEntry are the organiser's hand exclude of a
// flagged (or any) entry from judging, with a confirm on the client side
// (coordinator decision 2026-11-02, INTENT.md).
func (h *HackHandler) excludeEntry(w http.ResponseWriter, r *http.Request) {
	h.setEntryExcluded(w, r, true)
}
func (h *HackHandler) includeEntry(w http.ResponseWriter, r *http.Request) {
	h.setEntryExcluded(w, r, false)
}

func (h *HackHandler) setEntryExcluded(w http.ResponseWriter, r *http.Request, excluded bool) {
	a, ok := h.loadMember(w, r, r.PathValue("slug"), true, "organiser")
	if !ok {
		return
	}
	team, err := db.GetEventTeamBySlug(r.Context(), h.database, a.event.ID, strings.ToLower(strings.TrimSpace(r.PathValue("team"))))
	if errors.Is(err, sql.ErrNoRows) {
		writeHackErr(w, http.StatusNotFound, "team_not_found", "team not found")
		return
	}
	if err != nil {
		log.Printf("hack: entry exclude %s: %v", a.event.Slug, err)
		writeInternal(w)
		return
	}
	if _, err := db.GetEventEntry(r.Context(), h.database, team.ID); errors.Is(err, sql.ErrNoRows) {
		writeHackErr(w, http.StatusNotFound, "no_entry", "this team has no entry")
		return
	} else if err != nil {
		log.Printf("hack: entry exclude %s: %v", a.event.Slug, err)
		writeInternal(w)
		return
	}
	if _, err := db.SetEntryExcluded(r.Context(), h.database, team.ID, a.user.ID, excluded); err != nil {
		log.Printf("hack: entry exclude %s: %v", a.event.Slug, err)
		writeInternal(w)
		return
	}
	tl, err := h.loadTeamList(r.Context(), a.event)
	if err != nil {
		log.Printf("hack: entry exclude %s: %v", a.event.Slug, err)
		writeInternal(w)
		return
	}
	e, err := db.GetEventEntry(r.Context(), h.database, team.ID)
	if err != nil {
		log.Printf("hack: entry exclude %s: %v", a.event.Slug, err)
		writeInternal(w)
		return
	}
	tracks, err := db.ListEventTracks(r.Context(), h.database, a.event.ID)
	if err != nil {
		log.Printf("hack: entry exclude %s: %v", a.event.Slug, err)
		writeInternal(w)
		return
	}
	item, err := h.entryListItem(r.Context(), a.event, team, e, loadEntryRules(a.event.EntryRules), len(tracks) > 0, tl)
	if err != nil {
		log.Printf("hack: entry exclude %s: %v", a.event.Slug, err)
		writeInternal(w)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (h *HackHandler) putEntryScreenshot(w http.ResponseWriter, r *http.Request) {
	a, ok := h.loadMember(w, r, r.PathValue("slug"), true, "participant")
	if !ok {
		return
	}
	if !h.entryWrites.allow(a.user.ID) {
		writeJSON(w, http.StatusTooManyRequests, errorResponse{Error: "rate limit exceeded, slow down", Code: "rate_limited"})
		return
	}
	if _, ok := h.participantTeam(w, r, a); !ok {
		return
	}
	if !h.shotWrites.allow(a.user.ID) {
		writeJSON(w, http.StatusTooManyRequests, errorResponse{Error: "rate limit exceeded, slow down", Code: "rate_limited"})
		return
	}
	data, mime, ok := readEntryImage(w, r)
	if !ok {
		return
	}
	teamID, ok := h.entryWrite(w, r, a, func(ctx context.Context, tx *sql.Tx, teamID string, st db.TeamWriteState) error {
		before, err := db.GetEventEntry(ctx, tx, teamID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err := db.UpsertEventEntryScreenshot(ctx, tx, a.event.ID, teamID, a.user.ID, data, mime); err != nil {
			return err
		}
		return h.keepSubmittedComplete(ctx, tx, a.event, st, teamID, before.SubmittedAt.Valid)
	})
	if !ok {
		return
	}
	h.writeOwnEntry(w, r, a.event, teamID, a.user.ID)
}

func (h *HackHandler) deleteEntryScreenshot(w http.ResponseWriter, r *http.Request) {
	a, ok := h.loadMember(w, r, r.PathValue("slug"), true, "participant")
	if !ok {
		return
	}
	if !h.entryWrites.allow(a.user.ID) {
		writeJSON(w, http.StatusTooManyRequests, errorResponse{Error: "rate limit exceeded, slow down", Code: "rate_limited"})
		return
	}
	if _, ok := h.participantTeam(w, r, a); !ok {
		return
	}
	teamID, ok := h.entryWrite(w, r, a, func(ctx context.Context, tx *sql.Tx, teamID string, st db.TeamWriteState) error {
		before, err := db.GetEventEntry(ctx, tx, teamID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err := db.UpsertEventEntryScreenshot(ctx, tx, a.event.ID, teamID, a.user.ID, nil, ""); err != nil {
			return err
		}
		return h.keepSubmittedComplete(ctx, tx, a.event, st, teamID, before.SubmittedAt.Valid)
	})
	if !ok {
		return
	}
	h.writeOwnEntry(w, r, a.event, teamID, a.user.ID)
}

func (h *HackHandler) getEntryScreenshot(w http.ResponseWriter, r *http.Request) {
	a, ok := h.loadMember(w, r, r.PathValue("slug"), false, "participant")
	if !ok {
		return
	}
	team, ok := h.participantTeam(w, r, a)
	if !ok {
		return
	}
	h.serveTeamScreenshot(w, r, team.ID)
}

func (h *HackHandler) getTeamScreenshot(w http.ResponseWriter, r *http.Request) {
	a, ok := h.loadMember(w, r, r.PathValue("slug"), false, "organiser", "judge")
	if !ok {
		return
	}
	team, err := db.GetEventTeamBySlug(r.Context(), h.database, a.event.ID, strings.ToLower(strings.TrimSpace(r.PathValue("team"))))
	if errors.Is(err, sql.ErrNoRows) {
		writeHackErr(w, http.StatusNotFound, "team_not_found", "team not found")
		return
	}
	if err != nil {
		log.Printf("hack: team screenshot %s: %v", a.event.Slug, err)
		writeInternal(w)
		return
	}
	// Section 4.5: a judge sees screenshots only for teams in their queue
	// (assignment mode, conflicts, and now entry judgeability). Organisers
	// see every team's screenshot regardless. An ineligible team answers
	// the same team_not_found a genuinely unknown one would, revealing
	// nothing about why.
	if a.member.Role == "judge" {
		eligible, err := db.JudgeTeamEligible(r.Context(), h.database, a.event.ID, a.user.ID, team.ID)
		if err != nil {
			log.Printf("hack: team screenshot %s: %v", a.event.Slug, err)
			writeInternal(w)
			return
		}
		if !eligible {
			writeHackErr(w, http.StatusNotFound, "team_not_found", "team not found")
			return
		}
	}
	h.serveTeamScreenshot(w, r, team.ID)
}

// listEntries is every team of the event with its entry, for organisers and
// judges. Teams whose deadline has passed are pinned first, so the deadline
// version in the answer is the one that was live when the deadline passed.
func (h *HackHandler) listEntries(w http.ResponseWriter, r *http.Request) {
	a, ok := h.loadMember(w, r, r.PathValue("slug"), false, "organiser", "judge")
	if !ok {
		return
	}
	tl, err := h.loadTeamList(r.Context(), a.event)
	if err != nil {
		log.Printf("hack: entries %s: %v", a.event.Slug, err)
		writeInternal(w)
		return
	}
	teams, err := db.ListEventTeams(r.Context(), h.database, a.event.ID)
	if err != nil {
		log.Printf("hack: entries %s: %v", a.event.Slug, err)
		writeInternal(w)
		return
	}
	sort.Slice(teams, func(i, j int) bool {
		return strings.ToLower(teams[i].Name) < strings.ToLower(teams[j].Name)
	})
	stored, err := db.ListEventEntries(r.Context(), h.database, a.event.ID)
	if err != nil {
		log.Printf("hack: entries %s: %v", a.event.Slug, err)
		writeInternal(w)
		return
	}
	byTeam := map[string]db.EventEntry{}
	for _, e := range stored {
		byTeam[e.TeamID] = e
	}
	required := entryRequiredList(a.event.EntryRequired)
	fields := loadEntryRules(a.event.EntryRules)
	tracks, err := db.ListEventTracks(r.Context(), h.database, a.event.ID)
	if err != nil {
		log.Printf("hack: entries %s: %v", a.event.Slug, err)
		writeInternal(w)
		return
	}
	hasTracks := len(tracks) > 0
	mayLink, err := h.judgeSiteLinkGate(r.Context(), a)
	if err != nil {
		log.Printf("hack: entries %s: %v", a.event.Slug, err)
		writeInternal(w)
		return
	}
	counts := map[string]int{"submitted": 0, "drafts": 0, "not_started": 0, "not_submitted": 0, "sites_live": 0}
	out := make([]map[string]any, 0, len(teams))
	for _, team := range teams {
		e, hasEntry := byTeam[team.ID]
		item, err := h.entryListItem(r.Context(), a.event, team, e, fields, hasTracks, tl)
		if err != nil {
			log.Printf("hack: entries %s/%s: %v", a.event.Slug, team.Slug, err)
			writeInternal(w)
			return
		}
		if !mayLink(team.ID) {
			redactEntrySiteLinks(item)
		}
		out = append(out, item)
		switch {
		case !hasEntry:
			counts["not_started"]++
		case e.Status() == "submitted":
			counts["submitted"]++
		case e.Status() == "not_submitted":
			counts["not_submitted"]++
		default:
			counts["drafts"]++
		}
		if site := tl.site(team.Slug); site != nil && site.ActiveVersion > 0 {
			counts["sites_live"]++
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"required": required, "entries": out, "counts": counts})
}

// judgeSiteLinkGate says, per team, whether the caller may have the team's
// site preview and pinned links in an entry list item (security review F1,
// 2026-11-02). Organisers (and the admin key) always may. A judge may only
// for a team they can score -- the queue's own eligibility
// (db.EligibleJudgeTeams: entry submitted or settled and not excluded,
// assigned to them in automatic or manual mode or on their track's panel,
// no declared conflict) -- and only once that team can no longer change
// its site: entries are closed for the event (closed, judging, results,
// ended) or the team's own deadline has passed. Never while the team is
// still building, even if it pressed Submit early.
func (h *HackHandler) judgeSiteLinkGate(ctx context.Context, a hackAccess) (func(teamID string) bool, error) {
	if a.admin || a.member.Role == "organiser" {
		return func(string) bool { return true }, nil
	}
	eligible, err := db.EligibleJudgeTeams(ctx, h.database, a.event.ID, a.user.ID)
	if err != nil {
		return nil, err
	}
	// scoringStage also covers the standing demo, scored while building.
	if submissionsClosedStage(a.event.Stage) || a.event.Stage == "archived" || h.scoringStage(ctx, a.event) {
		return func(teamID string) bool { return eligible[teamID] }, nil
	}
	states, err := db.TeamWriteStatesForEvent(ctx, h.database, a.event.ID)
	if err != nil {
		return nil, err
	}
	return func(teamID string) bool {
		st, ok := states[teamID]
		return eligible[teamID] && ok && st.Frozen()
	}, nil
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

// hackWriteRefusalError is a specific, already-worded refusal fn wants
// entryWrite to send verbatim (status, code, message) instead of the
// generic errEntryChanged/500 handling -- used for the track write's own
// rule (team_choice_closed, track_not_found), which sits on top of
// entryWrite's own WriteRefusal check.
type hackWriteRefusalError struct {
	status int
	code   string
	msg    string
}

func (e *hackWriteRefusalError) Error() string { return e.msg }

// entryWouldBeIncompleteError is returned by fn when a save on a submitted
// entry would make it incomplete (section 4.1): entryWrite rolls back and
// answers 422 entry_would_be_incomplete, changing nothing.
type entryWouldBeIncompleteError struct {
	Missing []string
	// Submit is true for POST .../entry/submit, which answers
	// entry_incomplete; a save on a submitted entry answers
	// entry_would_be_incomplete (section 4.1).
	Submit bool
}

func (e *entryWouldBeIncompleteError) Error() string { return "entry would be incomplete" }

// entryWrite runs a change to the caller's entry in one transaction. Inside
// it the membership is read again FOR SHARE (a move or removal that committed
// meanwhile wins), and the team row is held FOR UPDATE so a deadline pin waits
// until this write commits. ok is false after the answer is written.
func (h *HackHandler) entryWrite(w http.ResponseWriter, r *http.Request, a hackAccess, fn func(ctx context.Context, tx *sql.Tx, teamID string, st db.TeamWriteState) error) (string, bool) {
	ctx := r.Context()
	tx, err := h.database.BeginTx(ctx, nil)
	if err != nil {
		writeInternal(w)
		return "", false
	}
	defer tx.Rollback()
	var teamID sql.NullString
	err = tx.QueryRowContext(ctx, `
		SELECT team_id FROM event_members
		 WHERE event_id = $1 AND user_id = $2 AND role = 'participant'
		 FOR SHARE`, a.event.ID, a.user.ID).Scan(&teamID)
	if errors.Is(err, sql.ErrNoRows) {
		writeEventNotFound(w)
		return "", false
	}
	if err != nil {
		log.Printf("hack: entry write %s: %v", a.event.Slug, err)
		writeInternal(w)
		return "", false
	}
	if !teamID.Valid || teamID.String == "" {
		writeHackErr(w, http.StatusConflict, "no_team", "join or start a team first")
		return "", false
	}
	if _, err = tx.ExecContext(ctx, `SELECT 1 FROM event_teams WHERE id=$1 FOR UPDATE`, teamID.String); err != nil {
		writeInternal(w)
		return "", false
	}
	st, err := db.TeamWriteStateFor(ctx, tx, teamID.String, true)
	if errors.Is(err, sql.ErrNoRows) {
		writeHackErr(w, http.StatusConflict, "no_team", "join or start a team first")
		return "", false
	}
	if err != nil {
		log.Printf("hack: entry write %s: %v", a.event.Slug, err)
		writeInternal(w)
		return "", false
	}
	if code := st.WriteRefusal(); code != "" {
		status, msg := auth.TeamRefusal(code)
		writeHackErr(w, status, code, msg)
		return "", false
	}
	if err := fn(ctx, tx, teamID.String, st); err != nil {
		if errors.Is(err, errEntryChanged) {
			tx.Rollback()
			view, loadErr := h.ownEntryJSON(ctx, a.event, teamID.String, a.user.ID)
			if loadErr != nil {
				writeInternal(w)
				return "", false
			}
			view["code"] = "entry_changed"
			view["error"] = "A teammate changed this since you opened it"
			writeJSON(w, 409, view)
			return "", false
		}
		var refusal *hackWriteRefusalError
		if errors.As(err, &refusal) {
			tx.Rollback()
			writeHackErr(w, refusal.status, refusal.code, refusal.msg)
			return "", false
		}
		var incomplete *entryWouldBeIncompleteError
		if errors.As(err, &incomplete) {
			tx.Rollback()
			code := "entry_would_be_incomplete"
			if incomplete.Submit {
				code = "entry_incomplete"
			}
			writeHackErrMissing(w, http.StatusUnprocessableEntity, code,
				"Add these first: "+entryMissingMessage(incomplete.Missing, loadEntryRules(a.event.EntryRules)), incomplete.Missing)
			return "", false
		}
		log.Printf("hack: entry write %s: %v", a.event.Slug, err)
		writeInternal(w)
		return "", false
	}
	if err := tx.Commit(); err != nil {
		log.Printf("hack: entry write %s: %v", a.event.Slug, err)
		writeInternal(w)
		return "", false
	}
	return teamID.String, true
}

func (h *HackHandler) writeOwnEntry(w http.ResponseWriter, r *http.Request, ev db.Event, teamID, userID string) {
	body, err := h.ownEntryJSON(r.Context(), ev, teamID, userID)
	if err != nil {
		log.Printf("hack: entry %s: %v", ev.Slug, err)
		writeInternal(w)
		return
	}
	writeJSON(w, http.StatusOK, body)
}

// ownEntryJSON is the participant's view of their team's entry (section
// 4.1): the summary card, the submit state, the event's rules and summary
// sentence, completeness, and the Home progress checklist. editable and
// refusal come from the team's write state, read outside the write
// transaction.
func (h *HackHandler) ownEntryJSON(ctx context.Context, ev db.Event, teamID, userID string) (map[string]any, error) {
	e, err := db.GetEventEntry(ctx, h.database, teamID)
	if errors.Is(err, sql.ErrNoRows) {
		e = db.EventEntry{}
	} else if err != nil {
		return nil, err
	}
	team, err := db.GetEventTeamByID(ctx, h.database, teamID)
	if err != nil {
		return nil, err
	}
	st, err := db.TeamWriteStateFor(ctx, h.database, teamID, false)
	if err != nil {
		return nil, err
	}
	tracks, err := db.ListEventTracks(ctx, h.database, ev.ID)
	if err != nil {
		return nil, err
	}
	hasTracks := len(tracks) > 0
	track, err := db.TeamTrack(ctx, h.database, teamID)
	if err != nil {
		return nil, err
	}
	fields := loadEntryRules(ev.EntryRules)
	siteExists, siteLiveVersion, siteLive := h.entrySiteState(ctx, ev, team.Slug, st.SiteTakenDown)
	check := evaluateEntry(fields, e, siteLive, hasTracks, track)

	code := st.WriteRefusal()
	var refusal any
	if code != "" {
		refusal = code
	}
	var updated any
	if e.Exists {
		updated = e.UpdatedAt.UTC().Format(time.RFC3339Nano)
	}
	var submittedBy any
	if e.SubmittedAt.Valid && e.SubmittedByName != "" {
		submittedBy = e.SubmittedByName
	}
	var trackJSON any
	if track != nil {
		trackJSON = map[string]any{"id": track.ID, "slug": track.Slug, "name": track.Name}
	}
	people, err := db.ListTeamPeople(ctx, h.database, team.ID)
	if err != nil {
		return nil, err
	}
	var teammates []string
	for _, p := range people {
		if p.UserID != userID {
			teammates = append(teammates, p.DisplayName)
		}
	}
	progress, err := h.entryProgressJSON(ctx, ev, team, teammates, st)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"entry": map[string]any{
			"title": e.Title, "tagline": e.Tagline, "description": e.Description,
			"video_url": e.VideoURL, "code_url": e.CodeURL, "has_screenshot": e.HasScreenshot,
			"updated_at": updated, "updated_by": e.UpdatedByName,
			"problem": e.Problem, "audience": e.Audience, "tools": nonNilStrings(e.Tools), "maturity": e.Maturity,
			"track": trackJSON,
		},
		"status": e.Status(), "submitted_at": rfc3339UTC(e.SubmittedAt), "submitted_by": submittedBy, "submit_auto": e.SubmitAuto,
		"rules": map[string]any{
			"preset": ev.EntryPreset, "fields": fields, "judge_first": ev.JudgeFirst, "summary": entryRulesSummary(fields),
		},
		"required": entryRequiredList(ev.EntryRequired),
		"complete": check.Complete, "missing": check.Missing,
		"site":                  map[string]any{"exists": siteExists, "live_version": siteLiveVersion},
		"editable":              code == "",
		"refusal":               refusal,
		"deadline":              rfc3339UTC(st.Deadline),
		"deadline_is_extension": st.HasOverride,
		"progress":              progress,
	}, nil
}

// nonNilStrings is tools, never nil in the JSON body (an entry with no
// tools yet still answers "tools": []).
func nonNilStrings(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// entryListItem is one team's entry as organisers and judges see it
// (section 4.5): the summary card, status, missing and the video and site
// evidence, with the embeddable video player (videoEmbed) and whether the
// judge screen may frame the site (entrySiteView).
func (h *HackHandler) entryListItem(ctx context.Context, ev db.Event, team db.EventTeam, e db.EventEntry, fields map[string]string, hasTracks bool, tl teamList) (map[string]any, error) {
	st := tl.states[team.ID]
	site := tl.site(team.Slug)
	siteLive := site != nil && site.ActiveVersion > 0 && !st.SiteTakenDown && !st.EventTakenDown
	track, err := db.TeamTrack(ctx, h.database, team.ID)
	if err != nil {
		return nil, err
	}
	check := evaluateEntry(fields, e, siteLive, hasTracks, track)
	var updated any
	if e.Exists {
		updated = e.UpdatedAt.UTC().Format(time.RFC3339Nano)
	}
	var pinned any
	var pinnedURL any
	if st.PinnedVersion.Valid {
		pinned = st.PinnedVersion.Int64
		if u, ok := h.pinnedLink(ev, team, tl, int(st.PinnedVersion.Int64)); ok {
			pinnedURL = u
		}
	}
	var trackJSON any
	if track != nil {
		trackJSON = map[string]any{"slug": track.Slug, "name": track.Name}
	}
	var video any
	if e.VideoURL != "" {
		var embed any
		if u := videoEmbed(e.VideoURL); u != "" {
			embed = u
		}
		video = map[string]any{"url": e.VideoURL, "host": hackURLHost(e.VideoURL), "embed_url": embed}
	}
	siteView, err := h.entrySiteView(ctx, ev, team, tl, pinnedURL)
	if err != nil {
		return nil, err
	}
	var excludedAt any
	if e.ExcludedAt.Valid {
		excludedAt = rfc3339UTC(e.ExcludedAt)
	}
	return map[string]any{
		"team":            map[string]any{"id": team.ID, "slug": team.Slug, "name": team.Name},
		"title":           e.Title,
		"tagline":         e.Tagline,
		"description":     e.Description,
		"video_url":       e.VideoURL,
		"code_url":        e.CodeURL,
		"has_screenshot":  e.HasScreenshot,
		"problem":         e.Problem,
		"audience":        e.Audience,
		"tools":           nonNilStrings(e.Tools),
		"maturity":        e.Maturity,
		"track":           trackJSON,
		"status":          e.Status(),
		"submitted_at":    rfc3339UTC(e.SubmittedAt),
		"submit_auto":     e.SubmitAuto,
		"incomplete":      e.Status() == "not_submitted",
		"excluded":        e.ExcludedAt.Valid,
		"excluded_at":     excludedAt,
		"complete":        check.Complete,
		"missing":         check.Missing,
		"video":           video,
		"site":            siteView,
		"updated_at":      updated,
		"site_url":        h.teamSiteURL(ev.Slug, team.Slug),
		"site_exists":     site != nil,
		"site_taken_down": st.SiteTakenDown || st.EventTakenDown,
		"deadline":        rfc3339UTC(st.Deadline),
		"frozen":          st.Frozen(),
		"pinned_version":  pinned,
		"pinned_url":      pinnedURL,
	}, nil
}

// entrySiteView is the site block of an entry list item: the address the
// judge screen frames or opens, the host it names, and whether framing is
// allowed. preview_url is the submitted (pinned) version once the team is
// pinned, else a preview of the live version; both are previews on the
// team's own host, which never carry owner rights and are the only pages
// that let the app frame them (servePreview). frameable is false for a
// site with a passcode or named viewers, one that is offline, taken down or
// not ready, and on a server with no per-site hosts (no preview link);
// not_frameable_why says which.
// nil when the team has no site.
func (h *HackHandler) entrySiteView(ctx context.Context, ev db.Event, team db.EventTeam, tl teamList, pinnedURL any) (any, error) {
	site := tl.site(team.Slug)
	if site == nil {
		return nil, nil
	}
	st := tl.states[team.ID]
	liveURL := h.teamSiteURL(ev.Slug, team.Slug)
	preview, _ := pinnedURL.(string)
	if preview == "" && site.ActiveVersion > 0 {
		if u, ok := h.pinnedLink(ev, team, tl, site.ActiveVersion); ok {
			preview = u
		}
	}
	// why names the first reason the site can't be framed, for the
	// screen's one-line explanation ("" when it can).
	why := ""
	switch {
	case st.SiteTakenDown || st.EventTakenDown || site.Suspended():
		why = "taken_down"
	case site.Offline:
		why = "offline"
	case !tl.ready || preview == "":
		why = "not_ready"
	}
	if why == "" {
		row, err := db.GetSitePasscode(ctx, h.database, site.ID)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, nil
			}
			return nil, err
		}
		switch {
		case row.Offline:
			why = "offline"
		case row.Enc != nil:
			why = "passcode"
		case row.NamedViewers:
			why = "viewers"
		}
	}
	frameable := why == ""
	var whyJSON any
	if why != "" {
		whyJSON = why
	}
	// Security review F1 (2026-11-02): a preview link carries no passcode,
	// viewer or offline check of its own (servePreview checks only the
	// token), so a site that can't be framed gets no preview link at all.
	var previewJSON any
	if preview != "" && frameable {
		previewJSON = preview
	}
	return map[string]any{
		"url":               liveURL,
		"preview_url":       previewJSON,
		"host":              hackURLHost(liveURL),
		"frameable":         frameable,
		"not_frameable_why": whyJSON,
	}, nil
}

// hackURLHost is the host shown next to a link (judge/organiser views,
// never the public page in this slice). "" on a URL that fails to parse,
// which cannot happen for a URL that passed checkEntryURL.
func hackURLHost(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return u.Host
}

// entryToolsMax is how many tools an entry may list, and entryToolMax is
// each one's length -- section 4.1's "cap of 10, each checkHackLine(1, 30)".
const entryToolsMax = 10

// checkEntryTools validates the tools array: at most 10 items, each through
// checkHackLine(1, 30), duplicates dropped case-insensitively (the first
// spelling kept).
func checkEntryTools(w http.ResponseWriter, in []string) ([]string, bool) {
	if len(in) > entryToolsMax {
		writeHackErr(w, http.StatusBadRequest, "invalid_tools", "tools takes at most 10 items")
		return nil, false
	}
	out := make([]string, 0, len(in))
	seen := map[string]bool{}
	for _, raw := range in {
		s, ok := checkHackLine(w, raw, "tools", 1, 30)
		if !ok {
			return nil, false
		}
		key := strings.ToLower(s)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, s)
	}
	return out, true
}

// checkEntryURL accepts "" or an absolute http or https URL with a host, at
// most 500 characters, with no whitespace or control characters.
func checkEntryURL(w http.ResponseWriter, s, field string) (string, bool) {
	if s == "" {
		return "", true
	}
	if utf8.RuneCountInString(s) > 500 || urlHasSpaceOrControl(s) || !absoluteHTTPURL(s) {
		writeHackErr(w, http.StatusBadRequest, "invalid_url", field+" must be an absolute http or https URL of at most 500 characters")
		return "", false
	}
	return s, true
}

func urlHasSpaceOrControl(s string) bool {
	for _, r := range s {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return true
		}
	}
	return false
}

func absoluteHTTPURL(s string) bool {
	u, err := url.Parse(s)
	if err != nil {
		return false
	}
	if u.Scheme != "http" && u.Scheme != "https" || u.User != nil {
		return false
	}
	return u.Host != ""
}

// readEntryImage reads a screenshot body. The limit is 2 MiB plus one byte
// so a body just over the limit is still this handler's 413, not a cut-off
// read. The type comes only from the magic bytes: Content-Type is ignored,
// and SVG and GIF are not images we store.
func readEntryImage(w http.ResponseWriter, r *http.Request) ([]byte, string, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, hackScreenshotMax+1)
	data, err := io.ReadAll(r.Body)
	var tooBig *http.MaxBytesError
	if errors.As(err, &tooBig) || len(data) > hackScreenshotMax {
		writeHackErr(w, http.StatusRequestEntityTooLarge, "image_too_large", "the screenshot must be 2 MB or smaller")
		return nil, "", false
	}
	if err != nil {
		writeHackErr(w, http.StatusBadRequest, "invalid_request", "could not read the screenshot")
		return nil, "", false
	}
	mime, ok := entryImageType(data)
	if !ok {
		writeHackErr(w, http.StatusUnsupportedMediaType, "unsupported_image", "the screenshot must be a PNG, JPEG or WebP image")
		return nil, "", false
	}
	// A small file can declare an enormous picture; refuse one no screen
	// needs.
	wd, ht, ok := 0, 0, false
	if mime == "image/webp" {
		wd, ht, ok = webpSize(data)
	} else if cfg, _, err := image.DecodeConfig(bytes.NewReader(data)); err == nil {
		wd, ht, ok = cfg.Width, cfg.Height, true
	}
	if !ok || wd < 1 || ht < 1 || wd > hackScreenshotMaxSide || ht > hackScreenshotMaxSide {
		writeHackErr(w, http.StatusUnsupportedMediaType, "unsupported_image", "the screenshot must be a PNG, JPEG or WebP image at most 8000 pixels on a side")
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

func (h *HackHandler) serveTeamScreenshot(w http.ResponseWriter, r *http.Request, teamID string) {
	data, mime, err := db.GetEventScreenshot(r.Context(), h.database, teamID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeHackErr(w, http.StatusNotFound, "no_screenshot", "this team has no screenshot")
			return
		}
		log.Printf("hack: screenshot: %v", err)
		writeInternal(w)
		return
	}
	if len(data) == 0 || !entryImageAllowed(mime) {
		writeHackErr(w, http.StatusNotFound, "no_screenshot", "this team has no screenshot")
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
