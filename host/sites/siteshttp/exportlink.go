package siteshttp

import (
	"crypto/rand"
	"database/sql"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/vsriram/simple-host/host/access/auth"
	httpx "github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/sites"
)

// Export links: a short-lived address that downloads one site's export
// without an API key. A person using Simple Host only through a chat app's
// connector has no key to send, and a chat app cannot hand a binary archive
// back inline, so the export_site tool mints one of these and gives the person
// the link to click.
//
// The link encrypts and authenticates (owner, site, expiry) with a per-process key used
// for nothing else. It opens that one site's export and nothing more, for
// exportLinkTTL, and stops working if the site is deleted, changes hands or
// the server restarts. It is not single-use on purpose: chat apps fetch links
// to preview them, and that fetch must not spend the person's download.

// ExportLinkDomain separates these tokens from anything else the key could ever
// be asked to sign.
const ExportLinkDomain = "simple-host site export v1"

func NewExportKey() []byte {
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
	return "https://" + h.SiteDomain
}

// SignExportToken binds a token to one owner, one site and an expiry.
func (h *SiteHandler) SignExportToken(userID, siteID string, expires time.Time) string {
	payload := userID + "." + siteID + "." + strconv.FormatInt(expires.Unix(), 10)
	return httpx.SealLink(h.ExportKey, ExportLinkDomain, []byte(payload))
}

var errExportLink = errors.New("this download link is not valid or has expired")

// CheckExportToken returns the owner and site a token was minted for, if its
// authentication holds and it has not expired.
func (h *SiteHandler) CheckExportToken(token string, now time.Time) (userID, siteID string, err error) {
	raw, err := httpx.OpenLink(h.ExportKey, ExportLinkDomain, token)
	if err != nil {
		return "", "", errExportLink
	}
	payload := string(raw)
	parts := strings.Split(payload, ".")
	if len(parts) != 3 {
		return "", "", errExportLink
	}
	exp, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil {
		return "", "", errExportLink
	}
	left := time.Unix(exp, 0).Sub(now)
	if left <= 0 || left > h.Limits.ExportLinkTTL {
		return "", "", errExportLink
	}
	return parts[0], parts[1], nil
}

// createExportLink POST /v1/sites/{sitename}/export-link — the owner mints a
// download link for their own site's export.
func (h *SiteHandler) createExportLink(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUser(r.Context())
	if user == nil {
		httpx.WriteJSON(w, http.StatusUnauthorized, httpx.ErrorResponse{Error: "unauthorized"})
		return
	}
	name := strings.ToLower(strings.TrimSpace(r.PathValue("sitename")))
	site, err := h.LifecycleSvc().ExportSite(r.Context(), sites.ExportSiteInput{UserID: user.ID, Name: name})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeSiteNotFound(w, name)
			return
		}
		httpx.WriteInternal(w)
		return
	}
	expires := time.Now().Add(h.Limits.ExportLinkTTL).Truncate(time.Second)
	link := h.exportLinkBase() + "/v1/export?token=" + url.QueryEscape(h.SignExportToken(site.UserID, site.ID, expires))
	w.Header().Set("Cache-Control", "no-store")
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"site":       site.Name,
		"url":        link,
		"expires_at": expires.UTC().Format(time.RFC3339),
		"expires_in": int(h.Limits.ExportLinkTTL.Seconds()),
	})
}

// downloadExport GET /v1/export?token=… — the export a link was minted for.
// No API key: the token is the whole authorization, for one site, briefly.
func (h *SiteHandler) downloadExport(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	userID, siteID, err := h.CheckExportToken(r.URL.Query().Get("token"), time.Now())
	if err != nil {
		httpx.WriteJSON(w, http.StatusNotFound, httpx.ErrorResponse{Error: err.Error(), Code: "export_link_invalid"})
		return
	}
	site, err := h.LifecycleSvc().ExportSite(r.Context(), sites.ExportSiteInput{UserID: userID, SiteID: siteID, DownloadLink: true})
	if err != nil {
		var e *sites.Error
		if errors.As(err, &e) && e.Code == "account_suspended" {
			auth.WriteSuspended(w, "", "")
			return
		}
		// Deleted, or no longer this person's: the link dies with the site.
		httpx.WriteJSON(w, http.StatusNotFound, httpx.ErrorResponse{Error: errExportLink.Error(), Code: "export_link_invalid"})
		return
	}
	h.writeExport(w, r, site)
}
