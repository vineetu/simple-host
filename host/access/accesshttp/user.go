package accesshttp

import (
	"database/sql"
	"net/http"
	"strings"
	"time"

	"github.com/vsriram/simple-host/host/access/gate"

	accounts "github.com/vsriram/simple-host/host/access"
	"github.com/vsriram/simple-host/host/access/auth"
	"github.com/vsriram/simple-host/host/access/keys"
	"github.com/vsriram/simple-host/host/access/profile"
	"github.com/vsriram/simple-host/host/access/signin"
	"github.com/vsriram/simple-host/host/access/teams"
	"github.com/vsriram/simple-host/host/edition"
	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/ops"
	"github.com/vsriram/simple-host/host/web"
	"github.com/vsriram/simple-host/host/workers"
)

// UserHandler is the person's own routes on the app's address, made of
// their features: sign-in (host/access/signin), the account's profile and
// email (profile), API keys and sign-out (keys) and teams (teams). Its
// setters reach the features that use what they set.
type UserHandler struct {
	httpCaches    httpx.Caches
	ed            *edition.Edition
	Limits        edition.Limits
	database      *sql.DB
	publicBaseURL string

	// IPLimiter caps requests per client IP across the auth routes;
	// EmailLimiter caps challenges/verifies aimed at a single address
	// (mail-bomb and code-grinding defense), shared with the site gate.
	IPLimiter    *httpx.RateLimiter
	EmailLimiter *httpx.RateLimiter

	// Alerts emails the owner after each sign-in (auth.SignInAlerts).
	Alerts *auth.SignInAlerts

	signin  *signin.Handler
	profile *profile.Handler
	teams   *teams.Handler
	keys    *keys.Handler
}

func NewUserHandler(database *sql.DB, service *accounts.Service, mailer accounts.CodeSender, publicBaseURL string, editions ...*edition.Edition) *UserHandler {
	var ed *edition.Edition
	if len(editions) > 0 {
		ed = editions[0]
	}
	lim := ed.Limits()
	// ~12 req/min/IP (burst 20) across both auth routes; ~1.2/min/email
	// (burst 5). Generous for a human signing in, tight against automation.
	ipLimiter := httpx.NewRateLimiterFor(lim.RateSigninIP.Burst, lim.RateSigninIP.Every)
	emailLimiter := httpx.NewRateLimiterFor(lim.RateSigninEmail.Burst, lim.RateSigninEmail.Every)
	checkLimiter := httpx.NewRateLimiterFor(lim.RateHandleCheck.Burst, lim.RateHandleCheck.Every)
	alerts := auth.NewSignInAlerts(service, mailer, publicBaseURL)
	h := &UserHandler{
		ed:            ed,
		Limits:        lim,
		database:      database,
		publicBaseURL: strings.TrimRight(publicBaseURL, "/"),
		IPLimiter:     ipLimiter,
		EmailLimiter:  emailLimiter,
		Alerts:        alerts,
		teams:         &teams.Handler{DB: database, Cutoff: ops.Cutoff{DB: database}},
		keys:          &keys.Handler{DB: database, Limits: lim},
	}
	h.signin = signin.New(database, service, mailer, publicBaseURL, ed, ipLimiter, emailLimiter, alerts, &h.httpCaches)
	h.profile = profile.New(database, service, mailer, publicBaseURL, ed, ipLimiter, emailLimiter, checkLimiter, alerts)
	h.httpCaches.Rate("http.user.ipLimiter", ipLimiter, 10*time.Minute, 30*time.Minute)
	h.httpCaches.Rate("http.user.emailLimiter", emailLimiter, 10*time.Minute, 30*time.Minute)
	h.httpCaches.Rate("http.user.checkLimiter", checkLimiter, 10*time.Minute, 30*time.Minute)
	return h
}

func (h *UserHandler) Register(mux *http.ServeMux, authMiddleware, noticeMiddleware func(http.Handler) http.Handler) {
	h.signin.Register(mux, authMiddleware, noticeMiddleware)
	if h.ed.Identity().PersonalSites {
		h.teams.Register(mux, authMiddleware, noticeMiddleware)
	}
	h.profile.Register(mux, authMiddleware, noticeMiddleware)
	h.keys.Register(mux, authMiddleware, noticeMiddleware)
	gate.RegisterSSOConsent(mux, h.database, h.publicBaseURL, authMiddleware)
}

// SetLimits replaces the limits h and its features answer with.
func (h *UserHandler) SetLimits(l edition.Limits) {
	h.Limits, h.keys.Limits, h.signin.Limits, h.profile.Limits = l, l, l, l
}

// SetSignupGeoBlock wires SIGNUP_BLOCKED_COUNTRIES for dashboard email-code sign-ins.
func (h *UserHandler) SetSignupGeoBlock(guard accounts.SignupGeoBlock) {
	h.signin.SetSignupGeoBlock(guard)
}

// SetPasswordSignIn turns PASSWORD_SIGNIN (and its self sign-up) on.
func (h *UserHandler) SetPasswordSignIn(enabled, selfSignup bool) {
	h.signin.SetPasswordSignIn(enabled, selfSignup)
	h.profile.PasswordEnabled = enabled
}

// SetPreviewAccounts names PREVIEW_ACCOUNTS, whose email cannot be changed.
func (h *UserHandler) SetPreviewAccounts(accounts map[string]bool) {
	h.profile.SetPreviewAccounts(accounts)
}

// SignInAlerts is the sign-in alert sender, shared with the connector's
// consent screen.
func (h *UserHandler) SignInAlerts() *auth.SignInAlerts { return h.Alerts }

// SetReviewerEmail names the plugin reviewer account (REVIEW_ACCOUNT_EMAIL):
// its email cannot be changed and it gets no sign-in alerts.
func (h *UserHandler) SetReviewerEmail(address string) {
	h.profile.SetReviewerEmail(address)
	h.Alerts.ReviewerEmail = strings.TrimSpace(address)
}

// SetSites wires the account's file usage and address (GET /v1/me) and the
// site locks and file removal of deleting a team, through Access.
func (h *UserHandler) SetSites(a *AccessHandler) {
	h.profile.SetSites(a.Hosts, a.Disk, a.Limits)
	h.teams.Cutoff = a.Cutoff()
}

// ShareAssets makes h render through the site handler's asset pipeline (SiteHandler.Pages).
func (h *UserHandler) ShareAssets(p *web.AssetPipeline) { h.profile.ShareAssets(p) }

func (h *UserHandler) HTTPCaches() workers.Owner { return &h.httpCaches }
