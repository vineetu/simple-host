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
	"github.com/vsriram/simple-host/internal/edition"
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
	return newImageScanFixtureDB(t, a.database, disk)
}

func newImageScanFixtureDB(t *testing.T, database *sql.DB, disk *storage.DiskStorage) *scanFixture {
	t.Helper()
	f := &scanFixture{matcher: &fakeMatcher{matches: map[string]imagescan.Match{}}, reporter: &fakeReporter{}, mailer: &fakeNoticeMailer{}, evidence: t.TempDir()}
	t.Cleanup(f.runs.Wait)
	f.scan = NewImageScan(database, disk, f.matcher, f.reporter, f.mailer, ImageScanConfig{
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
	r := a.at(t, "PUT", pcSiteDomain, "/v1/sites/"+site+"/files?create=1",
		map[string]any{"files": map[string]string{"index.html": "<h1>" + site + "</h1>"}, "files_base64": b64},
		map[string]string{"X-API-Key": p.key})
	if r.status != http.StatusCreated && r.status != http.StatusOK {
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
	isolateQueueTest(t)
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
	if st, _ := record["steps"].([]any); len(st) < 5 || record["path"] != "img/bad.png" || record["raw_reply"] == nil || record["writer_id"] != uid {
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
	if got, _ := db.GetImageMatch(context.Background(), a.database, m2.ID); got.ReportStatus != "failed" || got.ReportError != "report: provider rejected request or returned an invalid response" {
		t.Fatalf("after failed report: %+v", got)
	}
	f.reporter.err = nil
	if r := a.at(t, "POST", pcSiteDomain, "/v1/admin/image-scan/"+id64(m2.ID)+"/report", nil, admin); r.status != 200 {
		t.Fatalf("retry: %d %s", r.status, r.body)
	}
}

// 3. A files-storage object.
func TestImageScanStorageFile(t *testing.T) {
	isolateQueueTest(t)
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
	isolateQueueTest(t)
	a := newBridgeTeamApp(t)
	disk, err := storage.NewDiskStorage(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	matcher := &fakeMatcher{matches: map[string]imagescan.Match{}}
	mailer := &fakeNoticeMailer{}
	s := NewImageScan(a.database, disk, matcher, nil, mailer, ImageScanConfig{EvidenceDir: t.TempDir(), AlertEmail: "alerts@example.com"})
	s.prepare = fakePrepare
	var runs sync.WaitGroup
	t.Cleanup(runs.Wait)
	s.run = func(fn func()) {
		runs.Add(1)
		go func() {
			defer runs.Done()
			fn()
		}()
	}
	a.hack.SetImageScan(s)
	s.SetHackTakeDownSync(a.hack.SyncEventMarkers) // as main wires it
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
	var adaID string
	if err := a.database.QueryRow(`SELECT id FROM users WHERE username = $1`, ada.email).Scan(&adaID); err != nil {
		t.Fatal(err)
	}
	s.EnqueueEventIcon(ev.ID, adaID, icon)
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
	if sent := mailer.sent(); len(sent) != 1 || !strings.Contains(sent[0], "event "+slug) || !strings.Contains(sent[0], "report: not filed") || !strings.Contains(sent[0], "CyberTipline") {
		t.Fatalf("alert: %v", sent)
	}
	// The report names the organiser who uploaded the icon.
	if who := s.reporteeOf(context.Background(), matches[0]); !strings.Contains(who, ada.email) {
		t.Fatalf("reportee %q, want the uploader %s", who, ada.email)
	}
}

// 5. Retries: a retryable error and a permanent one both back off; an
// unsupported image is recorded as skipped and dropped.
func TestImageScanRetry(t *testing.T) {
	isolateQueueTest(t)
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
	if attempts != 1 || !next.After(time.Now().Add(30*time.Second)) || lastErr != "match: temporary provider failure" {
		t.Fatalf("after a retryable error: attempts=%d next=%s err=%q", attempts, next, lastErr)
	}
	if _, err := a.database.Exec(`UPDATE image_scan_queue SET next_attempt_at = now() WHERE site_id::text = $1`, siteID); err != nil {
		t.Fatal(err)
	}
	f.matcher.err = errors.New("permanent trouble")
	f.scan.RunOnce(context.Background())
	row()
	if attempts != 2 || !next.After(time.Now().Add(90*time.Second)) || lastErr != "match: provider rejected request or returned an invalid response" {
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
	s.EnqueueEventIcon("x", "y", nil)
	s.EnqueueEntryScreenshot("x", "y", "z", nil)
	s.Start(context.Background())
	if n := s.RunOnce(context.Background()); n != 0 {
		t.Fatal("nil RunOnce did work")
	}
	isolateQueueTest(t)
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
	t.Cleanup(func() { SetImageScanOn(was) })
	for _, on := range []bool{true, false} {
		SetImageScanOn(on)
		page, err := newAssetPipeline(edition.ForKind(edition.Host), "").chromePage("privacy.html", ChromeData{})
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

// rerun makes every parked-for-later job of a site due again (not the ones
// given up for good).
func rerun(t *testing.T, a *privateApp, siteID string) {
	t.Helper()
	if _, err := a.database.Exec(`UPDATE image_scan_queue SET next_attempt_at = now() WHERE site_id::text = $1 AND next_attempt_at <> 'infinity'`, siteID); err != nil {
		t.Fatal(err)
	}
}

// 2.1: the same bytes redeployed as v2 while v1 is pruned still get checked.
func TestImageScanVersionDedup(t *testing.T) {
	isolateQueueTest(t)
	a, _ := newSiteApp(t, "canonical")
	f := newImageScanFixture(t, a, a.sites.disk)
	a.sites.SetImageScan(f.scan)
	olive := a.newPerson(t, "olive")
	bad := testPNG(t)
	f.markMatch(sha256Hex(bad))
	deployWithImages(t, a, olive, "keep1", map[string][]byte{"bad.png": bad})
	f.wait()
	siteID := a.siteID(t, olive, "keep1")
	if _, err := a.database.Exec(`UPDATE sites SET keep_versions = 1 WHERE id = $1`, siteID); err != nil {
		t.Fatal(err)
	}
	deployWithImages(t, a, olive, "keep1", map[string][]byte{"bad.png": bad})
	f.wait()
	uid, _ := a.userID(t, olive)
	siteDir := a.sites.disk.SiteDir(uid, "keep1")
	if _, err := os.Stat(filepath.Join(siteDir, "v1")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("v1 was not pruned: %v", err)
	}
	if n := queueCount(t, a, siteID); n != 2 {
		t.Fatalf("queued %d jobs, want one per version", n)
	}
	f.scan.RunOnce(context.Background())
	if f.matcher.callCount() != 1 {
		t.Fatalf("matcher calls %d, want 1", f.matcher.callCount())
	}
	if _, err := os.Stat(filepath.Join(siteDir, "v2", "bad.png")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("v2/bad.png still there: %v", err)
	}
	if m := latestMatch(t, a, siteID); m.Version != 2 || !m.TakenDown {
		t.Fatalf("match row: %+v", m)
	}
}

// A3: a job for a site in Recently deleted waits without counting an
// attempt, and runs after a restore, also for a retained version: v1 holds
// the matched file, v2 a clean one, and v1's copy is quarantined. Pass 3:
// the restore makes the waiting jobs due at once (no hour's wait), and the
// admin queue counts the jobs waiting on a deleted site.
func TestImageScanRestore(t *testing.T) {
	isolateQueueTest(t)
	a, _ := newSiteApp(t, "canonical")
	f := newImageScanFixture(t, a, a.sites.disk)
	a.sites.SetImageScan(f.scan)
	olive := a.newPerson(t, "olive")
	okey := map[string]string{"X-API-Key": olive.key}
	bad, clean := testPNG(t), testPNG(t)
	f.markMatch(sha256Hex(bad))
	deployWithImages(t, a, olive, "comeback", map[string][]byte{"img/bad.png": bad})
	f.wait()
	deployWithImages(t, a, olive, "comeback", map[string][]byte{"img/bad.png": clean})
	f.wait()
	siteID := a.siteID(t, olive, "comeback")
	if n := queueCount(t, a, siteID); n != 2 {
		t.Fatalf("queued %d, want 2", n)
	}
	if r := a.at(t, "DELETE", pcSiteDomain, "/v1/sites/comeback", nil, okey); r.status != http.StatusNoContent {
		t.Fatalf("delete: %d %s", r.status, r.body)
	}
	queue := func() map[string]int {
		t.Helper()
		r := a.at(t, "GET", pcSiteDomain, "/v1/admin/image-scan", nil, map[string]string{"X-API-Key": a.admin})
		var page struct {
			Queue map[string]int `json:"queue"`
		}
		if err := json.Unmarshal(r.body, &page); err != nil {
			t.Fatalf("admin GET: %d %s", r.status, r.body)
		}
		return page.Queue
	}
	waiting := func() int { return queue()["site_deleted"] }
	// Pass 3b: a due job, and one on a retry, waiting on the deleted site
	// count as site_deleted only.
	if _, err := a.database.Exec(`UPDATE image_scan_queue SET attempts = 1 WHERE id = (SELECT min(id) FROM image_scan_queue WHERE site_id = $1)`, siteID); err != nil {
		t.Fatal(err)
	}
	if q := queue(); q["site_deleted"] != 2 || q["pending"] != 0 || q["retrying"] != 0 {
		t.Fatalf("queue while deleted and due: %v, want only site_deleted 2", q)
	}
	if _, err := a.database.Exec(`UPDATE image_scan_queue SET attempts = 0 WHERE site_id = $1`, siteID); err != nil {
		t.Fatal(err)
	}
	f.scan.RunOnce(context.Background())
	if n := queueCount(t, a, siteID); n != 2 || f.matcher.callCount() != 0 {
		t.Fatalf("while deleted: queue %d (want 2 kept), calls %d", n, f.matcher.callCount())
	}
	var maxAttempts int
	var soonest time.Time
	if err := a.database.QueryRow(`SELECT max(attempts), min(next_attempt_at) FROM image_scan_queue WHERE site_id::text = $1`, siteID).Scan(&maxAttempts, &soonest); err != nil {
		t.Fatal(err)
	}
	if maxAttempts != 0 || !soonest.After(time.Now().Add(50*time.Minute)) {
		t.Fatalf("postponed jobs: attempts %d, next %s", maxAttempts, soonest)
	}
	if n := waiting(); n != 2 {
		t.Fatalf("admin queue site_deleted %d, want 2", n)
	}
	if r := a.at(t, "POST", pcSiteDomain, "/v1/sites/comeback/restore", nil, okey); r.status != http.StatusOK {
		t.Fatalf("restore: %d %s", r.status, r.body)
	}
	if n := waiting(); n != 0 {
		t.Fatalf("admin queue site_deleted after restore %d, want 0", n)
	}
	f.scan.RunOnce(context.Background())
	if n := queueCount(t, a, siteID); n != 0 {
		t.Fatalf("queue after restore and a pass: %d", n)
	}
	uid, _ := a.userID(t, olive)
	siteDir := a.sites.disk.SiteDir(uid, "comeback")
	if _, err := os.Stat(filepath.Join(siteDir, "v1", "img", "bad.png")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("v1 copy still there after restore: %v", err)
	}
	for _, v := range []string{"v2", "current"} {
		if got, err := os.ReadFile(filepath.Join(siteDir, v, "img", "bad.png")); err != nil || !bytes.Equal(got, clean) {
			t.Fatalf("%s lost the clean file: %v", v, err)
		}
	}
	if m := latestMatch(t, a, siteID); m.Version != 1 || !m.TakenDown {
		t.Fatalf("match row: %+v", m)
	}
	if site, _ := db.GetSiteByID(context.Background(), a.database, siteID); !site.SiteSuspended {
		t.Fatal("restored site not taken down")
	}
	if !a.sites.disk.IsSuspended(uid, "comeback") {
		t.Fatal("no marker after restore")
	}
}

// 2.3: twelve failed tries, then the job is given up and never claimed again.
func TestImageScanGiveUp(t *testing.T) {
	isolateQueueTest(t)
	a, _ := newSiteApp(t, "canonical")
	f := newImageScanFixture(t, a, a.sites.disk)
	a.sites.SetImageScan(f.scan)
	olive := a.newPerson(t, "olive")
	f.matcher.err = imagescan.ErrRetryable
	deployWithImages(t, a, olive, "stuck", map[string][]byte{"pic.png": testPNG(t)})
	f.wait()
	siteID := a.siteID(t, olive, "stuck")
	for i := 0; i < 13; i++ {
		f.scan.RunOnce(context.Background())
		rerun(t, a, siteID)
	}
	if n := f.matcher.callCount(); n != 12 {
		t.Fatalf("matcher calls %d, want 12", n)
	}
	var attempts int
	var lastErr string
	var gaveUp bool
	if err := a.database.QueryRow(`SELECT attempts, last_error, next_attempt_at = 'infinity' FROM image_scan_queue WHERE site_id::text = $1`, siteID).Scan(&attempts, &lastErr, &gaveUp); err != nil {
		t.Fatal(err)
	}
	if attempts != 12 || !gaveUp || !strings.HasPrefix(lastErr, "gave up: ") {
		t.Fatalf("given-up row: attempts=%d infinity=%v err=%q", attempts, gaveUp, lastErr)
	}
	r := a.at(t, "GET", pcSiteDomain, "/v1/admin/image-scan", nil, map[string]string{"X-API-Key": a.admin})
	var page struct {
		Queue map[string]int `json:"queue"`
	}
	if err := json.Unmarshal(r.body, &page); err != nil || page.Queue["gave_up"] != 1 || page.Queue["retrying"] != 0 {
		t.Fatalf("admin queue: %s", r.body)
	}
}

// 2.5 and 2.6: no evidence copy, so the file stays; the site still goes
// down and the alert says what failed.
func TestImageScanEvidenceFailure(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can write anywhere")
	}
	isolateQueueTest(t)
	a, _ := newSiteApp(t, "canonical")
	f := newImageScanFixture(t, a, a.sites.disk)
	a.sites.SetImageScan(f.scan)
	if err := os.Chmod(f.evidence, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(f.evidence, 0o700) })
	olive := a.newPerson(t, "olive")
	bad := testPNG(t)
	f.markMatch(sha256Hex(bad))
	deployWithImages(t, a, olive, "nocopy", map[string][]byte{"bad.png": bad})
	f.wait()
	f.scan.RunOnce(context.Background())
	uid, _ := a.userID(t, olive)
	siteID := a.siteID(t, olive, "nocopy")
	for _, v := range []string{"v1", "current"} {
		if got, err := os.ReadFile(filepath.Join(a.sites.disk.SiteDir(uid, "nocopy"), v, "bad.png")); err != nil || !bytes.Equal(got, bad) {
			t.Fatalf("%s/bad.png was not kept: %v", v, err)
		}
	}
	if site, _ := db.GetSiteByID(context.Background(), a.database, siteID); !site.SiteSuspended {
		t.Fatal("site not taken down")
	}
	if !a.sites.disk.IsSuspended(uid, "nocopy") {
		t.Fatal("no marker")
	}
	sent := f.mailer.sent()
	if len(sent) != 1 || !strings.Contains(sent[0], "evidence copy: FAILED") || !strings.Contains(sent[0], "file was kept") || !strings.Contains(sent[0], "take-down: done") || !strings.Contains(sent[0], "CyberTipline") {
		t.Fatalf("alert: %v", sent)
	}
	if strings.Contains(sent[0], f.evidence) {
		t.Fatalf("alert names the evidence folder: %s", sent[0])
	}
}

// 2.8: only copies with the matched bytes are removed.
func TestImageScanOnlyMatchedBytes(t *testing.T) {
	isolateQueueTest(t)
	a, _ := newSiteApp(t, "canonical")
	f := newImageScanFixture(t, a, a.sites.disk)
	a.sites.SetImageScan(f.scan)
	olive := a.newPerson(t, "olive")
	bad, benign := testPNG(t), testPNG(t)
	f.markMatch(sha256Hex(bad))
	deployWithImages(t, a, olive, "swap", map[string][]byte{"img/bad.png": bad})
	f.wait()
	deployWithImages(t, a, olive, "swap", map[string][]byte{"img/bad.png": benign})
	f.wait()
	f.scan.RunOnce(context.Background())
	uid, _ := a.userID(t, olive)
	siteDir := a.sites.disk.SiteDir(uid, "swap")
	if _, err := os.Stat(filepath.Join(siteDir, "v1", "img", "bad.png")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("v1 copy still there: %v", err)
	}
	for _, v := range []string{"v2", "current"} {
		if got, err := os.ReadFile(filepath.Join(siteDir, v, "img", "bad.png")); err != nil || !bytes.Equal(got, benign) {
			t.Fatalf("%s lost the benign file: %v", v, err)
		}
	}
}

// 2.9: an evidence folder reached through a symlink into a site is refused.
func TestImageScanEvidenceSymlink(t *testing.T) {
	data := t.TempDir()
	disk, err := storage.NewDiskStorage(data)
	if err != nil {
		t.Fatal(err)
	}
	siteDir := disk.SiteDir("u1", "s1")
	if err := os.MkdirAll(siteDir, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "evidence-link")
	if err := os.Symlink(siteDir, link); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{link, filepath.Join(link, "not-yet")} {
		s := &ImageScan{disk: disk, cfg: ImageScanConfig{EvidenceDir: dir}}
		if why := s.evidenceDirRefused(); why == "" {
			t.Fatalf("%s accepted", dir)
		}
	}
	s := &ImageScan{disk: disk, cfg: ImageScanConfig{EvidenceDir: filepath.Join(data, ".evidence")}}
	if why := s.evidenceDirRefused(); why != "" {
		t.Fatalf("default refused: %s", why)
	}
}

// 2.11: two sites queue the same bytes; the service is asked once.
func TestImageScanWorkerHashCache(t *testing.T) {
	isolateQueueTest(t)
	a, _ := newSiteApp(t, "canonical")
	f := newImageScanFixture(t, a, a.sites.disk)
	a.sites.SetImageScan(f.scan)
	olive := a.newPerson(t, "olive")
	bad := testPNG(t)
	f.markMatch(sha256Hex(bad))
	deployWithImages(t, a, olive, "first", map[string][]byte{"a.png": bad})
	deployWithImages(t, a, olive, "second", map[string][]byte{"b.png": bad})
	f.wait()
	f.scan.RunOnce(context.Background())
	if n := f.matcher.callCount(); n != 1 {
		t.Fatalf("matcher calls %d, want 1", n)
	}
	for _, site := range []string{"first", "second"} {
		if m := latestMatch(t, a, a.siteID(t, olive, site)); !m.TakenDown {
			t.Fatalf("%s: %+v", site, m)
		}
	}
	if m := latestMatch(t, a, a.siteID(t, olive, "second")); m.Provider != "hash-cache" {
		t.Fatalf("second provider %q", m.Provider)
	}
}

// 2.4 and 2.14: an automatic report is filed before the alert, the alert
// drops the CyberTipline line, and record.json carries the report.
func TestImageScanAutoReport(t *testing.T) {
	isolateQueueTest(t)
	a, _ := newSiteApp(t, "canonical")
	f := newImageScanFixture(t, a, a.sites.disk)
	f.scan.cfg.AutoReport = true
	a.sites.SetImageScan(f.scan)
	olive := a.newPerson(t, "olive")
	bad := testPNG(t)
	f.markMatch(sha256Hex(bad))
	deployWithImages(t, a, olive, "auto", map[string][]byte{"bad.png": bad})
	f.wait()
	f.scan.RunOnce(context.Background())
	m := latestMatch(t, a, a.siteID(t, olive, "auto"))
	if m.ReportStatus != "filed" || m.ReportID != "RPT-TEST-1" {
		t.Fatalf("match row: %+v", m)
	}
	sent := f.mailer.sent()
	if len(sent) != 1 || !strings.Contains(sent[0], "report: done, filed as RPT-TEST-1") || strings.Contains(sent[0], "CyberTipline") {
		t.Fatalf("alert: %v", sent)
	}
	b, err := os.ReadFile(filepath.Join(m.EvidencePath, "record.json"))
	if err != nil {
		t.Fatal(err)
	}
	var rec map[string]any
	if err := json.Unmarshal(b, &rec); err != nil || rec["report_status"] != "filed" || rec["report_id"] != "RPT-TEST-1" || rec["reported_at"] == nil {
		t.Fatalf("record.json: %s", b)
	}
	// Reports not configured: the match is 'off' and the route still files it once configured.
	f.scan.cfg.ReportName = ""
	other := testPNG(t)
	f.markMatch(sha256Hex(other))
	deployWithImages(t, a, olive, "auto2", map[string][]byte{"x.png": other})
	f.wait()
	f.scan.RunOnce(context.Background())
	m2 := latestMatch(t, a, a.siteID(t, olive, "auto2"))
	if m2.ReportStatus != "off" {
		t.Fatalf("unconfigured match status %q", m2.ReportStatus)
	}
	f.scan.cfg.ReportName = "Tester"
	if r := a.at(t, "POST", pcSiteDomain, "/v1/admin/image-scan/"+id64(m2.ID)+"/report", nil, map[string]string{"X-API-Key": a.admin}); r.status != 200 {
		t.Fatalf("report of an 'off' match: %d %s", r.status, r.body)
	}
}

// 2.17: a reply cut at the cap is kept as a JSON string.
func TestImageScanRawJSON(t *testing.T) {
	if got := rawJSON(json.RawMessage(`{"IsMatch":true}`)); string(got) != `{"IsMatch":true}` {
		t.Fatalf("valid reply changed: %s", got)
	}
	got := rawJSON(json.RawMessage(`{"IsMatch":tr`))
	var s string
	if !json.Valid(got) || json.Unmarshal(got, &s) != nil || s != `{"IsMatch":tr` {
		t.Fatalf("cut reply: %s", got)
	}
}

// A1 and A2: a files-storage upload names who wrote it (the owner, or an
// anonymous visitor, never the owner in their place), and the match keeps
// the uploader and the address as they were at detection.
func TestImageScanStorageUploader(t *testing.T) {
	isolateQueueTest(t)
	ps := newPresetSite(t)
	a := ps.a
	f := newImageScanFixture(t, a, a.sites.disk)
	a.sites.SetImageScan(f.scan)
	ps.must(t, "PUT", "/resources/drop", map[string]string{"kind": "files", "preset": "inbox"}, ps.key, 201)
	anonPic, ownerPic := testPNG(t), testPNG(t)
	f.markMatch(sha256Hex(anonPic))
	f.markMatch(sha256Hex(ownerPic))
	anon := map[string]string{"Content-Type": "image/png"}
	for k, v := range ps.callers["anon"] {
		anon[k] = v
	}
	ps.must(t, "PUT", "/files/drop/objects/anon.png", string(anonPic), anon, 200)
	ps.must(t, "PUT", "/files/drop/objects/owner.png", string(ownerPic), map[string]string{"X-API-Key": ps.owner.key, "Content-Type": "image/png"}, 200)
	f.wait()
	f.scan.RunOnce(context.Background())
	rows, err := db.ListImageMatches(context.Background(), a.database, 50)
	if err != nil {
		t.Fatal(err)
	}
	byPath := map[string]db.ImageMatch{}
	for _, m := range rows {
		if m.SiteID == ps.sid {
			byPath[m.Path] = m
		}
	}
	am, om := byPath["anon.png"], byPath["owner.png"]
	if am.Uploader != "anonymous visitor (no account)" || am.Address == "" {
		t.Fatalf("anonymous upload: uploader %q address %q", am.Uploader, am.Address)
	}
	if !strings.Contains(om.Uploader, ps.owner.email) || om.WriterID != "owner" {
		t.Fatalf("owner upload: uploader %q writer %q", om.Uploader, om.WriterID)
	}
	admin := map[string]string{"X-API-Key": a.admin}
	if r := a.at(t, "POST", pcSiteDomain, "/v1/admin/image-scan/"+id64(am.ID)+"/report", nil, admin); r.status != 200 {
		t.Fatalf("report: %d %s", r.status, r.body)
	}
	if len(f.reporter.reports) != 1 || !strings.Contains(f.reporter.reports[0].ReporteeName, "anonymous visitor") || strings.Contains(f.reporter.reports[0].ReporteeName, ps.owner.email) {
		t.Fatalf("report names %+v", f.reporter.reports)
	}
	r := a.at(t, "GET", pcSiteDomain, "/v1/admin/image-scan", nil, admin)
	if !strings.Contains(string(r.body), `"uploader":"anonymous visitor (no account)"`) || !strings.Contains(string(r.body), `"address":"`+am.Address+`"`) {
		t.Fatalf("admin GET lacks uploader or address: %s", r.body)
	}
}

// A6: an existing folder for the match is never reused: the evidence step
// fails and the file stays in place.
func TestImageScanEvidenceFolderExists(t *testing.T) {
	isolateQueueTest(t)
	a, _ := newSiteApp(t, "canonical")
	f := newImageScanFixture(t, a, a.sites.disk)
	a.sites.SetImageScan(f.scan)
	var last int64
	if err := a.database.QueryRow(`SELECT nextval(pg_get_serial_sequence('image_scan_matches', 'id'))`).Scan(&last); err != nil {
		t.Fatal(err)
	}
	for id := last + 1; id <= last+5; id++ {
		if err := os.Mkdir(filepath.Join(f.evidence, id64(id)), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	olive := a.newPerson(t, "olive")
	bad := testPNG(t)
	f.markMatch(sha256Hex(bad))
	deployWithImages(t, a, olive, "taken", map[string][]byte{"bad.png": bad})
	f.wait()
	f.scan.RunOnce(context.Background())
	uid, _ := a.userID(t, olive)
	siteID := a.siteID(t, olive, "taken")
	m := latestMatch(t, a, siteID)
	if m.ID <= last || m.ID > last+5 {
		t.Fatalf("match id %d outside the pre-created %d..%d", m.ID, last+1, last+5)
	}
	if entries, _ := os.ReadDir(filepath.Join(f.evidence, id64(m.ID))); len(entries) != 0 || m.EvidencePath != "" {
		t.Fatalf("existing folder was written to: %d entries, evidence_path %q", len(entries), m.EvidencePath)
	}
	if got, err := os.ReadFile(filepath.Join(a.sites.disk.SiteDir(uid, "taken"), "current", "bad.png")); err != nil || !bytes.Equal(got, bad) {
		t.Fatalf("file not kept: %v", err)
	}
	if site, _ := db.GetSiteByID(context.Background(), a.database, siteID); !site.SiteSuspended {
		t.Fatal("site not taken down")
	}
	if sent := f.mailer.sent(); len(sent) != 1 || !strings.Contains(sent[0], "evidence copy: FAILED") || !strings.Contains(sent[0], "file was kept") {
		t.Fatalf("alert: %v", sent)
	}
}

// A7: a directory symlink out of a version folder is never followed for a
// removal.
func TestRemoveFileEverywhereSymlinkedDir(t *testing.T) {
	disk, err := storage.NewDiskStorage(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	bad := testPNG(t)
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "bad.png"), bad, 0o644); err != nil {
		t.Fatal(err)
	}
	siteDir := disk.SiteDir("u1", "s1")
	if err := os.MkdirAll(filepath.Join(siteDir, "v2", "img"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(siteDir, "v1"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(siteDir, "v1", "img")); err != nil {
		t.Fatal(err)
	}
	inside := filepath.Join(siteDir, "v2", "img", "bad.png")
	if err := os.WriteFile(inside, bad, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := disk.RemoveFileEverywhere("u1", "s1", "img/bad.png", sha256Hex(bad)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(outside, "bad.png")); err != nil {
		t.Fatalf("the file outside the site was removed: %v", err)
	}
	if _, err := os.Stat(inside); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the copy inside v2 was kept: %v", err)
	}
}

// Pass 3, item 1: a rename moves the files before the row has the new name.
// A job run in that window waits for the rename's lock and then reads the
// files at the new name; it is not dropped.
func TestImageScanRenameWindow(t *testing.T) {
	isolateQueueTest(t)
	a, _ := newSiteApp(t, "canonical")
	f := newImageScanFixture(t, a, a.sites.disk)
	a.sites.SetImageScan(f.scan)
	olive := a.newPerson(t, "olive")
	bad := testPNG(t)
	f.markMatch(sha256Hex(bad))
	deployWithImages(t, a, olive, "before", map[string][]byte{"bad.png": bad})
	f.wait()
	uid, _ := a.userID(t, olive)
	siteID := a.siteID(t, olive, "before")
	// The rename's window: its lock held, the files moved, the row not yet.
	unlock := a.sites.lockSite(uid, "before")
	if err := a.sites.disk.RenameSite(uid, "before", "after"); err != nil {
		unlock()
		t.Fatal(err)
	}
	done := make(chan int, 1)
	go func() { done <- f.scan.RunOnce(context.Background()) }()
	finished := false
	select {
	case <-done:
		finished = true
		t.Error("the worker ran inside the rename's window")
	case <-time.After(300 * time.Millisecond):
	}
	if err := db.RenameSite(context.Background(), a.database, siteID, "after", ""); err != nil {
		unlock()
		t.Fatal(err)
	}
	unlock()
	if !finished {
		<-done
	}
	if n := queueCount(t, a, siteID); n != 0 {
		t.Fatalf("queue after the pass: %d", n)
	}
	if f.matcher.callCount() != 1 {
		t.Fatalf("matcher calls %d, want 1 (the job was dropped)", f.matcher.callCount())
	}
	m := latestMatch(t, a, siteID)
	if m.SiteName != "after" || !m.TakenDown {
		t.Fatalf("match row: %+v", m)
	}
	if _, err := os.Stat(filepath.Join(a.sites.disk.SiteDir(uid, "after"), "current", "bad.png")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("bad.png still served under the new name: %v", err)
	}
}

// Pass 3, item 2: the service accepted a report but saving 'filed' failed.
// The save is tried again, and a second report is refused because
// record.json says it was filed; the reporter is not called twice.
func TestImageScanReportSaveFails(t *testing.T) {
	isolateQueueTest(t)
	a, _ := newSiteApp(t, "canonical")
	f := newImageScanFixture(t, a, a.sites.disk)
	a.sites.SetImageScan(f.scan)
	olive := a.newPerson(t, "olive")
	bad, other := testPNG(t), testPNG(t)
	f.markMatch(sha256Hex(bad))
	f.markMatch(sha256Hex(other))
	deployWithImages(t, a, olive, "nosave", map[string][]byte{"bad.png": bad})
	deployWithImages(t, a, olive, "flaky", map[string][]byte{"other.png": other})
	f.wait()
	f.scan.RunOnce(context.Background())
	ctx := context.Background()
	m := latestMatch(t, a, a.siteID(t, olive, "nosave"))
	saves := 0
	f.scan.saveReport = func(context.Context, db.Querier, int64, string, string, string) error {
		saves++
		return errors.New("database down")
	}
	_, _, err := f.scan.fileReport(ctx, m.ID)
	var saveErr *reportSaveError
	if !errors.As(err, &saveErr) {
		t.Fatalf("first report: %v", err)
	}
	if saves != imageReportSaveTries {
		t.Fatalf("save tried %d times, want %d", saves, imageReportSaveTries)
	}
	if got, _ := db.GetImageMatch(ctx, a.database, m.ID); got.ReportStatus == "filed" {
		t.Fatalf("status saved through a failing save: %+v", got)
	}
	rid, _, err := f.scan.fileReport(ctx, m.ID)
	if !errors.Is(err, errReportAlreadyFiled) || rid != "RPT-TEST-1" {
		t.Fatalf("second report: %q %v", rid, err)
	}
	if n := len(f.reporter.reports); n != 1 {
		t.Fatalf("reporter called %d times, want 1", n)
	}
	// A save that fails once is tried again and lands. The first call fails
	// whatever its context; a retry on the caller's context would fail too
	// when that context has ended.
	flaky := 0
	f.scan.saveReport = func(ctx context.Context, q db.Querier, id int64, st, rid, e string) error {
		flaky++
		if flaky == 1 {
			return errors.New("database blip")
		}
		return db.SetImageMatchReport(ctx, q, id, st, rid, e)
	}
	m2 := latestMatch(t, a, a.siteID(t, olive, "flaky"))
	if _, _, err := f.scan.fileReport(ctx, m2.ID); err != nil {
		t.Fatalf("report with one failed save: %v", err)
	}
	if flaky != 2 {
		t.Fatalf("save called %d times, want a failure then a success", flaky)
	}
	if got, _ := db.GetImageMatch(ctx, a.database, m2.ID); got.ReportStatus != "filed" || got.ReportID != "RPT-TEST-1" {
		t.Fatalf("after a retried save: %+v", got)
	}
	// The caller's request has ended: the retry still lands.
	if _, err := a.database.Exec(`UPDATE image_scan_matches SET report_status = 'pending', report_id = '' WHERE id = $1`, m2.ID); err != nil {
		t.Fatal(err)
	}
	flaky = 0
	gone, cancel := context.WithCancel(ctx)
	cancel()
	if err := f.scan.saveFiled(gone, m2.ID, "RPT-ENDED"); err != nil {
		t.Fatalf("save after the caller ended: %v", err)
	}
	if got, _ := db.GetImageMatch(ctx, a.database, m2.ID); flaky != 2 || got.ReportStatus != "filed" || got.ReportID != "RPT-ENDED" {
		t.Fatalf("after the caller ended: calls %d, %+v", flaky, got)
	}
}

// Pass 3, item 4: on Simple Hack a team member's deploy and files-storage
// upload record the member, and the report names the member, never the
// event's holding account.
func TestImageScanHackUploader(t *testing.T) {
	isolateQueueTest(t)
	a := newBridgeTeamApp(t)
	f := newImageScanFixtureDB(t, a.database, a.sites.disk)
	a.sites.SetImageScan(f.scan)
	org, p1 := a.newPerson(t, "org"), a.newPerson(t, "p1")
	slug := a.makeEvent(t, org)
	a.join(t, slug, p1, org)
	team, _ := a.startTeam(t, slug, "Alpha", p1)
	key := a.teamKey(t, slug, p1)
	markReady(t, a.certDir, slug)
	deployed, stored := testPNG(t), testPNG(t)
	f.markMatch(sha256Hex(deployed))
	f.markMatch(sha256Hex(stored))
	body := map[string]any{"files": map[string]string{"index.html": "<h1>alpha</h1>"},
		"files_base64": map[string]string{"img/bad.png": base64.StdEncoding.EncodeToString(deployed)}}
	if r := a.api(t, "PUT", "/v1/sites/"+team+"/files?create=1", body, key); r.status != http.StatusCreated && r.status != http.StatusOK {
		t.Fatalf("team deploy: %d %s", r.status, r.body)
	}
	wantTS(t, "team files resource", a.api(t, "PUT", "/v1/sites/"+team+"/storage/resources/pics", map[string]string{"kind": "files"}, key), 201, "")
	wantTS(t, "team file", a.api(t, "PUT", "/v1/sites/"+team+"/storage/files/pics/objects/bad.png", string(stored), key), 200, "")
	f.wait()
	f.scan.RunOnce(context.Background())
	member := a.uid(t, p1)
	rows, err := db.ListImageMatches(context.Background(), a.database, 50)
	if err != nil {
		t.Fatal(err)
	}
	bySHA := map[string]db.ImageMatch{}
	for _, m := range rows {
		bySHA[m.SHA256] = m
	}
	site, obj := bySHA[sha256Hex(deployed)], bySHA[sha256Hex(stored)]
	if site.Kind != "site_file" || site.WriterID != member {
		t.Fatalf("deploy match writer %q, want the member %s: %+v", site.WriterID, member, site)
	}
	if obj.Kind != "storage_file" || obj.WriterID != "user:"+member {
		t.Fatalf("storage match writer %q, want user:%s: %+v", obj.WriterID, member, obj)
	}
	for _, m := range []db.ImageMatch{site, obj} {
		if !strings.Contains(m.Uploader, p1.email) || strings.Contains(m.Uploader, holdingAccountSuffix) {
			t.Fatalf("%s: uploader %q, want %s", m.Kind, m.Uploader, p1.email)
		}
		if !strings.HasSuffix(m.OwnerEmail, holdingAccountSuffix) {
			t.Fatalf("%s: owner %q is not the holding account; the test proves nothing", m.Kind, m.OwnerEmail)
		}
		if _, _, err := f.scan.fileReport(context.Background(), m.ID); err != nil {
			t.Fatalf("%s report: %v", m.Kind, err)
		}
	}
	if len(f.reporter.reports) != 2 {
		t.Fatalf("reports %d", len(f.reporter.reports))
	}
	for _, rep := range f.reporter.reports {
		if !strings.Contains(rep.ReporteeName, p1.email) || strings.Contains(rep.ReporteeName, holdingAccountSuffix) {
			t.Fatalf("report names %q, want the member %s", rep.ReporteeName, p1.email)
		}
	}
}

// Pass 3, item 5: a report carries the uploader and address recorded at
// detection, not today's; the admin list shows an empty recorded value as
// empty.
func TestImageScanRecordedValues(t *testing.T) {
	isolateQueueTest(t)
	a, _ := newSiteApp(t, "canonical")
	f := newImageScanFixture(t, a, a.sites.disk)
	a.sites.SetImageScan(f.scan)
	olive := a.newPerson(t, "olive")
	bad := testPNG(t)
	f.markMatch(sha256Hex(bad))
	deployWithImages(t, a, olive, "recorded", map[string][]byte{"bad.png": bad})
	f.wait()
	f.scan.RunOnce(context.Background())
	ctx := context.Background()
	uid, _ := a.userID(t, olive)
	siteID := a.siteID(t, olive, "recorded")
	m := latestMatch(t, a, siteID)
	if !strings.Contains(m.Uploader, olive.email) || m.Address == "" {
		t.Fatalf("recorded: uploader %q address %q", m.Uploader, m.Address)
	}
	suffix := strconv.FormatInt(time.Now().UnixNano(), 36)
	if _, err := a.database.Exec(`UPDATE users SET username = $2, handle = $3 WHERE id = $1`, uid, "moved-"+suffix+"@example.com", "moved"+suffix); err != nil {
		t.Fatal(err)
	}
	if _, err := a.database.Exec(`UPDATE sites SET name = 'renamed' WHERE id = $1`, siteID); err != nil {
		t.Fatal(err)
	}
	if now := f.scan.reporteeOf(ctx, m); now == m.Uploader {
		t.Fatalf("the uploader did not change (%q); the test proves nothing", now)
	}
	if now := f.scan.addressOf(ctx, m); now == m.Address {
		t.Fatalf("the address did not change (%q); the test proves nothing", now)
	}
	if _, _, err := f.scan.fileReport(ctx, m.ID); err != nil {
		t.Fatal(err)
	}
	if len(f.reporter.reports) != 1 {
		t.Fatalf("reports %d", len(f.reporter.reports))
	}
	rep := f.reporter.reports[0]
	if rep.ReporteeName != m.Uploader || rep.Files[0].Metadata["address"] != m.Address {
		t.Fatalf("report names %q at %q, want the recorded %q at %q", rep.ReporteeName, rep.Files[0].Metadata["address"], m.Uploader, m.Address)
	}
	if _, err := a.database.Exec(`UPDATE image_scan_matches SET uploader = '', address = '' WHERE id = $1`, m.ID); err != nil {
		t.Fatal(err)
	}
	r := a.at(t, "GET", pcSiteDomain, "/v1/admin/image-scan", nil, map[string]string{"X-API-Key": a.admin})
	var page struct {
		Matches []map[string]any `json:"matches"`
	}
	if err := json.Unmarshal(r.body, &page); err != nil {
		t.Fatalf("admin GET: %d %s", r.status, r.body)
	}
	found := false
	for _, row := range page.Matches {
		if id, _ := row["id"].(float64); int64(id) == m.ID {
			found = true
			if row["uploader"] != "" || row["address"] != "" {
				t.Fatalf("empty recorded values filled in: uploader %v address %v", row["uploader"], row["address"])
			}
		}
	}
	if !found {
		t.Fatalf("match %d not listed", m.ID)
	}
}

// Pass 3, item 6a: a re-upload of bytes whose job was given up is queued
// again with a fresh count and checked.
func TestImageScanRequeueGivenUp(t *testing.T) {
	isolateQueueTest(t)
	a, _ := newSiteApp(t, "canonical")
	f := newImageScanFixture(t, a, a.sites.disk)
	a.sites.SetImageScan(f.scan)
	olive := a.newPerson(t, "olive")
	pic := testPNG(t)
	deployWithImages(t, a, olive, "again", map[string][]byte{"pic.png": pic})
	f.wait()
	uid, _ := a.userID(t, olive)
	siteID := a.siteID(t, olive, "again")
	if _, err := a.database.Exec(`UPDATE image_scan_queue SET attempts = 12, last_error = 'gave up: x', next_attempt_at = 'infinity',
		created_at = now() - interval '3 days' WHERE site_id = $1`, siteID); err != nil {
		t.Fatal(err)
	}
	// Pass 3b: someone else re-uploads the same bytes; the revived job
	// names them and the time of their upload.
	otto := a.newPerson(t, "otto")
	ottoID, _ := a.userID(t, otto)
	if ottoID == uid {
		t.Fatal("same writer; the test proves nothing")
	}
	f.scan.EnqueueSiteFiles(siteID, ottoID, "again", 1, map[string][]byte{"pic.png": pic})
	f.wait()
	var rows, attempts int
	var due bool
	var writer string
	var created time.Time
	if err := a.database.QueryRow(`SELECT count(*), max(attempts), bool_and(next_attempt_at <= now()), max(writer_id), max(created_at)
		 FROM image_scan_queue WHERE site_id = $1`, siteID).Scan(&rows, &attempts, &due, &writer, &created); err != nil {
		t.Fatal(err)
	}
	if rows != 1 || attempts != 0 || !due {
		t.Fatalf("after a re-upload: rows %d attempts %d due %v", rows, attempts, due)
	}
	if writer != ottoID || time.Since(created) > time.Hour {
		t.Fatalf("revived job: writer %q (want the re-uploader %s), created %s", writer, ottoID, created)
	}
	if n := f.scan.RunOnce(context.Background()); n != 1 || f.matcher.callCount() != 1 {
		t.Fatalf("RunOnce handled %d, matcher calls %d", n, f.matcher.callCount())
	}
}

// Pass 3b, item 2: the worker read the site as deleted, then a restore made
// the job due before the worker's postpone landed. The postpone must not
// push it an hour out again.
func TestImageScanPostponeAfterRestore(t *testing.T) {
	isolateQueueTest(t)
	a, _ := newSiteApp(t, "canonical")
	f := newImageScanFixture(t, a, a.sites.disk)
	a.sites.SetImageScan(f.scan)
	olive := a.newPerson(t, "olive")
	okey := map[string]string{"X-API-Key": olive.key}
	deployWithImages(t, a, olive, "racing", map[string][]byte{"pic.png": testPNG(t)})
	f.wait()
	siteID := a.siteID(t, olive, "racing")
	ctx := context.Background()
	var id int64
	if err := a.database.QueryRow(`SELECT id FROM image_scan_queue WHERE site_id = $1`, siteID).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if r := a.at(t, "DELETE", pcSiteDomain, "/v1/sites/racing", nil, okey); r.status != http.StatusNoContent {
		t.Fatalf("delete: %d %s", r.status, r.body)
	}
	due := func() bool {
		t.Helper()
		var d bool
		if err := a.database.QueryRow(`SELECT next_attempt_at <= now() FROM image_scan_queue WHERE id = $1`, id).Scan(&d); err != nil {
			t.Fatal(err)
		}
		return d
	}
	// While deleted the postpone works.
	if err := db.PostponeImageScan(ctx, a.database, id, imageScanDeletedWait); err != nil {
		t.Fatal(err)
	}
	if due() {
		t.Fatal("postpone on a deleted site left the job due")
	}
	// The worker sees the site deleted; the restore lands; then its postpone.
	if r := a.at(t, "POST", pcSiteDomain, "/v1/sites/racing/restore", nil, okey); r.status != http.StatusOK {
		t.Fatalf("restore: %d %s", r.status, r.body)
	}
	if !due() {
		t.Fatal("restore did not make the job due")
	}
	if err := db.PostponeImageScan(ctx, a.database, id, imageScanDeletedWait); err != nil {
		t.Fatal(err)
	}
	if !due() {
		t.Fatal("a postpone after the restore pushed the job out again")
	}
}

// hookMailer runs a hook before each notice, so a test can act while the
// worker is between its last step and its last record.json write.
type hookMailer struct {
	fakeNoticeMailer
	hook func()
}

func (h *hookMailer) SendNotice(to, subject, text string) error {
	if h.hook != nil {
		h.hook()
	}
	return h.fakeNoticeMailer.SendNotice(to, subject, text)
}

// Pass 3b, item 3: a report filed from the admin page while the worker is
// still finishing the match stays filed in record.json.
func TestImageScanRecordKeepsFiledReport(t *testing.T) {
	isolateQueueTest(t)
	a, _ := newSiteApp(t, "canonical")
	f := newImageScanFixture(t, a, a.sites.disk)
	a.sites.SetImageScan(f.scan)
	olive := a.newPerson(t, "olive")
	bad := testPNG(t)
	f.markMatch(sha256Hex(bad))
	deployWithImages(t, a, olive, "clicked", map[string][]byte{"bad.png": bad})
	f.wait()
	siteID := a.siteID(t, olive, "clicked")
	ctx := context.Background()
	var filedErr error
	filed := false
	f.scan.mailer = &hookMailer{hook: func() {
		m := latestMatch(t, a, siteID)
		_, _, filedErr = f.scan.fileReport(ctx, m.ID)
		filed = true
	}}
	f.scan.RunOnce(ctx)
	if !filed || filedErr != nil {
		t.Fatalf("admin report during the worker: ran %v, %v", filed, filedErr)
	}
	m := latestMatch(t, a, siteID)
	b, err := os.ReadFile(filepath.Join(m.EvidencePath, "record.json"))
	if err != nil {
		t.Fatal(err)
	}
	var rec map[string]any
	if err := json.Unmarshal(b, &rec); err != nil {
		t.Fatal(err)
	}
	if rec["report_status"] != "filed" || rec["report_id"] != "RPT-TEST-1" {
		t.Fatalf("record.json lost the filed report: status %v id %v", rec["report_status"], rec["report_id"])
	}
	if _, ok := rec["steps"]; !ok {
		t.Fatal("record.json has no steps; the worker's last write did not land")
	}
}

// Pass 3b, item 5: with no uploader on record on an event's site, the
// report names nobody, not the event's holding account.
func TestImageScanReporteeNoHoldingAccount(t *testing.T) {
	s := &ImageScan{}
	m := db.ImageMatch{Kind: "site_file", OwnerHandle: "ev-holding", OwnerEmail: "ev-holding" + holdingAccountSuffix}
	if got := s.reporteeOf(context.Background(), m); got != "uploader not recorded" {
		t.Fatalf("reportee %q, want %q", got, "uploader not recorded")
	}
	m = db.ImageMatch{Kind: "storage_file", WriterID: "owner", OwnerHandle: "ev-holding", OwnerEmail: "ev-holding" + holdingAccountSuffix}
	if got := s.reporteeOf(context.Background(), m); got != "uploader not recorded" {
		t.Fatalf("owner write: reportee %q, want %q", got, "uploader not recorded")
	}
}

// Pass 3b, item 6: an organiser's publish of the event website and an
// organiser's event-storage upload record the organiser.
func TestImageScanHackOrganiserUploader(t *testing.T) {
	isolateQueueTest(t)
	a := newBridgeTeamApp(t)
	f := newImageScanFixtureDB(t, a.database, a.sites.disk)
	a.sites.SetImageScan(f.scan)
	org := a.newPerson(t, "org")
	slug := a.makeEvent(t, org)
	markReady(t, a.certDir, slug)
	deployed, stored := testPNG(t), testPNG(t)
	f.markMatch(sha256Hex(deployed))
	f.markMatch(sha256Hex(stored))
	ev := "/v1/hack/events/" + slug + "/website"
	body := map[string]any{"files": map[string]string{"index.html": "<h1>event</h1>"},
		"files_base64": map[string]string{"img/bad.png": base64.StdEncoding.EncodeToString(deployed)}}
	wantTS(t, "organiser publish", a.api(t, "PUT", ev+"/files?create=1", body, org.key), 201, "")
	wantTS(t, "organiser files resource", a.api(t, "PUT", ev+"/storage/resources/pics", map[string]string{"kind": "files"}, org.key), 201, "")
	wantTS(t, "organiser file", a.api(t, "PUT", ev+"/storage/files/pics/objects/bad.png", string(stored), org.key), 200, "")
	f.wait()
	f.scan.RunOnce(context.Background())
	organiser := a.uid(t, org)
	rows, err := db.ListImageMatches(context.Background(), a.database, 50)
	if err != nil {
		t.Fatal(err)
	}
	bySHA := map[string]db.ImageMatch{}
	for _, m := range rows {
		bySHA[m.SHA256] = m
	}
	site, obj := bySHA[sha256Hex(deployed)], bySHA[sha256Hex(stored)]
	if site.Kind != "site_file" || site.WriterID != organiser {
		t.Fatalf("publish match writer %q, want the organiser %s: %+v", site.WriterID, organiser, site)
	}
	if obj.Kind != "storage_file" || obj.WriterID != "user:"+organiser {
		t.Fatalf("storage match writer %q, want user:%s: %+v", obj.WriterID, organiser, obj)
	}
	for _, m := range []db.ImageMatch{site, obj} {
		if !strings.HasSuffix(m.OwnerEmail, holdingAccountSuffix) {
			t.Fatalf("%s: owner %q is not the holding account; the test proves nothing", m.Kind, m.OwnerEmail)
		}
		if !strings.Contains(m.Uploader, org.email) {
			t.Fatalf("%s: uploader %q, want the organiser %s", m.Kind, m.Uploader, org.email)
		}
	}
}

// Pass 3c, item 4: a team key's deploy to an EXISTING team site, published
// and with publish=false, records the member as the writer.
func TestImageScanHackUploaderExistingSite(t *testing.T) {
	isolateQueueTest(t)
	a := newBridgeTeamApp(t)
	f := newImageScanFixtureDB(t, a.database, a.sites.disk)
	a.sites.SetImageScan(f.scan)
	org, p1 := a.newPerson(t, "org"), a.newPerson(t, "p1")
	slug := a.makeEvent(t, org)
	a.join(t, slug, p1, org)
	team, _ := a.startTeam(t, slug, "Alpha", p1)
	key := a.teamKey(t, slug, p1)
	markReady(t, a.certDir, slug)
	first := map[string]any{"files": map[string]string{"index.html": "<h1>alpha</h1>"}}
	if r := a.api(t, "PUT", "/v1/sites/"+team+"/files?create=1", first, key); r.status != http.StatusCreated && r.status != http.StatusOK {
		t.Fatalf("team create: %d %s", r.status, r.body)
	}
	published, unpublished := testPNG(t), testPNG(t)
	f.markMatch(sha256Hex(published))
	f.markMatch(sha256Hex(unpublished))
	deploy := func(query string, img []byte) {
		t.Helper()
		body := map[string]any{"files": map[string]string{"index.html": "<h1>alpha</h1>"},
			"files_base64": map[string]string{"img/bad.png": base64.StdEncoding.EncodeToString(img)}}
		if r := a.api(t, "PUT", "/v1/sites/"+team+"/files"+query, body, key); r.status != http.StatusOK {
			t.Fatalf("team update %q: %d %s", query, r.status, r.body)
		}
	}
	deploy("?publish=false", unpublished)
	deploy("", published)
	f.wait()
	member := a.uid(t, p1)
	var holding string
	if err := a.database.QueryRow(`SELECT s.user_id FROM sites s WHERE s.name = $1`, team).Scan(&holding); err != nil {
		t.Fatal(err)
	}
	if holding == member {
		t.Fatal("the team site is owned by the member; the test proves nothing")
	}
	for name, img := range map[string][]byte{"publish=false": unpublished, "published": published} {
		var writer string
		if err := a.database.QueryRow(`SELECT writer_id FROM image_scan_queue WHERE sha256 = $1`, sha256Hex(img)).Scan(&writer); err != nil {
			t.Fatalf("%s: queued job: %v", name, err)
		}
		if writer != member {
			t.Fatalf("%s: queued writer %q, want the member %s", name, writer, member)
		}
	}
}

// Pass 3c, item 1: when the site is deleted, loadJobBytes has postponed the
// job by the time it returns (so under the site lock, not after it).
func TestImageScanPostponeUnderLock(t *testing.T) {
	isolateQueueTest(t)
	a, _ := newSiteApp(t, "canonical")
	f := newImageScanFixture(t, a, a.sites.disk)
	a.sites.SetImageScan(f.scan)
	olive := a.newPerson(t, "olive")
	okey := map[string]string{"X-API-Key": olive.key}
	deployWithImages(t, a, olive, "locked", map[string][]byte{"pic.png": testPNG(t)})
	f.wait()
	siteID := a.siteID(t, olive, "locked")
	ctx := context.Background()
	j := db.ImageScanJob{Kind: "site_file", SiteID: siteID}
	if err := a.database.QueryRow(`SELECT id, version, path, sha256 FROM image_scan_queue WHERE site_id = $1`, siteID).Scan(&j.ID, &j.Version, &j.Path, &j.SHA256); err != nil {
		t.Fatal(err)
	}
	if r := a.at(t, "DELETE", pcSiteDomain, "/v1/sites/locked", nil, okey); r.status != http.StatusNoContent {
		t.Fatalf("delete: %d %s", r.status, r.body)
	}
	if _, err := f.scan.loadJobBytes(ctx, j); !errors.Is(err, errImageScanSiteDeleted) {
		t.Fatalf("loadJobBytes: %v, want errImageScanSiteDeleted", err)
	}
	var due bool
	if err := a.database.QueryRow(`SELECT next_attempt_at <= now() FROM image_scan_queue WHERE id = $1`, j.ID).Scan(&due); err != nil {
		t.Fatal(err)
	}
	if due {
		t.Fatal("loadJobBytes returned without postponing the job under the site lock")
	}
}

// Pass 3c, item 2: the restore's wake runs even when the request's context
// is already cancelled (the client hung up after the commit).
func TestImageScanWakeCancelledContext(t *testing.T) {
	isolateQueueTest(t)
	a, _ := newSiteApp(t, "canonical")
	f := newImageScanFixture(t, a, a.sites.disk)
	a.sites.SetImageScan(f.scan)
	olive := a.newPerson(t, "olive")
	deployWithImages(t, a, olive, "hungup", map[string][]byte{"pic.png": testPNG(t)})
	f.wait()
	siteID := a.siteID(t, olive, "hungup")
	if _, err := a.database.Exec(`UPDATE image_scan_queue SET next_attempt_at = now() + interval '1 hour' WHERE site_id = $1`, siteID); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	a.sites.wakeImageScans(ctx, siteID)
	var due bool
	if err := a.database.QueryRow(`SELECT bool_and(next_attempt_at <= now()) FROM image_scan_queue WHERE site_id = $1`, siteID).Scan(&due); err != nil {
		t.Fatal(err)
	}
	if !due {
		t.Fatal("a cancelled request context stopped the wake")
	}
}
