package accesshttp

import (
	"database/sql"
	"net/http"
	"sync"

	appstorage "github.com/vsriram/simple-host/host/data"
	"github.com/vsriram/simple-host/host/email"
	"github.com/vsriram/simple-host/host/sites/idempotency"
	"github.com/vsriram/simple-host/host/web"

	"github.com/vsriram/simple-host/host/data/notify"
	"github.com/vsriram/simple-host/host/edition"
	"github.com/vsriram/simple-host/host/httpx"
	operations "github.com/vsriram/simple-host/host/ops"
	"github.com/vsriram/simple-host/host/sites"
)

// AccessHandler serves who may use a site: accounts, passcodes, named
// viewers, visitor sign-in and sessions, suspension. hosts answers which
// site a host or path names.
type AccessHandler struct {
	Hosts *sites.Hosts
	// siteFiles is the sites service the account routes export, erase and
	// rename with (NewSiteHandlers builds it).
	SiteFiles          *sites.Service
	Ed                 *edition.Edition
	Limits             edition.Limits
	Database           *sql.DB
	Disk               sites.Disk
	Mailer             email.Sender
	EmailLimiter       *httpx.RateLimiter
	VisitorAuthLimiter *httpx.RateLimiter
	SiteDomain         string
	ContentHost        string
	ExportKey          []byte
	UploadLocks        *sync.Map
	// adminAPIKey / adminUserID recognize owner/admin X-API-Key callers on
	// routes that are not wrapped with auth.Middleware (the site storage
	// routes).
	AdminAPIKey string
	AdminUserID string

	// passcode is the site-passcode key and wrong-try counters (passcode.go).
	Passcode   passcodeState
	ssoStart   http.Handler
	ssoBaseURL string
	// noVisitorSignIn: this install has no way for a visitor to sign in (no
	// email codes, no Google/GitHub). noEmail: it sends no email. Both false
	// unless SetVisitorSignIn says. viewerGoogle: Google sign-in is set up,
	// so the sign-in page offers it next to the emailed code.
	noVisitorSignIn, noEmail, ViewerGoogle bool
	// geoBlock gates a visitor's email-code sign-in on a site by the
	// request's country (SIGNUP_BLOCKED_COUNTRIES). Zero value: off. See
	// signupgeo.go.
	geoBlock signupGeoBlock

	Pages             *web.AssetPipeline
	pagesOnce         sync.Once
	Notify            *notify.Engine
	HTTPCaches        *httpx.Caches
	CreateIdempotency *idempotency.Store
	Reporting         *operations.Reporting
	// storageBindings is the product's storage bindings (SetStorageBindings):
	// who counts as the site's owner on its own address.
	storageBindings appstorage.BindingProvider
	// ownerMarks notes the owner's own visitor sign-ins (SetOwnerMarks).
	ownerMarks *operations.OwnerMarks
}

// SetStorageBindings gives Access the product's storage bindings, once at
// startup (handler.SetStorageBindings): who counts as the site's owner on its
// own address. nil keeps the platform's own.
func (h *AccessHandler) SetStorageBindings(p appstorage.BindingProvider) { h.storageBindings = p }

// SetOwnerMarks turns marking the owner's own visitor sign-ins on.
func (h *AccessHandler) SetOwnerMarks(m *operations.OwnerMarks) { h.ownerMarks = m }

func (h *AccessHandler) storageBindingProvider() appstorage.BindingProvider {
	if h.storageBindings != nil {
		return h.storageBindings
	}
	return appstorage.DefaultProvider{DB: h.Database}
}

// emitSiteEvent queues a site event for the owner.
func (h *AccessHandler) emitSiteEvent(ev sites.SiteEvent) {
	if h.Notify != nil {
		h.Notify.EmitSiteEvent(h.Hosts, ev)
	}
}

// pipeline is the shared asset pipeline; a bare &AccessHandler{} (tests)
// gets one for its edition on first use.
func (h *AccessHandler) pipeline() *web.AssetPipeline {
	h.pagesOnce.Do(func() {
		if h.Pages == nil {
			h.Pages = web.NewAssetPipeline(h.Ed, "")
		}
	})
	return h.Pages
}

// sitesSvc is the sites workflows Access drives: passcode and viewer rows
// with their folder markers, suspension, the account's sites and files.
func (h *AccessHandler) sitesSvc() *sites.Service {
	deps := sites.Deps{
		SiteViewersMax: h.Limits.SiteViewersMax, ContentHost: h.ContentHost, SiteDomain: h.SiteDomain,
		LockSite: func(userID, name string) func() { return sites.LockSiteName(h.UploadLocks, userID, name) },
	}
	if h.Ed != nil {
		deps.PersonalSitesEnabled = h.Ed.Identity().PersonalSites
	}
	if h.Notify != nil {
		deps.DeleteViewerPush = h.Notify.DeleteViewerPush
	}
	return sites.New(h.Database, h.Disk, nil, 0, deps)
}
