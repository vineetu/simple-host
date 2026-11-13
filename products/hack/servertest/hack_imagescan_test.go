package servertest

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/vsriram/simple-host/internal/app/operations"
	accountrepo "github.com/vsriram/simple-host/internal/repository/accounts"
	opsrepo "github.com/vsriram/simple-host/internal/repository/operations"
	"github.com/vsriram/simple-host/internal/storage"
	eventrepo "github.com/vsriram/simple-host/products/hack/repository/hackevents"
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
	if len(buf.Bytes()) < operations.ImageScanMinBytes {
		t.Fatalf("test PNG too small: %d bytes", buf.Len())
	}
	return buf.Bytes()
}

type fakeMatcher struct {
	mu      sync.Mutex
	matches map[string]operations.ScanMatch // by SHA-256 of the original bytes
	calls   []string
	err     error
}

func (f *fakeMatcher) Name() string { return "fake" }
func (f *fakeMatcher) Match(_ context.Context, img *operations.ScanImage) (operations.ScanMatch, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, img.SHA256)
	if f.err != nil {
		return operations.ScanMatch{}, f.err
	}
	if m, ok := f.matches[img.SHA256]; ok {
		return m, nil
	}
	return operations.ScanMatch{TrackingID: "t-clean"}, nil
}
func (f *fakeMatcher) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

type fakeReporter struct {
	mu      sync.Mutex
	reports []operations.ScanReport
	err     error
}

func (f *fakeReporter) Report(_ context.Context, in operations.ScanReport) (operations.ScanReportResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return operations.ScanReportResult{}, f.err
	}
	f.reports = append(f.reports, in)
	return operations.ScanReportResult{ReportID: "RPT-TEST-1"}, nil
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
func fakePrepare(raw []byte, _ operations.ScanPrepareOptions) (*operations.ScanImage, string, error) {
	ct, ok := operations.ImageCandidate(raw)
	if !ok {
		return nil, "not an image", nil
	}
	return &operations.ScanImage{Data: raw, ContentType: ct, Width: 24, Height: 24, SHA256: operations.ImageHash(raw)}, "", nil
}

// scanFixture is an ImageScan with fakes, and a way to wait for the
// enqueue work that runs off the request.
type scanFixture struct {
	scan     *operations.ImageScan
	matcher  *fakeMatcher
	reporter *fakeReporter
	mailer   *fakeNoticeMailer
	evidence string
	runs     sync.WaitGroup
}

func newImageScanFixtureDB(t *testing.T, database *sql.DB, disk *storage.DiskStorage) *scanFixture {
	t.Helper()
	f := &scanFixture{matcher: &fakeMatcher{matches: map[string]operations.ScanMatch{}}, reporter: &fakeReporter{}, mailer: &fakeNoticeMailer{}, evidence: t.TempDir()}
	t.Cleanup(f.runs.Wait)
	f.scan = operations.NewImageScan(database, disk, f.matcher, f.reporter, f.mailer, operations.ImageScanConfig{
		EvidenceDir: f.evidence, AlertEmail: "alerts@example.com", ReportOrg: "Test Org", ReportName: "Tester",
		ReportEmail: "reports@example.com", PublicBaseURL: "https://host.test",
	})
	if f.scan == nil {
		t.Fatal("NewImageScan returned nil with a matcher")
	}
	f.scan.Prepare = fakePrepare
	f.scan.Dispatch = func(fn func()) {
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
	f.matcher.matches[sha] = operations.ScanMatch{IsMatch: true, TrackingID: "t-match", Source: "NCMEC", Violations: []string{"A1", "B2"}, Raw: json.RawMessage(`{"IsMatch":true}`)}
}

const holdingAccountSuffix = "@events.invalid"

// 4. An event icon: cleared, and the event taken down.
func TestImageScanEventIcon(t *testing.T) {
	isolateQueueTest(t)
	a := newTeamSiteApp(t)
	disk, err := storage.NewDiskStorage(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	matcher := &fakeMatcher{matches: map[string]operations.ScanMatch{}}
	mailer := &fakeNoticeMailer{}
	s := operations.NewImageScan(a.database, disk, matcher, nil, mailer, operations.ImageScanConfig{EvidenceDir: t.TempDir(), AlertEmail: "alerts@example.com"})
	s.Prepare = fakePrepare
	var runs sync.WaitGroup
	t.Cleanup(runs.Wait)
	s.Dispatch = func(fn func()) {
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
	ev, err := eventrepo.GetEventBySlug(context.Background(), a.database, slug)
	if err != nil {
		t.Fatal(err)
	}
	icon := testPNG(t)
	matcher.mu.Lock()
	matcher.matches[operations.ImageHash(icon)] = operations.ScanMatch{IsMatch: true, TrackingID: "t-icon", Source: "IWF"}
	matcher.mu.Unlock()
	if err := eventrepo.SetEventIcon(context.Background(), a.database, ev.ID, "image/png", icon); err != nil {
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
	if typ, body, err := eventrepo.GetEventIcon(context.Background(), a.database, ev.ID); err != nil || typ != "" || len(body) != 0 {
		t.Fatalf("icon not cleared: %q %d %v", typ, len(body), err)
	}
	after, err := eventrepo.GetEventByID(context.Background(), a.database, ev.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !after.TakenDown() || after.TakenDownReason != operations.ImageScanTakeDownMsg {
		t.Fatalf("event not taken down: %+v", after.TakenDownAt)
	}
	if susp, _ := accountrepo.UserSuspended(context.Background(), a.database, ev.AccountID); !susp {
		t.Fatal("holding account not suspended with the event")
	}
	matches, err := opsrepo.ListImageMatches(context.Background(), a.database, 10)
	if err != nil || len(matches) == 0 || matches[0].Kind != "event_icon" || matches[0].EventSlug != slug || matches[0].ReportStatus != "off" || !matches[0].TakenDown {
		t.Fatalf("match row: %+v %v", matches, err)
	}
	if sent := mailer.sent(); len(sent) != 1 || !strings.Contains(sent[0], "event "+slug) || !strings.Contains(sent[0], "report: not filed") || !strings.Contains(sent[0], "CyberTipline") {
		t.Fatalf("alert: %v", sent)
	}
	// The report names the organiser who uploaded the icon.
	if who := s.ReporteeOf(context.Background(), matches[0]); !strings.Contains(who, ada.email) {
		t.Fatalf("reportee %q, want the uploader %s", who, ada.email)
	}
}

// Pass 3, item 4: on Simple Hack a team member's deploy and files-storage
// upload record the member, and the report names the member, never the
// event's holding account.
func TestImageScanHackUploader(t *testing.T) {
	isolateQueueTest(t)
	a := newTeamSiteApp(t)
	f := newImageScanFixtureDB(t, a.database, a.disk)
	a.sites.SetImageScan(f.scan)
	org, p1 := a.newPerson(t, "org"), a.newPerson(t, "p1")
	slug := a.makeEvent(t, org)
	a.join(t, slug, p1, org)
	team, _ := a.startTeam(t, slug, "Alpha", p1)
	key := a.teamKey(t, slug, p1)
	markReady(t, a.certDir, slug)
	deployed, stored := testPNG(t), testPNG(t)
	f.markMatch(operations.ImageHash(deployed))
	f.markMatch(operations.ImageHash(stored))
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
	rows, err := opsrepo.ListImageMatches(context.Background(), a.database, 50)
	if err != nil {
		t.Fatal(err)
	}
	bySHA := map[string]opsrepo.ImageMatch{}
	for _, m := range rows {
		bySHA[m.SHA256] = m
	}
	site, obj := bySHA[operations.ImageHash(deployed)], bySHA[operations.ImageHash(stored)]
	if site.Kind != "site_file" || site.WriterID != member {
		t.Fatalf("deploy match writer %q, want the member %s: %+v", site.WriterID, member, site)
	}
	if obj.Kind != "storage_file" || obj.WriterID != "user:"+member {
		t.Fatalf("storage match writer %q, want user:%s: %+v", obj.WriterID, member, obj)
	}
	for _, m := range []opsrepo.ImageMatch{site, obj} {
		if !strings.Contains(m.Uploader, p1.email) || strings.Contains(m.Uploader, holdingAccountSuffix) {
			t.Fatalf("%s: uploader %q, want %s", m.Kind, m.Uploader, p1.email)
		}
		if !strings.HasSuffix(m.OwnerEmail, holdingAccountSuffix) {
			t.Fatalf("%s: owner %q is not the holding account; the test proves nothing", m.Kind, m.OwnerEmail)
		}
		if _, _, err := f.scan.FileReport(context.Background(), m.ID); err != nil {
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

// Pass 3b, item 6: an organiser's publish of the event website and an
// organiser's event-storage upload record the organiser.
func TestImageScanHackOrganiserUploader(t *testing.T) {
	isolateQueueTest(t)
	a := newTeamSiteApp(t)
	f := newImageScanFixtureDB(t, a.database, a.disk)
	a.sites.SetImageScan(f.scan)
	org := a.newPerson(t, "org")
	slug := a.makeEvent(t, org)
	markReady(t, a.certDir, slug)
	deployed, stored := testPNG(t), testPNG(t)
	f.markMatch(operations.ImageHash(deployed))
	f.markMatch(operations.ImageHash(stored))
	ev := "/v1/hack/events/" + slug + "/website"
	body := map[string]any{"files": map[string]string{"index.html": "<h1>event</h1>"},
		"files_base64": map[string]string{"img/bad.png": base64.StdEncoding.EncodeToString(deployed)}}
	wantTS(t, "organiser publish", a.api(t, "PUT", ev+"/files?create=1", body, org.key), 201, "")
	wantTS(t, "organiser files resource", a.api(t, "PUT", ev+"/storage/resources/pics", map[string]string{"kind": "files"}, org.key), 201, "")
	wantTS(t, "organiser file", a.api(t, "PUT", ev+"/storage/files/pics/objects/bad.png", string(stored), org.key), 200, "")
	f.wait()
	f.scan.RunOnce(context.Background())
	organiser := a.uid(t, org)
	rows, err := opsrepo.ListImageMatches(context.Background(), a.database, 50)
	if err != nil {
		t.Fatal(err)
	}
	bySHA := map[string]opsrepo.ImageMatch{}
	for _, m := range rows {
		bySHA[m.SHA256] = m
	}
	site, obj := bySHA[operations.ImageHash(deployed)], bySHA[operations.ImageHash(stored)]
	if site.Kind != "site_file" || site.WriterID != organiser {
		t.Fatalf("publish match writer %q, want the organiser %s: %+v", site.WriterID, organiser, site)
	}
	if obj.Kind != "storage_file" || obj.WriterID != "user:"+organiser {
		t.Fatalf("storage match writer %q, want user:%s: %+v", obj.WriterID, organiser, obj)
	}
	for _, m := range []opsrepo.ImageMatch{site, obj} {
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
	a := newTeamSiteApp(t)
	f := newImageScanFixtureDB(t, a.database, a.disk)
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
	f.markMatch(operations.ImageHash(published))
	f.markMatch(operations.ImageHash(unpublished))
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
		if err := a.database.QueryRow(`SELECT writer_id FROM image_scan_queue WHERE sha256 = $1`, operations.ImageHash(img)).Scan(&writer); err != nil {
			t.Fatalf("%s: queued job: %v", name, err)
		}
		if writer != member {
			t.Fatalf("%s: queued writer %q, want the member %s", name, writer, member)
		}
	}
}
