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
	// A report is refused without ReportOrg.
	ReportOrg   string
	ReportName  string
	ReportEmail string
	// AutoReport files the report as soon as a match is recorded.
	AutoReport bool
	// PublicBaseURL is where the admin page lives (the alert links to it).
	PublicBaseURL string
}

// ImageScan is the queue worker and the quarantine. A nil *ImageScan is the
// feature off: every method is a no-op.
type ImageScan struct {
	database *sql.DB
	disk     *storage.DiskStorage
	matcher  imagescan.Matcher
	reporter imagescan.Reporter // nil: reports cannot be filed
	mailer   email.Sender
	cfg      ImageScanConfig
	now      func() time.Time
	// run starts the enqueue work: a goroutine in production, synchronous
	// in tests.
	run func(func())
	// prepare is imagescan.Prepare; tests substitute a trivial one.
	prepare func([]byte, imagescan.PrepareOptions) (*imagescan.Image, string, error)
	// sites and hack are set by the SetImageScan setters: the site lock
	// and address, and the event take-down markers.
	sites *SiteHandler
	hack  *HackHandler
	// gaveUp remembers which jobs were logged as given up, once each.
	gaveUp sync.Map
}

const (
	imageScanMinBytes    = 256
	imageScanBootDelay   = 15 * time.Second
	imageScanEvery       = 10 * time.Second
	imageScanClaim       = 4
	imageScanConcurrency = 2
	imageScanMaxAttempts = 12
	imageScanListLimit   = 200
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
	s := &ImageScan{
		database: database, disk: disk, matcher: matcher, reporter: reporter, mailer: mailer, cfg: cfg,
		now:     time.Now,
		run:     func(f func()) { go f() },
		prepare: imagescan.Prepare,
	}
	if why := s.evidenceDirRefused(); why != "" {
		log.Printf("image scan: WARNING: %s; no evidence copy can be kept", why)
	}
	return s
}

// Enabled reports whether image matching is on.
func (s *ImageScan) Enabled() bool { return s != nil }

// SetImageScan connects the worker to the site side (site locks, addresses,
// take-down markers).
func (h *SiteHandler) SetImageScan(s *ImageScan) {
	h.imageScan = s
	if s != nil {
		s.sites = h
	}
}

// SetImageScan connects the worker to the events side (event take-down markers).
func (h *HackHandler) SetImageScan(s *ImageScan) {
	h.imageScan = s
	if s != nil {
		s.hack = h
	}
}

// imageScanOn is read by the privacy page: the image-matching line is served
// only while the feature is on (SetImageScanOn, from main).
var imageScanOn bool

// SetImageScanOn records whether image matching is on for the served pages.
func SetImageScanOn(on bool) { imageScanOn = on }

var imageScanPrivacyItem = regexp.MustCompile(`(?s)[ \t]*<li data-sh="image-scan">.*?</li>[ \t]*\r?\n?`)

// stripImageScanItem removes the privacy page's image-matching line while
// the feature is off.
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

// EnqueueSiteFiles queues the images of a deployed version. Nil-safe; the
// work runs off the request through s.run.
func (s *ImageScan) EnqueueSiteFiles(siteID, userID, siteName string, version int, files map[string][]byte) {
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
			Kind: "site_file", SiteID: siteID, Version: version, Path: filepath.ToSlash(filepath.Clean(p)), WriterID: userID,
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

// EnqueueEventIcon queues an event's icon.
func (s *ImageScan) EnqueueEventIcon(eventID string, raw []byte) {
	if s == nil {
		return
	}
	ct, ok := imageCandidate(raw)
	if !ok {
		return
	}
	s.enqueue([]enqueueCandidate{{data: raw, ct: ct, job: db.ImageScanJob{Kind: "event_icon", EventID: eventID, Path: "icon"}}})
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
	s.run(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		for _, c := range cands {
			c.job.SHA256 = sha256Hex(c.data)
			c.job.Bytes = int64(len(c.data))
			verdict, tracking, found, err := db.LookupImageHash(ctx, s.database, c.job.SHA256)
			if err != nil {
				log.Printf("image scan: hash lookup: %v", err)
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
					log.Printf("image scan: enqueue %s %s: %v", c.job.Kind, c.job.Path, err)
				}
			}
		}
	})
}

// Start runs the worker: a short wait after boot, then a pass every few
// seconds until ctx ends.
func (s *ImageScan) Start(ctx context.Context) {
	if s == nil {
		return
	}
	go func() {
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
	}()
}

// RunOnce claims due jobs and runs them, a few at a time. Returns how many
// jobs were handled.
func (s *ImageScan) RunOnce(ctx context.Context) int {
	if s == nil {
		return 0
	}
	jobs, err := db.ClaimImageScans(ctx, s.database, imageScanClaim)
	if err != nil {
		log.Printf("image scan: claim: %v", err)
		return 0
	}
	sem := make(chan struct{}, imageScanConcurrency)
	var wg sync.WaitGroup
	for _, j := range jobs {
		wg.Add(1)
		sem <- struct{}{}
		go func(j db.ImageScanJob) {
			defer wg.Done()
			defer func() { <-sem }()
			s.runJob(ctx, j)
		}(j)
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
		var p string
		if j.Kind == "site_file" {
			dir := s.disk.VersionDir(site.UserID, site.Name, j.Version)
			if dir == "" {
				return nil, nil
			}
			p = filepath.Join(dir, filepath.FromSlash(j.Path))
		} else {
			p = storageFileOnDisk(s.disk, site.UserID, site.Name, j.ResourceName, j.Path)
		}
		raw, err = os.ReadFile(p)
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

func (s *ImageScan) runJob(ctx context.Context, j db.ImageScanJob) {
	raw, err := s.loadJobBytes(ctx, j)
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
		s.record(ctx, j.SHA256, imagescan.VerdictSkipped, "", err.Error())
		s.finish(ctx, j.ID)
	case err != nil:
		s.deferJob(ctx, j, err)
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
		log.Printf("image scan: record %s %s: %v", v, sha[:12], err)
	}
}

func (s *ImageScan) finish(ctx context.Context, id int64) {
	if id == 0 {
		return
	}
	if err := db.DeleteImageScan(ctx, s.database, id); err != nil {
		log.Printf("image scan: delete job %d: %v", id, err)
	}
}

// deferJob schedules another attempt with a growing wait. After the limit
// the job stays with its error (one log line), so the admin page's retrying
// count and the log say what happened.
func (s *ImageScan) deferJob(ctx context.Context, j db.ImageScanJob, err error) {
	attempts := j.Attempts + 1
	text := err.Error()
	if attempts > imageScanMaxAttempts {
		text = "gave up: " + text
		if _, logged := s.gaveUp.LoadOrStore(j.ID, true); !logged {
			log.Printf("image scan: job %d (%s %s) gave up after %d attempts: %v", j.ID, j.Kind, j.Path, j.Attempts, err)
		}
	} else if imagescan.IsRetryable(err) {
		log.Printf("image scan: job %d (%s %s) attempt %d: %v (will retry)", j.ID, j.Kind, j.Path, attempts, err)
	} else {
		log.Printf("image scan: job %d (%s %s) attempt %d: %v", j.ID, j.Kind, j.Path, attempts, err)
	}
	if derr := db.DeferImageScan(ctx, s.database, j.ID, text, attempts); derr != nil {
		log.Printf("image scan: defer job %d: %v", j.ID, derr)
	}
}

// evidenceDirRefused says why the evidence folder cannot be used: it must
// never sit under a served path.
func (s *ImageScan) evidenceDirRefused() string {
	dir, err := filepath.Abs(s.cfg.EvidenceDir)
	if err != nil {
		return fmt.Sprintf("evidence dir %q: %v", s.cfg.EvidenceDir, err)
	}
	if s.disk == nil {
		return ""
	}
	for _, served := range []string{"by-id", "handles", "domains"} {
		base, err := filepath.Abs(filepath.Join(s.disk.DataDir(), served))
		if err != nil {
			continue
		}
		rel, err := filepath.Rel(base, dir)
		if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return fmt.Sprintf("evidence dir %q is under the served path %s", dir, base)
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

// quarantine acts on a match, in this order: record the match row first so
// nothing is lost, keep the evidence copy, remove the file from every served
// copy, take the site or event down, email the alert, file the report. Each
// step logs its own failure and the next one still runs.
func (s *ImageScan) quarantine(ctx context.Context, f imageFinding) {
	j := f.job
	m := db.ImageMatch{
		Kind: j.Kind, SiteID: j.SiteID, EventID: j.EventID, TeamID: j.TeamID, Version: j.Version,
		ResourceName: j.ResourceName, Path: j.Path, WriterID: j.WriterID, SHA256: j.SHA256, Bytes: int64(len(f.data)),
		ContentType: f.contentType, Provider: f.provider, TrackingID: f.match.TrackingID, Source: f.match.Source,
		Violations: strings.Join(f.match.Violations, ","), Raw: f.match.Raw, UploadedAt: j.CreatedAt,
		ReportStatus: "pending",
	}
	if s.reporter == nil {
		m.ReportStatus = "off"
	}
	var site db.Site
	var ev db.Event
	switch j.Kind {
	case "site_file", "storage_file":
		var err error
		if site, err = db.GetSiteByID(ctx, s.database, j.SiteID); err != nil {
			log.Printf("image scan: site %s: %v", j.SiteID, err)
		} else {
			m.SiteName, m.OwnerID, m.OwnerHandle = site.Name, site.UserID, site.OwnerHandle
			if u, err := db.GetUserByID(ctx, s.database, site.UserID); err == nil && strings.Contains(u.Username, "@") {
				m.OwnerEmail = u.Username
			}
		}
	default:
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
	var did []string
	note := func(format string, args ...any) {
		line := fmt.Sprintf(format, args...)
		did = append(did, line)
		log.Printf("image scan: match on %s %s: %s", matchWhere(m), m.Path, line)
	}

	// 1. The record.
	id, err := db.InsertImageMatch(ctx, s.database, m)
	if err != nil {
		log.Printf("image scan: MATCH on %s %s could not be recorded: %v (acting on it anyway)", matchWhere(m), m.Path, err)
	}
	m.ID = id
	m.DetectedAt = s.now().UTC()
	log.Printf("image scan: MATCH #%d on %s %s (%s, %s)", id, matchWhere(m), m.Path, f.provider, m.Source)

	// 2. The evidence copy.
	var evidence string
	if why := s.evidenceDirRefused(); why != "" {
		note("evidence copy NOT kept: %s", why)
	} else if id == 0 {
		note("evidence copy NOT kept: no match id")
	} else {
		evidence = filepath.Join(s.cfg.EvidenceDir, strconv.FormatInt(id, 10))
		if err := os.MkdirAll(evidence, 0o700); err != nil {
			note("evidence folder %s: %v", evidence, err)
			evidence = ""
		} else if err := os.WriteFile(filepath.Join(evidence, "image"+imageExt(f.contentType)), f.data, 0o600); err != nil {
			note("evidence copy in %s: %v", evidence, err)
		} else {
			note("copy kept in %s", evidence)
			m.EvidencePath = evidence
			if err := db.SetImageMatchEvidence(ctx, s.database, id, evidence); err != nil {
				log.Printf("image scan: evidence path of #%d: %v", id, err)
			}
			s.writeRecord(evidence, m, did)
		}
	}

	// 3. Out of service.
	switch j.Kind {
	case "site_file":
		if site.ID == "" {
			note("file NOT removed: site row missing")
		} else {
			unlock := s.lockSite(site)
			err := s.disk.RemoveFileEverywhere(site.UserID, site.Name, j.Path)
			unlock()
			if err != nil {
				note("file removal: %v", err)
			} else {
				note("file removed from every version and the live copy")
			}
		}
	case "storage_file":
		if site.ID == "" {
			note("object NOT removed: site row missing")
		} else {
			p := storageFileOnDisk(s.disk, site.UserID, site.Name, j.ResourceName, j.Path)
			if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
				note("object removal: %v", err)
			} else if err := db.DeleteStorageFileRow(ctx, s.database, j.SiteID, j.ResourceName, j.Path); err != nil {
				note("object row removal: %v", err)
			} else {
				note("object removed from the files resource")
				s.disk.MarkChanged()
			}
		}
	case "event_icon":
		if err := db.ClearEventIcon(ctx, s.database, j.EventID); err != nil {
			note("icon removal: %v", err)
		} else {
			note("event icon removed")
		}
	case "entry_screenshot":
		if err := db.ClearEventScreenshot(ctx, s.database, j.TeamID); err != nil {
			note("screenshot removal: %v", err)
		} else {
			note("entry screenshot removed")
		}
	}

	// 4. Take-down.
	switch j.Kind {
	case "site_file", "storage_file":
		if site.ID == "" {
			note("site NOT taken down: site row missing")
		} else if err := db.SetSiteSuspended(ctx, s.database, site.ID, imageScanTakeDownMsg); err != nil {
			note("take-down: %v", err)
		} else {
			fresh, err := db.GetSiteByID(ctx, s.database, site.ID)
			if err != nil {
				note("site taken down, but the marker could not be written: %v", err)
			} else if err := s.disk.SetSuspended(fresh.UserID, fresh.Name, fresh.Suspended()); err != nil {
				note("site taken down, but the marker could not be written: %v", err)
			} else {
				note("site taken down")
			}
			m.TakenDown = true
		}
	default:
		if ev.ID == "" {
			note("event NOT taken down: event row missing")
		} else if err := s.takeDownEvent(ctx, ev); err != nil {
			note("event take-down: %v", err)
		} else {
			note("event taken down")
			m.TakenDown = true
		}
	}
	if m.TakenDown && id != 0 {
		if err := db.SetImageMatchTakenDown(ctx, s.database, id); err != nil {
			log.Printf("image scan: taken_down of #%d: %v", id, err)
		}
	}

	// 5. The alert.
	if err := s.sendAlert(m); err != nil {
		note("alert email: %v", err)
	} else if s.cfg.AlertEmail != "" {
		note("alert emailed to %s", s.cfg.AlertEmail)
		if id != 0 {
			if err := db.SetImageMatchAlerted(ctx, s.database, id); err != nil {
				log.Printf("image scan: alerted_at of #%d: %v", id, err)
			}
		}
	} else {
		note("no alert address configured; not emailed")
	}

	// 6. The report.
	if s.cfg.AutoReport && s.reporter != nil && id != 0 {
		if rid, _, err := s.fileReport(ctx, id); err != nil {
			note("report: %v", err)
		} else {
			note("report filed (%s)", rid)
		}
	}
	if evidence != "" {
		s.writeRecord(evidence, m, did)
	}
}

// lockSite serialises the removal with deploys of the same site.
func (s *ImageScan) lockSite(site db.Site) func() {
	if s.sites == nil {
		return func() {}
	}
	return s.sites.lockSite(site.UserID, site.Name)
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
	if s.hack != nil && !s.hack.syncEventMarkers(ctx, ev) {
		return errors.New("team site markers could not all be written")
	}
	return nil
}

// writeRecord keeps record.json beside the evidence copy: the match row's
// fields plus what was done.
func (s *ImageScan) writeRecord(dir string, m db.ImageMatch, did []string) {
	rec := s.matchJSON(m)
	rec["what_happened"] = did
	b, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		log.Printf("image scan: record.json of #%d: %v", m.ID, err)
		return
	}
	if err := os.WriteFile(filepath.Join(dir, "record.json"), b, 0o600); err != nil {
		log.Printf("image scan: record.json of #%d: %v", m.ID, err)
	}
}

// alert emails the alert address. Never the image, never a link to it.
func (s *ImageScan) sendAlert(m db.ImageMatch) error {
	if s.cfg.AlertEmail == "" {
		return nil
	}
	sender, ok := s.mailer.(noticeSender)
	if !ok || s.mailer == nil {
		return errors.New("the mailer cannot send a notice")
	}
	where := matchWhere(m)
	address := s.addressOf(m)
	subject := fmt.Sprintf("Simple Host: image match on %s (#%d)", where, m.ID)
	if hackMode {
		subject = fmt.Sprintf("Simple Hack: image match on %s (#%d)", where, m.ID)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "An uploaded image matched a known abuse hash set.\n\n")
	fmt.Fprintf(&b, "Match: #%d\nKind: %s\nWhere: %s", m.ID, m.Kind, where)
	if address != "" {
		fmt.Fprintf(&b, " (%s)", address)
	}
	fmt.Fprintf(&b, "\nOwner: %s %s\n", m.OwnerHandle, m.OwnerEmail)
	if m.WriterID != "" {
		fmt.Fprintf(&b, "Uploaded by: %s\n", m.WriterID)
	}
	fmt.Fprintf(&b, "Path: %s\nDetected: %s\nService: %s\nTracking id: %s\nSource: %s\nViolations: %s\n\n",
		m.Path, m.DetectedAt.UTC().Format(time.RFC3339), m.Provider, m.TrackingID, m.Source, m.Violations)
	fmt.Fprintf(&b, "What was done: a copy was kept in the evidence folder, the file was removed from every version and the live copy, and the %s was taken down.\n\n", strings.Fields(where)[0])
	fmt.Fprintf(&b, "What to do next: open %s/admin#moderation.\n", strings.TrimRight(s.cfg.PublicBaseURL, "/"))
	if !s.cfg.AutoReport {
		fmt.Fprintf(&b, "File the CyberTipline report (the Report button on the admin page, or https://report.cybertip.org/) as soon as reasonably possible; the law requires it.\n")
	}
	return sender.SendNotice(s.cfg.AlertEmail, subject, b.String())
}

// addressOf is the site's own https address (as the dashboard shows it) or
// the event slug.
func (s *ImageScan) addressOf(m db.ImageMatch) string {
	if m.EventSlug != "" {
		return m.EventSlug
	}
	if m.SiteID == "" {
		return ""
	}
	site, err := db.GetSiteByID(context.Background(), s.database, m.SiteID)
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
	errReportUnavailable  = errors.New("reports cannot be filed: set IMAGE_SCAN_REPORT_ORG and a PhotoDNA key")
)

// fileReport files the CyberTipline report for one match through the
// service and records the outcome. Refused when already filed or when no
// reporter is configured; a service failure leaves 'failed' with the error
// so the admin page can retry.
func (s *ImageScan) fileReport(ctx context.Context, id int64) (string, time.Time, error) {
	m, err := db.GetImageMatch(ctx, s.database, id)
	if err != nil {
		return "", time.Time{}, err
	}
	if m.ReportStatus == "filed" {
		return m.ReportID, m.ReportedAt.Time, errReportAlreadyFiled
	}
	if s.reporter == nil || s.cfg.ReportOrg == "" {
		return "", time.Time{}, errReportUnavailable
	}
	data, ct, err := s.evidenceImage(m)
	if err != nil {
		_ = db.SetImageMatchReport(ctx, s.database, id, "failed", "", err.Error())
		return "", time.Time{}, err
	}
	reportee := strings.TrimSpace(m.OwnerHandle + " " + m.OwnerEmail)
	if m.WriterID != "" && m.Kind == "storage_file" {
		reportee = "visitor " + m.WriterID
	}
	platform := "Simple Host"
	if hackMode {
		platform = "Simple Hack"
	}
	in := imagescan.Report{
		OrgName: s.cfg.ReportOrg, ReporterName: s.cfg.ReportName, ReporterEmail: s.cfg.ReportEmail,
		IncidentTime: m.DetectedAt, ReporteeName: reportee,
		Files: []imagescan.ReportFile{{
			Name: path.Base(m.Path), Data: data, ContentType: ct, UploadedAt: m.UploadedAt,
			Metadata: map[string]string{
				"site": m.SiteName, "address": s.addressOf(m), "path": m.Path, "kind": m.Kind,
				"provider": m.Provider, "tracking_id": m.TrackingID, "source": m.Source, "violations": m.Violations,
				"match_id": strconv.FormatInt(m.ID, 10),
			},
		}},
		Metadata: map[string]string{"platform": platform},
	}
	res, err := s.reporter.Report(ctx, in)
	if err != nil {
		if serr := db.SetImageMatchReport(ctx, s.database, id, "failed", "", err.Error()); serr != nil {
			log.Printf("image scan: report failure of #%d: %v", id, serr)
		}
		return "", time.Time{}, err
	}
	if err := db.SetImageMatchReport(ctx, s.database, id, "filed", res.ReportID, ""); err != nil {
		log.Printf("image scan: report of #%d filed as %s but not recorded: %v", id, res.ReportID, err)
	}
	log.Printf("image scan: report of #%d filed (%s)", id, res.ReportID)
	return res.ReportID, s.now().UTC(), nil
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
// queue, and the newest matches. Matches are listed whether or not the
// feature is on: a record outlives a config change.
func (h *SiteHandler) adminImageScan(w http.ResponseWriter, r *http.Request) {
	s := h.imageScan
	out := map[string]any{
		"enabled": s.Enabled(), "provider": "", "auto_report": false, "report_configured": false, "evidence_dir": "",
		"queue": map[string]int{"pending": 0, "retrying": 0},
	}
	if s != nil {
		out["provider"] = s.matcher.Name()
		out["auto_report"] = s.cfg.AutoReport
		out["report_configured"] = s.reporter != nil && s.cfg.ReportOrg != ""
		out["evidence_dir"] = s.cfg.EvidenceDir
		if c, err := db.ImageScanQueueCountsNow(r.Context(), h.database); err == nil {
			out["queue"] = map[string]int{"pending": c.Pending, "retrying": c.Retrying}
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
		writeJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "image matching is off; set PHOTODNA_API_KEY and IMAGE_SCAN_REPORT_ORG to file reports from here", Code: "report_unavailable"})
		return
	}
	rid, at, err := h.imageScan.fileReport(r.Context(), id)
	switch {
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
