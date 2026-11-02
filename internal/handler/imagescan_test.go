package handler

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	db "github.com/vsriram/simple-host/internal/db"
	"github.com/vsriram/simple-host/internal/imagescan"
	"github.com/vsriram/simple-host/internal/storage"
)

// Image matching (imagescan.go), end to end against the private database:
// deploys queue images, the worker quarantines a match (evidence copy, file
// removed, site taken down, alert sent), the hash cache short-circuits the
// service, retries back off, and the admin routes list and report. Needs
// DB_DSN with db/migrations/zz-sim-200-image-scan.sql applied.

// testPNG is a real PNG, unique per call so the hash cache never knows it.
func testPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 24, 24))
	// Noise does not compress, so the file clears the candidate size.
	_, _ = rand.Read(img.Pix)
	img.Set(0, 0, color.RGBA{0, 0, 0, 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	if len(buf.Bytes()) < imageScanMinBytes {
		t.Fatalf("test PNG too small: %d bytes", buf.Len())
	}
	return buf.Bytes()
}

type fakeMatcher struct {
	mu      sync.Mutex
	matches map[string]imagescan.Match // by SHA-256 of the original bytes
	calls   []string
	err     error
}

func (f *fakeMatcher) Name() string { return "fake" }
func (f *fakeMatcher) Match(_ context.Context, img *imagescan.Image) (imagescan.Match, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, img.SHA256)
	if f.err != nil {
		return imagescan.Match{}, f.err
	}
	if m, ok := f.matches[img.SHA256]; ok {
		return m, nil
	}
	return imagescan.Match{TrackingID: "t-clean"}, nil
}
func (f *fakeMatcher) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

type fakeReporter struct {
	mu      sync.Mutex
	reports []imagescan.Report
	err     error
}

func (f *fakeReporter) Report(_ context.Context, in imagescan.Report) (imagescan.ReportResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return imagescan.ReportResult{}, f.err
	}
	f.reports = append(f.reports, in)
	return imagescan.ReportResult{ReportID: "RPT-TEST-1"}, nil
}

type fakeNoticeMailer struct {
	mu      sync.Mutex
	notices []string // "to|subject|text"
}

func (f *fakeNoticeMailer) SendSignInCode(string, string, string) error { return nil }
func (f *fakeNoticeMailer) SendNotice(to, subject, text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.notices = append(f.notices, to+"|"+subject+"|"+text)
	return nil
}
func (f *fakeNoticeMailer) sent() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.notices...)
}

// fakePrepare stands in for imagescan.Prepare: the bytes as they are.
func fakePrepare(raw []byte, _ imagescan.PrepareOptions) (*imagescan.Image, string, error) {
	ct, ok := imageCandidate(raw)
	if !ok {
		return nil, "not an image", nil
	}
	return &imagescan.Image{Data: raw, ContentType: ct, Width: 24, Height: 24, SHA256: sha256Hex(raw)}, "", nil
}

// scanFixture is an ImageScan with fakes, and a way to wait for the
// enqueue work that runs off the request.
type scanFixture struct {
	scan     *ImageScan
	matcher  *fakeMatcher
	reporter *fakeReporter
	mailer   *fakeNoticeMailer
	evidence string
	runs     sync.WaitGroup
}

func newImageScanFixture(t *testing.T, a *privateApp, disk *storage.DiskStorage) *scanFixture {
	t.Helper()
	if _, err := a.database.Exec(`TRUNCATE image_scan_queue`); err != nil {
		t.Fatal(err)
	}
	f := &scanFixture{matcher: &fakeMatcher{matches: map[string]imagescan.Match{}}, reporter: &fakeReporter{}, mailer: &fakeNoticeMailer{}, evidence: t.TempDir()}
	f.scan = NewImageScan(a.database, disk, f.matcher, f.reporter, f.mailer, ImageScanConfig{
		EvidenceDir: f.evidence, AlertEmail: "alerts@example.com", ReportOrg: "Test Org", ReportName: "Tester",
		ReportEmail: "reports@example.com", PublicBaseURL: "https://host.test",
	})
	if f.scan == nil {
		t.Fatal("NewImageScan returned nil with a matcher")
	}
	f.scan.prepare = fakePrepare
	f.scan.run = func(fn func()) {
		f.runs.Add(1)
		go func() {
			defer f.runs.Done()
			fn()
		}()
	}
	return f
}

func (f *scanFixture) wait() { f.runs.Wait() }

func (f *scanFixture) markMatch(sha string) {
	f.matcher.mu.Lock()
	defer f.matcher.mu.Unlock()
	f.matcher.matches[sha] = imagescan.Match{IsMatch: true, TrackingID: "t-match", Source: "NCMEC", Violations: []string{"A1", "B2"}, Raw: json.RawMessage(`{"IsMatch":true}`)}
}

func deployWithImages(t *testing.T, a *privateApp, p person, site string, files map[string][]byte) {
	t.Helper()
	b64 := map[string]string{}
	for name, b := range files {
		b64[name] = base64.StdEncoding.EncodeToString(b)
	}
	r := a.at(t, "POST", pcSiteDomain, "/v1/sites/"+site+"/files",
		map[string]any{"files": map[string]string{"index.html": "<h1>" + site + "</h1>"}, "files_base64": b64},
		map[string]string{"X-API-Key": p.key})
	if r.status != http.StatusCreated {
		t.Fatalf("deploy %s: %d %s", site, r.status, r.body)
	}
}

func queueCount(t *testing.T, a *privateApp, siteID string) int {
	t.Helper()
	var n int
	if err := a.database.QueryRow(`SELECT count(*) FROM image_scan_queue WHERE site_id::text = $1`, siteID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func hashVerdict(t *testing.T, a *privateApp, sha string) string {
	t.Helper()
	var v string
	err := a.database.QueryRow(`SELECT verdict FROM image_scan_hashes WHERE sha256 = $1`, sha).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return ""
	}
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func latestMatch(t *testing.T, a *privateApp, siteID string) db.ImageMatch {
	t.Helper()
	rows, err := db.ListImageMatches(context.Background(), a.database, 50)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range rows {
		if m.SiteID == siteID {
			return m
		}
	}
	t.Fatalf("no match row for site %s", siteID)
	return db.ImageMatch{}
}

// 1. A deploy with a matched PNG, a clean PNG and an SVG.
func TestImageScanDeployMatch(t *testing.T) {
	a, _ := newSiteApp(t, "canonical")
	f := newImageScanFixture(t, a, a.sites.disk)
	a.sites.SetImageScan(f.scan)
	olive := a.newPerson(t, "olive")
	bad, good := testPNG(t), testPNG(t)
	f.markMatch(sha256Hex(bad))
	svg := []byte(`<svg xmlns="http://www.w3.org/2000/svg" width="300" height="300">` + strings.Repeat("<rect width='10' height='10'/>", 20) + `</svg>`)
	deployWithImages(t, a, olive, "shop", map[string][]byte{"img/bad.png": bad, "img/good.png": good, "logo.svg": svg})
	f.wait()
	uid, _ := a.userID(t, olive)
	siteID := a.siteID(t, olive, "shop")
	if n := queueCount(t, a, siteID); n != 2 {
		t.Fatalf("queued %d jobs, want 2 (the SVG is never queued)", n)
	}
	if n := f.scan.RunOnce(context.Background()); n != 2 {
		t.Fatalf("RunOnce handled %d, want 2", n)
	}
	if n := queueCount(t, a, siteID); n != 0 {
		t.Fatalf("queue after the pass: %d rows", n)
	}
	if v := hashVerdict(t, a, sha256Hex(bad)); v != "match" {
		t.Fatalf("bad hash verdict %q", v)
	}
	if v := hashVerdict(t, a, sha256Hex(good)); v != "clean" {
		t.Fatalf("good hash verdict %q", v)
	}
	if v := hashVerdict(t, a, sha256Hex(svg)); v != "" {
		t.Fatalf("svg has a verdict %q", v)
	}
	m := latestMatch(t, a, siteID)
	if m.SiteName != "shop" || m.OwnerID != uid || m.OwnerEmail != olive.email || m.Path != "img/bad.png" || m.Provider != "fake" || m.Source != "NCMEC" || m.Violations != "A1,B2" || m.TrackingID != "t-match" || m.ContentType != "image/png" || m.Bytes != int64(len(bad)) {
		t.Fatalf("match row: %+v", m)
	}
	if !m.TakenDown || !m.AlertedAt.Valid || m.ReportStatus != "pending" {
		t.Fatalf("match flags: taken_down=%v alerted=%v report=%s", m.TakenDown, m.AlertedAt.Valid, m.ReportStatus)
	}
	// Evidence: image.png and record.json, private modes.
	dir := filepath.Join(f.evidence, id64(m.ID))
	if m.EvidencePath != dir {
		t.Fatalf("evidence_path %q, want %q", m.EvidencePath, dir)
	}
	if st, err := os.Stat(dir); err != nil || st.Mode().Perm() != 0o700 {
		t.Fatalf("evidence dir: %v %v", st, err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "image.png"))
	if err != nil || !bytes.Equal(got, bad) {
		t.Fatalf("evidence image: %v (equal %v)", err, bytes.Equal(got, bad))
	}
	if st, _ := os.Stat(filepath.Join(dir, "image.png")); st.Mode().Perm() != 0o600 {
		t.Fatalf("evidence image mode %o", st.Mode().Perm())
	}
	rec, err := os.ReadFile(filepath.Join(dir, "record.json"))
	if err != nil {
		t.Fatal(err)
	}
	var record map[string]any
	if err := json.Unmarshal(rec, &record); err != nil {
		t.Fatal(err)
	}
	if wh, _ := record["what_happened"].([]any); len(wh) < 4 || record["path"] != "img/bad.png" {
		t.Fatalf("record.json: %s", rec)
	}
	if st, _ := os.Stat(filepath.Join(dir, "record.json")); st.Mode().Perm() != 0o600 {
		t.Fatalf("record.json mode %o", st.Mode().Perm())
	}
	// The file is gone from v1 and current; the clean one stays.
	siteDir := a.sites.disk.SiteDir(uid, "shop")
	for _, v := range []string{"v1", "current"} {
		if _, err := os.Stat(filepath.Join(siteDir, v, "img", "bad.png")); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("%s/img/bad.png still there: %v", v, err)
		}
		if _, err := os.Stat(filepath.Join(siteDir, v, "img", "good.png")); err != nil {
			t.Fatalf("%s/img/good.png gone: %v", v, err)
		}
	}
	// Taken down with the generic reason; marker on disk.
	site, err := db.GetSiteByID(context.Background(), a.database, siteID)
	if err != nil {
		t.Fatal(err)
	}
	if !site.SiteSuspended || site.SiteSuspendedReason != imageScanTakeDownMsg {
		t.Fatalf("site not taken down: %+v", site)
	}
	if !a.sites.disk.IsSuspended(uid, "shop") {
		t.Fatal("no take-down marker on disk")
	}
	// One alert with the id, no image.
	sent := f.mailer.sent()
	if len(sent) != 1 {
		t.Fatalf("alerts sent: %d", len(sent))
	}
	if !strings.HasPrefix(sent[0], "alerts@example.com|Simple Host: image match on site shop (#"+id64(m.ID)+")|") || !strings.Contains(sent[0], "img/bad.png") || !strings.Contains(sent[0], "https://host.test/admin#moderation") || !strings.Contains(sent[0], "CyberTipline") {
		t.Fatalf("alert: %s", sent[0])
	}
	if strings.Contains(sent[0], base64.StdEncoding.EncodeToString(bad[:32])) || strings.Contains(sent[0], f.evidence) {
		t.Fatalf("alert carries the image or a path to it: %s", sent[0])
	}

	// 2. The same bytes on another site: no service call, quarantined at once.
	calls := f.matcher.callCount()
	deployWithImages(t, a, olive, "shop2", map[string][]byte{"again.png": bad})
	f.wait()
	if f.matcher.callCount() != calls {
		t.Fatalf("matcher called again for a cached match")
	}
	site2ID := a.siteID(t, olive, "shop2")
	if n := queueCount(t, a, site2ID); n != 0 {
		t.Fatalf("cached match was queued: %d rows", n)
	}
	m2 := latestMatch(t, a, site2ID)
	if m2.Provider != "hash-cache" || m2.TrackingID != "t-match" || !m2.TakenDown || m2.Path != "again.png" {
		t.Fatalf("second match row: %+v", m2)
	}
	if _, err := os.Stat(filepath.Join(a.sites.disk.SiteDir(uid, "shop2"), "current", "again.png")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("again.png still served: %v", err)
	}
	if len(f.mailer.sent()) != 2 {
		t.Fatalf("alerts after the second match: %d", len(f.mailer.sent()))
	}

	// 6. The admin routes.
	admin := map[string]string{"X-API-Key": a.admin}
	r := a.at(t, "GET", pcSiteDomain, "/v1/admin/image-scan", nil, admin)
	if r.status != 200 {
		t.Fatalf("admin GET: %d %s", r.status, r.body)
	}
	var page struct {
		Enabled          bool             `json:"enabled"`
		Provider         string           `json:"provider"`
		ReportConfigured bool             `json:"report_configured"`
		Queue            map[string]int   `json:"queue"`
		Matches          []map[string]any `json:"matches"`
	}
	if err := json.Unmarshal(r.body, &page); err != nil {
		t.Fatal(err)
	}
	if !page.Enabled || page.Provider != "fake" || !page.ReportConfigured || page.Queue["pending"] != 0 || len(page.Matches) < 2 {
		t.Fatalf("admin GET body: %s", r.body)
	}
	if id, _ := page.Matches[0]["id"].(float64); int64(id) != m2.ID {
		t.Fatalf("newest first: got %v want %d", page.Matches[0]["id"], m2.ID)
	}
	if r := a.at(t, "GET", pcSiteDomain, "/v1/admin/image-scan", nil, map[string]string{"X-API-Key": olive.key}); r.status != http.StatusForbidden {
		t.Fatalf("non-admin GET: %d", r.status)
	}
	// No reporter: 503.
	saved := f.scan.reporter
	f.scan.reporter = nil
	if r := a.at(t, "POST", pcSiteDomain, "/v1/admin/image-scan/"+id64(m.ID)+"/report", nil, admin); r.status != http.StatusServiceUnavailable || r.json(t)["code"] != "report_unavailable" {
		t.Fatalf("report without a reporter: %d %s", r.status, r.body)
	}
	f.scan.reporter = saved
	r = a.at(t, "POST", pcSiteDomain, "/v1/admin/image-scan/"+id64(m.ID)+"/report", nil, admin)
	if r.status != 200 || r.json(t)["report_id"] != "RPT-TEST-1" {
		t.Fatalf("report: %d %s", r.status, r.body)
	}
	if len(f.reporter.reports) != 1 || f.reporter.reports[0].OrgName != "Test Org" || len(f.reporter.reports[0].Files) != 1 || f.reporter.reports[0].Files[0].Name != "bad.png" || !bytes.Equal(f.reporter.reports[0].Files[0].Data, bad) || f.reporter.reports[0].Files[0].Metadata["match_id"] != id64(m.ID) {
		t.Fatalf("report sent: %+v", f.reporter.reports)
	}
	if got, _ := db.GetImageMatch(context.Background(), a.database, m.ID); got.ReportStatus != "filed" || got.ReportID != "RPT-TEST-1" || !got.ReportedAt.Valid {
		t.Fatalf("after report: %+v", got)
	}
	if r := a.at(t, "POST", pcSiteDomain, "/v1/admin/image-scan/"+id64(m.ID)+"/report", nil, admin); r.status != http.StatusConflict || r.json(t)["code"] != "already_reported" {
		t.Fatalf("second report: %d %s", r.status, r.body)
	}
	if r := a.at(t, "POST", pcSiteDomain, "/v1/admin/image-scan/999999999/report", nil, admin); r.status != http.StatusNotFound {
		t.Fatalf("unknown id: %d %s", r.status, r.body)
	}
	// A failing service leaves 'failed' with the error, and the button can retry.
	f.reporter.err = errors.New("service down")
	if r := a.at(t, "POST", pcSiteDomain, "/v1/admin/image-scan/"+id64(m2.ID)+"/report", nil, admin); r.status != http.StatusBadGateway || r.json(t)["code"] != "report_failed" {
		t.Fatalf("failed report: %d %s", r.status, r.body)
	}
	if got, _ := db.GetImageMatch(context.Background(), a.database, m2.ID); got.ReportStatus != "failed" || !strings.Contains(got.ReportError, "service down") {
		t.Fatalf("after failed report: %+v", got)
	}
	f.reporter.err = nil
	if r := a.at(t, "POST", pcSiteDomain, "/v1/admin/image-scan/"+id64(m2.ID)+"/report", nil, admin); r.status != 200 {
		t.Fatalf("retry: %d %s", r.status, r.body)
	}
}

// 3. A files-storage object.
func TestImageScanStorageFile(t *testing.T) {
	a, _ := newSiteApp(t, "canonical")
	f := newImageScanFixture(t, a, a.sites.disk)
	a.sites.SetImageScan(f.scan)
	olive := a.newPerson(t, "olive")
	a.deploy(t, olive, "gallery")
	okey := map[string]string{"X-API-Key": olive.key}
	if r := a.at(t, "PUT", pcSiteDomain, "/v1/sites/gallery/storage/resources/photos", map[string]string{"kind": "files", "read": "anyone", "write": "owner"}, okey); r.status != 201 {
		t.Fatalf("files resource: %d %s", r.status, r.body)
	}
	bad := testPNG(t)
	f.markMatch(sha256Hex(bad))
	if r := a.at(t, "PUT", pcSiteDomain, "/v1/sites/gallery/storage/files/photos/objects/pics/bad.png", string(bad), map[string]string{"X-API-Key": olive.key, "Content-Type": "image/png"}); r.status != 200 {
		t.Fatalf("put object: %d %s", r.status, r.body)
	}
	f.wait()
	uid, _ := a.userID(t, olive)
	siteID := a.siteID(t, olive, "gallery")
	onDisk := storageFileOnDisk(a.sites.disk, uid, "gallery", "photos", "pics/bad.png")
	if _, err := os.Stat(onDisk); err != nil {
		t.Fatalf("object not on disk where the worker looks: %v", err)
	}
	if n := queueCount(t, a, siteID); n != 1 {
		t.Fatalf("queued %d, want 1", n)
	}
	f.scan.RunOnce(context.Background())
	if _, err := os.Stat(onDisk); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("object still on disk: %v", err)
	}
	var rows int
	if err := a.database.QueryRow(`SELECT count(*) FROM site_storage_files WHERE site_id = $1 AND resource_name = 'photos'`, siteID).Scan(&rows); err != nil || rows != 0 {
		t.Fatalf("object row remains: %d %v", rows, err)
	}
	m := latestMatch(t, a, siteID)
	if m.Kind != "storage_file" || m.ResourceName != "photos" || m.Path != "pics/bad.png" || !m.TakenDown {
		t.Fatalf("match row: %+v", m)
	}
	if site, _ := db.GetSiteByID(context.Background(), a.database, siteID); !site.SiteSuspended {
		t.Fatal("site not taken down")
	}
	if !a.sites.disk.IsSuspended(uid, "gallery") {
		t.Fatal("no marker")
	}
}

// 4. An event icon: cleared, and the event taken down.
func TestImageScanEventIcon(t *testing.T) {
	a := newHackApp(t)
	disk, err := storage.NewDiskStorage(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.database.Exec(`TRUNCATE image_scan_queue`); err != nil {
		t.Fatal(err)
	}
	matcher := &fakeMatcher{matches: map[string]imagescan.Match{}}
	mailer := &fakeNoticeMailer{}
	s := NewImageScan(a.database, disk, matcher, nil, mailer, ImageScanConfig{EvidenceDir: t.TempDir(), AlertEmail: "alerts@example.com"})
	s.prepare = fakePrepare
	var runs sync.WaitGroup
	s.run = func(fn func()) {
		runs.Add(1)
		go func() {
			defer runs.Done()
			fn()
		}()
	}
	a.hack.SetImageScan(s)
	ada := a.newPerson(t, "ada")
	slug := uniqueSlug()
	if r := a.createEvent(t, ada, slug, nil); r.status != http.StatusCreated {
		t.Fatalf("create event: %d %s", r.status, r.body)
	}
	ev, err := db.GetEventBySlug(context.Background(), a.database, slug)
	if err != nil {
		t.Fatal(err)
	}
	icon := testPNG(t)
	matcher.mu.Lock()
	matcher.matches[sha256Hex(icon)] = imagescan.Match{IsMatch: true, TrackingID: "t-icon", Source: "IWF"}
	matcher.mu.Unlock()
	if err := db.SetEventIcon(context.Background(), a.database, ev.ID, "image/png", icon); err != nil {
		t.Fatal(err)
	}
	s.EnqueueEventIcon(ev.ID, icon)
	runs.Wait()
	if n := s.RunOnce(context.Background()); n != 1 {
		t.Fatalf("RunOnce handled %d", n)
	}
	if typ, body, err := db.GetEventIcon(context.Background(), a.database, ev.ID); err != nil || typ != "" || len(body) != 0 {
		t.Fatalf("icon not cleared: %q %d %v", typ, len(body), err)
	}
	after, err := db.GetEventByID(context.Background(), a.database, ev.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !after.TakenDown() || after.TakenDownReason != imageScanTakeDownMsg {
		t.Fatalf("event not taken down: %+v", after.TakenDownAt)
	}
	if susp, _ := db.UserSuspended(context.Background(), a.database, ev.AccountID); !susp {
		t.Fatal("holding account not suspended with the event")
	}
	matches, err := db.ListImageMatches(context.Background(), a.database, 10)
	if err != nil || len(matches) == 0 || matches[0].Kind != "event_icon" || matches[0].EventSlug != slug || matches[0].ReportStatus != "off" || !matches[0].TakenDown {
		t.Fatalf("match row: %+v %v", matches, err)
	}
	if sent := mailer.sent(); len(sent) != 1 || !strings.Contains(sent[0], "event "+slug) {
		t.Fatalf("alert: %v", sent)
	}
}

// 5. Retries: a retryable error and a permanent one both back off; an
// unsupported image is recorded as skipped and dropped.
func TestImageScanRetry(t *testing.T) {
	a, _ := newSiteApp(t, "canonical")
	f := newImageScanFixture(t, a, a.sites.disk)
	a.sites.SetImageScan(f.scan)
	olive := a.newPerson(t, "olive")
	pic := testPNG(t)
	f.matcher.err = imagescan.ErrRetryable
	deployWithImages(t, a, olive, "retry", map[string][]byte{"pic.png": pic})
	f.wait()
	siteID := a.siteID(t, olive, "retry")
	f.scan.RunOnce(context.Background())
	var attempts int
	var next time.Time
	var lastErr string
	row := func() {
		t.Helper()
		if err := a.database.QueryRow(`SELECT attempts, next_attempt_at, last_error FROM image_scan_queue WHERE site_id::text = $1`, siteID).Scan(&attempts, &next, &lastErr); err != nil {
			t.Fatal(err)
		}
	}
	row()
	if attempts != 1 || !next.After(time.Now().Add(30*time.Second)) || !strings.Contains(lastErr, "retryable") {
		t.Fatalf("after a retryable error: attempts=%d next=%s err=%q", attempts, next, lastErr)
	}
	if _, err := a.database.Exec(`UPDATE image_scan_queue SET next_attempt_at = now() WHERE site_id::text = $1`, siteID); err != nil {
		t.Fatal(err)
	}
	f.matcher.err = errors.New("permanent trouble")
	f.scan.RunOnce(context.Background())
	row()
	if attempts != 2 || !next.After(time.Now().Add(2*time.Minute)) || lastErr != "permanent trouble" {
		t.Fatalf("after a permanent error: attempts=%d next=%s err=%q", attempts, next, lastErr)
	}
	if _, err := a.database.Exec(`UPDATE image_scan_queue SET next_attempt_at = now() WHERE site_id::text = $1`, siteID); err != nil {
		t.Fatal(err)
	}
	f.matcher.err = imagescan.ErrUnsupported
	f.scan.RunOnce(context.Background())
	if n := queueCount(t, a, siteID); n != 0 {
		t.Fatalf("unsupported image still queued: %d", n)
	}
	if v := hashVerdict(t, a, sha256Hex(pic)); v != "skipped" {
		t.Fatalf("verdict %q, want skipped", v)
	}
	if site, _ := db.GetSiteByID(context.Background(), a.database, siteID); site.SiteSuspended {
		t.Fatal("site taken down without a match")
	}
}

// 7. Off: nil everywhere, deploys unchanged, nothing queued.
func TestImageScanDisabled(t *testing.T) {
	if s := NewImageScan(nil, nil, nil, nil, nil, ImageScanConfig{}); s != nil {
		t.Fatal("NewImageScan with no matcher is not nil")
	}
	var s *ImageScan
	if s.Enabled() {
		t.Fatal("nil is enabled")
	}
	s.EnqueueSiteFiles("x", "y", "z", 1, map[string][]byte{"a.png": testPNG(t)})
	s.EnqueueStorageFile("x", "r", "p", "", nil)
	s.EnqueueEventIcon("x", nil)
	s.EnqueueEntryScreenshot("x", "y", "z", nil)
	s.Start(context.Background())
	if n := s.RunOnce(context.Background()); n != 0 {
		t.Fatal("nil RunOnce did work")
	}
	a, _ := newSiteApp(t, "canonical")
	a.sites.SetImageScan(nil)
	olive := a.newPerson(t, "olive")
	deployWithImages(t, a, olive, "plain", map[string][]byte{"pic.png": testPNG(t)})
	if n := queueCount(t, a, a.siteID(t, olive, "plain")); n != 0 {
		t.Fatalf("queued %d with the feature off", n)
	}
	admin := map[string]string{"X-API-Key": a.admin}
	r := a.at(t, "GET", pcSiteDomain, "/v1/admin/image-scan", nil, admin)
	if r.status != 200 || r.json(t)["enabled"] != false {
		t.Fatalf("admin GET while off: %d %s", r.status, r.body)
	}
}

// The privacy page carries its image-matching line only while the feature is on.
func TestImageScanPrivacyItem(t *testing.T) {
	src, err := staticFiles.ReadFile("static/privacy.html")
	if err != nil {
		t.Fatal(err)
	}
	const marker = `data-sh="image-scan"`
	if !bytes.Contains(src, []byte(marker)) {
		t.Skip("privacy.html has no image-scan item yet")
	}
	was := imageScanOn
	t.Cleanup(func() { imageScanOn = was })
	for _, on := range []bool{true, false} {
		imageScanOn = on
		page, err := chromePage("privacy.html", chromeData{})
		if err != nil {
			t.Fatal(err)
		}
		if got := bytes.Contains(page, []byte(marker)); got != on {
			t.Fatalf("on=%v: item served=%v", on, got)
		}
		if !on && bytes.Contains(page, []byte("image-scan")) {
			t.Fatalf("off: a trace of the item remains")
		}
	}
}

func TestImageCandidate(t *testing.T) {
	if _, ok := imageCandidate([]byte("<svg xmlns='http://www.w3.org/2000/svg'>" + strings.Repeat(" ", 300) + "</svg>")); ok {
		t.Fatal("svg accepted")
	}
	if _, ok := imageCandidate(testPNG(t)[:100]); ok {
		t.Fatal("tiny image accepted")
	}
	if ct, ok := imageCandidate(testPNG(t)); !ok || ct != "image/png" {
		t.Fatalf("png refused: %q %v", ct, ok)
	}
	if _, ok := imageCandidate(append([]byte("\x00\x00\x01\x00"), make([]byte, 400)...)); ok {
		t.Fatal("ico accepted")
	}
}

func id64(n int64) string { return strconv.FormatInt(n, 10) }
