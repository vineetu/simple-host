package handler

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/vsriram/simple-host/internal/auth"
	"github.com/vsriram/simple-host/internal/db"
)

// Export links: a short-lived address that downloads one site's export
// without an API key. A person using Simple Host only through a chat app's
// connector has no key to send, and a chat app cannot hand a binary archive
// back inline, so the export_site tool mints one of these and gives the person
// the link to click.
//
// The link is an HMAC over (owner, site, expiry) with a per-process key used
// for nothing else. It opens that one site's export and nothing more, for
// exportLinkTTL, and stops working if the site is deleted, changes hands or
// the server restarts. It is not single-use on purpose: chat apps fetch links
// to preview them, and that fetch must not spend the person's download.

// exportLinkDomain separates these MACs from anything else the key could ever
// be asked to sign.
const exportLinkDomain = "simple-host site export v1"

func newExportKey() []byte {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		panic("export links: no randomness: " + err.Error())
	}
	return key
}

// SetPublicBaseURL is the apex address export links are minted on
// (PUBLIC_BASE_URL, e.g. https://simple-host.app).
func (h *SiteHandler) SetPublicBaseURL(base string) {
	h.publicBaseURL = strings.TrimRight(strings.TrimSpace(base), "/")
}

func (h *SiteHandler) exportLinkBase() string {
	if h.publicBaseURL != "" {
		return h.publicBaseURL
	}
	return "https://" + h.siteDomain
}

func (h *SiteHandler) exportMAC(payload string) []byte {
	mac := hmac.New(sha256.New, h.exportKey)
	mac.Write([]byte(exportLinkDomain))
	mac.Write([]byte{0})
	mac.Write([]byte(payload))
	return mac.Sum(nil)
}

// signExportToken binds a token to one owner, one site and an expiry.
func (h *SiteHandler) signExportToken(userID, siteID string, expires time.Time) string {
	payload := userID + "." + siteID + "." + strconv.FormatInt(expires.Unix(), 10)
	return base64.RawURLEncoding.EncodeToString([]byte(payload)) + "." +
		base64.RawURLEncoding.EncodeToString(h.exportMAC(payload))
}

var errExportLink = errors.New("this download link is not valid or has expired")

// checkExportToken returns the owner and site a token was minted for, if its
// signature holds and it has not expired.
func (h *SiteHandler) checkExportToken(token string, now time.Time) (userID, siteID string, err error) {
	encPayload, encMAC, ok := strings.Cut(token, ".")
	if !ok {
		return "", "", errExportLink
	}
	rawPayload, err1 := base64.RawURLEncoding.DecodeString(encPayload)
	gotMAC, err2 := base64.RawURLEncoding.DecodeString(encMAC)
	if err1 != nil || err2 != nil {
		return "", "", errExportLink
	}
	payload := string(rawPayload)
	if !hmac.Equal(gotMAC, h.exportMAC(payload)) {
		return "", "", errExportLink
	}
	parts := strings.Split(payload, ".")
	if len(parts) != 3 {
		return "", "", errExportLink
	}
	exp, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil {
		return "", "", errExportLink
	}
	left := time.Unix(exp, 0).Sub(now)
	if left <= 0 || left > exportLinkTTL() {
		return "", "", errExportLink
	}
	return parts[0], parts[1], nil
}

// createExportLink POST /v1/sites/{sitename}/export-link — the owner mints a
// download link for their own site's export.
func (h *SiteHandler) createExportLink(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUser(r.Context())
	if user == nil {
		writeJSON(w, http.StatusUnauthorized, errorResponse{Error: "unauthorized"})
		return
	}
	name := strings.ToLower(strings.TrimSpace(r.PathValue("sitename")))
	site, err := db.GetSiteByUser(r.Context(), h.database, user.ID, name)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeSiteNotFound(w, name)
			return
		}
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	expires := time.Now().Add(exportLinkTTL()).Truncate(time.Second)
	link := h.exportLinkBase() + "/v1/export?token=" + url.QueryEscape(h.signExportToken(site.UserID, site.ID, expires))
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{
		"site":       site.Name,
		"url":        link,
		"expires_at": expires.UTC().Format(time.RFC3339),
		"expires_in": int(exportLinkTTL().Seconds()),
	})
}

// downloadExport GET /v1/export?token=… — the export a link was minted for.
// No API key: the token is the whole authorization, for one site, briefly.
func (h *SiteHandler) downloadExport(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	userID, siteID, err := h.checkExportToken(r.URL.Query().Get("token"), time.Now())
	if err != nil {
		writeJSON(w, http.StatusNotFound, errorResponse{Error: err.Error(), Code: "export_link_invalid"})
		return
	}
	site, err := db.GetSiteByID(r.Context(), h.database, siteID)
	if err != nil || site.UserID != userID || site.Deleted {
		// Deleted, or no longer this person's: the link dies with the site.
		writeJSON(w, http.StatusNotFound, errorResponse{Error: errExportLink.Error(), Code: "export_link_invalid"})
		return
	}
	// A link minted before the person was suspended stops with them. (A site
	// taken down on its own still exports to its owner, as the keyed route does.)
	if site.OwnerSuspended {
		writeAccountSuspended(w)
		return
	}
	h.writeExport(w, r, site)
}
