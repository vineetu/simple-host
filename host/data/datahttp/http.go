package datahttp

import (
	"database/sql"
	"strings"
	"sync"

	accounts "github.com/vsriram/simple-host/host/access"
	appstorage "github.com/vsriram/simple-host/host/data"
	"github.com/vsriram/simple-host/host/data/notify"
	"github.com/vsriram/simple-host/host/edition"
	"github.com/vsriram/simple-host/host/email"
	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/sites"
	"github.com/vsriram/simple-host/host/sites/idempotency"
	"github.com/vsriram/simple-host/host/storage"
)

// DataHandler serves a site's saved data: storage, notifications, web push
// and the spam check. Access resolves the site and the caller in front of its
// routes; passcode, uploadLocks and httpCaches are shared with the site handler.
type DataHandler struct {
	Ed                *edition.Edition
	Limits            edition.Limits
	Database          *sql.DB
	Disk              *storage.DiskStorage
	Mailer            email.Sender
	ExportKey         []byte
	publicBaseURL     string
	Hosts             *sites.Hosts
	CreateIdempotency *idempotency.Store
	HTTPCaches        *httpx.Caches
	PasscodeKeys      *accounts.PasscodeKeys
	UploadLocks       *sync.Map

	// sqlite and sqliteSites are the SQLite concurrency gate and the per-site
	// admission slots, sized from h.limits by NewSiteHandlers.
	Sqlite                      *sqliteGate
	SqliteSites                 *appstorage.SiteSlots
	storageBindings             appstorage.BindingProvider
	storageBindingSchema        appstorage.BindingSchema
	storageProductBindingSchema appstorage.BindingSchema

	notifyOnce    sync.Once
	Notifications *notify.Engine

	StorageIPLimiter      *httpx.RateLimiter
	StorageVisitorLimiter *httpx.RateLimiter
	// The spam check behind auth.checked (turnstile.go); nil: off.
	turnstile       *turnstileVerifier
	SpamIPLimiter   *httpx.RateLimiter
	SpamSiteLimiter *httpx.RateLimiter
}

// SetPublicBaseURL is the apex address photo and file links are minted on
// (PUBLIC_BASE_URL, e.g. https://simple-host.app).
func (h *DataHandler) SetPublicBaseURL(base string) {
	h.publicBaseURL = strings.TrimRight(strings.TrimSpace(base), "/")
}
