package handler

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/vsriram/simple-host/internal/db"
)

// Event archive links follow the site's export-link pattern. Their signature
// is process-local, and live event membership is checked again at download.
const hackArchiveLinkDomain = "simple-hack event archive v1"

// The shared EXPORT_LINK_TTL_MINUTES knob also controls event archive links.
func hackArchiveLinkTTL() time.Duration { return exportLinkTTL() }

type hackArchiveClaim struct {
	UserID  string `json:"u"`
	EventID string `json:"e"`
	Slug    string `json:"s"`
	TeamID  string `json:"t,omitempty"`
	Kind    string `json:"k"`
	Expiry  int64  `json:"x"`
}

func (h *HackHandler) archiveMAC(payload string) []byte {
	mac := hmac.New(sha256.New, h.archiveLinkKey)
	mac.Write([]byte(hackArchiveLinkDomain))
	mac.Write([]byte{0})
	mac.Write([]byte(payload))
	return mac.Sum(nil)
}

func (h *HackHandler) signHackArchiveClaim(c hackArchiveClaim) string {
	raw, _ := json.Marshal(c)
	payload := base64.RawURLEncoding.EncodeToString(raw)
	return payload + "." + base64.RawURLEncoding.EncodeToString(h.archiveMAC(payload))
}

func (h *HackHandler) checkHackArchiveClaim(token string, now time.Time) (hackArchiveClaim, bool) {
	var c hackArchiveClaim
	payload, mac, ok := strings.Cut(token, ".")
	if !ok || strings.Contains(mac, ".") {
		return c, false
	}
	got, err := base64.RawURLEncoding.DecodeString(mac)
	if err != nil || !hmac.Equal(got, h.archiveMAC(payload)) {
		return c, false
	}
	raw, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil || json.Unmarshal(raw, &c) != nil {
		return c, false
	}
	left := time.Unix(c.Expiry, 0).Sub(now)
	if left <= 0 || left > hackArchiveLinkTTL() || !uuidShape.MatchString(c.UserID) || !uuidShape.MatchString(c.EventID) ||
		(c.Kind != "projects" && c.Kind != "own-team") || (c.Kind == "own-team") != (c.TeamID != "") ||
		(c.TeamID != "" && !uuidShape.MatchString(c.TeamID)) || c.Slug == "" {
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
	if _, err := db.GetEventMember(r.Context(), h.database, a.event.ID, a.user.ID); err != nil {
		writeEventNotFound(w)
		return
	}
	claim := hackArchiveClaim{UserID: a.user.ID, EventID: a.event.ID, Slug: a.event.Slug, Kind: kind,
		Expiry: time.Now().Add(hackArchiveLinkTTL()).Truncate(time.Second).Unix()}
	if kind == "own-team" {
		team, ok := h.participantTeam(w, r, a)
		if !ok {
			return
		}
		claim.TeamID = team.ID
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{
		"url":        h.publicBaseURL + "/v1/hack/archive?token=" + url.QueryEscape(h.signHackArchiveClaim(claim)),
		"expires_at": time.Unix(claim.Expiry, 0).UTC().Format(time.RFC3339),
		"expires_in": int(hackArchiveLinkTTL().Seconds()),
	})
}

func (h *HackHandler) downloadHackArchive(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	invalid := func() {
		writeHackErr(w, http.StatusNotFound, "archive_link_invalid", "this archive link is not valid or has expired")
	}
	c, ok := h.checkHackArchiveClaim(r.URL.Query().Get("token"), time.Now())
	if !ok {
		invalid()
		return
	}
	ev, err := db.GetEventBySlug(r.Context(), h.database, c.Slug)
	if err != nil || ev.ID != c.EventID {
		invalid()
		return
	}
	m, err := db.GetEventMember(r.Context(), h.database, ev.ID, c.UserID)
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
		status, err := db.MemberApprovalStatus(r.Context(), h.database, ev.ID, c.UserID)
		if err != nil || status != "approved" {
			invalid()
			return
		}
		team, err := db.GetEventTeamByID(r.Context(), h.database, c.TeamID)
		if err != nil || team.EventID != ev.ID {
			invalid()
			return
		}
		teamSlug = team.Slug
	}
	sites, ok := h.sites.(hackAdministrationSites)
	if !ok {
		writeInternal(w)
		return
	}
	sites.ExportEventProjects(w, r, ev, teamSlug)
}
