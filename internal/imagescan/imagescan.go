// Package imagescan checks uploaded images against known child sexual abuse
// material (CSAM) hash sets through a matching service, and files the report
// US law requires (18 U.S.C. 2258A) through the same service. It is a leaf
// package: no other internal package is imported (scripts/check-layering.sh).
//
// The handler package owns the queue, the on-disk evidence copy, the
// take-down and the alert; this package only prepares an image, talks to the
// service and parses what it says. Nothing here ever sees a visitor's IP
// address, and nothing here keeps a copy of an image.
package imagescan

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"
)

// Verdict is the outcome of one check.
type Verdict string

const (
	// VerdictClean: the service found no match.
	VerdictClean Verdict = "clean"
	// VerdictMatch: the service matched a known hash. Act on it.
	VerdictMatch Verdict = "match"
	// VerdictSkipped: not something the service can check (not an image we
	// decode, too small, vector art). Never retried for the same bytes.
	VerdictSkipped Verdict = "skipped"
)

// Image is what Prepare hands to a Matcher: bytes in a format the service
// accepts, and the SHA-256 of the ORIGINAL upload (the dedupe key).
type Image struct {
	Data        []byte
	ContentType string // image/jpeg, image/png, image/gif, image/bmp or image/tiff
	Width       int
	Height      int
	SHA256      string // hex, of the original bytes as uploaded
}

// PrepareOptions bounds Prepare. Zero values take the defaults.
type PrepareOptions struct {
	// MaxBytes is the largest payload the service takes (default 4 MB). A
	// larger image is decoded, scaled down and re-encoded.
	MaxBytes int64
	// MinSide is the smallest width or height worth checking (default 50
	// px); smaller images are skipped.
	MinSide int
	// MaxSide is the longest side after a downscale (default 1600 px).
	MaxSide int
}

// Prepare lives in prepare.go.

// Match is what the service answered for one image.
type Match struct {
	IsMatch    bool
	TrackingID string   // the service's id for this check, for the record
	Source     string   // the hash set that matched, e.g. "NCMEC", "IWF" (empty when clean)
	Violations []string // the service's violation codes (empty when clean)
	// Raw is the service's own response body, kept with a match as evidence.
	Raw json.RawMessage
}

// Matcher checks one prepared image.
type Matcher interface {
	// Name identifies the service in records ("photodna").
	Name() string
	Match(ctx context.Context, img *Image) (Match, error)
}

// ReportFile is one matched image in a report.
type ReportFile struct {
	Name        string
	Data        []byte
	ContentType string
	UploadedAt  time.Time
	// Metadata is extra key/value detail for the report (site address, path,
	// writer reference). Never an IP address.
	Metadata map[string]string
}

// Report is a CyberTipline report filed through the service.
type Report struct {
	OrgName       string
	ReporterName  string
	ReporterEmail string
	IncidentTime  time.Time
	// ReporteeName identifies the uploading account to the service (an
	// account id or email the operator holds). Never an IP address.
	ReporteeName string
	Files        []ReportFile
	Metadata     map[string]string
}

// ReportResult is the service's acknowledgement.
type ReportResult struct {
	ReportID string
	Raw      json.RawMessage
}

// Reporter files a report through the service.
type Reporter interface {
	Report(ctx context.Context, in Report) (ReportResult, error)
}

// ErrRetryable marks a failure worth trying again later (network, timeout,
// 429, 5xx). Test with IsRetryable; anything else is permanent for these bytes.
var ErrRetryable = errors.New("imagescan: retryable")

// IsRetryable reports whether err wraps ErrRetryable.
func IsRetryable(err error) bool { return errors.Is(err, ErrRetryable) }

// ErrUnsupported marks bytes the service refused as not checkable (bad
// format, too small). The caller records VerdictSkipped.
var ErrUnsupported = errors.New("imagescan: unsupported image")

// PhotoDNA is Microsoft's PhotoDNA Cloud Service client. Zero fields take
// the defaults from NewPhotoDNA.
type PhotoDNA struct {
	Key       string // Ocp-Apim-Subscription-Key
	MatchURL  string // default DefaultMatchURL
	ReportURL string // default DefaultReportURL
	Client    *http.Client
	// Enhance asks the Match operation for its enhanced matching
	// (?enhance=true). Off by default.
	Enhance bool
}

const (
	// DefaultMatchURL and DefaultReportURL are the v1.0 operations. The
	// operator can point both elsewhere (PHOTODNA_MATCH_URL, PHOTODNA_REPORT_URL).
	DefaultMatchURL  = "https://api.microsoftmoderator.com/photodna/v1.0/Match"
	DefaultReportURL = "https://api.microsoftmoderator.com/photodna/v1.0/Report"
	// StatusOK is the service's own success code inside the JSON body.
	StatusOK = 3000
)

// NewPhotoDNA returns a client with the defaults and a 30 s timeout.
func NewPhotoDNA(key string) *PhotoDNA {
	return &PhotoDNA{Key: key, MatchURL: DefaultMatchURL, ReportURL: DefaultReportURL, Client: &http.Client{Timeout: 30 * time.Second}}
}

// Name implements Matcher.
func (p *PhotoDNA) Name() string { return "photodna" }

// Match and Report live in photodna.go.
