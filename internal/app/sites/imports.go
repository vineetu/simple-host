package sites

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/vsriram/simple-host/internal/storage"
	"github.com/vsriram/simple-host/internal/wpimport"
)

// ImportLink carries the job identity and its credential refresh operation.
type ImportLink struct {
	ID, Owner, Site, ThemeURL string
	Expires                   time.Time
	Mint                      func(context.Context, bool) (string, func(), error)
}

var ConvertImport = wpimport.Convert

type ImportRecord struct {
	Owner, Site                         string
	Created, Expires, Started, Finished time.Time
	State, Stage, Message, SiteURL      string
	Done, Total                         int
	Report                              *wpimport.Report
	Warnings                            []string
	Spent                               bool
}
type ImportManager struct {
	Mu             sync.Mutex
	Records        map[string]*ImportRecord
	Running, Slots chan struct{}
}

func NewImportManager(n int) *ImportManager {
	return &ImportManager{Records: map[string]*ImportRecord{}, Running: make(chan struct{}, n), Slots: make(chan struct{}, n+5)}
}
func (m *ImportManager) SweepLocked() {
	for id, r := range m.Records {
		if (!r.Finished.IsZero() && time.Since(r.Finished) > 24*time.Hour) ||
			(r.State == "waiting" && r.Started.IsZero() && !r.Expires.After(time.Now())) {
			delete(m.Records, id)
		}
	}
}
func (m *ImportManager) Created(link ImportLink) error {
	m.Mu.Lock()
	defer m.Mu.Unlock()
	m.SweepLocked()
	var ids []string
	for id, r := range m.Records {
		if r.Owner == link.Owner {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return m.Records[ids[i]].Finished.Before(m.Records[ids[j]].Finished) })
	if len(ids) >= 20 {
		// Usable links and active jobs always retain their reports.
		removed := false
		for _, id := range ids {
			r := m.Records[id]
			if !r.Finished.IsZero() && (r.Spent || !r.Expires.After(time.Now())) {
				delete(m.Records, id)
				removed = true
				break
			}
		}
		if !removed {
			return errors.New("there are already 20 imports in progress for this account")
		}
	}
	m.Records[link.ID] = &ImportRecord{Owner: link.Owner, Site: link.Site, Created: time.Now(), Expires: link.Expires, State: "waiting", Message: "Open the link and upload your WordPress export."}
	return nil
}
func (m *ImportManager) Receiving(id string) {
	m.Update(id, func(rec *ImportRecord) { rec.State = "receiving"; rec.Started = time.Now(); rec.Finished = time.Time{} })
}
func (m *ImportManager) Update(id string, fn func(*ImportRecord)) {
	m.Mu.Lock()
	defer m.Mu.Unlock()
	if r := m.Records[id]; r != nil {
		fn(r)
	}
}
func importTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}
func (m *ImportManager) Report(owner, id string) (map[string]any, error) {
	m.Mu.Lock()
	defer m.Mu.Unlock()
	m.SweepLocked()
	r := m.Records[id]
	if r == nil || r.Owner != owner {
		return nil, errors.New("no import with that id for this account")
	}
	steps := []string{}
	if r.Report != nil {
		for _, f := range r.Report.Features {
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
	out := map[string]any{"state": r.State, "message": r.Message, "site": r.Site, "site_url": r.SiteURL, "started_at": importTime(r.Started), "finished_at": importTime(r.Finished), "next_steps": steps}
	if r.Report != nil {
		out["report"] = r.Report
	}
	return out, nil
}
func (m *ImportManager) Run(token string, link ImportLink, revoke func(), files *ImportFiles, theme string, photos bool, release func(string, bool), publish func(context.Context, ImportLink, io.Reader, int64) (string, error)) {
	spent := false
	defer func() { release(token, spent) }()
	defer storage.RemoveImportDir(files.Dir)
	defer revoke()
	defer func() { <-m.Slots }()
	defer func() {
		if recover() != nil {
			m.Update(link.ID, func(r *ImportRecord) {
				r.State = "failed"
				r.Stage = "Import failed"
				r.Message = "The import could not finish. Ask your chat for a new link and try again."
				r.Finished = time.Now()
			})
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	fail := func(err error) {
		m.Update(link.ID, func(r *ImportRecord) {
			r.State = "failed"
			r.Stage = "Import failed"
			r.Message = err.Error()
			r.Finished = time.Now()
		})
	}
	select {
	case m.Running <- struct{}{}:
		defer func() { <-m.Running }()
	case <-ctx.Done():
		fail(errors.New("The import timed out. Ask your chat for a new link and try again."))
		return
	}
	spent = true
	release(token, true)
	m.Update(link.ID, func(r *ImportRecord) {
		r.Spent = true
		r.State = "converting"
		r.Stage = "Reading your WordPress export"
		r.Message = r.Stage
	})
	var readers []io.Reader
	for _, name := range files.Xml {
		f, err := os.Open(name)
		if err != nil {
			fail(err)
			return
		}
		defer f.Close()
		readers = append(readers, f)
	}
	hosts := importSourceHosts(files.Xml)
	var warnings []string
	if theme != "" {
		u, err := url.Parse(theme)
		if err != nil || !hosts[strings.ToLower(u.Hostname())] || (u.Scheme != "http" && u.Scheme != "https") {
			theme = ""
			warnings = append(warnings, "The old site's address did not match the WordPress export, so its look was not copied.")
		}
	}
	fetch := newImportFetcher(hosts, theme, photos, files.Limits)
	defer fetch.client.CloseIdleConnections()
	result, err := ConvertImport(ctx, wpimport.Input{Site: link.Site, WXR: readers, Media: files.Media, ThemeURL: theme, Fetch: fetch.Fetch, Limits: wpimport.Limits{MaxXMLBytes: int64(files.Limits.ImportMaxXMLMB) << 20}, Progress: func(stage string, done, total int) {
		m.Update(link.ID, func(r *ImportRecord) { r.Stage = stage; r.Message = stage; r.Done = done; r.Total = total })
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
	m.Update(link.ID, func(r *ImportRecord) {
		r.State = "publishing"
		r.Stage = "Publishing your site"
		r.Message = r.Stage
		r.Warnings = result.Report.Warnings
	})
	archive, err := storage.CreateImportFile(files.Dir, "site-*.tar.gz")
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
	siteURL, err := publish(ctx, link, archive, size)
	if err != nil {
		fail(err)
		return
	}
	m.Update(link.ID, func(r *ImportRecord) {
		r.State = "done"
		r.Stage = "Your site is live"
		r.Message = "Go back to your chat and say: done."
		r.SiteURL = siteURL
		r.Report = &result.Report
		r.Warnings = result.Report.Warnings
		r.Finished = time.Now()
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
		clean, err := ImportPath(p)
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
