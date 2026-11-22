package sitesroute

import (
	"crypto/rand"
	"database/sql"
	"net/http"
	"sync"
	"time"

	appstorage "github.com/vsriram/simple-host/host/data"
	operations "github.com/vsriram/simple-host/host/ops"

	"github.com/vsriram/simple-host/host/data/notify"
	"github.com/vsriram/simple-host/host/edition"
	"github.com/vsriram/simple-host/host/email"
	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/model"
	"github.com/vsriram/simple-host/host/sites"
	"github.com/vsriram/simple-host/host/sites/idempotency"
	"github.com/vsriram/simple-host/host/web"
	"github.com/vsriram/simple-host/host/workers"
)

// Handler is what every Sites route shares: the per-request site services
// (SitesSvc, LifecycleSvc, OpsSitesSvc), the instance's locks, limiters,
// caches and collaborators, and the start-up setters. Feature packages
// embed it in their routes; siteshttp.SiteHandler embeds it too.
type Handler struct {
	// notify queues the owner's site events; sqliteSites admits SQLite
	// work per site (both shared with the data routes).
	Notify        *notify.Engine
	SqliteSites   *appstorage.SiteSlots
	sitemapFilter sites.SitemapFilter
	Caches        httpx.Caches
	WorkerStatus  func() []workers.Status
	Ed            *edition.Edition
	// limits is ed.Limits(), held once so a request does not copy the
	// whole snapshot on every read.
	Limits            edition.Limits
	Assets            *web.AssetPipeline
	assetsOnce        sync.Once
	CreateIdempotency *idempotency.Store
	Mailer            email.Sender
	Database          *sql.DB
	Disk              sites.Disk
	SiteDomain        string
	ContentHost       string // shared v3 content host, e.g. sites.simple-host.app
	CNAMETarget       string // CNAME target for custom domains, e.g. cname.simple-host.app
	CustomDomainIP    string // box public IPv4 for APEX custom-domain A records
	DeployScript      string

	// uploadLocks serializes write+promote per site name (sitename -> *sync.Mutex).
	UploadLocks sync.Map

	// usage caches the disk measurement behind /v1/admin/usage. Measuring walks
	// every file under the data directory, so it is not done per request.
	Usage     operations.UsageCache
	Reporting *operations.Reporting

	Network   *operations.NetworkUsage
	TrafficOn bool
	// ownerMarks files the owner's own visits apart from People (ownermarks.go); nil when the analytics log is off.
	ownerMarks *operations.OwnerMarks

	UploadLimiter          *httpx.RateLimiter
	DomainCheckLimiter     *httpx.RateLimiter
	DomainCheckUserLimiter *httpx.RateLimiter

	// previewAccounts (by username/email) get ephemeral sites: a site they create
	// expires after previewTTL and is removed by the background sweep. Empty =off.
	PreviewAccounts map[string]bool
	PreviewTTL      time.Duration

	Hosts                  *sites.Hosts
	Gate                   http.Handler
	noVisitorSignIn        bool
	FamilyCheckLimiter     *httpx.RateLimiter
	FamilyCheckUserLimiter *httpx.RateLimiter
	IdleCleanup            bool
	IdleExempt             model.IdleExempt

	// exportKey signs short-lived export download links (exportlink.go); per
	// process, used for nothing else. publicBaseURL is the apex they point at.
	ExportKey []byte

	PublicBaseURL string

	ShowcaseLimiter *httpx.RateLimiter
	groupGate       sites.GroupGate
}

// lockSite acquires the per-site upload mutex, keyed by account and name so two people's same-named sites never wait.
func (h *Handler) LockSite(userID, name string) func() {
	return sites.LockSiteName(&h.UploadLocks, userID, name)
}

func (h *Handler) SiteReporting() *operations.Reporting {
	return operations.NewReporting(h.Database)
}

func NewExportKey() []byte {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		panic("export links: no randomness: " + err.Error())
	}
	return key
}
