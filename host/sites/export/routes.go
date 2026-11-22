package export

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/vsriram/simple-host/host/access/auth"
	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/model"
	"github.com/vsriram/simple-host/host/sites"
	"github.com/vsriram/simple-host/host/sites/sitesroute"
)

// routes are the export handlers on the shared route state.
type routes struct{ *sitesroute.Handler }

// Register mounts the exports: the operator's whole-instance archive, one
// site's archive (behind exportLimiter, shared with the account export), the
// signed export link and its download.
func Register(mux *http.ServeMux, h *sitesroute.Handler, authMiddleware, noticeMiddleware func(http.Handler) http.Handler, exportLimiter *httpx.RateLimiter) {
	e := routes{h}
	mux.Handle("GET /v1/admin/export.tar.gz", authMiddleware(http.HandlerFunc(e.exportAll)))
	// Take your work with you. An event box is destroyed when the event ends and
	// nothing is backed up, so the only honest answer is to make leaving easy.
	mux.Handle("GET /v1/sites/{sitename}/export.tar.gz", authMiddleware(httpx.RateLimitByIP(exportLimiter, http.HandlerFunc(e.exportSite))))
	// The same export behind a 10-minute signed link, for people with no API
	// key (chat-app connector users): minted by the owner, opened by a click.
	mux.Handle("POST /v1/sites/{sitename}/export-link", authMiddleware(h.CreateIdempotency.Wrap(noticeMiddleware(http.HandlerFunc(e.createExportLink)))))
	mux.Handle("GET /v1/export", httpx.RateLimitByIP(exportLimiter, http.HandlerFunc(e.downloadExport)))
}

// exportSite streams one site (files and saved data) as a .tar.gz. An event box is destroyed
// when the event ends with nothing backed up, so taking the site away must be trivial.
// Streamed, not assembled: a site can hold 100 MB and an event box has 1 GB of RAM.
func (h routes) exportSite(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUser(r.Context())
	if user == nil {
		httpx.WriteJSON(w, http.StatusUnauthorized, httpx.ErrorResponse{Error: "unauthorized"})
		return
	}
	name := strings.ToLower(strings.TrimSpace(r.PathValue("sitename")))
	site, err := Service{Service: h.LifecycleSvc()}.ExportSite(r.Context(), ExportSiteInput{UserID: user.ID, Name: name})
	if err != nil {
		httpx.WriteJSON(w, http.StatusNotFound, httpx.ErrorResponse{Error: "not found"})
		return
	}

	h.writeExport(w, r, site)
}

// writeExport streams the archive for one site, already authorized. Shared by
// the keyed route and the short-lived download link (exportlink.go).
func (h routes) writeExport(w http.ResponseWriter, r *http.Request, site model.Site) {
	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.tar.gz"`, site.Name))

	gz := gzip.NewWriter(w)
	tw := tar.NewWriter(gz)
	if err := h.writeSiteTar(r.Context(), tw, site.Name, site); err != nil {
		panic(http.ErrAbortHandler)
	}
	FinishSiteTar(tw, gz)
}

// exportAll streams every site on the instance as one .tar.gz, each under
// <handle>/<site>/ in the same layout as the per-site export. For an organiser
// who has to delete the box today and wants to keep every entry and what it
// saved. Admin only.
func (h routes) exportAll(w http.ResponseWriter, r *http.Request) {
	if !auth.Admin(w, r) {
		return
	}
	sites, err := Service{Service: h.LifecycleSvc()}.ExportSites(r.Context())
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="all-entries-%s.tar.gz"`, time.Now().UTC().Format("2006-01-02")))
	gz := gzip.NewWriter(w)
	tw := tar.NewWriter(gz)
	for _, s := range sites {
		owner := s.OwnerHandle
		if owner == "" {
			owner = s.OwnerUsername
		}
		// Owner names are handles or account names; keep the archive path to
		// one plain segment whatever they hold.
		owner = strings.NewReplacer("/", "_", "\\", "_", "..", "_").Replace(owner)
		if err := h.writeSiteTar(r.Context(), tw, owner+"/"+s.Name, s); err != nil {
			panic(http.ErrAbortHandler)
		}
	}
	FinishSiteTar(tw, gz)
}

// FinishSiteTar finalizes only complete exports. Aborting without closing the
// archive writers prevents a failed export from becoming a valid partial backup.
func FinishSiteTar(tw *tar.Writer, gz *gzip.Writer) {
	if err := tw.Close(); err != nil {
		panic(http.ErrAbortHandler)
	}
	if err := gz.Close(); err != nil {
		panic(http.ErrAbortHandler)
	}
}

func (h routes) writeSiteTar(ctx context.Context, tw *tar.Writer, prefix string, site model.Site) error {
	return h.SiteArchive().WriteArchive(ctx, sites.TarPut(tw), prefix, site.ID, sites.FileVersion{UserID: site.UserID, SiteName: site.Name})
}

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

func (h routes) exportLinkBase() string {
	if h.PublicBaseURL != "" {
		return h.PublicBaseURL
	}
	return "https://" + h.SiteDomain
}

func SignExportToken(h *sitesroute.Handler, userID, siteID string, expires time.Time) string {
	payload := userID + "." + siteID + "." + strconv.FormatInt(expires.Unix(), 10)
	return httpx.SealLink(h.ExportKey, ExportLinkDomain, []byte(payload))
}

var errExportLink = errors.New("this download link is not valid or has expired")

// CheckExportToken returns the owner and site a token was minted for, if its
// authentication holds and it has not expired.
func CheckExportToken(h *sitesroute.Handler, token string, now time.Time) (userID, siteID string, err error) {
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

func (h routes) createExportLink(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUser(r.Context())
	if user == nil {
		httpx.WriteJSON(w, http.StatusUnauthorized, httpx.ErrorResponse{Error: "unauthorized"})
		return
	}
	name := strings.ToLower(strings.TrimSpace(r.PathValue("sitename")))
	site, err := Service{Service: h.LifecycleSvc()}.ExportSite(r.Context(), ExportSiteInput{UserID: user.ID, Name: name})
	if err != nil {
		sitesroute.WriteSitesError(w, err, name)
		return
	}
	expires := time.Now().Add(h.Limits.ExportLinkTTL).Truncate(time.Second)
	link := h.exportLinkBase() + "/v1/export?token=" + url.QueryEscape(SignExportToken(h.Handler, site.UserID, site.ID, expires))
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
func (h routes) downloadExport(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	userID, siteID, err := CheckExportToken(h.Handler, r.URL.Query().Get("token"), time.Now())
	if err != nil {
		httpx.WriteJSON(w, http.StatusNotFound, httpx.ErrorResponse{Error: err.Error(), Code: "export_link_invalid"})
		return
	}
	site, err := Service{Service: h.LifecycleSvc()}.ExportSite(r.Context(), ExportSiteInput{UserID: userID, SiteID: siteID, DownloadLink: true})
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
