package handler

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"database/sql"
	"encoding/csv"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strconv"

	"github.com/vsriram/simple-host/internal/db"
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
	mux.Handle("GET /v1/hack/events/{slug}/usage", wrap(h.eventUsage))
}

func (h *HackHandler) administrationWrite(w http.ResponseWriter, r *http.Request) (hackAccess, bool) {
	a, ok := h.loadMember(w, r, r.PathValue("slug"), true, "organiser")
	if ok && a.event.Stage == "archived" {
		writeHackErr(w, 409, "event_closed", "an ended event cannot be changed")
		return a, false
	}
	return a, ok
}

func (h *HackHandler) createOrganiserInvite(w http.ResponseWriter, r *http.Request) {
	a, ok := h.administrationWrite(w, r)
	if !ok {
		return
	}
	code, err := randomHackCode(32)
	if err != nil {
		writeInternal(w)
		return
	}
	expires, err := db.SetOrganiserInvite(r.Context(), h.database, a.event.ID, a.user.ID, db.HashAPIKey(code))
	if err != nil {
		writeInternal(w)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 201, map[string]any{"url": h.publicBaseURL + "/organiser/" + code, "expires_at": rfc3339Time(expires)})
}

func (h *HackHandler) revokeOrganiserInvite(w http.ResponseWriter, r *http.Request) {
	a, ok := h.administrationWrite(w, r)
	if !ok {
		return
	}
	if err := db.DeleteOrganiserInvite(r.Context(), h.database, a.event.ID); err != nil {
		writeInternal(w)
		return
	}
	w.WriteHeader(204)
}

func (h *HackHandler) getOrganiserInvite(w http.ResponseWriter, r *http.Request) {
	if h.rateLimited(w, r, "") {
		return
	}
	ev, err := db.OrganiserInviteEvent(r.Context(), h.database, db.HashAPIKey(normalizeHackCode(r.PathValue("code"))), false)
	if errors.Is(err, sql.ErrNoRows) {
		writeHackErr(w, 404, "invalid_code", "invalid or expired invitation")
		return
	}
	if err != nil {
		writeInternal(w)
		return
	}
	if ev.TakenDown() {
		writeHackErr(w, 410, "event_taken_down", "this event has been taken down")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, map[string]any{"slug": ev.Slug, "title": ev.Title, "stage": ev.Stage, "organiser_name": ev.OrganiserName, "organisation": ev.Organisation, "role": "organiser", "joinable": ev.Stage != "archived", "coc_default": HackDefaultCoC, "coc_text": ev.CocText})
}

func (h *HackHandler) acceptOrganiserInvite(w http.ResponseWriter, r *http.Request) {
	user := hackNeedUser(w, r)
	if user == nil {
		return
	}
	if h.rateLimited(w, r, user.ID) {
		return
	}
	if user.IsAdmin {
		writeHackErr(w, 409, "admin_cannot_join", "the platform admin cannot join an event")
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
		writeHackErr(w, 400, "coc_required", "you must accept the code of conduct")
		return
	}
	name, ok := checkHackLine(w, req.DisplayName, "display_name", 1, 100)
	if !ok {
		return
	}
	tx, err := h.database.BeginTx(r.Context(), nil)
	if err != nil {
		writeInternal(w)
		return
	}
	defer tx.Rollback()
	ev, err := db.OrganiserInviteEvent(r.Context(), tx, db.HashAPIKey(normalizeHackCode(r.PathValue("code"))), true)
	if errors.Is(err, sql.ErrNoRows) {
		writeHackErr(w, 404, "invalid_code", "invalid or expired invitation")
		return
	}
	if err != nil {
		writeInternal(w)
		return
	}
	if ev.TakenDown() {
		writeHackErr(w, 403, "event_taken_down", "this event has been taken down")
		return
	}
	if ev.Stage == "archived" {
		writeHackErr(w, 409, "event_closed", "an ended event cannot be changed")
		return
	}
	if _, err = db.GetEventMember(r.Context(), tx, ev.ID, user.ID); err == nil {
		writeHackErr(w, 409, "already_member", "you already belong to this event; roles cannot be changed through an invitation")
		return
	} else if !errors.Is(err, sql.ErrNoRows) {
		writeInternal(w)
		return
	}
	member, err := db.InsertEventMember(r.Context(), tx, ev.ID, user.ID, "organiser", name)
	if dbUnique(err) {
		writeHackErr(w, 409, "already_member", "you already belong to this event")
		return
	}
	if err != nil {
		writeInternal(w)
		return
	}
	if err = db.DeleteOrganiserInvite(r.Context(), tx, ev.ID); err != nil {
		writeInternal(w)
		return
	}
	if err = tx.Commit(); err != nil {
		writeInternal(w)
		return
	}
	writeJSON(w, 200, h.eventView(r.Context(), ev, member, true))
}

func (h *HackHandler) removeOrganiser(w http.ResponseWriter, r *http.Request) {
	a, ok := h.administrationWrite(w, r)
	if !ok {
		return
	}
	id := r.PathValue("user_id")
	if !uuidShape.MatchString(id) {
		writeEventNotFound(w)
		return
	}
	if a.event.CreatedBy.Valid && id == a.event.CreatedBy.String {
		writeHackErr(w, 409, "primary_organiser", "the event creator cannot be removed")
		return
	}
	tx, err := h.database.BeginTx(r.Context(), nil)
	if err != nil {
		writeInternal(w)
		return
	}
	defer tx.Rollback()
	if err := db.RemoveCoOrganiser(r.Context(), tx, a.event.ID, a.user.ID, id); errors.Is(err, sql.ErrNoRows) {
		writeEventNotFound(w)
		return
	} else if err != nil {
		writeInternal(w)
		return
	}
	if err := tx.Commit(); err != nil {
		writeInternal(w)
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
	name, ok := checkHackLine(w, stripInvisible(req.Name), "team_name", 1, 80)
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
		writeHackErr(w, 409, "team_name_taken", "another team has that name")
		return
	}
	if err != nil {
		writeInternal(w)
		return
	}
	writeJSON(w, 200, map[string]any{"name": name, "slug": slug})
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
			writeInternal(w)
			return
		}
		rows = append(rows, []string{"name", "email", "team", "team_slug", "joined_at", "coc_accepted_at"})
		for _, p := range people {
			if p.Role == "participant" && p.ApprovalStatus == "approved" {
				accepted := ""
				if p.CocAcceptedAt.Valid {
					accepted = rfc3339Time(p.CocAcceptedAt.Time)
				}
				rows = append(rows, []string{p.DisplayName, p.Email, p.TeamName.String, p.TeamSlug.String, rfc3339Time(p.JoinedAt), accepted})
			}
		}
	case "teams.csv":
		teams, err := db.ListEventTeams(r.Context(), h.database, a.event.ID)
		if err != nil {
			writeInternal(w)
			return
		}
		people, err := db.ListEventPeople(r.Context(), h.database, a.event.ID)
		if err != nil {
			writeInternal(w)
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
			writeInternal(w)
			return
		}
		teams, err := db.ListEventTeams(r.Context(), h.database, a.event.ID)
		if err != nil {
			writeInternal(w)
			return
		}
		byID := map[string]db.EventTeam{}
		for _, t := range teams {
			byID[t.ID] = t
		}
		rows = append(rows, []string{"team", "team_slug", "title", "tagline", "description", "video_url", "code_url", "has_screenshot", "updated_at"})
		for _, e := range entries {
			t := byID[e.TeamID]
			rows = append(rows, []string{t.Name, t.Slug, e.Title, e.Tagline, e.Description, e.VideoURL, e.CodeURL, strconv.FormatBool(e.HasScreenshot), rfc3339Time(e.UpdatedAt)})
		}
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s-%s"`, a.event.Slug, kind))
	cw := csv.NewWriter(w)
	defer cw.Flush()
	for _, row := range rows {
		for i := range row {
			row[i] = csvSafe(row[i])
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
		writeInternal(w)
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
		writeInternal(w)
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
		writeInternal(w)
		return
	}
	sites, ok := h.sites.(hackAdministrationSites)
	if !ok {
		writeInternal(w)
		return
	}
	u.FileBytes, err = sites.EventFileBytes(r.Context(), a.event)
	if err != nil {
		writeInternal(w)
		return
	}
	writeJSON(w, 200, u)
}

// ExportEventProjects is called only after event/own-team authorization. It
// selects this event's teams and holding account; no instance-wide export is used.
func (h *SiteHandler) ExportEventProjects(w http.ResponseWriter, r *http.Request, ev db.Event, ownSlug string) {
	teams, err := db.ListEventTeams(r.Context(), h.database, ev.ID)
	if err != nil {
		writeInternal(w)
		return
	}
	var sites []db.Site
	for _, team := range teams {
		if ownSlug != "" && team.Slug != ownSlug {
			continue
		}
		site, err := db.GetSiteByUser(r.Context(), h.database, ev.AccountID, team.Slug)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			writeInternal(w)
			return
		}
		sites = append(sites, site)
	}
	if ownSlug != "" && len(sites) == 0 {
		writeHackErr(w, 404, "no_project", "your team has not published a project")
		return
	}
	name := ev.Slug + "-projects"
	if ownSlug != "" {
		name = ev.Slug + "-" + ownSlug
	}
	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.tar.gz"`, name))
	gz := gzip.NewWriter(w)
	defer gz.Close()
	tw := tar.NewWriter(gz)
	defer tw.Close()
	for _, site := range sites {
		// Walk an actual version directory: WalkDir does not follow a root symlink.
		current := h.disk.VersionDir(site.UserID, site.Name, site.ActiveVersion)
		if err := h.writeSiteArchive(r.Context(), tarPut(tw), site.Name, site.ID, current); err != nil {
			return
		}
	}
}

func (h *SiteHandler) EventFileBytes(ctx context.Context, ev db.Event) (int64, error) {
	var total int64
	for _, area := range []string{"by-id", "deleted"} {
		root := filepath.Join(h.disk.DataDir(), area, ev.AccountID)
		err := filepath.WalkDir(root, func(_ string, d fs.DirEntry, err error) error {
			if errors.Is(err, os.ErrNotExist) {
				return nil
			}
			if err != nil {
				return err
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if d.Type().IsRegular() {
				info, e := d.Info()
				if e != nil {
					return e
				}
				total += info.Size()
			}
			return nil
		})
		if err != nil {
			return 0, err
		}
	}
	return total, nil
}
