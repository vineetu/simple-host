package hackhttp

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"time"

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

func (h *HackHandler) SignHackArchiveClaim(c HackArchiveClaim) string {
	raw, _ := json.Marshal(c)
	return httpx.SealLink(h.archiveLinkKey, HackArchiveLinkDomain, raw)
}

func (h *HackHandler) CheckHackArchiveClaim(token string, now time.Time) (HackArchiveClaim, bool) {
	var c HackArchiveClaim
	raw, err := httpx.OpenLink(h.archiveLinkKey, HackArchiveLinkDomain, token)
	if err != nil || json.Unmarshal(raw, &c) != nil {
		return c, false
	}
	left := time.Unix(c.Expiry, 0).Sub(now)
	if left <= 0 || left > h.limits.ExportLinkTTL || !sites.UUIDShape.MatchString(c.UserID) || !sites.UUIDShape.MatchString(c.EventID) ||
		(c.Kind != "projects" && c.Kind != "own-team") || (c.Kind == "own-team") != (c.TeamID != "") ||
		(c.TeamID != "" && !sites.UUIDShape.MatchString(c.TeamID)) || c.Slug == "" {
		return c, false
	}
	return c, true
}

func (h *HackHandler) createHackArchiveLink(w http.ResponseWriter, r *http.Request) {
	kind := "projects"
	role := "organiser"
	if strings.HasSuffix(r.URL.Path, "/own-team-link") {
		kind, role = "own-team", "participant"
	}
	a, ok := loadMember(w, r, h.members, r.PathValue("slug"), false, role)
	if !ok {
		return
	}
	// loadMember grants a platform admin read access to any event.
	if _, err := h.events.EventMember(r.Context(), a.event.ID, a.user.ID); err != nil {
		writeEventNotFound(w)
		return
	}
	claim := HackArchiveClaim{UserID: a.user.ID, EventID: a.event.ID, Slug: a.event.Slug, Kind: kind,
		Expiry: time.Now().Add(h.limits.ExportLinkTTL).Truncate(time.Second).Unix()}
	if kind == "own-team" {
		team, ok := participantTeam(w, r, h.teams, a)
		if !ok {
			return
		}
		claim.TeamID = team.ID
	}
	w.Header().Set("Cache-Control", "no-store")
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"url":        h.links.publicBaseURL + "/v1/hack/archive?token=" + url.QueryEscape(h.SignHackArchiveClaim(claim)),
		"expires_at": time.Unix(claim.Expiry, 0).UTC().Format(time.RFC3339),
		"expires_in": int(h.limits.ExportLinkTTL.Seconds()),
	})
}

func (h *HackHandler) downloadHackArchive(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	c, ok := h.CheckHackArchiveClaim(r.URL.Query().Get("token"), time.Now())
	if !ok {
		writeArchiveLinkInvalid(w)
		return
	}
	ev, teamSlug, err := h.members.ArchiveHolder(r.Context(), c.Slug, c.EventID, c.UserID, c.TeamID)
	if ev.ID != "" {
		paperHackEvent(r, ev)
	}
	if err != nil {
		writeArchiveLinkInvalid(w)
		return
	}
	sites, ok := h.links.hooks.(hackAdministrationSites)
	if !ok {
		httpx.WriteInternal(w)
		return
	}
	h.exportEventProjects(w, r, sites, ev, teamSlug)
}

func writeArchiveLinkInvalid(w http.ResponseWriter) {
	httpx.WriteError(w, http.StatusNotFound, "archive_link_invalid", "this archive link is not valid or has expired")
}
