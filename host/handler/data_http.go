package handler

import (
	"database/sql"
	"sync"

	appstorage "github.com/vsriram/simple-host/host/data"
	"github.com/vsriram/simple-host/host/edition"
	"github.com/vsriram/simple-host/host/httpx"
)

// DataHandler serves a site's saved data: storage, notifications, web push
// and the spam check. site resolves sites for it, access visitor sessions;
// passcode, uploadLocks and httpCaches are the site handler's own.
type DataHandler struct {
	site   *SiteHandler
	access *AccessHandler
	ed     *edition.Edition
	limitSet
	database          *sql.DB
	disk              SiteDisk
	mailer            signInMailer
	siteDomain        string
	contentHost       string
	exportKey         []byte
	createIdempotency *createIdempotencyStore
	httpCaches        *httpx.Caches
	passcode          *passcodeState
	uploadLocks       *sync.Map

	// sqlite is this handler's SQLite concurrency gate (site_storage_sql.go),
	// sized from h.limits. NewSiteHandlers builds it; a bare &DataHandler{}
	// (tests) gets one on first use via sqliteGateFor.
	sqlite                      *sqliteGate
	sqliteOnce                  sync.Once
	sqliteSites                 *appstorage.SiteSlots
	sqliteSitesOnce             sync.Once
	storageBindings             appstorage.BindingProvider
	storageBindingSchema        appstorage.BindingSchema
	storageProductBindingSchema appstorage.BindingSchema

	notifyOnce    sync.Once
	notifications *notificationEngine

	storageIPLimiter      *httpx.RateLimiter
	storageVisitorLimiter *httpx.RateLimiter
	// The spam check behind auth.checked (turnstile.go); nil: off.
	turnstile       *turnstileVerifier
	spamIPLimiter   *httpx.RateLimiter
	spamSiteLimiter *httpx.RateLimiter

	// webPushAccounts/webPushAccountsAll gate SIM-360 (WEB_PUSH_ACCOUNTS):
	// empty map and false = web push off everywhere. Set once at startup
	// via SetWebPushAccounts.
	webPushAccounts    map[string]bool
	webPushAccountsAll bool

	// webPushVapid* cache the one VAPID keypair for this install, generated
	// on first use and persisted sealed in instance_config (see
	// notify_push.go vapidKeys). Only a success is cached: a transient
	// failure (a canceled request's context, a momentary DB error) must
	// not poison this process forever the way sync.Once would.
	webPushVapidMu    sync.Mutex
	webPushVapidReady bool
	webPushVapidPub   string
	webPushVapidPriv  string
}
