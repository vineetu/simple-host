package exports

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"time"

	core "github.com/vsriram/simple-host/products/hack/core"

	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/rules/sites"
)

// Event archive links follow the site's export-link pattern.
const HackArchiveLinkDomain = "simple-hack event archive v1"

type HackArchiveClaim struct {
	UserID  string `json:"u"`
	EventID string `json:"e"`
	Slug    string `json:"s"`
	TeamID  string `json:"t,omitempty"`
	Kind    string `json:"k"`
	Expiry  int64  `json:"x"`
}

func (h *Handler) SignHackArchiveClaim(c HackArchiveClaim) string {
	raw, _ := json.Marshal(c)
	return httpx.SealLink(h.archiveLinkKey, HackArchiveLinkDomain, raw)
}

func (h *Handler) CheckHackArchiveClaim(token string, now time.Time) (HackArchiveClaim, bool) {
	var c HackArchiveClaim
	raw, err := httpx.OpenLink(h.archiveLinkKey, HackArchiveLinkDomain, token)
	if err != nil || json.Unmarshal(raw, &c) != nil {
		return c, false
	}
	left := time.Unix(c.Expiry, 0).Sub(now)
	if left <= 0 || left > h.Limits.ExportLinkTTL || !sites.UUIDShape.MatchString(c.UserID) || !sites.UUIDShape.MatchString(c.EventID) ||
		(c.Kind != "projects" && c.Kind != "own-team") || (c.Kind == "own-team") != (c.TeamID != "") ||
		(c.TeamID != "" && !sites.UUIDShape.MatchString(c.TeamID)) || c.Slug == "" {
		return c, false
	}
	return c, true
}

func (h *Handler) createHackArchiveLink(w http.ResponseWriter, r *http.Request) {
	kind := "projects"
	role := "organiser"
	if strings.HasSuffix(r.URL.Path, "/own-team-link") {
		kind, role = "own-team", "participant"
	}
	a, ok := core.LoadMember(w, r, h.Members, r.PathValue("slug"), false, role)
	if !ok {
		return
	}
	// loadMember grants a platform admin read access to any event.
	if _, err := h.Events.EventMember(r.Context(), a.Event.ID, a.User.ID); err != nil {
		core.WriteEventNotFound(w)
		return
	}
	claim := HackArchiveClaim{UserID: a.User.ID, EventID: a.Event.ID, Slug: a.Event.Slug, Kind: kind,
		Expiry: time.Now().Add(h.Limits.ExportLinkTTL).Truncate(time.Second).Unix()}
	if kind == "own-team" {
		team, ok := core.ParticipantTeam(w, r, h.Teams, a)
		if !ok {
			return
		}
		claim.TeamID = team.ID
	}
	w.Header().Set("Cache-Control", "no-store")
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"url":        h.Links.PublicBaseURL + "/v1/hack/archive?token=" + url.QueryEscape(h.SignHackArchiveClaim(claim)),
		"expires_at": time.Unix(claim.Expiry, 0).UTC().Format(time.RFC3339),
		"expires_in": int(h.Limits.ExportLinkTTL.Seconds()),
	})
}

func (h *Handler) downloadHackArchive(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	c, ok := h.CheckHackArchiveClaim(r.URL.Query().Get("token"), time.Now())
	if !ok {
		writeArchiveLinkInvalid(w)
		return
	}
	ev, teamSlug, err := h.ArchiveHolder(r.Context(), c.Slug, c.EventID, c.UserID, c.TeamID)
	if ev.ID != "" {
		core.PaperEvent(r, ev)
	}
	if err != nil {
		writeArchiveLinkInvalid(w)
		return
	}
	sites, ok := h.Links.Hooks.(hackAdministrationSites)
	if !ok {
		httpx.WriteInternal(w)
		return
	}
	h.exportEventProjects(w, r, sites, ev, teamSlug)
}

func writeArchiveLinkInvalid(w http.ResponseWriter) {
	httpx.WriteError(w, http.StatusNotFound, "archive_link_invalid", "this archive link is not valid or has expired")
}
