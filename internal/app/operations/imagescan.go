package operations

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/vsriram/simple-host/internal/email"
	"github.com/vsriram/simple-host/internal/imagescan"
	"github.com/vsriram/simple-host/internal/model"
	accounts "github.com/vsriram/simple-host/internal/repository/accounts"
	entries "github.com/vsriram/simple-host/internal/repository/hackentries"
	events "github.com/vsriram/simple-host/internal/repository/hackevents"
	db "github.com/vsriram/simple-host/internal/repository/operations"
	sites "github.com/vsriram/simple-host/internal/repository/sites"
	"github.com/vsriram/simple-host/internal/repository/sqlkit"
	"github.com/vsriram/simple-host/internal/storage"
	"github.com/vsriram/simple-host/internal/workers"
)

// Image matching (SIM-200). Images that reach the box (site files from a
// deploy, files-storage objects, event icons, entry screenshots) are hashed
// and queued; a worker sends the bytes to the matching service. On a match
// the file is copied to the evidence folder, removed from every served copy,
// the site or event is taken down with the operator take-down, the alert
// address is emailed, and the CyberTipline report is filed (at once when
// PHOTODNA_AUTO_REPORT is on, else from the admin page). Nothing here sees a
// visitor's address; only image bytes go to the service. The whole feature
// is off, and nothing is constructed, until PHOTODNA_API_KEY is set.

// ImageScanConfig is what the worker needs from the environment.
type ImageScanConfig struct {
	// EvidenceDir holds one folder per match (0700). Empty: <DATA_DIR>/.evidence.
	EvidenceDir string
	// AlertEmail gets one email per match. Empty: no email, a log line only.
	AlertEmail string
	// ReportOrg, ReportName and ReportEmail name the operator on a report.
	// A report is refused unless all three are set.
	ReportOrg   string
	ReportName  string
	ReportEmail string
	// AutoReport files the report as soon as a match is recorded.
	AutoReport bool
	// ProductName is the edition name used in alerts and reports.
	ProductName string
	// PublicBaseURL is where the admin page lives (the alert links to it).
	PublicBaseURL string
}

// ImageScan is the queue worker and the quarantine. A nil *ImageScan is the
// feature off: every method is a no-op.
type ImageScan struct {
	runner        *workers.Runner
	tracker       *workers.Tracker
	enqueueCtx    context.Context
	enqueueRunner *workers.Runner
	enqueueMu     sync.Mutex
	closing       bool
	startOnce     sync.Once
	stopOnce      sync.Once
	stopDone      chan struct{}
	database      *sql.DB
	disk          *storage.DiskStorage
	matcher       imagescan.Matcher
	Reporter      imagescan.Reporter // nil: reports cannot be filed
	Mailer        email.Sender
	Config        ImageScanConfig
	now           func() time.Time
	// Dispatch overrides child dispatch in integration fixtures; production uses the retained runner.
	Dispatch func(func())
	// Prepare is the configured image preparation collaborator.
	Prepare func([]byte, imagescan.PrepareOptions) (*imagescan.Image, string, error)
	// SaveReport persists a filed report; fixtures can inject failed writes.
	SaveReport func(context.Context, sqlkit.Querier, int64, string, string, string) error
	// siteHooks shares the site service lock and address policy.
	siteHooks *ImageScanSiteHooks
	// hackTakeDownSync updates event take-down markers after commit.
	hackTakeDownSync func(ctx context.Context, ev model.Event) bool
}

const (
	ImageScanMinBytes    = 256
	imageScanBootDelay   = 15 * time.Second
	imageScanEvery       = 10 * time.Second
	imageScanClaim       = 4
	imageScanConcurrency = 2
	imageScanMaxAttempts = 12
	imageScanListLimit   = 200
	ImageScanDeletedWait = time.Hour
	ImageScanTakeDownMsg = "Taken down by the operator after an automated content check. Write to the support address if you believe this is a mistake."
)

// NewImageScan wires the worker. nil when matcher is nil (the feature is off).
func NewImageScan(database *sql.DB, disk *storage.DiskStorage, matcher imagescan.Matcher, reporter imagescan.Reporter, mailer email.Sender, cfg ImageScanConfig) *ImageScan {
	if matcher == nil {
		return nil
	}
	if cfg.EvidenceDir == "" && disk != nil {
		cfg.EvidenceDir = filepath.Join(disk.DataDir(), ".evidence")
	}
	t := workers.NewTracker("operations.image_scan")
	s := &ImageScan{runner: workers.NewRunner(t), tracker: t,
		enqueueCtx: context.Background(), enqueueRunner: workers.NewRunner(t), stopDone: make(chan struct{}),
		database: database, disk: disk, matcher: matcher, Reporter: reporter, Mailer: mailer, Config: cfg,
		now:        time.Now,
		Prepare:    imagescan.Prepare,
		SaveReport: db.SetImageMatchReport,
	}
	if why := s.evidenceDirRefused(); why != "" {
		log.Printf("image scan: WARNING: %s; no evidence copy can be kept", why)
	}
	return s
}

// Enabled reports whether image matching is on.
func (s *ImageScan) Enabled() bool { return s != nil }

// reportReady reports whether a CyberTipline report can be filed: a
// reporter, and the organisation, name, and email a report carries.
func (s *ImageScan) reportReady() bool {
	return s != nil && s.Reporter != nil && s.Config.ReportOrg != "" && s.Config.ReportName != "" && s.Config.ReportEmail != ""
}

// SetHackTakeDownSync wires the events-side take-down-marker sync (owned by
// HackHandler, which calls this from its own SetImageScan in hack.go); nil
// keeps the hack take-down path a no-op, same as before this was a func.
func (s *ImageScan) SetHackTakeDownSync(fn func(ctx context.Context, ev model.Event) bool) {
	if s != nil {
		s.hackTakeDownSync = fn
	}
}

// ImageCandidate is the cheap filter before anything is hashed or queued.
func ImageCandidate(b []byte) (string, bool) {
	if len(b) < ImageScanMinBytes {
		return "", false
	}
	ct := http.DetectContentType(b)
	if i := strings.Index(ct, ";"); i >= 0 {
		ct = ct[:i]
	}
	if !strings.HasPrefix(ct, "image/") || ct == "image/svg+xml" || ct == "image/x-icon" {
		return "", false
	}
	return ct, true
}

func ImageHash(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// imageFinding is a match about to be quarantined: the job it came from (ID
// 0 and the current time for a hash-cache hit) and what the service said.
type imageFinding struct {
	job         model.ImageScanJob
	data        []byte
	contentType string
	provider    string
	match       imagescan.Match
}

// enqueueCandidate is one image on its way to the queue.
type enqueueCandidate struct {
	job  model.ImageScanJob
	data []byte
	ct   string
}

// EnqueueSiteFiles queues the images of a deployed version. writerID is the
// account of the person who deployed it (siteWriterID). Nil-safe; the work
// runs off the request through s.Dispatch.
func (s *ImageScan) EnqueueSiteFiles(siteID, writerID, siteName string, version int, files map[string][]byte) {
	if s == nil || len(files) == 0 {
		return
	}
	var cands []enqueueCandidate
	for p, b := range files {
		ct, ok := ImageCandidate(b)
		if !ok {
			continue
		}
		cands = append(cands, enqueueCandidate{data: b, ct: ct, job: model.ImageScanJob{
			Kind: "site_file", SiteID: siteID, Version: version, Path: filepath.ToSlash(filepath.Clean(p)), WriterID: writerID,
		}})
	}
	s.enqueue(cands)
}

// EnqueueStorageFile queues one files-storage object.
func (s *ImageScan) EnqueueStorageFile(siteID, resourceName, path, writerID string, body []byte) {
	if s == nil {
		return
	}
	ct, ok := ImageCandidate(body)
	if !ok {
		return
	}
	s.enqueue([]enqueueCandidate{{data: body, ct: ct, job: model.ImageScanJob{
		Kind: "storage_file", SiteID: siteID, ResourceName: resourceName, Path: path, WriterID: writerID,
	}}})
}

// EnqueueEventIcon queues an event's icon. userID is the organiser who
// uploaded it (the report names them).
func (s *ImageScan) EnqueueEventIcon(eventID, userID string, raw []byte) {
	if s == nil {
		return
	}
	ct, ok := ImageCandidate(raw)
	if !ok {
		return
	}
	s.enqueue([]enqueueCandidate{{data: raw, ct: ct, job: model.ImageScanJob{Kind: "event_icon", EventID: eventID, WriterID: userID, Path: "icon"}}})
}

// EnqueueEntryScreenshot queues a team's entry screenshot.
func (s *ImageScan) EnqueueEntryScreenshot(eventID, teamID, userID string, raw []byte) {
	if s == nil {
		return
	}
	ct, ok := ImageCandidate(raw)
	if !ok {
		return
	}
	s.enqueue([]enqueueCandidate{{data: raw, ct: ct, job: model.ImageScanJob{
		Kind: "entry_screenshot", EventID: eventID, TeamID: teamID, WriterID: userID, Path: "screenshot",
	}}})
}

// enqueue hashes each candidate and consults the hash cache: a known clean
// or skipped image is dropped, a known match is quarantined at once, and an
// unknown one becomes a queue row.
func (s *ImageScan) enqueue(cands []enqueueCandidate) {
	if len(cands) == 0 {
		return
	}
	s.runEnqueue(func(ctx context.Context) {
		ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
		defer cancel()
		s.tracker.Attempt()
		defer s.updatePending(ctx)
		for _, c := range cands {
			c.job.SHA256 = ImageHash(c.data)
			c.job.Bytes = int64(len(c.data))
			verdict, tracking, found, err := db.LookupImageHash(ctx, s.database, c.job.SHA256)
			if err != nil {
				s.tracker.Fail("hash lookup", err)
				continue
			}
			switch {
			case found && verdict == string(imagescan.VerdictMatch):
				c.job.CreatedAt = s.now()
				s.quarantine(ctx, imageFinding{job: c.job, data: c.data, contentType: c.ct, provider: "hash-cache",
					match: imagescan.Match{IsMatch: true, TrackingID: tracking}})
			case found:
				// clean or skipped: nothing to do for these bytes.
			default:
				if _, err := db.EnqueueImageScan(ctx, s.database, c.job); err != nil {
					s.tracker.Fail("enqueue", err)
				} else {
					s.tracker.Progress()
				}
			}
		}
	})
}

// runEnqueue retains production enqueue work in the owner. Tests may replace
// the dispatch hook and retain its goroutines in their fixture.
func (s *ImageScan) runEnqueue(fn func(context.Context)) {
	if s.Dispatch != nil {
		s.Dispatch(func() { fn(context.Background()) })
		return
	} // test dispatch hook
	s.enqueueMu.Lock()
	defer s.enqueueMu.Unlock()
	if !s.closing {
		s.enqueueRunner.Go(s.enqueueCtx, fn)
	}
}

// Stop closes enqueue admission and stops scans. Accepted persistence work has
// ten seconds to drain before cancellation; Wait joins that work even on timeout.
func (s *ImageScan) Stop() {
	if s == nil {
		return
	}
	s.stopOnce.Do(func() {
		s.enqueueMu.Lock()
		s.closing = true
		s.enqueueMu.Unlock()
		s.runner.Stop()
		go func() {
			done := make(chan struct{})
			go func() { s.enqueueRunner.Wait(); close(done) }()
			timer := time.NewTimer(10 * time.Second)
			defer timer.Stop()
			select {
			case <-done:
			case <-timer.C:
				s.enqueueRunner.Stop()
				<-done
			}
			s.enqueueRunner.Stop()
			close(s.stopDone)
		}()
	})
}
func (s *ImageScan) Wait() {
	if s == nil {
		return
	}
	s.runner.Wait()
	s.enqueueMu.Lock()
	closing := s.closing
	s.enqueueMu.Unlock()
	if closing {
		<-s.stopDone
	} else {
		s.enqueueRunner.Wait()
	}
}
func (s *ImageScan) Status() []workers.Status {
	if s == nil {
		return nil
	}
	status := s.runner.Status()
	status[0].Running = status[0].Running || s.enqueueRunner.Status()[0].Running
	return status
}

// Start waits briefly after boot, then scans every few seconds.
func (s *ImageScan) Start(ctx context.Context) {
	if s == nil {
		return
	}
	s.startOnce.Do(func() {
		s.runner.Go(ctx, func(ctx context.Context) {
			<-ctx.Done()
			s.Stop()
		})
		s.runner.Go(ctx, func(ctx context.Context) {
			select {
			case <-ctx.Done():
				return
			case <-time.After(imageScanBootDelay):
			}
			t := time.NewTicker(imageScanEvery)
			defer t.Stop()
			for {
				s.RunOnce(ctx)
				select {
				case <-ctx.Done():
					return
				case <-t.C:
				}
			}
		})
	})
}

// RunOnce claims due jobs and runs them, a few at a time. Jobs for the same
// bytes run one after another, so the second finds the first one's verdict
// in the hash cache. Returns how many jobs were handled.
func (s *ImageScan) RunOnce(ctx context.Context) int {
	if s == nil {
		return 0
	}
	s.tracker.Attempt()
	defer s.updatePending(ctx)
	jobs, err := db.ClaimImageScans(ctx, s.database, imageScanClaim)
	if err != nil {
		s.tracker.Fail("claim", err)
		return 0
	}
	groups := map[string][]model.ImageScanJob{}
	var order []string
	for _, j := range jobs {
		if _, ok := groups[j.SHA256]; !ok {
			order = append(order, j.SHA256)
		}
		groups[j.SHA256] = append(groups[j.SHA256], j)
	}
	sem := make(chan struct{}, imageScanConcurrency)
	var wg sync.WaitGroup
	for _, sha := range order {
		wg.Add(1)
		sem <- struct{}{}
		go func(group []model.ImageScanJob) {
			defer wg.Done()
			defer func() { <-sem }()
			for _, j := range group {
				s.runJob(ctx, j)
			}
		}(groups[sha])
	}
	wg.Wait()
	return len(jobs)
}

// LoadJobBytes finds the bytes a job names. nil, nil when they are gone
// (version pruned, file replaced, row deleted).
func (s *ImageScan) LoadJobBytes(ctx context.Context, j model.ImageScanJob) ([]byte, error) {
	var raw []byte
	switch j.Kind {
	case "site_file", "storage_file":
		site, err := sites.GetSiteByID(ctx, s.database, j.SiteID)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		// Read under the site's lock: a rename holds it from moving the
		// files until the row has the new name, so the row read under it
		// names where the files are.
		site, unlock, err := s.lockFreshSite(ctx, site)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		if site.Deleted {
			// Postpone under the lock: a restore takes the same lock, so it
			// cannot land between this check and the postpone.
			if perr := db.PostponeImageScan(ctx, s.database, j.ID, ImageScanDeletedWait); perr != nil {
				s.tracker.Fail("postpone deleted-site job", perr)
			}
			unlock()
			return nil, ErrImageScanSiteDeleted
		}
		raw, err = s.readSiteJobFile(site, j)
		unlock()
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
	case "event_icon":
		_, body, err := events.GetEventIcon(ctx, s.database, j.EventID)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		raw = body
	case "entry_screenshot":
		data, _, err := entries.GetEventScreenshot(ctx, s.database, j.TeamID)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		raw = data
	default:
		return nil, fmt.Errorf("unknown kind %q", j.Kind)
	}
	if len(raw) == 0 || ImageHash(raw) != j.SHA256 {
		// Replaced since it was queued; the new bytes have their own job.
		return nil, nil
	}
	return raw, nil
}

// readSiteJobFile reads a site-file or files-storage job's bytes from the
// site's folder as the row names it. os.ErrNotExist when they are not there.
func (s *ImageScan) readSiteJobFile(site model.Site, j model.ImageScanJob) ([]byte, error) {
	if j.Kind == "storage_file" {
		return os.ReadFile(storage.RuntimeFilePath(s.disk, site.UserID, site.Name, j.ResourceName, j.Path))
	}
	dir := s.disk.VersionDir(site.UserID, site.Name, j.Version)
	if dir == "" {
		return nil, os.ErrNotExist
	}
	return os.ReadFile(filepath.Join(dir, filepath.FromSlash(j.Path)))
}

// ErrImageScanSiteDeleted: the job's site is in Recently deleted.
// LoadJobBytes has postponed the job (ImageScanDeletedWait) to wait for a
// restore, which brings the bytes back at the same paths, or for the purge,
// which removes the row.
var ErrImageScanSiteDeleted = errors.New("the site is in Recently deleted")

func (s *ImageScan) runJob(ctx context.Context, j model.ImageScanJob) {
	raw, err := s.LoadJobBytes(ctx, j)
	if errors.Is(err, ErrImageScanSiteDeleted) {
		// LoadJobBytes has postponed it.
		return
	}
	if err != nil {
		s.deferJob(ctx, j, err)
		return
	}
	if raw == nil {
		log.Printf("image scan: job %d (%s %s): bytes are gone; dropped", j.ID, j.Kind, j.Path)
		s.finish(ctx, j.ID)
		return
	}
	ct, _ := ImageCandidate(raw)
	// The same bytes may have been checked since this job was queued.
	verdict, tracking, found, err := db.LookupImageHash(ctx, s.database, j.SHA256)
	if err != nil {
		s.deferJob(ctx, j, fmt.Errorf("hash lookup: %w", err))
		return
	}
	if found {
		if verdict == string(imagescan.VerdictMatch) {
			s.quarantine(ctx, imageFinding{job: j, data: raw, contentType: ct, provider: "hash-cache",
				match: imagescan.Match{IsMatch: true, TrackingID: tracking}})
		}
		s.finish(ctx, j.ID)
		return
	}
	img, skip, err := s.Prepare(raw, imagescan.PrepareOptions{})
	if err != nil {
		s.deferJob(ctx, j, fmt.Errorf("prepare: %w", err))
		return
	}
	if skip != "" || img == nil {
		s.record(ctx, j.SHA256, imagescan.VerdictSkipped, "", skip)
		s.finish(ctx, j.ID)
		return
	}
	m, err := s.matcher.Match(ctx, img)
	switch {
	case errors.Is(err, imagescan.ErrUnsupported):
		s.record(ctx, j.SHA256, imagescan.VerdictSkipped, "", imagescan.SafeError("match", err).Error())
		s.finish(ctx, j.ID)
	case err != nil:
		s.deferJob(ctx, j, imagescan.SafeError("match", err))
	case !m.IsMatch:
		s.record(ctx, j.SHA256, imagescan.VerdictClean, m.TrackingID, "")
		s.finish(ctx, j.ID)
	default:
		s.quarantine(ctx, imageFinding{job: j, data: raw, contentType: ct, provider: s.matcher.Name(), match: m})
		s.record(ctx, j.SHA256, imagescan.VerdictMatch, m.TrackingID, strings.Join(m.Violations, ","))
		s.finish(ctx, j.ID)
	}
}

func (s *ImageScan) record(ctx context.Context, sha string, v imagescan.Verdict, tracking, detail string) {
	if err := db.RecordImageHash(ctx, s.database, sha, string(v), s.matcher.Name(), tracking, detail); err != nil {
		s.tracker.Fail("record hash verdict", err)
	}
}

func (s *ImageScan) finish(ctx context.Context, id int64) {
	if id == 0 {
		return
	}
	if err := db.DeleteImageScan(ctx, s.database, id); err != nil {
		s.tracker.Fail("delete job", err)
	} else {
		s.tracker.Progress()
	}
}

// deferJob schedules another attempt with a growing wait. The last allowed
// failure parks the job for good with its error (one log line), so the
// admin page's gave-up count and the log say what happened.
func (s *ImageScan) deferJob(ctx context.Context, j model.ImageScanJob, err error) {
	s.tracker.Fail("scan image", err)
	attempts := j.Attempts + 1
	text := err.Error()
	if attempts >= imageScanMaxAttempts {
		log.Printf("image scan: job %d (%s %s) gave up after %d attempts: %v", j.ID, j.Kind, j.Path, attempts, err)
		if derr := db.GiveUpImageScan(ctx, s.database, j.ID, "gave up: "+text, attempts); derr != nil {
			s.tracker.Fail("park failed job", derr)
		}
		return
	}
	if imagescan.IsRetryable(err) {
		log.Printf("image scan: job %d (%s %s) attempt %d: %v (will retry)", j.ID, j.Kind, j.Path, attempts, err)
	} else {
		log.Printf("image scan: job %d (%s %s) attempt %d: %v", j.ID, j.Kind, j.Path, attempts, err)
	}
	if derr := db.DeferImageScan(ctx, s.database, j.ID, text, attempts); derr != nil {
		s.tracker.Fail("defer job", derr)
	}
}

// resolvedPath is p made absolute with its symlinks resolved. A path that
// does not exist yet is resolved through its nearest existing parent.
func resolvedPath(p string) (string, error) {
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	rest := ""
	for cur := abs; ; {
		if r, err := filepath.EvalSymlinks(cur); err == nil {
			return filepath.Join(r, rest), nil
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return abs, nil
		}
		rest = filepath.Join(filepath.Base(cur), rest)
		cur = parent
	}
}

// evidenceDirRefused says why the evidence folder cannot be used: it must
// never sit under a served path, also through a symlink.
func (s *ImageScan) evidenceDirRefused() string {
	dir, err := resolvedPath(s.Config.EvidenceDir)
	if err != nil {
		return fmt.Sprintf("evidence dir %q: %v", s.Config.EvidenceDir, err)
	}
	if s.disk == nil {
		return ""
	}
	for _, served := range []string{"by-id", "handles", "domains"} {
		p := filepath.Join(s.disk.DataDir(), served)
		abs, aerr := filepath.Abs(p)
		res, rerr := resolvedPath(p)
		for _, base := range []string{abs, res} {
			if (base == abs && aerr != nil) || (base == res && rerr != nil) {
				continue
			}
			rel, err := filepath.Rel(base, dir)
			if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				return fmt.Sprintf("evidence dir %q is under the served path %s", dir, base)
			}
		}
	}
	return ""
}

func imageExt(ct string) string {
	switch ct {
	case "image/jpeg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	case "image/bmp":
		return ".bmp"
	case "image/tiff":
		return ".tiff"
	}
	return ".bin"
}

// matchWhere names the site or event a match is on, for logs and the alert.
func matchWhere(m model.ImageMatch) string {
	if m.EventSlug != "" {
		return "event " + m.EventSlug
	}
	return "site " + m.SiteName
}

// imageStep is one action taken on a match, as the alert, the log, and
// record.json list it. Detail goes in the alert email, which never has a
// path or an image; Error carries the underlying error for the log and record.json only.
type imageStep struct {
	Step   string `json:"step"`
	Status string `json:"status"` // done, FAILED, or (for the report) not filed
	Detail string `json:"detail,omitempty"`
	Error  string `json:"error,omitempty"`
}

const (
	stepDone      = "done"
	stepFailed    = "FAILED"
	stepNotFiled  = "not filed"
	keptNoCopyMsg = "the file was kept in place because the evidence copy failed"
)

// rawJSON is the service's reply as valid JSON: as it is, or, when it is
// not valid JSON (a reply cut at the size cap), as a JSON string.
func rawJSON(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 || json.Valid(raw) {
		return raw
	}
	b, err := json.Marshal(string(raw))
	if err != nil {
		return nil
	}
	return b
}

// quarantine acts on a match, in this order: record the match row first so
// nothing is lost, keep the evidence copy, remove the file from every served
// copy (only once the evidence copy is safe), take the site or event down,
// file the report when automatic reports are on, then email the alert,
// which lists what happened. Each step logs its own failure and the next
// one still runs.
func (s *ImageScan) quarantine(ctx context.Context, f imageFinding) {
	j := f.job
	m := model.ImageMatch{
		Kind: j.Kind, SiteID: j.SiteID, EventID: j.EventID, TeamID: j.TeamID, Version: j.Version,
		ResourceName: j.ResourceName, Path: j.Path, WriterID: j.WriterID, SHA256: j.SHA256, Bytes: int64(len(f.data)),
		ContentType: f.contentType, Provider: f.provider, TrackingID: f.match.TrackingID, Source: f.match.Source,
		Violations: strings.Join(f.match.Violations, ","), Raw: rawJSON(f.match.Raw), UploadedAt: j.CreatedAt,
		ReportStatus: "pending",
	}
	if !s.reportReady() {
		m.ReportStatus = "off"
	}
	siteKind := j.Kind == "site_file" || j.Kind == "storage_file"
	var site model.Site
	var ev model.Event
	if siteKind {
		var err error
		if site, err = sites.GetSiteByID(ctx, s.database, j.SiteID); err != nil {
			log.Printf("image scan: site %s: %v", j.SiteID, err)
		} else {
			s.fillSiteOwner(ctx, &m, site)
		}
	} else {
		var err error
		if ev, err = events.GetEventByID(ctx, s.database, j.EventID); err != nil {
			log.Printf("image scan: event %s: %v", j.EventID, err)
		} else {
			m.EventSlug, m.OwnerID, m.OwnerEmail = ev.Slug, ev.AccountID, ev.ContactEmail
			if u, err := accounts.GetUserByID(ctx, s.database, ev.AccountID); err == nil && u.Handle.Valid {
				m.OwnerHandle = u.Handle.String
			}
		}
	}
	var steps []imageStep
	add := func(name, status, detail string, err error) {
		st := imageStep{Step: name, Status: status, Detail: detail}
		if err != nil {
			st.Error = err.Error()
			s.tracker.Fail("quarantine "+name, err)
		}
		steps = append(steps, st)
		log.Printf("image scan: match #%d on %s %s: %s: %s %s %s", m.ID, matchWhere(m), m.Path, name, status, detail, st.Error)
	}
	ok := func(b bool) string {
		if b {
			return stepDone
		}
		return stepFailed
	}

	// 1. The record, with the uploader and the address as they are now.
	m.Uploader, m.Address = s.ReporteeOf(ctx, m), s.AddressOf(ctx, m)
	id, err := db.InsertImageMatch(ctx, s.database, m)
	if err != nil {
		log.Printf("image scan: MATCH on %s %s could not be recorded: %v (acting on it anyway)", matchWhere(m), m.Path, err)
	}
	m.ID = id
	m.DetectedAt = s.now().UTC()
	log.Printf("image scan: MATCH #%d on %s %s (%s, %s)", id, matchWhere(m), m.Path, f.provider, m.Source)

	// 2. The evidence copy. Without it the file stays where it is.
	evidence, evidenceOK := "", false
	if why := s.evidenceDirRefused(); why != "" {
		add("evidence copy", stepFailed, "the evidence folder is refused", errors.New(why))
	} else if id == 0 {
		add("evidence copy", stepFailed, "the match could not be recorded", nil)
	} else {
		// A folder of its own, never one that already exists, and files
		// created new, so nothing there is reused or followed.
		dir := filepath.Join(s.Config.EvidenceDir, strconv.FormatInt(id, 10))
		if err := os.MkdirAll(s.Config.EvidenceDir, 0o700); err != nil {
			add("evidence copy", stepFailed, "", err)
		} else if err := os.Mkdir(dir, 0o700); err != nil {
			detail := ""
			if errors.Is(err, os.ErrExist) {
				detail = "the folder for this match already exists"
			}
			add("evidence copy", stepFailed, detail, err)
		} else if err := writeNewFile(filepath.Join(dir, "image"+imageExt(f.contentType)), f.data); err != nil {
			add("evidence copy", stepFailed, "", err)
			evidence = dir // record.json still goes beside it, when it can
		} else {
			evidence, evidenceOK = dir, true
			add("evidence copy", stepDone, "kept in the evidence folder", nil)
			m.EvidencePath = dir
			if err := db.SetImageMatchEvidence(ctx, s.database, id, dir); err != nil {
				log.Printf("image scan: evidence path of #%d: %v", id, err)
			}
			s.writeRecordKeepFiled(dir, m, steps)
		}
	}

	// 3 and 4. Out of service, and the take-down.
	if siteKind {
		s.takeDownSite(ctx, &m, site, evidenceOK, add, ok)
	} else {
		if !evidenceOK {
			add("file removal", stepFailed, keptNoCopyMsg, nil)
		} else {
			var cleared bool
			var err error
			if j.Kind == "event_icon" {
				cleared, err = db.ClearEventIcon(ctx, s.database, j.EventID, j.SHA256)
			} else {
				cleared, err = db.ClearEventScreenshot(ctx, s.database, j.TeamID, j.SHA256)
			}
			switch {
			case err != nil:
				add("file removal", stepFailed, "", err)
			case cleared:
				add("file removal", stepDone, "the image was removed", nil)
			default:
				add("file removal", stepDone, "the stored image had already changed; nothing to remove", nil)
			}
		}
		if ev.ID == "" {
			add("take-down", stepFailed, "the event row is missing", nil)
		} else {
			err := s.takeDownEvent(ctx, ev)
			add("take-down", ok(err == nil), "the event", err)
			m.TakenDown = err == nil
		}
	}
	if m.TakenDown && id != 0 {
		if err := db.SetImageMatchTakenDown(ctx, s.database, id); err != nil {
			log.Printf("image scan: taken_down of #%d: %v", id, err)
		}
	}

	// 5. The report, before the alert so the alert can say whether it went.
	reportFiled := false
	switch {
	case s.Config.AutoReport && s.reportReady() && id != 0:
		rid, at, err := s.FileReport(ctx, id)
		var saveErr *ReportSaveError
		switch {
		case errors.As(err, &saveErr):
			// The service accepted it: it counts as filed.
			reportFiled = true
			add("report", stepDone, "report filed, id "+rid+", saving the status failed", err)
			m.ReportStatus, m.ReportID, m.ReportedAt = "filed", rid, sql.NullTime{Time: at, Valid: true}
		case err != nil:
			add("report", stepFailed, "", err)
			m.ReportStatus, m.ReportError = "failed", err.Error()
		default:
			reportFiled = true
			add("report", stepDone, "filed as "+rid, nil)
			m.ReportStatus, m.ReportID, m.ReportedAt = "filed", rid, sql.NullTime{Time: at, Valid: true}
		}
	case !s.reportReady():
		add("report", stepNotFiled, "reports are not configured", nil)
	default:
		add("report", stepNotFiled, "file it from the admin page", nil)
	}

	// 6. The alert.
	if err := s.sendAlert(ctx, m, steps, reportFiled); err != nil {
		add("alert email", stepFailed, "", err)
	} else if s.Config.AlertEmail != "" {
		add("alert email", stepDone, "sent to "+s.Config.AlertEmail, nil)
		if id != 0 {
			if err := db.SetImageMatchAlerted(ctx, s.database, id); err != nil {
				log.Printf("image scan: alerted_at of #%d: %v", id, err)
			}
			m.AlertedAt = sql.NullTime{Time: s.now().UTC(), Valid: true}
		}
	} else {
		add("alert email", stepNotFiled, "no alert address is configured", nil)
	}
	if evidence != "" {
		s.writeRecordKeepFiled(evidence, m, steps)
	}
}

// writeRecordKeepFiled is writeRecord for the worker. Once the evidence
// path is saved, a report filed from the admin page can write record.json
// under imageReportMu at any time; this write takes the same lock and keeps
// that filed report.
func (s *ImageScan) writeRecordKeepFiled(dir string, m model.ImageMatch, steps []imageStep) {
	imageReportMu.Lock()
	defer imageReportMu.Unlock()
	if rid, at, filed := filedInRecord(model.ImageMatch{EvidencePath: dir}); filed && m.ReportStatus != "filed" {
		m.ReportStatus, m.ReportID, m.ReportError, m.ReportedAt = "filed", rid, "", sql.NullTime{Time: at, Valid: true}
	}
	s.writeRecord(dir, m, steps)
}

// fillSiteOwner copies the site's name and owner onto the match.
func (s *ImageScan) fillSiteOwner(ctx context.Context, m *model.ImageMatch, site model.Site) {
	m.SiteName, m.OwnerID, m.OwnerHandle = site.Name, site.UserID, site.OwnerHandle
	m.OwnerEmail = ""
	if u, err := accounts.GetUserByID(ctx, s.database, site.UserID); err == nil && strings.Contains(u.Username, "@") {
		m.OwnerEmail = u.Username
	}
}

// takeDownSite removes the matched file (when the evidence copy is safe) and
// takes the site down, the way the operator take-down does (suspend.go):
// under the site's lock, with the row read again under it so a rename in
// between is seen, held through the removal, the suspension, and the marker.
// The same lock guards the files-storage PUT path. TakenDown is set only
// when both the suspension and the marker succeeded.
func (s *ImageScan) takeDownSite(ctx context.Context, m *model.ImageMatch, site model.Site, evidenceOK bool, add func(string, string, string, error), ok func(bool) string) {
	if site.ID == "" {
		add("file removal", stepFailed, "the site row is missing", nil)
		add("take-down", stepFailed, "the site row is missing", nil)
		return
	}
	site, unlock, err := s.lockFreshSite(ctx, site)
	if err != nil {
		add("file removal", stepFailed, "the site could not be read", err)
		add("take-down", stepFailed, "the site could not be read", err)
		return
	}
	defer unlock()
	s.fillSiteOwner(ctx, m, site)
	switch {
	case !evidenceOK:
		add("file removal", stepFailed, keptNoCopyMsg, nil)
	case m.Kind == "site_file":
		err := s.disk.RemoveFileEverywhere(site.UserID, site.Name, m.Path, m.SHA256)
		add("file removal", ok(err == nil), "every copy with the matched bytes, in every version and the live copy", err)
	default:
		detail, err := s.removeStorageObject(ctx, site, *m)
		add("file removal", ok(err == nil), detail, err)
	}
	if err := sites.SetSiteSuspended(ctx, s.database, site.ID, ImageScanTakeDownMsg); err != nil {
		add("take-down", stepFailed, "the site", err)
		return
	}
	fresh, err := sites.GetSiteByID(ctx, s.database, site.ID)
	if err == nil && !fresh.Deleted {
		// A site in Recently deleted has no served folder; Restore writes
		// its marker from the database.
		err = s.disk.SetSuspended(fresh.UserID, fresh.Name, fresh.Suspended())
	}
	if err != nil {
		add("take-down", stepFailed, "the site was marked taken down, but its files could not be marked", err)
		return
	}
	add("take-down", stepDone, "the site", nil)
	m.TakenDown = true
}

// lockFreshSite takes the site's lock and reads the row again under it,
// retrying under the new name after a rename (suspend.go does the same).
func (s *ImageScan) lockFreshSite(ctx context.Context, site model.Site) (model.Site, func(), error) {
	if s.siteHooks == nil {
		fresh, err := sites.GetSiteByID(ctx, s.database, site.ID)
		return fresh, func() {}, err
	}
	for {
		unlock := s.siteHooks.Lock(site.UserID, site.Name)
		fresh, err := sites.GetSiteByID(ctx, s.database, site.ID)
		if err != nil {
			unlock()
			return model.Site{}, nil, err
		}
		if fresh.UserID == site.UserID && fresh.Name == site.Name {
			return fresh, unlock, nil
		}
		unlock()
		site = fresh
	}
}

// removeStorageObject removes a files-storage object and its row, only when
// the object still holds the matched bytes. The caller holds the site lock.
func (s *ImageScan) removeStorageObject(ctx context.Context, site model.Site, m model.ImageMatch) (string, error) {
	p := storage.RuntimeFilePath(s.disk, site.UserID, site.Name, m.ResourceName, m.Path)
	b, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return "the object was already gone", nil
	}
	if err != nil {
		return "", err
	}
	if ImageHash(b) != m.SHA256 {
		return "the object had already been replaced; nothing to remove", nil
	}
	if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	s.disk.MarkChanged()
	if err := db.DeleteStorageFileRow(ctx, s.database, site.ID, m.ResourceName, m.Path); err != nil {
		return "the object was removed, but its row could not be", err
	}
	return "the object was removed from the files resource", nil
}

// takeDownEvent is the operator take-down of an event (hack_admin.go): the
// event is stamped, its holding account suspended, and its team sites marked.
func (s *ImageScan) takeDownEvent(ctx context.Context, ev model.Event) error {
	tx, err := s.database.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := events.TakeDownEvent(ctx, tx, ev.ID, ImageScanTakeDownMsg); err != nil {
		return err
	}
	if err := accounts.SetUserSuspended(ctx, tx, ev.AccountID, ImageScanTakeDownMsg); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if s.hackTakeDownSync != nil && !s.hackTakeDownSync(ctx, ev) {
		return errors.New("team site markers could not all be written")
	}
	return nil
}

// writeRecord keeps record.json beside the evidence copy: the match row's
// fields, the service's raw reply, and the steps taken.
func (s *ImageScan) writeRecord(dir string, m model.ImageMatch, steps []imageStep) {
	rec := s.matchJSON(m)
	rec["steps"] = steps
	if len(m.Raw) > 0 {
		rec["raw_reply"] = m.Raw
	}
	s.saveRecord(dir, m.ID, rec)
}

// recordReport rewrites record.json after a report attempt so it carries
// the report's outcome. A missing or unreadable record starts from the row.
func (s *ImageScan) recordReport(m model.ImageMatch) {
	if m.EvidencePath == "" {
		return
	}
	rec := map[string]any{}
	if b, err := os.ReadFile(filepath.Join(m.EvidencePath, "record.json")); err != nil || json.Unmarshal(b, &rec) != nil {
		rec = s.matchJSON(m)
		if len(m.Raw) > 0 {
			rec["raw_reply"] = m.Raw
		}
	}
	rec["report_status"], rec["report_id"], rec["report_error"] = m.ReportStatus, m.ReportID, m.ReportError
	if m.ReportedAt.Valid {
		rec["reported_at"] = m.ReportedAt.Time.UTC()
	}
	s.saveRecord(m.EvidencePath, m.ID, rec)
}

func (s *ImageScan) saveRecord(dir string, id int64, rec map[string]any) {
	b, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		log.Printf("image scan: record.json of #%d: %v", id, err)
		return
	}
	// A new file beside it, then a rename over record.json: an existing
	// file or symlink there is replaced, never written through.
	tmp, err := os.CreateTemp(dir, ".record-*.json")
	if err != nil {
		log.Printf("image scan: record.json of #%d: %v", id, err)
		return
	}
	_, werr := tmp.Write(b)
	if cerr := tmp.Close(); werr == nil {
		werr = cerr
	}
	if werr == nil {
		werr = os.Rename(tmp.Name(), filepath.Join(dir, "record.json"))
	}
	if werr != nil {
		_ = os.Remove(tmp.Name())
		log.Printf("image scan: record.json of #%d: %v", id, werr)
	}
}

// writeNewFile creates p (0600) and writes b, failing when anything,
// a symlink included, is already there.
func writeNewFile(p string, b []byte) error {
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	_, werr := f.Write(b)
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	return werr
}

func (s *ImageScan) productName() string {
	if s.Config.ProductName != "" {
		return s.Config.ProductName
	}
	return "Simple Host"
}

// sendAlert emails the alert address what happened, step by step. Never the
// image, never a path or link to it.
func (s *ImageScan) sendAlert(ctx context.Context, m model.ImageMatch, steps []imageStep, reportFiled bool) error {
	if s.Config.AlertEmail == "" {
		return nil
	}
	sender, ok := s.Mailer.(noticeSender)
	if !ok || s.Mailer == nil {
		return errors.New("the mailer cannot send a notice")
	}
	where := matchWhere(m)
	address := m.Address
	subject := fmt.Sprintf("%s: image match on %s (#%d)", s.productName(), where, m.ID)
	var b strings.Builder
	fmt.Fprintf(&b, "An uploaded image matched a known abuse hash set.\n\n")
	fmt.Fprintf(&b, "Match: #%d\nKind: %s\nWhere: %s", m.ID, m.Kind, where)
	if address != "" {
		fmt.Fprintf(&b, " (%s)", address)
	}
	fmt.Fprintf(&b, "\nOwner: %s %s\n", m.OwnerHandle, m.OwnerEmail)
	fmt.Fprintf(&b, "Uploaded by: %s\n", m.Uploader)
	fmt.Fprintf(&b, "Path: %s\nDetected: %s\nService: %s\nTracking id: %s\nSource: %s\nViolations: %s\n\n",
		m.Path, m.DetectedAt.UTC().Format(time.RFC3339), m.Provider, m.TrackingID, m.Source, m.Violations)
	fmt.Fprintf(&b, "What was done:\n")
	for _, st := range steps {
		fmt.Fprintf(&b, "- %s: %s", st.Step, st.Status)
		if st.Detail != "" {
			fmt.Fprintf(&b, ", %s", st.Detail)
		}
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "\nWhat to do next: open %s/admin#moderation.\n", strings.TrimRight(s.Config.PublicBaseURL, "/"))
	if !reportFiled {
		fmt.Fprintf(&b, "File the CyberTipline report (the Report button on the admin page, or https://report.cybertip.org/) as soon as reasonably possible; the law requires it.\n")
	}
	return email.SendNoticeCtx(ctx, sender, s.Config.AlertEmail, subject, b.String())
}

// AddressOf is the site's own https address (as the dashboard shows it) or
// the event slug.
func (s *ImageScan) AddressOf(ctx context.Context, m model.ImageMatch) string {
	if m.EventSlug != "" {
		return m.EventSlug
	}
	if m.SiteID == "" {
		return ""
	}
	site, err := sites.GetSiteByID(ctx, s.database, m.SiteID)
	if err != nil {
		return ""
	}
	if s.siteHooks != nil {
		return s.siteHooks.URL(site)
	}
	return site.SiteURL
}

var (
	ErrReportAlreadyFiled = errors.New("already reported")
	ErrReportUnavailable  = errors.New("reports cannot be filed: set IMAGE_SCAN_REPORT_ORG, IMAGE_SCAN_REPORT_NAME, and IMAGE_SCAN_REPORT_EMAIL")
	// imageReportMu serialises FileReport, so two clicks (or a click and
	// the automatic report) cannot file the same match twice.
	imageReportMu sync.Mutex
)

// ReportSaveError is a report the service accepted whose 'filed' status
// could not be saved.
type ReportSaveError struct {
	reportID string
	err      error
}

func (e *ReportSaveError) Error() string {
	return fmt.Sprintf("the report WAS filed with id %s, but saving that failed: %v", e.reportID, e.err)
}

// holdingAccountSuffix marks a Simple Hack event's holding account
// (hack.go), which owns the event's sites and is no person.
const holdingAccountSuffix = "@events.invalid"

// ReporteeOf names the person a report is about: the account recorded as
// the writer (who deployed a site file, the participant or organiser who
// uploaded an entry screenshot or event icon, the team member or organiser
// who wrote a files-storage object), a visitor's id, or an anonymous
// visitor. A site file or owner write with no writer on record names the
// site owner, never an event's holding account.
func (s *ImageScan) ReporteeOf(ctx context.Context, m model.ImageMatch) string {
	switch m.Kind {
	case "entry_screenshot", "event_icon", "site_file":
		if m.WriterID != "" {
			if name := s.accountName(ctx, m.WriterID); name != "" {
				return name
			}
			if m.Kind != "site_file" {
				return "user " + m.WriterID
			}
		}
	case "storage_file":
		switch {
		case m.WriterID == "owner":
		case m.WriterID == "":
			return "anonymous visitor (no account)"
		case strings.HasPrefix(m.WriterID, "user:"):
			id := strings.TrimPrefix(m.WriterID, "user:")
			if name := s.accountName(ctx, id); name != "" {
				return name
			}
			return "user " + id
		default:
			return "visitor " + m.WriterID
		}
	}
	if strings.HasSuffix(m.OwnerEmail, holdingAccountSuffix) {
		return "uploader not recorded"
	}
	return strings.TrimSpace(m.OwnerHandle + " " + m.OwnerEmail)
}

// accountName is an account's handle and email, or "" when it is unknown or
// an event's holding account.
func (s *ImageScan) accountName(ctx context.Context, id string) string {
	u, err := accounts.GetUserByID(ctx, s.database, id)
	if err != nil || strings.HasSuffix(u.Username, holdingAccountSuffix) {
		return ""
	}
	name := u.Handle.String
	if strings.Contains(u.Username, "@") {
		name += " " + u.Username
	}
	return strings.TrimSpace(name)
}

// FileReport files the CyberTipline report for one match through the
// service and records the outcome, in the database and in record.json.
// Refused when already filed or when reports are not configured; a service
// failure leaves 'failed' with the error so the admin page can retry.
func (s *ImageScan) FileReport(ctx context.Context, id int64) (string, time.Time, error) {
	imageReportMu.Lock()
	defer imageReportMu.Unlock()
	m, err := db.GetImageMatch(ctx, s.database, id)
	if err != nil {
		return "", time.Time{}, err
	}
	if m.ReportStatus == "filed" {
		return m.ReportID, m.ReportedAt.Time, ErrReportAlreadyFiled
	}
	if rid, at, ok := filedInRecord(m); ok {
		// The service took a report whose status could not be saved: never
		// a second one. Save it now.
		if err := s.SaveReport(ctx, s.database, id, "filed", rid, ""); err != nil {
			log.Printf("image scan: report of #%d is filed as %s in record.json; saving that failed again: %v", id, rid, err)
		}
		return rid, at, ErrReportAlreadyFiled
	}
	if !s.reportReady() {
		return "", time.Time{}, ErrReportUnavailable
	}
	failed := func(err error) (string, time.Time, error) {
		if serr := db.SetImageMatchReport(ctx, s.database, id, "failed", "", err.Error()); serr != nil {
			log.Printf("image scan: report failure of #%d: %v", id, serr)
		}
		m.ReportStatus, m.ReportID, m.ReportError = "failed", "", err.Error()
		s.recordReport(m)
		return "", time.Time{}, err
	}
	data, ct, err := s.evidenceImage(m)
	if err != nil {
		return failed(err)
	}
	platform := s.productName()
	in := imagescan.Report{
		OrgName: s.Config.ReportOrg, ReporterName: s.Config.ReportName, ReporterEmail: s.Config.ReportEmail,
		IncidentTime: m.DetectedAt, ReporteeName: m.Uploader,
		Files: []imagescan.ReportFile{{
			Name: path.Base(m.Path), Data: data, ContentType: ct, UploadedAt: m.UploadedAt,
			Metadata: map[string]string{
				"site": m.SiteName, "address": m.Address, "path": m.Path, "kind": m.Kind,
				"provider": m.Provider, "tracking_id": m.TrackingID, "source": m.Source, "violations": m.Violations,
				"match_id": strconv.FormatInt(m.ID, 10),
			},
		}},
		Metadata: map[string]string{"platform": platform},
	}
	res, err := s.Reporter.Report(ctx, in)
	if err != nil {
		return failed(imagescan.SafeError("report", err))
	}
	at := s.now().UTC()
	m.ReportStatus, m.ReportID, m.ReportError, m.ReportedAt = "filed", res.ReportID, "", sql.NullTime{Time: at, Valid: true}
	s.recordReport(m)
	if err := s.SaveFiled(ctx, id, res.ReportID); err != nil {
		log.Printf("image scan: report of #%d WAS filed as %s, but saving that failed: %v", id, res.ReportID, err)
		return res.ReportID, at, &ReportSaveError{reportID: res.ReportID, err: err}
	}
	log.Printf("image scan: report of #%d filed (%s)", id, res.ReportID)
	return res.ReportID, at, nil
}

// ImageReportSaveTries is how many times an accepted report's status is
// saved before giving up (record.json already says it was filed).
const ImageReportSaveTries = 3

// SaveFiled saves an accepted report as filed. A failed save is tried again
// on a context of its own, so a request that ended cannot stop it.
func (s *ImageScan) SaveFiled(ctx context.Context, id int64, reportID string) error {
	err := s.SaveReport(ctx, s.database, id, "filed", reportID, "")
	for try := 1; err != nil && try < ImageReportSaveTries; try++ {
		time.Sleep(time.Duration(try) * 200 * time.Millisecond)
		c, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		err = s.SaveReport(c, s.database, id, "filed", reportID, "")
		cancel()
	}
	return err
}

// filedInRecord reports whether the match's record.json says the report
// was filed (written when the service accepts it, before the database).
func filedInRecord(m model.ImageMatch) (string, time.Time, bool) {
	if m.EvidencePath == "" {
		return "", time.Time{}, false
	}
	b, err := os.ReadFile(filepath.Join(m.EvidencePath, "record.json"))
	if err != nil {
		return "", time.Time{}, false
	}
	var rec struct {
		Status     string    `json:"report_status"`
		ID         string    `json:"report_id"`
		ReportedAt time.Time `json:"reported_at"`
	}
	if json.Unmarshal(b, &rec) != nil || rec.Status != "filed" {
		return "", time.Time{}, false
	}
	return rec.ID, rec.ReportedAt, true
}

// evidenceImage reads the quarantined copy back for the report.
func (s *ImageScan) evidenceImage(m model.ImageMatch) ([]byte, string, error) {
	if m.EvidencePath == "" {
		return nil, "", errors.New("no evidence copy was kept for this match")
	}
	entries, err := os.ReadDir(m.EvidencePath)
	if err != nil {
		return nil, "", err
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "image") {
			b, err := os.ReadFile(filepath.Join(m.EvidencePath, e.Name()))
			if err != nil {
				return nil, "", err
			}
			ct := m.ContentType
			if ct == "" {
				ct, _ = ImageCandidate(b)
			}
			return b, ct, nil
		}
	}
	return nil, "", errors.New("the evidence copy is missing")
}

// matchJSON is a match as the admin page and record.json show it.
func (s *ImageScan) matchJSON(m model.ImageMatch) map[string]any {
	out := map[string]any{
		"id": m.ID, "kind": m.Kind, "site_id": m.SiteID, "site_name": m.SiteName,
		"owner_handle": m.OwnerHandle, "owner_email": m.OwnerEmail, "event_slug": m.EventSlug,
		"path": m.Path, "resource_name": m.ResourceName, "version": m.Version,
		"sha256": m.SHA256, "bytes": m.Bytes, "content_type": m.ContentType,
		"provider": m.Provider, "tracking_id": m.TrackingID, "source": m.Source, "violations": m.Violations,
		"detected_at": m.DetectedAt.UTC(), "evidence_path": m.EvidencePath, "taken_down": m.TakenDown,
		"report_status": m.ReportStatus, "report_id": m.ReportID, "report_error": m.ReportError,
		"event_id": m.EventID, "team_id": m.TeamID, "writer_id": m.WriterID, "uploaded_at": m.UploadedAt.UTC(),
		"uploader": m.Uploader, "address": m.Address,
	}
	if m.AlertedAt.Valid {
		out["alerted_at"] = m.AlertedAt.Time.UTC()
	}
	if m.ReportedAt.Valid {
		out["reported_at"] = m.ReportedAt.Time.UTC()
	}
	return out
}

func (s *ImageScan) updatePending(ctx context.Context) {
	var n int64
	if err := db.PendingImageScans(ctx, s.database, &n); err != nil {
		s.tracker.Fail("count pending jobs", err)
	} else {
		s.tracker.SetPending(n)
	}
}

// ImageScanSiteHooks shares the site's existing lock set and address policy.
type ImageScanSiteHooks struct {
	Lock func(string, string) func()
	URL  func(model.Site) string
}

func (s *ImageScan) SetSiteHooks(hooks ImageScanSiteHooks) {
	if s != nil {
		s.siteHooks = &hooks
	}
}

// ImageScanSummary reports the configured provider and persistent queue/matches.
func (reporting *Reporting) ImageScanSummary(ctx context.Context, s *ImageScan) (map[string]any, error) {
	database := reporting.database
	out := map[string]any{"enabled": s.Enabled(), "provider": "", "auto_report": false, "report_configured": false, "evidence_dir": "", "queue": map[string]int{"pending": 0, "retrying": 0, "gave_up": 0, "site_deleted": 0}}
	if s != nil {
		out["provider"] = s.matcher.Name()
		out["auto_report"] = s.Config.AutoReport
		out["report_configured"] = s.reportReady()
		out["evidence_dir"] = s.Config.EvidenceDir
		if c, err := db.ImageScanQueueCountsNow(ctx, database); err == nil {
			out["queue"] = map[string]int{"pending": c.Pending, "retrying": c.Retrying, "gave_up": c.GaveUp, "site_deleted": c.SiteDeleted}
		} else {
			log.Printf("image scan: queue counts: %v", err)
		}
	}
	matches, err := db.ListImageMatches(ctx, database, imageScanListLimit)
	if err != nil {
		return nil, err
	}
	list := make([]map[string]any, 0, len(matches))
	for _, m := range matches {
		list = append(list, s.matchJSON(m))
	}
	out["matches"] = list
	return out, nil
}
func (reporting *Reporting) ImageMatchExists(ctx context.Context, id int64) error {
	_, err := db.GetImageMatch(ctx, reporting.database, id)
	return err
}

// Client request/result types are exposed for composition and transport fixtures.
type ScanImage = imagescan.Image
type ScanMatch = imagescan.Match
type ScanReport = imagescan.Report
type ScanReportResult = imagescan.ReportResult
type ScanPrepareOptions = imagescan.PrepareOptions

var ScanErrRetryable = imagescan.ErrRetryable
var ScanErrUnsupported = imagescan.ErrUnsupported

type noticeSender interface {
	SendNotice(string, string, string) error
}
