package core

import (
	"context"
	"database/sql"
	"strings"
	"time"

	appsites "github.com/vsriram/simple-host/host/sites"
	productconfig "github.com/vsriram/simple-host/products/hack/config"

	"github.com/vsriram/simple-host/host/edition"
	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/model"
	entries "github.com/vsriram/simple-host/products/hack/app/entries"
	"github.com/vsriram/simple-host/products/hack/app/events"
	"github.com/vsriram/simple-host/products/hack/app/members"
	"github.com/vsriram/simple-host/products/hack/app/teams"
)

// HackDefaultCoC is the default code of conduct shown when an organiser has
// not written their own. Plain text; never turned into HTML.
const DefaultCoC = "This event is a respectful, harassment-free space. Treat everyone with care, whatever their background or experience.\n\nDo not post hateful, sexual or abusive content. Do not copy or interfere with another team's work.\n\nIf something is wrong, tell the organiser. Organisers may remove anyone who breaks this code."

var StagesOffered = []string{"draft", "open", "building", "closed", "judging", "results", "archived"}

// Hack is the module's shared state: the services, the instance's links and
// limits, and the one instance of each rate limiter. Every feature reads it.
type Hack struct {
	DB       *sql.DB
	Events   *events.Service
	Entries  *entries.Service
	Teams    *teams.Service
	Members  *members.Service
	Links    *Links
	Limits   *productconfig.Limits
	Website  *WebsiteLimits
	Codes    *CodeLimiter
	Mailer   events.Mailer
	CanEmail bool   // Mailer can send notices
	DemoSlug string // the demo event's slug, "" when none

	NamesUser       *httpx.RateLimiter // event address checks, per account
	ScreenshotsIP   *httpx.RateLimiter // public gallery screenshots, per network address
	EntryWrites     *httpx.RateLimiter // entry and screenshot writes, per account
	ShotWrites      *httpx.RateLimiter // screenshot uploads, per account (2 MB each)
	ApplyIP         *httpx.RateLimiter // applications without a join link, per network address
	ApplyUser       *httpx.RateLimiter // the same, per account
	AssistantWrites *httpx.RateLimiter // assistant questions, per account
}

type SiteHooks interface {
	TeamSitesReady(eventSlug string) bool
	TeamSiteURL(eventSlug, teamSlug string) string
	TeamSiteInfo(ctx context.Context, accountID, teamSlug string) (model.TeamSite, model.Site, error)
	TeamPreviewLink(ctx context.Context, accountID, eventSlug, teamSlug string, n int) (string, time.Time, bool)
	TeamPreviewLinkFor(site model.Site, eventSlug string, n int) (string, bool)
	SuspendHeldSite(context.Context, appsites.HeldSiteSuspension) error
	TrashTeamSite(ctx context.Context, accountID, name string) error
	RequestSiteCert(handle string)
	RemoveAccountFiles(userID, handle string) error
	SyncAccountMarkers(ctx context.Context, accountID string) error
	TeamSiteFlatHostsOn() bool
}

// New builds the module's shared state.
func New(database *sql.DB, publicBaseURL, siteDomain string, ed *edition.Edition) *Hack {
	lim := productconfig.DefaultLimits()
	lim.Limits = ed.Limits()
	codes := NewCodeLimiter(lim)
	h := &Hack{
		DB:      database,
		Teams:   teams.New(database),
		Limits:  &lim,
		Website: &WebsiteLimits{database: database, limits: &lim, ArchiveDefault: defaultSiteArchiveSize},
		Codes:   &codes,
		// Rate limit: burst 40 (above the 30/day business limit and the 18-question eval set), refills one every 5s; abuse defense only.
		AssistantWrites: httpx.NewRateLimiter(40, 0.2),
		ScreenshotsIP:   httpx.NewRateLimiter(120, 5),
		EntryWrites:     httpx.NewRateLimiter(30, 0.5),
		ShotWrites:      httpx.NewRateLimiter(5, 0.1),
	}
	h.setRateLimits(lim)
	h.Links = &Links{Teams: h.Teams, PublicBaseURL: strings.TrimRight(publicBaseURL, "/"), SiteDomain: strings.Trim(strings.ToLower(siteDomain), ".")}
	h.Entries = entries.New(database, h.Links.EntrySiteLive)
	h.Events = events.New(database, h.Entries)
	h.Members = members.New(database, h.Events, h.Entries)
	return h
}

// setRateLimits rebuilds the limiters whose rates come from the product limits.
func (h *Hack) setRateLimits(limits productconfig.Limits) {
	*h.Codes = NewCodeLimiter(limits)
	h.NamesUser = httpx.NewRateLimiter(float64(limits.RateEventNamesUser.Burst), limits.RateEventNamesUser.PerSecond())
	h.ApplyIP = httpx.NewRateLimiter(float64(limits.RateEventApplyIP.Burst), limits.RateEventApplyIP.PerSecond())
	h.ApplyUser = httpx.NewRateLimiter(float64(limits.RateEventApplyUser.Burst), limits.RateEventApplyUser.PerSecond())
}

// SetProductLimits replaces the product limits (keeping the edition's platform limits) and the limiters made from them.
func (h *Hack) SetProductLimits(limits productconfig.Limits) {
	limits.Limits = h.Limits.Limits
	*h.Limits = limits
	h.setRateLimits(limits)
}

const defaultSiteArchiveSize int64 = 100 << 20

// hackMaxBody caps a JSON request body.
const MaxBody = 64 << 10

// SetMailer is the instance's mail sender (archive warnings, notices, announcements). Call once at startup.
func (h *Hack) SetMailer(m events.Mailer) {
	h.Mailer = m
	_, h.CanEmail = NoticeSender(m)
}

// SetHandleChecker injects the Host handle check, which this package may not import.
func (h *Hack) SetHandleChecker(fn events.HandleChecker) {
	h.Events.SetHandleChecker(fn)
}

// SetSites is the platform side team sites ask of (the SiteHandler; tests pass a stub).
func (h *Hack) SetSites(s SiteHooks) { h.Links.Hooks = s }

// SetSiteArchiveDefault records the instance's default per-site size cap.
func (h *Hack) SetSiteArchiveDefault(bytes int64) {
	if bytes > 0 {
		h.Website.ArchiveDefault = bytes
	}
}

// SettleDueEntries settles the entries whose deadline has passed (the site sweep).
func (h *Hack) SettleDueEntries(ctx context.Context) {
	h.Entries.SettleDue(ctx, SweepReport{Ctx: ctx})
}
