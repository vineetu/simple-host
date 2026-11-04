package model

import (
	"database/sql"
	"encoding/json"
	"time"
)

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
	// Uploader and Address are who uploaded the file and where it was
	// served, as they stood when the match was found.
	Uploader string
	Address  string
}

// ImageScanQueueCounts is the queue at a glance.
type ImageScanQueueCounts struct {
	Pending  int // due now
	Retrying int // tried at least once, still to be tried again
	GaveUp   int // stopped after the last allowed attempt
	// SiteDeleted is waiting because its site is in Recently deleted: it
	// runs after a restore and is dropped at the purge.
	SiteDeleted int
}
