package hackhttp

import (
	"context"
	"database/sql"
	"encoding/csv"
	"errors"
	"fmt"
	"github.com/vsriram/simple-host/internal/httpx"
	"github.com/vsriram/simple-host/internal/rules/sites"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/vsriram/simple-host/internal/db"
	"github.com/vsriram/simple-host/internal/rules/hackevents"
)

func (h *HackHandler) registerAdministration(mux *http.ServeMux, wrap func(http.HandlerFunc) http.Handler) {
	mux.Handle("POST /v1/hack/events/{slug}/organiser-invite", wrap(h.createOrganiserInvite))
	mux.Handle("DELETE /v1/hack/events/{slug}/organiser-invite", wrap(h.revokeOrganiserInvite))
	mux.Handle("DELETE /v1/hack/events/{slug}/organisers/{user_id}", wrap(h.removeOrganiser))
	mux.Handle("GET /v1/hack/organiser/{code}", http.HandlerFunc(h.getOrganiserInvite))
	mux.Handle("POST /v1/hack/organiser/{code}", wrap(h.acceptOrganiserInvite))
	mux.Handle("PATCH /v1/hack/events/{slug}/teams/{team}", wrap(h.renameTeam))
	mux.Handle("GET /v1/hack/events/{slug}/export/participants.csv", wrap(h.exportAdministrationCSV))
	mux.Handle("GET /v1/hack/events/{slug}/export/teams.csv", wrap(h.exportAdministrationCSV))
	mux.Handle("GET /v1/hack/events/{slug}/export/entries.csv", wrap(h.exportAdministrationCSV))
	mux.Handle("GET /v1/hack/events/{slug}/export/projects.tar.gz", wrap(h.exportProjects))
	mux.Handle("GET /v1/hack/events/{slug}/team/export.tar.gz", wrap(h.exportOwnTeam))
	mux.Handle("POST /v1/hack/events/{slug}/export/projects-link", wrap(h.createHackArchiveLink))
	mux.Handle("POST /v1/hack/events/{slug}/export/own-team-link", wrap(h.createHackArchiveLink))
	mux.Handle("GET /v1/hack/archive", http.HandlerFunc(h.downloadHackArchive))
	mux.Handle("GET /v1/hack/events/{slug}/usage", wrap(h.eventUsage))
}

func (h *HackHandler) administrationWrite(w http.ResponseWriter, r *http.Request) (hackAccess, bool) {
	a, ok := h.loadMember(w, r, r.PathValue("slug"), true, "organiser")
	if ok && a.event.Stage == "archived" {
		httpx.WriteError(w, 409, "event_closed", "an ended event cannot be changed")
		return a, false
	}
	return a, ok
}

func (h *HackHandler) createOrganiserInvite(w http.ResponseWriter, r *http.Request) {
	a, ok := h.administrationWrite(w, r)
	if !ok {
		return
	}
	code, err := hackevents.RandomCode(32)
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	expires, err := db.SetOrganiserInvite(r.Context(), h.database, a.event.ID, a.user.ID, db.HashAPIKey(code))
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpx.WriteJSON(w, 201, map[string]any{"url": h.publicBaseURL + "/organiser/" + code, "expires_at": rfc3339Time(expires)})
}

func (h *HackHandler) revokeOrganiserInvite(w http.ResponseWriter, r *http.Request) {
	a, ok := h.administrationWrite(w, r)
	if !ok {
		return
	}
	if err := db.DeleteOrganiserInvite(r.Context(), h.database, a.event.ID); err != nil {
		httpx.WriteInternal(w)
		return
	}
	w.WriteHeader(204)
}

func (h *HackHandler) getOrganiserInvite(w http.ResponseWriter, r *http.Request) {
	if h.rateLimited(w, r, "") {
		return
	}
	ev, err := db.OrganiserInviteEvent(r.Context(), h.database, db.HashAPIKey(hackevents.NormalizeCode(r.PathValue("code"))), false)
	if errors.Is(err, sql.ErrNoRows) {
		httpx.WriteError(w, 404, "invalid_code", "invalid or expired invitation")
		return
	}
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	if ev.TakenDown() {
		httpx.WriteError(w, 410, "event_taken_down", "this event has been taken down")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpx.WriteJSON(w, 200, map[string]any{"slug": ev.Slug, "title": ev.Title, "icon_url": h.iconURL(ev), "stage": ev.Stage, "organiser_name": ev.OrganiserName, "organisation": ev.Organisation, "role": "organiser", "joinable": ev.Stage != "archived", "coc_default": HackDefaultCoC, "coc_text": ev.CocText})
}

func (h *HackHandler) acceptOrganiserInvite(w http.ResponseWriter, r *http.Request) {
	user := hackNeedUser(w, r)
	if user == nil {
		return
	}
	if h.rateLimited(w, r, user.ID) {
		return
	}
	var req struct {
		AcceptCoC   bool   `json:"accept_coc"`
		DisplayName string `json:"display_name"`
	}
	if !decodeHackJSON(w, r, &req) {
		return
	}
	if !req.AcceptCoC {
		httpx.WriteError(w, 400, "coc_required", "you must accept the code of conduct")
		return
	}
	name, ok := checkHackLine(w, req.DisplayName, "display_name", 1, 100)
	if !ok {
		return
	}
	hash := db.HashAPIKey(hackevents.NormalizeCode(r.PathValue("code")))
	// Find which event the invite names before taking any lock (lock-order
	// fix, SIM-340 2.4): leaving an event holds the event row FOR UPDATE
	// before the leaver's own member row, so taking the event row first here
	// too, ahead of the invite and inviter-member rows, avoids a lock-order
	// cycle when the leaver is the invite's inviter.
	unlocked, err := db.OrganiserInviteEvent(r.Context(), h.database, hash, false)
	if errors.Is(err, sql.ErrNoRows) {
		httpx.WriteError(w, 404, "invalid_code", "invalid or expired invitation")
		return
	}
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	tx, err := h.database.BeginTx(r.Context(), nil)
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	defer tx.Rollback()
	ev, err := db.GetEventForUpdate(r.Context(), tx, unlocked.ID)
	if errors.Is(err, sql.ErrNoRows) {
		// The last organiser's concurrent "delete" won the race (Codex
		// review, SIM-340): the event is gone, same as any other invalid
		// invitation.
		httpx.WriteError(w, 404, "invalid_code", "invalid or expired invitation")
		return
	}
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	// Re-locks the invite and the inviter's member row now that the event row
	// is held; re-checks the invite still exists and has not expired. Ahead
	// of the takedown/archived checks below (Codex review, SIM-340): the
	// last organiser's concurrent "end" deletes every outstanding invite as
	// part of the same transaction that archives the event, so a losing
	// acceptance must see the more specific "invalid_code" instead of
	// "event_closed".
	if _, err = db.OrganiserInviteEvent(r.Context(), tx, hash, true); errors.Is(err, sql.ErrNoRows) {
		httpx.WriteError(w, 404, "invalid_code", "invalid or expired invitation")
		return
	} else if err != nil {
		httpx.WriteInternal(w)
		return
	}
	if ev.TakenDown() {
		httpx.WriteError(w, 403, "event_taken_down", "this event has been taken down")
		return
	}
	if ev.Stage == "archived" {
		httpx.WriteError(w, 409, "event_closed", "an ended event cannot be changed")
		return
	}
	if _, err = db.GetEventMember(r.Context(), tx, ev.ID, user.ID); err == nil {
		httpx.WriteError(w, 409, "already_member", "you already belong to this event; roles cannot be changed through an invitation")
		return
	} else if !errors.Is(err, sql.ErrNoRows) {
		httpx.WriteInternal(w)
		return
	}
	member, err := db.InsertEventMember(r.Context(), tx, ev.ID, user.ID, "organiser", name)
	if dbUnique(err) {
		httpx.WriteError(w, 409, "already_member", "you already belong to this event")
		return
	}
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	if err = db.DeleteOrganiserInvite(r.Context(), tx, ev.ID); err != nil {
		httpx.WriteInternal(w)
		return
	}
	if err = tx.Commit(); err != nil {
		httpx.WriteInternal(w)
		return
	}
	httpx.WriteJSON(w, 200, h.eventView(r.Context(), ev, member, true))
}

func (h *HackHandler) removeOrganiser(w http.ResponseWriter, r *http.Request) {
	a, ok := h.administrationWrite(w, r)
	if !ok {
		return
	}
	id := r.PathValue("user_id")
	if !sites.UUIDShape.MatchString(id) {
		writeEventNotFound(w)
		return
	}
	if a.event.CreatedBy.Valid && id == a.event.CreatedBy.String {
		httpx.WriteError(w, 409, "primary_organiser", "the event creator cannot be removed")
		return
	}
	tx, err := h.database.BeginTx(r.Context(), nil)
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	defer tx.Rollback()
	if err := db.RemoveCoOrganiser(r.Context(), tx, a.event.ID, a.user.ID, id); errors.Is(err, sql.ErrNoRows) {
		writeEventNotFound(w)
		return
	} else if err != nil {
		httpx.WriteInternal(w)
		return
	}
	if err := tx.Commit(); err != nil {
		httpx.WriteInternal(w)
		return
	}
	w.WriteHeader(204)
}

func (h *HackHandler) renameTeam(w http.ResponseWriter, r *http.Request) {
	a, ok := h.administrationWrite(w, r)
	if !ok {
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	if !decodeHackJSON(w, r, &req) {
		return
	}
	name, ok := checkHackLine(w, hackevents.StripInvisible(req.Name), "team_name", 1, 80)
	if !ok {
		return
	}
	slug := r.PathValue("team")
	err := db.RenameEventTeam(r.Context(), h.database, a.event.ID, slug, name)
	if errors.Is(err, sql.ErrNoRows) {
		writeEventNotFound(w)
		return
	}
	if dbUnique(err) {
		httpx.WriteError(w, 409, "team_name_taken", "another team has that name")
		return
	}
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	httpx.WriteJSON(w, 200, map[string]any{"name": name, "slug": slug})
}

func (h *HackHandler) exportAdministrationCSV(w http.ResponseWriter, r *http.Request) {
	a, ok := h.loadMember(w, r, r.PathValue("slug"), false, "organiser")
	if !ok {
		return
	}
	var rows [][]string
	kind := filepath.Base(r.URL.Path)
	switch kind {
	case "participants.csv":
		people, err := db.ListEventPeople(r.Context(), h.database, a.event.ID)
		if err != nil {
			httpx.WriteInternal(w)
			return
		}
		// D11: appended at the end, so existing spreadsheet imports keep working.
		rows = append(rows, []string{"name", "email", "team", "team_slug", "joined_at", "coc_accepted_at", "city", "country", "age_range", "gender", "joined_via"})
		for _, p := range people {
			if p.Role == "participant" && p.ApprovalStatus == "approved" {
				accepted := ""
				if p.CocAcceptedAt.Valid {
					accepted = rfc3339Time(p.CocAcceptedAt.Time)
				}
				rows = append(rows, []string{
					p.DisplayName, p.Email, p.TeamName.String, p.TeamSlug.String, rfc3339Time(p.JoinedAt), accepted,
					p.City, hackevents.CountryName(p.Country), hackevents.AgeRangeLabels[p.AgeRange], hackevents.GenderLabels[p.Gender], hackevents.JoinedViaLabel(p.JoinedVia),
				})
			}
		}
	case "teams.csv":
		teams, err := db.ListEventTeams(r.Context(), h.database, a.event.ID)
		if err != nil {
			httpx.WriteInternal(w)
			return
		}
		people, err := db.ListEventPeople(r.Context(), h.database, a.event.ID)
		if err != nil {
			httpx.WriteInternal(w)
			return
		}
		counts := map[string]int{}
		for _, p := range people {
			if p.TeamID.Valid {
				counts[p.TeamID.String]++
			}
		}
		rows = append(rows, []string{"name", "slug", "members", "site_url", "created_at"})
		for _, t := range teams {
			rows = append(rows, []string{t.Name, t.Slug, strconv.Itoa(counts[t.ID]), h.teamSiteURL(a.event.Slug, t.Slug), rfc3339Time(t.CreatedAt)})
		}
	case "entries.csv":
		entries, err := db.ListEventEntries(r.Context(), h.database, a.event.ID)
		if err != nil {
			httpx.WriteInternal(w)
			return
		}
		teams, err := db.ListEventTeams(r.Context(), h.database, a.event.ID)
		if err != nil {
			httpx.WriteInternal(w)
			return
		}
		byID := map[string]db.EventTeam{}
		for _, t := range teams {
			byID[t.ID] = t
		}
		fields := loadEntryRules(a.event.EntryRules)
		tracks, err := db.ListEventTracks(r.Context(), h.database, a.event.ID)
		if err != nil {
			httpx.WriteInternal(w)
			return
		}
		hasTracks := len(tracks) > 0
		// D11-style: the new columns are appended at the end, so existing
		// spreadsheet imports keep working (section 4.7).
		rows = append(rows, []string{"team", "team_slug", "title", "tagline", "description", "video_url", "code_url", "has_screenshot", "updated_at",
			"status", "submitted_at", "submit_auto", "problem", "audience", "track", "tools", "maturity", "missing", "excluded"})
		for _, e := range entries {
			t := byID[e.TeamID]
			track, err := db.TeamTrack(r.Context(), h.database, e.TeamID)
			if err != nil {
				httpx.WriteInternal(w)
				return
			}
			siteLive := h.entrySiteLive(r.Context(), a.event, t.Slug, t.SiteTakenDownAt.Valid)
			check := evaluateEntry(fields, e, siteLive, hasTracks, track)
			trackName := ""
			if track != nil {
				trackName = track.Name
			}
			submittedAt := ""
			if e.SubmittedAt.Valid {
				submittedAt = rfc3339Time(e.SubmittedAt.Time)
			}
			rows = append(rows, []string{
				t.Name, t.Slug, e.Title, e.Tagline, e.Description, e.VideoURL, e.CodeURL, strconv.FormatBool(e.HasScreenshot), rfc3339Time(e.UpdatedAt),
				e.Status(), submittedAt, strconv.FormatBool(e.SubmitAuto), e.Problem, e.Audience, trackName, strings.Join(e.Tools, "; "), e.Maturity,
				strings.Join(check.Missing, "; "), strconv.FormatBool(e.ExcludedAt.Valid),
			})
		}
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s-%s"`, a.event.Slug, kind))
	cw := csv.NewWriter(w)
	defer cw.Flush()
	for _, row := range rows {
		for i := range row {
			row[i] = httpx.CSVSafe(row[i])
		}
		if err := cw.Write(row); err != nil {
			return
		}
	}
}

type hackAdministrationSites interface {
	ExportEventProjects(http.ResponseWriter, *http.Request, db.Event, string)
	EventFileBytes(context.Context, db.Event) (int64, error)
}

func (h *HackHandler) exportProjects(w http.ResponseWriter, r *http.Request) {
	a, ok := h.loadMember(w, r, r.PathValue("slug"), false, "organiser")
	if !ok {
		return
	}
	sites, ok := h.sites.(hackAdministrationSites)
	if !ok {
		httpx.WriteInternal(w)
		return
	}
	sites.ExportEventProjects(w, r, a.event, "")
}
func (h *HackHandler) exportOwnTeam(w http.ResponseWriter, r *http.Request) {
	a, ok := h.loadMember(w, r, r.PathValue("slug"), false, "participant")
	if !ok {
		return
	}
	team, ok := h.participantTeam(w, r, a)
	if !ok {
		return
	}
	sites, ok := h.sites.(hackAdministrationSites)
	if !ok {
		httpx.WriteInternal(w)
		return
	}
	sites.ExportEventProjects(w, r, a.event, team.Slug)
}
func (h *HackHandler) eventUsage(w http.ResponseWriter, r *http.Request) {
	a, ok := h.loadMember(w, r, r.PathValue("slug"), false, "organiser")
	if !ok {
		return
	}
	u, err := db.EventStorage(r.Context(), h.database, a.event)
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	sites, ok := h.sites.(hackAdministrationSites)
	if !ok {
		httpx.WriteInternal(w)
		return
	}
	u.FileBytes, err = sites.EventFileBytes(r.Context(), a.event)
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	httpx.WriteJSON(w, 200, u)
}

// ExportEventProjects and EventFileBytes (the *SiteHandler side of Hack
// administration exports) moved to hack_administration_sites.go (cleanup
// 3.2 slice 1c): they stay in handler while this file's *HackHandler
// methods move to internal/hackhttp.
