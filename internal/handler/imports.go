package handler

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/vsriram/simple-host/internal/config"
	"github.com/vsriram/simple-host/internal/mcp"
	"github.com/vsriram/simple-host/internal/wpimport"
)

var convertImport = wpimport.Convert

type importRecord struct {
	owner, site                         string
	created, expires, started, finished time.Time
	state, stage, message, siteURL      string
	done, total                         int
	report                              *wpimport.Report
	warnings                            []string
	spent                               bool
}
type importManager struct {
	mu             sync.Mutex
	records        map[string]*importRecord
	running, slots chan struct{}
}

func newImportManager() *importManager {
	n := config.Active().ImportMaxConcurrent
	return &importManager{records: map[string]*importRecord{}, running: make(chan struct{}, n), slots: make(chan struct{}, n+5)}
}
func (m *importManager) sweepLocked() {
	for id, r := range m.records {
		if (!r.finished.IsZero() && time.Since(r.finished) > 24*time.Hour) ||
			(r.state == "waiting" && r.started.IsZero() && !r.expires.After(time.Now())) {
			delete(m.records, id)
		}
	}
}
func (m *importManager) created(link mcp.ImportLink) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sweepLocked()
	var ids []string
	for id, r := range m.records {
		if r.owner == link.Owner {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return m.records[ids[i]].finished.Before(m.records[ids[j]].finished) })
	if len(ids) >= 20 {
		// Usable links and active jobs always retain their reports.
		removed := false
		for _, id := range ids {
			r := m.records[id]
			if !r.finished.IsZero() && (r.spent || !r.expires.After(time.Now())) {
				delete(m.records, id)
				removed = true
				break
			}
		}
		if !removed {
			return errors.New("there are already 20 imports in progress for this account")
		}
	}
	m.records[link.ID] = &importRecord{owner: link.Owner, site: link.Site, created: time.Now(), expires: link.Expires, state: "waiting", message: "Open the link and upload your WordPress export."}
	return nil
}
func (m *importManager) receiving(id string) {
	m.update(id, func(rec *importRecord) { rec.state = "receiving"; rec.started = time.Now(); rec.finished = time.Time{} })
}
func (m *importManager) update(id string, fn func(*importRecord)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if r := m.records[id]; r != nil {
		fn(r)
	}
}
func importTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}
func (m *importManager) report(owner, id string) (map[string]any, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sweepLocked()
	r := m.records[id]
	if r == nil || r.owner != owner {
		return nil, errors.New("no import with that id for this account")
	}
	steps := []string{}
	if r.report != nil {
		for _, f := range r.report.Features {
			if f.Status == wpimport.StatusNeedsSetup {
				setup, _ := json.Marshal(f.Setup)
				first := "/"
				if len(f.Pages) > 0 {
					first = f.Pages[0]
				}
				steps = append(steps, fmt.Sprintf("Switch on %s on %s: run these calls in order, one at a time (each needs the one before it): %s; then check %s", f.Kind, strings.Join(f.Pages, ", "), setup, first))
				if f.Kind == "comments" || f.Kind == "guestbook" {
					steps = append(steps, "For "+f.Kind+": follow what the person asked about approval. If they want to approve posts before they show, the storage_set_resource body must include \"approve_first\": true; if they want posts to show at once, leave it out.")
				}
			} else {
				steps = append(steps, fmt.Sprintf("%s (%s): %s. %s", f.Kind, f.Status, f.Use, f.Note))
			}
		}
	}
	steps = append(steps, "Show the person the live address and offer connect_domain for their own domain.")
	out := map[string]any{"state": r.state, "message": r.message, "site": r.site, "site_url": r.siteURL, "started_at": importTime(r.started), "finished_at": importTime(r.finished), "next_steps": steps}
	if r.report != nil {
		out["report"] = r.report
	}
	return out, nil
}
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
			http.NotFound(w, r)
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
	}{ok, link.Site, link.ThemeURL}
	raw, err := staticFiles.ReadFile("static/import.html")
	if err != nil {
		http.Error(w, "could not open import page", 500)
		return
	}
	page, err := withChrome(raw, chromeDataFor(r, ""))
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
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sweepLocked()
	rec := m.records[r.PathValue("import_id")]
	if rec == nil {
		writeJSON(w, 404, errorResponse{Error: "no import with that id"})
		return
	}
	warnings := append([]string{}, rec.warnings...)
	if len(warnings) > 10 {
		warnings = warnings[:10]
	}
	out := map[string]any{"state": rec.state, "stage": rec.stage, "done": rec.done, "total": rec.total, "message": rec.message, "pages": 0, "posts": 0, "media": 0, "warnings": warnings}
	if rec.state == "done" {
		out["site_url"] = rec.siteURL
	}
	if rec.report != nil {
		out["pages"] = rec.report.Pages
		out["posts"] = rec.report.Posts
		out["media"] = rec.report.MediaCopied
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
	case m.slots <- struct{}{}:
	default:
		m.update(link.ID, func(rec *importRecord) { rec.state = "waiting"; rec.started = time.Time{} })
		h.mcp.ReleaseImport(token, false)
		writeJSON(w, 503, errorResponse{Error: "busy, try again in a few minutes"})
		return
	}
	spent := false
	handedOff := false
	defer func() {
		if !handedOff {
			<-m.slots
		}
	}()

	// A stalled browser must release its admission slot and reservation.
	lim := *config.Active()
	bodyLimit := int64(lim.ImportMaxUploadMB) << 20
	rc := http.NewResponseController(w)
	_ = rc.SetReadDeadline(time.Now().Add(bodyReadTimeout(bodyLimit)))
	defer rc.SetReadDeadline(time.Time{})
	defer func() {
		if !handedOff && !spent {
			m.update(link.ID, func(rec *importRecord) {
				rec.state = "waiting"
				rec.started = time.Time{}
				rec.stage = ""
				rec.message = "Upload rejected. Try again with your WordPress export."
			})
		}
		if !handedOff {
			h.mcp.ReleaseImport(token, spent)
		}
	}()
	_, revoke, err := link.Mint(r.Context(), false)
	if err != nil {
		spent = true
		h.imports.update(link.ID, func(rec *importRecord) {
			rec.state = "failed"
			rec.message = "The import link's sign-in has ended. Sign in again and ask your chat for a new link."
			rec.finished = time.Now()
			rec.spent = true
		})
		writeJSON(w, 403, errorResponse{Error: "this import link's sign-in has ended (signed out, disconnected or expired); sign in again and ask your chat for a new link", Code: "import_link_signed_out"})
		return
	}
	defer func() {
		if !handedOff {
			revoke()
		}
	}()
	dir, err := os.MkdirTemp("", "simple-host-import-")
	if err != nil {
		writeJSON(w, 500, errorResponse{Error: "could not store the upload; try again"})
		return
	}
	defer func() {
		if !handedOff {
			os.RemoveAll(dir)
		}
	}()
	m.update(link.ID, func(rec *importRecord) {
		rec.state = "receiving"
		rec.stage = "Uploading your files"
		rec.message = rec.stage
	})

	r.Body = http.MaxBytesReader(w, r.Body, bodyLimit)
	reader, err := r.MultipartReader()
	if err != nil {
		writeJSON(w, 400, errorResponse{Error: "send files using the import page"})
		return
	}
	files := &importFiles{dir: dir, limits: lim, media: map[string]wpimport.MediaFile{}}
	theme := link.ThemeURL
	photos := true
	count := 0
	reject := func(err error) {
		code := 400
		var big *http.MaxBytesError
		if errors.As(err, &big) || errors.Is(err, errImportTooLarge) {
			code = 413
		}
		writeJSON(w, code, errorResponse{Error: err.Error()})
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
		dst, err := os.CreateTemp(dir, "upload-")
		if err != nil {
			reject(err)
			return
		}
		_, err = io.Copy(dst, part)
		closeErr := dst.Close()
		part.Close()
		if err != nil {
			reject(err)
			return
		}
		if closeErr != nil {
			reject(closeErr)
			return
		}
		if err = files.unpack(dst.Name()); err != nil {
			reject(err)
			return
		}
	}
	if len(files.xml) == 0 {
		reject(errors.New("no WordPress XML export found; download Tools > Export > Export all > Download, then try again"))
		return
	}
	// The link stays reserved while waiting in line and is spent only when
	// conversion starts. Temporary files and credentials now belong to the job.
	m.update(link.ID, func(rec *importRecord) {
		rec.state = "waiting"
		rec.stage = "Waiting in line"
		rec.message = rec.stage
		rec.started = time.Now()
	})
	writeJSON(w, 202, map[string]any{"import_id": link.ID, "status_url": "/v1/imports/status/" + link.ID})
	handedOff = true
	go h.runImport(token, link, revoke, files, theme, photos)
}
func (h *ConnectorHandler) runImport(token string, link mcp.ImportLink, revoke func(), files *importFiles, theme string, photos bool) {
	m := h.imports
	spent := false
	defer func() { h.mcp.ReleaseImport(token, spent) }()
	defer os.RemoveAll(files.dir)
	defer revoke()
	defer func() { <-m.slots }()
	defer func() {
		if recover() != nil {
			m.update(link.ID, func(r *importRecord) {
				r.state = "failed"
				r.stage = "Import failed"
				r.message = "The import could not finish. Ask your chat for a new link and try again."
				r.finished = time.Now()
			})
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	fail := func(err error) {
		m.update(link.ID, func(r *importRecord) {
			r.state = "failed"
			r.stage = "Import failed"
			r.message = err.Error()
			r.finished = time.Now()
		})
	}
	select {
	case m.running <- struct{}{}:
		defer func() { <-m.running }()
	case <-ctx.Done():
		fail(errors.New("The import timed out. Ask your chat for a new link and try again."))
		return
	}
	spent = true
	h.mcp.ReleaseImport(token, true)
	m.update(link.ID, func(r *importRecord) {
		r.spent = true
		r.state = "converting"
		r.stage = "Reading your WordPress export"
		r.message = r.stage
	})
	var readers []io.Reader
	for _, name := range files.xml {
		f, err := os.Open(name)
		if err != nil {
			fail(err)
			return
		}
		defer f.Close()
		readers = append(readers, f)
	}
	hosts := importSourceHosts(files.xml)
	var warnings []string
	if theme != "" {
		u, err := url.Parse(theme)
		if err != nil || !hosts[strings.ToLower(u.Hostname())] || (u.Scheme != "http" && u.Scheme != "https") {
			theme = ""
			warnings = append(warnings, "The old site's address did not match the WordPress export, so its look was not copied.")
		}
	}
	fetch := newImportFetcher(hosts, theme, photos, files.limits)
	defer fetch.client.CloseIdleConnections()
	result, err := convertImport(ctx, wpimport.Input{Site: link.Site, WXR: readers, Media: files.media, ThemeURL: theme, Fetch: fetch.Fetch, Limits: wpimport.Limits{MaxXMLBytes: int64(files.limits.ImportMaxXMLMB) << 20}, Progress: func(stage string, done, total int) {
		m.update(link.ID, func(r *importRecord) { r.stage = stage; r.message = stage; r.done = done; r.total = total })
	}})
	if err != nil {
		fail(err)
		return
	}
	if result == nil {
		fail(errors.New("The export could not be converted. Export it again and ask your chat for a new link."))
		return
	}
	result.Report.Warnings = append(warnings, result.Report.Warnings...)
	m.update(link.ID, func(r *importRecord) {
		r.state = "publishing"
		r.stage = "Publishing your site"
		r.message = r.stage
		r.warnings = result.Report.Warnings
	})
	archive, err := os.CreateTemp(files.dir, "site-*.tar.gz")
	if err != nil {
		fail(err)
		return
	}
	defer archive.Close()
	if err = packImport(archive, result.Files); err != nil {
		fail(err)
		return
	}
	size, err := archive.Seek(0, io.SeekCurrent)
	if err != nil {
		fail(err)
		return
	}
	archive.Seek(0, io.SeekStart)
	req, err := http.NewRequestWithContext(ctx, "PUT", "/v1/sites/"+url.PathEscape(link.Site)+"?create=1", archive)
	if err != nil {
		fail(err)
		return
	}
	// Internal connector credentials expire in 15 minutes. Refresh after a
	// potentially long upload/queue/conversion, and recheck the connection again.
	key, revokeDeploy, err := link.Mint(ctx, false)
	if err != nil {
		fail(errors.New("The import's sign-in ended before publishing. Sign in again and ask your chat for a new link."))
		return
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
		fail(errors.New(deploy.Error))
		return
	}
	if err != nil || deploy.SiteURL == "" {
		fail(errors.New("The server did not return a live address. Ask your chat to check the site."))
		return
	}
	m.update(link.ID, func(r *importRecord) {
		r.state = "done"
		r.stage = "Your site is live"
		r.message = "Go back to your chat and say: done."
		r.siteURL = deploy.SiteURL
		r.report = &result.Report
		r.warnings = result.Report.Warnings
		r.finished = time.Now()
	})
}
func packImport(dst io.Writer, files map[string][]byte) error {
	gz := gzip.NewWriter(dst)
	tw := tar.NewWriter(gz)
	var paths []string
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		clean, err := importPath(p)
		if err != nil {
			return err
		}
		b := files[p]
		if err = tw.WriteHeader(&tar.Header{Name: clean, Mode: 0644, Size: int64(len(b))}); err != nil {
			return err
		}
		if _, err = tw.Write(b); err != nil {
			return err
		}
	}
	if err := tw.Close(); err != nil {
		return err
	}
	return gz.Close()
}
