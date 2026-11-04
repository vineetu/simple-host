package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"time"

	"github.com/vsriram/simple-host/internal/app/sites"
	"github.com/vsriram/simple-host/internal/mcp"
)

func importHeaders(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Robots-Tag", "noindex")
}

// The random token (or random status ID), never ambient cookies, is the
// authority. Even so, only the API origin may drive browser import requests.
func (h *ConnectorHandler) importOrigin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		importHeaders(w)
		if h.hackTeams {
			if isAPIPath(r.URL.Path) {
				writeJSON(w, http.StatusNotFound, errorResponse{Error: "imports are not offered here", Code: "not_offered"})
			} else {
				http.NotFound(w, r)
			}
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" && origin != h.issuer {
			writeJSON(w, 403, errorResponse{Error: "open the import link on this site's own address", Code: "origin_not_allowed"})
			return
		}
		next.ServeHTTP(w, r)
	})
}
func (h *ConnectorHandler) importPage(w http.ResponseWriter, r *http.Request) {
	importHeaders(w)
	link, ok := h.mcp.ImportLinkForPage(r.PathValue("token"))
	data := struct {
		Valid          bool
		Site, ThemeURL string
		MaxUploadMB    int
	}{ok, link.Site, link.ThemeURL, h.limits.ImportMaxUploadMB}
	raw, err := staticFiles.ReadFile("static/import.html")
	if err != nil {
		http.Error(w, "could not open import page", 500)
		return
	}
	page, err := withChrome(raw, h.pipeline().chromeDataFor(r, ""))
	if err != nil {
		http.Error(w, "could not open import page", 500)
		return
	}
	tmpl, err := template.New("import").Parse(string(page))
	if err != nil {
		http.Error(w, "could not open import page", 500)
		return
	}
	var b bytes.Buffer
	if err = tmpl.Execute(&b, data); err != nil {
		http.Error(w, "could not open import page", 500)
		return
	}
	page = b.Bytes()

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if !ok {
		w.WriteHeader(404)
	}
	w.Write(stampNonce(r, page))
}
func (h *ConnectorHandler) importStatus(w http.ResponseWriter, r *http.Request) {
	importHeaders(w)
	m := h.imports
	m.Mu.Lock()
	defer m.Mu.Unlock()
	m.SweepLocked()
	rec := m.Records[r.PathValue("import_id")]
	if rec == nil {
		writeJSON(w, 404, errorResponse{Error: "no import with that id"})
		return
	}
	warnings := append([]string{}, rec.Warnings...)
	if len(warnings) > 10 {
		warnings = warnings[:10]
	}
	out := map[string]any{"state": rec.State, "stage": rec.Stage, "done": rec.Done, "total": rec.Total, "message": rec.Message, "pages": 0, "posts": 0, "media": 0, "warnings": warnings}
	if rec.State == "done" {
		out["site_url"] = rec.SiteURL
	}
	if rec.Report != nil {
		out["pages"] = rec.Report.Pages
		out["posts"] = rec.Report.Posts
		out["media"] = rec.Report.MediaCopied
	}
	writeJSON(w, 200, out)
}
func (h *ConnectorHandler) receiveImport(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	link, status := h.mcp.ReserveImport(token)
	if status != 0 {
		msg := "this import link has run out or was used; ask your chat for a new one"
		if status == 409 {
			msg = "already uploading"
		}
		writeJSON(w, status, errorResponse{Error: msg})
		return
	}
	m := h.imports
	select {
	case m.Slots <- struct{}{}:
	default:
		m.Update(link.ID, func(rec *sites.ImportRecord) { rec.State = "waiting"; rec.Started = time.Time{} })
		h.mcp.ReleaseImport(token, false)
		writeJSON(w, 503, errorResponse{Error: "busy, try again in a few minutes"})
		return
	}
	spent := false
	handedOff := false
	defer func() {
		if !handedOff {
			<-m.Slots
		}
	}()

	// A stalled browser must release its admission slot and reservation.
	lim := h.limits
	bodyLimit := int64(lim.ImportMaxUploadMB) << 20
	rc := http.NewResponseController(w)
	_ = rc.SetReadDeadline(time.Now().Add(bodyReadTimeout(bodyLimit)))
	defer rc.SetReadDeadline(time.Time{})
	defer func() {
		if !handedOff && !spent {
			m.Update(link.ID, func(rec *sites.ImportRecord) {
				rec.State = "waiting"
				rec.Started = time.Time{}
				rec.Stage = ""
				rec.Message = "Upload rejected. Try again with your WordPress export."
			})
		}
		if !handedOff {
			h.mcp.ReleaseImport(token, spent)
		}
	}()
	_, revoke, err := link.Mint(r.Context(), false)
	if err != nil {
		spent = true
		h.imports.Update(link.ID, func(rec *sites.ImportRecord) {
			rec.State = "failed"
			rec.Message = "The import link's sign-in has ended. Sign in again and ask your chat for a new link."
			rec.Finished = time.Now()
			rec.Spent = true
		})
		writeJSON(w, 403, errorResponse{Error: "this import link's sign-in has ended (signed out, disconnected or expired); sign in again and ask your chat for a new link", Code: "import_link_signed_out"})
		return
	}
	defer func() {
		if !handedOff {
			revoke()
		}
	}()
	files, err := sites.BeginImportUpload(lim)
	if err != nil {
		writeJSON(w, 500, errorResponse{Error: "could not store the upload; try again"})
		return
	}
	defer func() {
		if !handedOff {
			files.DiscardUpload()
		}
	}()
	m.Update(link.ID, func(rec *sites.ImportRecord) {
		rec.State = "receiving"
		rec.Stage = "Uploading your files"
		rec.Message = rec.Stage
	})

	r.Body = http.MaxBytesReader(w, r.Body, bodyLimit)
	reader, err := r.MultipartReader()
	if err != nil {
		writeJSON(w, 400, errorResponse{Error: "send files using the import page"})
		return
	}
	theme := link.ThemeURL
	photos := true
	count := 0
	reject := func(err error) {
		code := 400
		var big *http.MaxBytesError
		msg := err.Error()
		if errors.As(err, &big) || errors.Is(err, sites.ErrImportTooLarge) {
			code = 413
			msg = fmt.Sprintf("These files are larger than one import can take (%d MB uploaded, %d MB unpacked). Upload the WordPress export on its own: while your old site is online, the import copies its photos from there.", lim.ImportMaxUploadMB, lim.ImportMaxUnpackedMB)
			if strings.Contains(err.Error(), "XML") {
				msg = fmt.Sprintf("The WordPress export is larger than %d MB, the most one import can read, so this site is too large to move this way.", lim.ImportMaxXMLMB)
			}
		}
		writeJSON(w, code, errorResponse{Error: msg})
	}
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			reject(err)
			return
		}
		if part.FileName() == "" {
			body, err := io.ReadAll(io.LimitReader(part, 8193))
			part.Close()
			if err != nil {
				reject(err)
				return
			}
			if len(body) > 8192 {
				reject(errors.New("form field too large"))
				return
			}
			switch part.FormName() {
			case "theme_url":
				// An empty box keeps the address the chat gave with the link.
				if v := strings.TrimSpace(string(body)); v != "" {
					theme = v
				}
			case "copy_photos":
				photos = string(body) != "false" && string(body) != "0"
			}
			continue
		}
		count++
		if count > 10 {
			part.Close()
			reject(errors.New("choose at most 10 files"))
			return
		}
		if err := files.ReceiveUpload(part); err != nil {
			reject(err)
			return
		}
	}
	if len(files.Xml) == 0 {
		reject(errors.New("no WordPress XML export found; download Tools > Export > Export all > Download, then try again"))
		return
	}
	// The link stays reserved while waiting in line and is spent only when
	// conversion starts. Temporary files and credentials now belong to the job.
	m.Update(link.ID, func(rec *sites.ImportRecord) {
		rec.State = "waiting"
		rec.Stage = "Waiting in line"
		rec.Message = rec.Stage
		rec.Started = time.Now()
	})
	writeJSON(w, 202, map[string]any{"import_id": link.ID, "status_url": "/v1/imports/status/" + link.ID})
	handedOff = true
	go m.Run(token, importJobLink(link), revoke, files, theme, photos, h.mcp.ReleaseImport, h.publishImport)
}
func (h *ConnectorHandler) publishImport(ctx context.Context, link sites.ImportLink, archive io.Reader, size int64) (string, error) {
	req, err := http.NewRequestWithContext(ctx, "PUT", "/v1/sites/"+url.PathEscape(link.Site)+"?create=1", archive)
	if err != nil {
		return "", err
	}
	// Internal connector credentials expire in 15 minutes. Refresh after a
	// potentially long upload/queue/conversion, and recheck the connection again.
	key, revokeDeploy, err := link.Mint(ctx, false)
	if err != nil {
		return "", errors.New("The import's sign-in ended before publishing. Sign in again and ask your chat for a new link.")
	}
	defer revokeDeploy()
	apiURL, _ := url.Parse(h.issuer)
	req.Host = apiURL.Host
	req.Header.Set("X-API-Key", key)
	req.Header.Set("X-Skill-Version", h.skillVersion)
	req.Header.Set("Content-Type", "application/gzip")
	req.ContentLength = size
	response := httptest.NewRecorder()
	h.upstream.ServeHTTP(response, req)
	var deploy struct {
		SiteURL       string `json:"site_url"`
		ActiveVersion int    `json:"active_version"`
		Error         string `json:"error"`
	}
	err = json.Unmarshal(response.Body.Bytes(), &deploy)
	if response.Code < 200 || response.Code >= 300 {
		if deploy.Error == "" {
			deploy.Error = "The server refused this site. Ask your chat to check the site's name and size."
		}
		return "", errors.New(deploy.Error)
	}
	if err != nil || deploy.SiteURL == "" {
		return "", errors.New("The server did not return a live address. Ask your chat to check the site.")
	}
	return deploy.SiteURL, nil
}

func importJobLink(link mcp.ImportLink) sites.ImportLink {
	return sites.ImportLink{ID: link.ID, Owner: link.Owner, Site: link.Site, ThemeURL: link.ThemeURL, Expires: link.Expires, Mint: link.Mint}
}
