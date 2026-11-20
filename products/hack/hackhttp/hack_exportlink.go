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

// Event archive links follow the site's export-link pattern. Their signature
// is process-local, and live event membership is checked again at download.
// The shared EXPORT_LINK_TTL_MINUTES knob (limits.ExportLinkTTL) sets their life.
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
	a, ok := h.loadMember(w, r, r.PathValue("slug"), false, role)
	if !ok {
		return
	}
	// loadMember grants a platform admin read access to any event. An archive
	// link is private data, so only actual event membership may mint it.
	if _, err := h.events.EventMember(r.Context(), a.event.ID, a.user.ID); err != nil {
		writeEventNotFound(w)
		return
	}
	claim := HackArchiveClaim{UserID: a.user.ID, EventID: a.event.ID, Slug: a.event.Slug, Kind: kind,
		Expiry: time.Now().Add(h.limits.ExportLinkTTL).Truncate(time.Second).Unix()}
	if kind == "own-team" {
		team, ok := h.participantTeam(w, r, a)
		if !ok {
			return
		}
		claim.TeamID = team.ID
	}
	w.Header().Set("Cache-Control", "no-store")
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"url":        h.publicBaseURL + "/v1/hack/archive?token=" + url.QueryEscape(h.SignHackArchiveClaim(claim)),
		"expires_at": time.Unix(claim.Expiry, 0).UTC().Format(time.RFC3339),
		"expires_in": int(h.limits.ExportLinkTTL.Seconds()),
	})
}

func (h *HackHandler) downloadHackArchive(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	invalid := func() {
		httpx.WriteError(w, http.StatusNotFound, "archive_link_invalid", "this archive link is not valid or has expired")
	}
	c, ok := h.CheckHackArchiveClaim(r.URL.Query().Get("token"), time.Now())
	if !ok {
		invalid()
		return
	}
	ev, err := h.events.EventBySlug(r.Context(), c.Slug)
	if err == nil {
		paperHackEvent(r, ev)
	}
	if err != nil || ev.ID != c.EventID {
		invalid()
		return
	}
	m, err := h.events.EventMember(r.Context(), ev.ID, c.UserID)
	if err != nil {
		invalid()
		return
	}
	teamSlug := ""
	if c.Kind == "projects" {
		if m.Role != "organiser" {
			invalid()
			return
		}
	} else {
		if m.Role != "participant" || !m.TeamID.Valid || m.TeamID.String != c.TeamID {
			invalid()
			return
		}
		status, err := h.members.MemberApprovalStatusLookup(r.Context(), ev.ID, c.UserID)
		if err != nil || status != "approved" {
			invalid()
			return
		}
		team, err := h.teams.TeamByID(r.Context(), c.TeamID)
		if err != nil || team.EventID != ev.ID {
			invalid()
			return
		}
		teamSlug = team.Slug
	}
	sites, ok := h.sites.(hackAdministrationSites)
	if !ok {
		httpx.WriteInternal(w)
		return
	}
	h.exportEventProjects(w, r, sites, ev, teamSlug)
}
