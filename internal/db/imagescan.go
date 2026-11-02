package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// Image matching (SIM-200): the hash cache, the work queue and the record of
// each match. No row here references a site, event or account by foreign
// key, so a record outlives whatever it was found on. Nothing here holds an
// image; the evidence copy lives on disk under the handler's evidence dir.

// ImageScanJob is one queued check.
type ImageScanJob struct {
	ID           int64
	Kind         string // site_file, storage_file, event_icon, entry_screenshot
	SiteID       string
	EventID      string
	TeamID       string
	Version      int
	ResourceName string
	Path         string
	WriterID     string
	SHA256       string
	Bytes        int64
	Attempts     int
	LastError    string
	CreatedAt    time.Time
}

// ImageMatch is one image_scan_matches row.
type ImageMatch struct {
	ID           int64
	Kind         string
	SiteID       string
	SiteName     string
	OwnerID      string
	OwnerHandle  string
	OwnerEmail   string
	EventID      string
	EventSlug    string
	TeamID       string
	Version      int
	ResourceName string
	Path         string
	WriterID     string
	SHA256       string
	Bytes        int64
	ContentType  string
	Provider     string
	TrackingID   string
	Source       string
	Violations   string
	Raw          json.RawMessage
	EvidencePath string
	UploadedAt   time.Time
	DetectedAt   time.Time
	TakenDown    bool
	AlertedAt    sql.NullTime
	ReportStatus string
	ReportID     string
	ReportedAt   sql.NullTime
	ReportError  string
}

// ImageScanQueueCounts is the queue at a glance.
type ImageScanQueueCounts struct {
	Pending  int // due now
	Retrying int // tried at least once, still to be tried again
	GaveUp   int // stopped after the last allowed attempt
}

// Lease length while a worker holds a job, and the retry ceiling.
const (
	imageScanLease      = 15 * time.Minute
	imageScanMaxBackoff = 6 * time.Hour
)

func nullUUID(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func nullVersion(n int) any {
	if n == 0 {
		return nil
	}
	return n
}

// LookupImageHash answers the cached verdict for these bytes.
func LookupImageHash(ctx context.Context, q Querier, sha string) (verdict, trackingID string, found bool, err error) {
	err = q.QueryRowContext(ctx, `SELECT verdict, tracking_id FROM image_scan_hashes WHERE sha256 = $1`, sha).Scan(&verdict, &trackingID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", false, nil
	}
	if err != nil {
		return "", "", false, err
	}
	return verdict, trackingID, true, nil
}

// RecordImageHash stores or replaces the verdict for these bytes.
func RecordImageHash(ctx context.Context, q Querier, sha, verdict, provider, trackingID, detail string) error {
	_, err := q.ExecContext(ctx, `
		INSERT INTO image_scan_hashes (sha256, verdict, provider, tracking_id, detail, checked_at)
		VALUES ($1, $2, $3, $4, $5, now())
		ON CONFLICT (sha256) DO UPDATE SET verdict = EXCLUDED.verdict, provider = EXCLUDED.provider,
			tracking_id = EXCLUDED.tracking_id, detail = EXCLUDED.detail, checked_at = now()`,
		sha, verdict, provider, trackingID, detail)
	return err
}

// EnqueueImageScan adds a job unless the same bytes at the same place and
// version are already waiting. Returns whether a row was inserted.
func EnqueueImageScan(ctx context.Context, q Querier, j ImageScanJob) (bool, error) {
	var exists bool
	err := q.QueryRowContext(ctx, `
		SELECT EXISTS (SELECT 1 FROM image_scan_queue
		 WHERE kind = $1 AND site_id IS NOT DISTINCT FROM $2::uuid AND event_id IS NOT DISTINCT FROM $3::uuid
		   AND team_id IS NOT DISTINCT FROM $4::uuid AND resource_name = $5 AND path = $6 AND sha256 = $7
		   AND version IS NOT DISTINCT FROM $8::integer)`,
		j.Kind, nullUUID(j.SiteID), nullUUID(j.EventID), nullUUID(j.TeamID), j.ResourceName, j.Path, j.SHA256, nullVersion(j.Version)).Scan(&exists)
	if err != nil {
		return false, err
	}
	if exists {
		return false, nil
	}
	_, err = q.ExecContext(ctx, `
		INSERT INTO image_scan_queue (kind, site_id, event_id, team_id, version, resource_name, path, writer_id, sha256, bytes)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		j.Kind, nullUUID(j.SiteID), nullUUID(j.EventID), nullUUID(j.TeamID), nullVersion(j.Version), j.ResourceName, j.Path, j.WriterID, j.SHA256, j.Bytes)
	if err != nil {
		return false, err
	}
	return true, nil
}

const imageScanJobColumns = `id, kind, COALESCE(site_id::text, ''), COALESCE(event_id::text, ''), COALESCE(team_id::text, ''),
	COALESCE(version, 0), resource_name, path, writer_id, sha256, bytes, attempts, last_error, created_at`

func scanImageScanJob(rows *sql.Rows) (ImageScanJob, error) {
	var j ImageScanJob
	err := rows.Scan(&j.ID, &j.Kind, &j.SiteID, &j.EventID, &j.TeamID, &j.Version, &j.ResourceName, &j.Path,
		&j.WriterID, &j.SHA256, &j.Bytes, &j.Attempts, &j.LastError, &j.CreatedAt)
	return j, err
}

// ClaimImageScans takes up to limit due jobs, oldest first, and leases them
// (next_attempt_at moves fifteen minutes out) so another worker skips them.
func ClaimImageScans(ctx context.Context, database *sql.DB, limit int) ([]ImageScanJob, error) {
	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `
		SELECT `+imageScanJobColumns+` FROM image_scan_queue
		 WHERE next_attempt_at <= now()
		 ORDER BY id
		 LIMIT $1
		 FOR UPDATE SKIP LOCKED`, limit)
	if err != nil {
		return nil, err
	}
	var jobs []ImageScanJob
	for rows.Next() {
		j, err := scanImageScanJob(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		jobs = append(jobs, j)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, j := range jobs {
		if _, err := tx.ExecContext(ctx, `UPDATE image_scan_queue SET next_attempt_at = now() + $2 * interval '1 second' WHERE id = $1`,
			j.ID, int(imageScanLease.Seconds())); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return jobs, nil
}

// DeferImageScan records a failed attempt and schedules the next one: one
// minute after the first, doubled per attempt, six hours at most.
func DeferImageScan(ctx context.Context, q Querier, id int64, errText string, attempts int) error {
	wait := time.Minute
	for i := 1; i < attempts && wait < imageScanMaxBackoff; i++ {
		wait *= 2
	}
	if wait > imageScanMaxBackoff {
		wait = imageScanMaxBackoff
	}
	_, err := q.ExecContext(ctx, `
		UPDATE image_scan_queue SET attempts = $2, last_error = $3, next_attempt_at = now() + $4 * interval '1 second'
		 WHERE id = $1`, id, attempts, errText, int(wait.Seconds()))
	return err
}

// GiveUpImageScan records the last failed attempt and parks the job for
// good (next_attempt_at 'infinity'): it stays listed, never claimed again.
func GiveUpImageScan(ctx context.Context, q Querier, id int64, errText string, attempts int) error {
	_, err := q.ExecContext(ctx, `
		UPDATE image_scan_queue SET attempts = $2, last_error = $3, next_attempt_at = 'infinity'
		 WHERE id = $1`, id, attempts, errText)
	return err
}

// DeleteImageScan removes a finished job.
func DeleteImageScan(ctx context.Context, q Querier, id int64) error {
	_, err := q.ExecContext(ctx, `DELETE FROM image_scan_queue WHERE id = $1`, id)
	return err
}

// ImageScanQueueCountsNow reports what is due, what is on a retry, and what
// was given up.
func ImageScanQueueCountsNow(ctx context.Context, q Querier) (ImageScanQueueCounts, error) {
	var c ImageScanQueueCounts
	err := q.QueryRowContext(ctx, `
		SELECT count(*) FILTER (WHERE next_attempt_at <= now()),
		       count(*) FILTER (WHERE attempts > 0 AND next_attempt_at <> 'infinity'),
		       count(*) FILTER (WHERE next_attempt_at = 'infinity')
		  FROM image_scan_queue`).Scan(&c.Pending, &c.Retrying, &c.GaveUp)
	return c, err
}

// InsertImageMatch records a match and returns its id.
func InsertImageMatch(ctx context.Context, q Querier, m ImageMatch) (int64, error) {
	var raw any
	if len(m.Raw) > 0 && json.Valid(m.Raw) {
		raw = string(m.Raw)
	}
	var uploaded any
	if !m.UploadedAt.IsZero() {
		uploaded = m.UploadedAt
	}
	status := m.ReportStatus
	if status == "" {
		status = "pending"
	}
	var id int64
	err := q.QueryRowContext(ctx, `
		INSERT INTO image_scan_matches (kind, site_id, site_name, owner_id, owner_handle, owner_email,
			event_id, event_slug, team_id, version, resource_name, path, writer_id,
			sha256, bytes, content_type, provider, tracking_id, source, violations, raw, uploaded_at, report_status)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22, $23)
		RETURNING id`,
		m.Kind, nullUUID(m.SiteID), m.SiteName, nullUUID(m.OwnerID), m.OwnerHandle, m.OwnerEmail,
		nullUUID(m.EventID), m.EventSlug, nullUUID(m.TeamID), nullVersion(m.Version), m.ResourceName, m.Path, m.WriterID,
		m.SHA256, m.Bytes, m.ContentType, m.Provider, m.TrackingID, m.Source, m.Violations, raw, uploaded, status).Scan(&id)
	return id, err
}

const imageMatchColumns = `id, kind, COALESCE(site_id::text, ''), site_name, COALESCE(owner_id::text, ''), owner_handle, owner_email,
	COALESCE(event_id::text, ''), event_slug, COALESCE(team_id::text, ''), COALESCE(version, 0), resource_name, path, writer_id,
	sha256, bytes, content_type, provider, tracking_id, source, violations, COALESCE(raw::text, ''), evidence_path,
	COALESCE(uploaded_at, detected_at), detected_at, taken_down, alerted_at, report_status, report_id, reported_at, report_error`

type imageMatchScanner interface {
	Scan(dest ...any) error
}

func scanImageMatch(row imageMatchScanner) (ImageMatch, error) {
	var m ImageMatch
	var raw string
	err := row.Scan(&m.ID, &m.Kind, &m.SiteID, &m.SiteName, &m.OwnerID, &m.OwnerHandle, &m.OwnerEmail,
		&m.EventID, &m.EventSlug, &m.TeamID, &m.Version, &m.ResourceName, &m.Path, &m.WriterID,
		&m.SHA256, &m.Bytes, &m.ContentType, &m.Provider, &m.TrackingID, &m.Source, &m.Violations, &raw, &m.EvidencePath,
		&m.UploadedAt, &m.DetectedAt, &m.TakenDown, &m.AlertedAt, &m.ReportStatus, &m.ReportID, &m.ReportedAt, &m.ReportError)
	if err != nil {
		return ImageMatch{}, err
	}
	if raw != "" {
		m.Raw = json.RawMessage(raw)
	}
	return m, nil
}

// ListImageMatches is every match whose report is still open (pending,
// failed, or off) plus the newest limit matches, newest first.
func ListImageMatches(ctx context.Context, q Querier, limit int) ([]ImageMatch, error) {
	rows, err := queryContext(ctx, q, `SELECT `+imageMatchColumns+` FROM image_scan_matches
		 WHERE report_status IN ('pending', 'failed', 'off')
		    OR id IN (SELECT id FROM image_scan_matches ORDER BY detected_at DESC, id DESC LIMIT $1)
		 ORDER BY detected_at DESC, id DESC`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ImageMatch{}
	for rows.Next() {
		m, err := scanImageMatch(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// GetImageMatch loads one match. sql.ErrNoRows for an unknown id.
func GetImageMatch(ctx context.Context, q Querier, id int64) (ImageMatch, error) {
	return scanImageMatch(q.QueryRowContext(ctx, `SELECT `+imageMatchColumns+` FROM image_scan_matches WHERE id = $1`, id))
}

// SetImageMatchEvidence records where the quarantined copy went.
func SetImageMatchEvidence(ctx context.Context, q Querier, id int64, path string) error {
	_, err := q.ExecContext(ctx, `UPDATE image_scan_matches SET evidence_path = $2 WHERE id = $1`, id, path)
	return err
}

// SetImageMatchTakenDown marks the site or event as taken down for this match.
func SetImageMatchTakenDown(ctx context.Context, q Querier, id int64) error {
	_, err := q.ExecContext(ctx, `UPDATE image_scan_matches SET taken_down = true WHERE id = $1`, id)
	return err
}

// SetImageMatchAlerted stamps the alert email as sent.
func SetImageMatchAlerted(ctx context.Context, q Querier, id int64) error {
	_, err := q.ExecContext(ctx, `UPDATE image_scan_matches SET alerted_at = now() WHERE id = $1`, id)
	return err
}

// SetImageMatchReport records the outcome of filing the report. 'filed'
// stamps reported_at; any status keeps the error text given.
func SetImageMatchReport(ctx context.Context, q Querier, id int64, status, reportID, errText string) error {
	switch status {
	case "pending", "filed", "failed", "off":
	default:
		return fmt.Errorf("db: unknown report status %q", status)
	}
	_, err := q.ExecContext(ctx, `
		UPDATE image_scan_matches SET report_status = $2, report_id = $3, report_error = $4,
			reported_at = CASE WHEN $2 = 'filed' THEN now() ELSE reported_at END
		 WHERE id = $1`, id, status, reportID, errText)
	return err
}

// ClearEventIcon removes an event's icon (the image bytes and their type),
// only while the stored bytes still hash to sha. Reports whether it did.
func ClearEventIcon(ctx context.Context, q Querier, eventID, sha string) (bool, error) {
	res, err := q.ExecContext(ctx, `UPDATE events SET icon_media_type = '', icon_bytes = NULL, updated_at = now()
		 WHERE id = $1 AND icon_bytes IS NOT NULL AND encode(sha256(icon_bytes), 'hex') = $2`, eventID, sha)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// ClearEventScreenshot removes a team's entry screenshot, only while the
// stored bytes still hash to sha, and leaves the entry text and its last
// writer as they are. Reports whether it did.
func ClearEventScreenshot(ctx context.Context, q Querier, teamID, sha string) (bool, error) {
	res, err := q.ExecContext(ctx, `UPDATE event_entries SET screenshot = NULL, screenshot_type = ''
		 WHERE team_id = $1 AND screenshot IS NOT NULL AND encode(sha256(screenshot), 'hex') = $2`, teamID, sha)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// DeleteStorageFileRow drops a files-storage object's row after its bytes
// are removed from disk.
func DeleteStorageFileRow(ctx context.Context, q Querier, siteID, resourceName, path string) error {
	_, err := q.ExecContext(ctx, `DELETE FROM site_storage_files WHERE site_id = $1 AND resource_name = $2 AND path = $3`, siteID, resourceName, path)
	return err
}
