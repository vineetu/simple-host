package handler

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
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/vsriram/simple-host/internal/auth"
	"github.com/vsriram/simple-host/internal/db"
	"github.com/vsriram/simple-host/internal/email"
	"github.com/vsriram/simple-host/internal/imagescan"
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
	reporter      imagescan.Reporter // nil: reports cannot be filed
	mailer        email.Sender
	cfg           ImageScanConfig
	now           func() time.Time
	// run overrides dispatch in tests; production uses the retained runner.
	run func(func())
	// prepare is imagescan.Prepare; tests substitute a trivial one.
	prepare func([]byte, imagescan.PrepareOptions) (*imagescan.Image, string, error)
	// saveReport is db.SetImageMatchReport for a filed report; tests make
	// it fail.
	saveReport func(context.Context, db.Querier, int64, string, string, string) error
	// sites is set by SiteHandler's SetImageScan: the site lock and address.
	sites *SiteHandler
	// hackTakeDownSync is HackHandler's syncEventMarkers (hack.go),
	// connected by HackHandler's own SetImageScan (moved there in cleanup
	// 3.2 slice 1c, since a method with a *HackHandler receiver must live
	// in that type's package). A func, not a *HackHandler field, so this
	// file never names HackHandler: it stays in handler while HackHandler
	// moves to internal/hackhttp.
	hackTakeDownSync func(ctx context.Context, ev db.Event) bool
}

const (
	imageScanMinBytes    = 256
	imageScanBootDelay   = 15 * time.Second
	imageScanEvery       = 10 * time.Second
	imageScanClaim       = 4
	imageScanConcurrency = 2
	imageScanMaxAttempts = 12
	imageScanListLimit   = 200
	imageScanDeletedWait = time.Hour
	imageScanTakeDownMsg = "Taken down by the operator after an automated content check. Write to the support address if you believe this is a mistake."
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
		database: database, disk: disk, matcher: matcher, reporter: reporter, mailer: mailer, cfg: cfg,
		now:        time.Now,
		prepare:    imagescan.Prepare,
		saveReport: db.SetImageMatchReport,
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
	return s != nil && s.reporter != nil && s.cfg.ReportOrg != "" && s.cfg.ReportName != "" && s.cfg.ReportEmail != ""
}

// SetImageScan connects the worker to the site side (site locks, addresses,
// take-down markers).
func (h *SiteHandler) SetImageScan(s *ImageScan) {
	h.imageScan = s
	if s != nil {
		s.sites = h
	}
}

// SetHackTakeDownSync wires the events-side take-down-marker sync (owned by
// HackHandler, which calls this from its own SetImageScan in hack.go); nil
// keeps the hack take-down path a no-op, same as before this was a func.
func (s *ImageScan) SetHackTakeDownSync(fn func(ctx context.Context, ev db.Event) bool) {
	if s != nil {
		s.hackTakeDownSync = fn
	}
}

// imageScanOn is read by the privacy page: the image-matching line is served
// only while the feature is on (SetImageScanOn, from main).
var imageScanOn bool

// SetImageScanOn records whether image matching is on for the served pages.
// The privacy page is assembled once and cached (chromePageRaw strips the
// line there), so a change drops its cached copies.
func SetImageScanOn(on bool) {
	imageScanOn = on
	chromeCache.Range(func(k, _ any) bool {
		if k.(chromeCacheKey).name == "privacy.html" {
			chromeCache.Delete(k)
		}
		return true
	})
}

var imageScanPrivacyItem = regexp.MustCompile(`(?s)[ \t]*<li data-sh="image-scan">.*?</li>[ \t]*\r?\n?`)

// stripImageScanItem removes the privacy page's image-matching line while
// the feature is off. Called once per cached page (chromePageRaw).
func stripImageScanItem(page []byte) []byte {
	if imageScanOn {
		return page
	}
	return imageScanPrivacyItem.ReplaceAll(page, nil)
}

// imageCandidate is the cheap filter before anything is hashed or queued.
func imageCandidate(b []byte) (string, bool) {
	if len(b) < imageScanMinBytes {
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

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// imageFinding is a match about to be quarantined: the job it came from (ID
// 0 and the current time for a hash-cache hit) and what the service said.
type imageFinding struct {
	job         db.ImageScanJob
	data        []byte
	contentType string
	provider    string
	match       imagescan.Match
}

// enqueueCandidate is one image on its way to the queue.
type enqueueCandidate struct {
	job  db.ImageScanJob
	data []byte
	ct   string
}

// EnqueueSiteFiles queues the images of a deployed version. writerID is the
// account of the person who deployed it (siteWriterID). Nil-safe; the work
// runs off the request through s.run.
func (s *ImageScan) EnqueueSiteFiles(siteID, writerID, siteName string, version int, files map[string][]byte) {
	if s == nil || len(files) == 0 {
		return
	}
	var cands []enqueueCandidate
	for p, b := range files {
		ct, ok := imageCandidate(b)
		if !ok {
			continue
		}
		cands = append(cands, enqueueCandidate{data: b, ct: ct, job: db.ImageScanJob{
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
	ct, ok := imageCandidate(body)
	if !ok {
		return
	}
	s.enqueue([]enqueueCandidate{{data: body, ct: ct, job: db.ImageScanJob{
		Kind: "storage_file", SiteID: siteID, ResourceName: resourceName, Path: path, WriterID: writerID,
	}}})
}

// siteWriterID is the account of the person behind a deploy. On Simple Hack
// the site belongs to the event's holding account, so a team key names its
// member and an organiser's publish names the organiser.
func siteWriterID(u *db.User) string {
	switch {
	case u.Team != nil && u.Team.MemberID != "":
		return u.Team.MemberID
	case u.EventWebsite != nil && u.EventWebsite.OrganiserID != "":
		return u.EventWebsite.OrganiserID
	}
	return u.ID
}

// storageUploader is who wrote a files-storage object, as a report names
// them: "owner" for the site's owner, "user:<id>" for another account
// writing with owner rights (a Simple Hack team member or organiser), else
// the visitor's id ("" when the visitor was not signed in).
func storageUploader(c storageCall) string {
	if c.owner {
		if c.uploaderID != "" && c.uploaderID != c.ownerID {
			return "user:" + c.uploaderID
		}
		return "owner"
	}
	return c.stampID()
}

// EnqueueEventIcon queues an event's icon. userID is the organiser who
// uploaded it (the report names them).
func (s *ImageScan) EnqueueEventIcon(eventID, userID string, raw []byte) {
	if s == nil {
		return
	}
	ct, ok := imageCandidate(raw)
	if !ok {
		return
	}
	s.enqueue([]enqueueCandidate{{data: raw, ct: ct, job: db.ImageScanJob{Kind: "event_icon", EventID: eventID, WriterID: userID, Path: "icon"}}})
}

// EnqueueEntryScreenshot queues a team's entry screenshot.
func (s *ImageScan) EnqueueEntryScreenshot(eventID, teamID, userID string, raw []byte) {
	if s == nil {
		return
	}
	ct, ok := imageCandidate(raw)
	if !ok {
		return
	}
	s.enqueue([]enqueueCandidate{{data: raw, ct: ct, job: db.ImageScanJob{
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
			c.job.SHA256 = sha256Hex(c.data)
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
	if s.run != nil {
		s.run(func() { fn(context.Background()) })
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
	groups := map[string][]db.ImageScanJob{}
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
		go func(group []db.ImageScanJob) {
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

// loadJobBytes finds the bytes a job names. nil, nil when they are gone
// (version pruned, file replaced, row deleted).
func (s *ImageScan) loadJobBytes(ctx context.Context, j db.ImageScanJob) ([]byte, error) {
	var raw []byte
	switch j.Kind {
	case "site_file", "storage_file":
		site, err := db.GetSiteByID(ctx, s.database, j.SiteID)
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
			if perr := db.PostponeImageScan(ctx, s.database, j.ID, imageScanDeletedWait); perr != nil {
				s.tracker.Fail("postpone deleted-site job", perr)
			}
			unlock()
			return nil, errImageScanSiteDeleted
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
		_, body, err := db.GetEventIcon(ctx, s.database, j.EventID)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		raw = body
	case "entry_screenshot":
		data, _, err := db.GetEventScreenshot(ctx, s.database, j.TeamID)
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
	if len(raw) == 0 || sha256Hex(raw) != j.SHA256 {
		// Replaced since it was queued; the new bytes have their own job.
		return nil, nil
	}
	return raw, nil
}

// readSiteJobFile reads a site-file or files-storage job's bytes from the
// site's folder as the row names it. os.ErrNotExist when they are not there.
func (s *ImageScan) readSiteJobFile(site db.Site, j db.ImageScanJob) ([]byte, error) {
	if j.Kind == "storage_file" {
		return os.ReadFile(storageFileOnDisk(s.disk, site.UserID, site.Name, j.ResourceName, j.Path))
	}
	dir := s.disk.VersionDir(site.UserID, site.Name, j.Version)
	if dir == "" {
		return nil, os.ErrNotExist
	}
	return os.ReadFile(filepath.Join(dir, filepath.FromSlash(j.Path)))
}

// errImageScanSiteDeleted: the job's site is in Recently deleted.
// loadJobBytes has postponed the job (imageScanDeletedWait) to wait for a
// restore, which brings the bytes back at the same paths, or for the purge,
// which removes the row.
var errImageScanSiteDeleted = errors.New("the site is in Recently deleted")

func (s *ImageScan) runJob(ctx context.Context, j db.ImageScanJob) {
	raw, err := s.loadJobBytes(ctx, j)
	if errors.Is(err, errImageScanSiteDeleted) {
		// loadJobBytes has postponed it.
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
	ct, _ := imageCandidate(raw)
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
	img, skip, err := s.prepare(raw, imagescan.PrepareOptions{})
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
func (s *ImageScan) deferJob(ctx context.Context, j db.ImageScanJob, err error) {
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
	dir, err := resolvedPath(s.cfg.EvidenceDir)
	if err != nil {
		return fmt.Sprintf("evidence dir %q: %v", s.cfg.EvidenceDir, err)
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
func matchWhere(m db.ImageMatch) string {
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
	m := db.ImageMatch{
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
	var site db.Site
	var ev db.Event
	if siteKind {
		var err error
		if site, err = db.GetSiteByID(ctx, s.database, j.SiteID); err != nil {
			log.Printf("image scan: site %s: %v", j.SiteID, err)
		} else {
			s.fillSiteOwner(ctx, &m, site)
		}
	} else {
		var err error
		if ev, err = db.GetEventByID(ctx, s.database, j.EventID); err != nil {
			log.Printf("image scan: event %s: %v", j.EventID, err)
		} else {
			m.EventSlug, m.OwnerID, m.OwnerEmail = ev.Slug, ev.AccountID, ev.ContactEmail
			if u, err := db.GetUserByID(ctx, s.database, ev.AccountID); err == nil && u.Handle.Valid {
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
	m.Uploader, m.Address = s.reporteeOf(ctx, m), s.addressOf(ctx, m)
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
		dir := filepath.Join(s.cfg.EvidenceDir, strconv.FormatInt(id, 10))
		if err := os.MkdirAll(s.cfg.EvidenceDir, 0o700); err != nil {
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
	case s.cfg.AutoReport && s.reportReady() && id != 0:
		rid, at, err := s.fileReport(ctx, id)
		var saveErr *reportSaveError
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
	} else if s.cfg.AlertEmail != "" {
		add("alert email", stepDone, "sent to "+s.cfg.AlertEmail, nil)
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
func (s *ImageScan) writeRecordKeepFiled(dir string, m db.ImageMatch, steps []imageStep) {
	imageReportMu.Lock()
	defer imageReportMu.Unlock()
	if rid, at, filed := filedInRecord(db.ImageMatch{EvidencePath: dir}); filed && m.ReportStatus != "filed" {
		m.ReportStatus, m.ReportID, m.ReportError, m.ReportedAt = "filed", rid, "", sql.NullTime{Time: at, Valid: true}
	}
	s.writeRecord(dir, m, steps)
}

// fillSiteOwner copies the site's name and owner onto the match.
func (s *ImageScan) fillSiteOwner(ctx context.Context, m *db.ImageMatch, site db.Site) {
	m.SiteName, m.OwnerID, m.OwnerHandle = site.Name, site.UserID, site.OwnerHandle
	m.OwnerEmail = ""
	if u, err := db.GetUserByID(ctx, s.database, site.UserID); err == nil && strings.Contains(u.Username, "@") {
		m.OwnerEmail = u.Username
	}
}

// takeDownSite removes the matched file (when the evidence copy is safe) and
// takes the site down, the way the operator take-down does (suspend.go):
// under the site's lock, with the row read again under it so a rename in
// between is seen, held through the removal, the suspension, and the marker.
// The same lock guards the files-storage PUT path. TakenDown is set only
// when both the suspension and the marker succeeded.
func (s *ImageScan) takeDownSite(ctx context.Context, m *db.ImageMatch, site db.Site, evidenceOK bool, add func(string, string, string, error), ok func(bool) string) {
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
	if err := db.SetSiteSuspended(ctx, s.database, site.ID, imageScanTakeDownMsg); err != nil {
		add("take-down", stepFailed, "the site", err)
		return
	}
	fresh, err := db.GetSiteByID(ctx, s.database, site.ID)
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
func (s *ImageScan) lockFreshSite(ctx context.Context, site db.Site) (db.Site, func(), error) {
	if s.sites == nil {
		fresh, err := db.GetSiteByID(ctx, s.database, site.ID)
		return fresh, func() {}, err
	}
	for {
		unlock := s.sites.lockSite(site.UserID, site.Name)
		fresh, err := db.GetSiteByID(ctx, s.database, site.ID)
		if err != nil {
			unlock()
			return db.Site{}, nil, err
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
func (s *ImageScan) removeStorageObject(ctx context.Context, site db.Site, m db.ImageMatch) (string, error) {
	p := storageFileOnDisk(s.disk, site.UserID, site.Name, m.ResourceName, m.Path)
	b, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return "the object was already gone", nil
	}
	if err != nil {
		return "", err
	}
	if sha256Hex(b) != m.SHA256 {
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
func (s *ImageScan) takeDownEvent(ctx context.Context, ev db.Event) error {
	tx, err := s.database.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := db.TakeDownEvent(ctx, tx, ev.ID, imageScanTakeDownMsg); err != nil {
		return err
	}
	if err := db.SetUserSuspended(ctx, tx, ev.AccountID, imageScanTakeDownMsg); err != nil {
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
func (s *ImageScan) writeRecord(dir string, m db.ImageMatch, steps []imageStep) {
	rec := s.matchJSON(m)
	rec["steps"] = steps
	if len(m.Raw) > 0 {
		rec["raw_reply"] = m.Raw
	}
	s.saveRecord(dir, m.ID, rec)
}

// recordReport rewrites record.json after a report attempt so it carries
// the report's outcome. A missing or unreadable record starts from the row.
func (s *ImageScan) recordReport(m db.ImageMatch) {
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
	if s.cfg.ProductName != "" {
		return s.cfg.ProductName
	}
	return "Simple Host"
}

// sendAlert emails the alert address what happened, step by step. Never the
// image, never a path or link to it.
func (s *ImageScan) sendAlert(ctx context.Context, m db.ImageMatch, steps []imageStep, reportFiled bool) error {
	if s.cfg.AlertEmail == "" {
		return nil
	}
	sender, ok := s.mailer.(noticeSender)
	if !ok || s.mailer == nil {
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
	fmt.Fprintf(&b, "\nWhat to do next: open %s/admin#moderation.\n", strings.TrimRight(s.cfg.PublicBaseURL, "/"))
	if !reportFiled {
		fmt.Fprintf(&b, "File the CyberTipline report (the Report button on the admin page, or https://report.cybertip.org/) as soon as reasonably possible; the law requires it.\n")
	}
	return sender.SendNotice(s.cfg.AlertEmail, subject, b.String())
}

// addressOf is the site's own https address (as the dashboard shows it) or
// the event slug.
func (s *ImageScan) addressOf(ctx context.Context, m db.ImageMatch) string {
	if m.EventSlug != "" {
		return m.EventSlug
	}
	if m.SiteID == "" {
		return ""
	}
	site, err := db.GetSiteByID(ctx, s.database, m.SiteID)
	if err != nil {
		return ""
	}
	if s.sites != nil {
		return s.sites.siteURLFor(site)
	}
	return site.SiteURL
}

var (
	errReportAlreadyFiled = errors.New("already reported")
	errReportUnavailable  = errors.New("reports cannot be filed: set IMAGE_SCAN_REPORT_ORG, IMAGE_SCAN_REPORT_NAME, and IMAGE_SCAN_REPORT_EMAIL")
	// imageReportMu serialises fileReport, so two clicks (or a click and
	// the automatic report) cannot file the same match twice.
	imageReportMu sync.Mutex
)

// reportSaveError is a report the service accepted whose 'filed' status
// could not be saved.
type reportSaveError struct {
	reportID string
	err      error
}

func (e *reportSaveError) Error() string {
	return fmt.Sprintf("the report WAS filed with id %s, but saving that failed: %v", e.reportID, e.err)
}

// holdingAccountSuffix marks a Simple Hack event's holding account
// (hack.go), which owns the event's sites and is no person.
const holdingAccountSuffix = "@events.invalid"

// reporteeOf names the person a report is about: the account recorded as
// the writer (who deployed a site file, the participant or organiser who
// uploaded an entry screenshot or event icon, the team member or organiser
// who wrote a files-storage object), a visitor's id, or an anonymous
// visitor. A site file or owner write with no writer on record names the
// site owner, never an event's holding account.
func (s *ImageScan) reporteeOf(ctx context.Context, m db.ImageMatch) string {
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
	u, err := db.GetUserByID(ctx, s.database, id)
	if err != nil || strings.HasSuffix(u.Username, holdingAccountSuffix) {
		return ""
	}
	name := u.Handle.String
	if strings.Contains(u.Username, "@") {
		name += " " + u.Username
	}
	return strings.TrimSpace(name)
}

// fileReport files the CyberTipline report for one match through the
// service and records the outcome, in the database and in record.json.
// Refused when already filed or when reports are not configured; a service
// failure leaves 'failed' with the error so the admin page can retry.
func (s *ImageScan) fileReport(ctx context.Context, id int64) (string, time.Time, error) {
	imageReportMu.Lock()
	defer imageReportMu.Unlock()
	m, err := db.GetImageMatch(ctx, s.database, id)
	if err != nil {
		return "", time.Time{}, err
	}
	if m.ReportStatus == "filed" {
		return m.ReportID, m.ReportedAt.Time, errReportAlreadyFiled
	}
	if rid, at, ok := filedInRecord(m); ok {
		// The service took a report whose status could not be saved: never
		// a second one. Save it now.
		if err := s.saveReport(ctx, s.database, id, "filed", rid, ""); err != nil {
			log.Printf("image scan: report of #%d is filed as %s in record.json; saving that failed again: %v", id, rid, err)
		}
		return rid, at, errReportAlreadyFiled
	}
	if !s.reportReady() {
		return "", time.Time{}, errReportUnavailable
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
		OrgName: s.cfg.ReportOrg, ReporterName: s.cfg.ReportName, ReporterEmail: s.cfg.ReportEmail,
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
	res, err := s.reporter.Report(ctx, in)
	if err != nil {
		return failed(imagescan.SafeError("report", err))
	}
	at := s.now().UTC()
	m.ReportStatus, m.ReportID, m.ReportError, m.ReportedAt = "filed", res.ReportID, "", sql.NullTime{Time: at, Valid: true}
	s.recordReport(m)
	if err := s.saveFiled(ctx, id, res.ReportID); err != nil {
		log.Printf("image scan: report of #%d WAS filed as %s, but saving that failed: %v", id, res.ReportID, err)
		return res.ReportID, at, &reportSaveError{reportID: res.ReportID, err: err}
	}
	log.Printf("image scan: report of #%d filed (%s)", id, res.ReportID)
	return res.ReportID, at, nil
}

// imageReportSaveTries is how many times an accepted report's status is
// saved before giving up (record.json already says it was filed).
const imageReportSaveTries = 3

// saveFiled saves an accepted report as filed. A failed save is tried again
// on a context of its own, so a request that ended cannot stop it.
func (s *ImageScan) saveFiled(ctx context.Context, id int64, reportID string) error {
	err := s.saveReport(ctx, s.database, id, "filed", reportID, "")
	for try := 1; err != nil && try < imageReportSaveTries; try++ {
		time.Sleep(time.Duration(try) * 200 * time.Millisecond)
		c, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		err = s.saveReport(c, s.database, id, "filed", reportID, "")
		cancel()
	}
	return err
}

// filedInRecord reports whether the match's record.json says the report
// was filed (written when the service accepts it, before the database).
func filedInRecord(m db.ImageMatch) (string, time.Time, bool) {
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
func (s *ImageScan) evidenceImage(m db.ImageMatch) ([]byte, string, error) {
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
				ct, _ = imageCandidate(b)
			}
			return b, ct, nil
		}
	}
	return nil, "", errors.New("the evidence copy is missing")
}

// matchJSON is a match as the admin page and record.json show it.
func (s *ImageScan) matchJSON(m db.ImageMatch) map[string]any {
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

// adminImageScan is GET /v1/admin/image-scan: the feature's state, the
// queue, every match whose report is still open, and the newest matches. Matches are listed whether or not the
// feature is on: a record outlives a config change.
func (h *SiteHandler) adminImageScan(w http.ResponseWriter, r *http.Request) {
	s := h.imageScan
	out := map[string]any{
		"enabled": s.Enabled(), "provider": "", "auto_report": false, "report_configured": false, "evidence_dir": "",
		"queue": map[string]int{"pending": 0, "retrying": 0, "gave_up": 0, "site_deleted": 0},
	}
	if s != nil {
		out["provider"] = s.matcher.Name()
		out["auto_report"] = s.cfg.AutoReport
		out["report_configured"] = s.reportReady()
		out["evidence_dir"] = s.cfg.EvidenceDir
		if c, err := db.ImageScanQueueCountsNow(r.Context(), h.database); err == nil {
			out["queue"] = map[string]int{"pending": c.Pending, "retrying": c.Retrying, "gave_up": c.GaveUp, "site_deleted": c.SiteDeleted}
		} else {
			log.Printf("image scan: queue counts: %v", err)
		}
	}
	matches, err := db.ListImageMatches(r.Context(), h.database, imageScanListLimit)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	list := make([]map[string]any, 0, len(matches))
	for _, m := range matches {
		list = append(list, s.matchJSON(m))
	}
	out["matches"] = list
	writeJSON(w, http.StatusOK, out)
}

// adminImageScanReport is POST /v1/admin/image-scan/{id}/report.
func (h *SiteHandler) adminImageScanReport(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeJSON(w, http.StatusNotFound, errorResponse{Error: "match not found"})
		return
	}
	if h.imageScan == nil {
		if _, err := db.GetImageMatch(r.Context(), h.database, id); errors.Is(err, sql.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, errorResponse{Error: "match not found"})
			return
		}
		writeJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "image matching is off; set PHOTODNA_API_KEY, IMAGE_SCAN_REPORT_ORG, IMAGE_SCAN_REPORT_NAME, and IMAGE_SCAN_REPORT_EMAIL to file reports from here", Code: "report_unavailable"})
		return
	}
	rid, at, err := h.imageScan.fileReport(r.Context(), id)
	var saveErr *reportSaveError
	switch {
	case errors.As(err, &saveErr):
		writeJSON(w, http.StatusBadGateway, errorResponse{Error: err.Error(), Code: "report_failed"})
	case errors.Is(err, sql.ErrNoRows):
		writeJSON(w, http.StatusNotFound, errorResponse{Error: "match not found"})
	case errors.Is(err, errReportAlreadyFiled):
		writeJSON(w, http.StatusConflict, errorResponse{Error: "this match was already reported as " + rid, Code: "already_reported"})
	case errors.Is(err, errReportUnavailable):
		writeJSON(w, http.StatusServiceUnavailable, errorResponse{Error: err.Error(), Code: "report_unavailable"})
	case err != nil:
		writeJSON(w, http.StatusBadGateway, errorResponse{Error: "the report could not be filed: " + err.Error(), Code: "report_failed"})
	default:
		admin := auth.GetUser(r.Context())
		log.Printf("admin_image_scan_report match=%d report_id=%s by=%s", id, rid, admin.ID)
		writeJSON(w, http.StatusOK, map[string]any{"report_id": rid, "reported_at": at})
	}
}

func (s *ImageScan) updatePending(ctx context.Context) {
	var n int64
	if err := s.database.QueryRowContext(ctx, `SELECT count(*) FROM image_scan_queue WHERE next_attempt_at < 'infinity'`).Scan(&n); err != nil {
		s.tracker.Fail("count pending jobs", err)
	} else {
		s.tracker.SetPending(n)
	}
}
