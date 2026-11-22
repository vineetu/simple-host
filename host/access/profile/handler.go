package profile

import (
	"database/sql"
	"net/http"
	"strings"
	"sync"

	accounts "github.com/vsriram/simple-host/host/access"
	"github.com/vsriram/simple-host/host/access/auth"
	"github.com/vsriram/simple-host/host/edition"
	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/sites"
	"github.com/vsriram/simple-host/host/web"
)

// Handler serves the person's account: GET /v1/me, the address check, the
// sign-in email change with its undo, and the linked sign-ins.
type Handler struct {
	database      *sql.DB
	accounts      *accounts.Service
	ed            *edition.Edition
	Limits        edition.Limits
	mailer        accounts.CodeSender
	publicBaseURL string

	IPLimiter, EmailLimiter *httpx.RateLimiter
	// checkLimiter caps address checks per client IP (GET /v1/handles/check).
	checkLimiter *httpx.RateLimiter
	// Alerts sends the email-change notices in the background.
	Alerts *auth.SignInAlerts
	// PasswordEnabled: PASSWORD_SIGNIN is on, so GET /v1/me says whether the
	// account has a password and its login.
	PasswordEnabled bool

	// reviewerEmail is the plugin reviewer account (REVIEW_ACCOUNT_EMAIL) and
	// previewAccounts PREVIEW_ACCOUNTS (lowercased): their email cannot be
	// changed.
	reviewerEmail   string
	previewAccounts map[string]bool

	// Hosts, Disk and FileLimits are the account's sites as GET /v1/me
	// reports them (SetSites); nil Hosts: none yet.
	Hosts      *sites.Hosts
	Disk       sites.Disk
	FileLimits edition.Limits

	// assets renders the message pages (email.go); read it through pipeline().
	assets     *web.AssetPipeline
	assetsOnce sync.Once
}

// New builds the profile routes' handler.
func New(database *sql.DB, service *accounts.Service, mailer accounts.CodeSender, publicBaseURL string, ed *edition.Edition, ipLimiter, emailLimiter, checkLimiter *httpx.RateLimiter, alerts *auth.SignInAlerts) *Handler {
	return &Handler{database: database, accounts: service, ed: ed, Limits: ed.Limits(), mailer: mailer, publicBaseURL: strings.TrimRight(publicBaseURL, "/"),
		IPLimiter: ipLimiter, EmailLimiter: emailLimiter, checkLimiter: checkLimiter, Alerts: alerts}
}

// Register mounts the profile routes.
func (h *Handler) Register(mux *http.ServeMux, authMiddleware, noticeMiddleware func(http.Handler) http.Handler) {
	mux.Handle("GET /v1/handles/check", httpx.RateLimitByIP(h.checkLimiter, auth.Optional(authMiddleware)(http.HandlerFunc(h.handleCheck))))
	mux.Handle("GET /v1/me", noticeMiddleware(authMiddleware(http.HandlerFunc(h.me))))
	mux.Handle("POST /v1/me/email", noticeMiddleware(authMiddleware(http.HandlerFunc(h.requestEmailChange))))
	mux.Handle("POST /v1/me/email/verify", noticeMiddleware(authMiddleware(http.HandlerFunc(h.verifyEmailChange))))
	mux.Handle("GET /v1/me/email/undo", httpx.RateLimitByIP(h.IPLimiter, http.HandlerFunc(h.undoEmailChange)))
	mux.Handle("POST /v1/me/email/undo", httpx.RateLimitByIP(h.IPLimiter, http.HandlerFunc(h.undoEmailChange)))
	mux.Handle("GET /v1/me/identities", noticeMiddleware(authMiddleware(http.HandlerFunc(h.listIdentities))))
	mux.Handle("DELETE /v1/me/identities/{id}", noticeMiddleware(authMiddleware(http.HandlerFunc(h.unlinkIdentity))))
}

// SetReviewerEmail names the plugin reviewer account.
func (h *Handler) SetReviewerEmail(address string) { h.reviewerEmail = strings.TrimSpace(address) }

// SetSites gives GET /v1/me the account's sites: their hosts, disk and limits.
func (h *Handler) SetSites(hosts *sites.Hosts, disk sites.Disk, limits edition.Limits) {
	h.Hosts, h.Disk, h.FileLimits = hosts, disk, limits
}

// ShareAssets makes h render through the site handler's asset pipeline.
func (h *Handler) ShareAssets(p *web.AssetPipeline) { h.assets = p }

// pipeline is h's asset pipeline; a bare handler (tests) gets one for its
// edition on first use.
func (h *Handler) pipeline() *web.AssetPipeline {
	h.assetsOnce.Do(func() {
		if h.assets == nil {
			h.assets = web.NewAssetPipeline(h.ed, h.publicBaseURL)
		}
	})
	return h.assets
}
