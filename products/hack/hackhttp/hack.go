package hackhttp

import (
	"context"
	"crypto/rand"
	"database/sql"
	"io/fs"
	"net/http"
	"strings"
	"sync"
	"time"

	accountapp "github.com/vsriram/simple-host/host/access"
	"github.com/vsriram/simple-host/host/edition"
	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/model"
	appsites "github.com/vsriram/simple-host/host/sites"
	"github.com/vsriram/simple-host/products/hack/app/entries"
	"github.com/vsriram/simple-host/products/hack/app/events"
	"github.com/vsriram/simple-host/products/hack/app/members"
	"github.com/vsriram/simple-host/products/hack/app/teams"
	productconfig "github.com/vsriram/simple-host/products/hack/config"
	hackmodel "github.com/vsriram/simple-host/products/hack/model"
)

// HackDefaultCoC is the default code of conduct shown when an organiser has
// not written their own. Plain text; never turned into HTML.
const HackDefaultCoC = "This event is a respectful, harassment-free space. Treat everyone with care, whatever their background or experience.\n\nDo not post hateful, sexual or abusive content. Do not copy or interfere with another team's work.\n\nIf something is wrong, tell the organiser. Organisers may remove anyone who breaks this code."

// extraEventReserved is the extra names the spec refuses for an event slug,
// on top of labelReservedForNew / judgeHandle.
var extraEventReserved = map[string]bool{
	"www": true, "api": true, "admin": true, "app": true,
	"judge": true, "judges": true, "vote": true, "results": true,
	"e": true, "events": true, "event": true, "join": true,
	"signin": true, "sign-in": true, "sites": true, "site": true,
	"team": true, "teams": true, "manage": true, "new": true,
	"help": true, "docs": true, "status": true, "mail": true,
	"blog": true, "support": true, "report": true, "static": true,
	"cdn": true, "mcp": true, "auth": true,
	"mta-sts": true, "autoconfig": true, "autodiscover": true, "webmail": true,
	// Names the certificate issuer never issues for (deploy/site-certs/
	// issue.sh RESERVED): an event under one could never have team sites.
	"lab": true, "cname": true, "test": true, "dev": true, "localhost": true,
}

const (
	hackJoinCodeLen  = 8
	hackJudgeCodeLen = 12
	hackMaxBody      = 64 << 10
)

var hackStages = map[string]bool{
	"draft": true, "open": true, "building": true, "closed": true,
	"judging": true, "results": true, "archived": true,
}

// hackStagesOffered are the stages an organiser can set. Judging and results
// are offered with M3: the public page names them, submissions stay closed,
// and judges can no longer join once the stage is results.
var hackStagesOffered = map[string]bool{
	"draft": true, "open": true, "building": true, "closed": true,
	"judging": true, "results": true, "archived": true,
}

// hackStagesOfferedList is hackStagesOffered in stage order, for the pages.
var hackStagesOfferedList = []string{"draft", "open", "building", "closed", "judging", "results", "archived"}

// HackHandler is the hosted-events API (EVENTS=hosted): event CRUD, the
// startup setters, workers and the route table. Judging, participation, the
// public event and the assistant are their own receivers over the same base.
type HackHandler struct {
	*hackBase
	caches             httpx.Caches
	limitersOnce       sync.Once
	contentOnce        sync.Once
	contentOwner       *contentDeliveryOwner
	eventCleanupOnce   sync.Once
	eventCleanupWorker *eventCleanupOwner
	namesUser          *httpx.RateLimiter // event address checks, per account
	screenshotsIP      *httpx.RateLimiter // public gallery screenshots, per network address
	namePeer           func(ctx context.Context, name string) (bool, error)
	usageFn            func(ctx context.Context) (int64, error)
	archiveLinkKey     []byte
	// noticeTiming overrides the notice email delay and window (tests only;
	// hack_notices.go).
	noticeTiming *noticeMailPolicy
	// assets is the asset/chrome pipeline for RegisterDemo's pages
	// (hack_watch.go), set once by ShareAssets right after construction.
	assets hackPages

	judging       *judgingHTTP
	participation *participationHTTP
	publicEvent   *publicEventHTTP
	assistant     *assistantHTTP
}

// hackBase is what every hack receiver shares: the services, the team-site
// hooks, the instance addresses and limits, and the access checks and views
// built only from those. One pointer, so a setter reaches every receiver.
type hackBase struct {
	database *sql.DB
	events   *events.Service
	entries  *entries.Service
	teams    *teams.Service
	members  *members.Service
	// sites reaches the team sites (the SiteHandler). nil in tests that do
	// not need it: team addresses then count as not ready.
	sites hackSiteHooks
	// mailer sends announcements, notices and the one archive warning. nil,
	// or a sender that cannot send a notice, makes those log and do nothing.
	mailer        events.Mailer
	publicBaseURL string
	siteDomain    string
	// demoSlug is the demo event (RegisterDemo, after Register).
	demoSlug string
	limits   productconfig.Limits
	// siteArchiveDefault is the default per-site size cap when an account has
	// no MAX_ARCHIVE_MB_OVERRIDES entry (SetSiteArchiveDefault).
	siteArchiveDefault int64
	codesIP            *httpx.RateLimiter // join, judge and team codes, per network address
	codesUser          *httpx.RateLimiter // the same, per account
}

// judgingHTTP serves rubrics, scores, assignments, voting and results.
type judgingHTTP struct{ *hackBase }

// publicEventHTTP serves the public event JSON, content, icons and website.
type publicEventHTTP struct{ *hackBase }

// participationHTTP serves teams, entries, registration and leaving.
type participationHTTP struct {
	*hackBase
	entryWrites *httpx.RateLimiter // entry and screenshot writes, per account
	shotWrites  *httpx.RateLimiter // screenshot uploads, per account (2 MB each)
	applyIP     *httpx.RateLimiter // applications without a join link, per network address
	applyUser   *httpx.RateLimiter // the same, per account
}

// assistantHTTP serves the organiser assistant (SIM-394).
type assistantHTTP struct {
	*hackBase
	// service is nil until SetAssistant: every route then answers as off.
	service *events.AssistantService
	writes  *httpx.RateLimiter // questions, per account
}

// HandleVerdict is the Host handle check for one name: Status 0 means free.
type HandleVerdict = events.HandleVerdict

// SetHandleChecker injects the Host handle namespace check (handler.JudgeHandle),
// which this package may not import.
func (h *HackHandler) SetHandleChecker(fn events.HandleChecker) {
	h.events.SetHandleChecker(fn)
}

// hackPages is what Hack pages need of the site's asset/chrome pipeline
// (*assetPipeline in host/handler, which this package may not import).
type hackPages interface {
	// InkBaseURL is this Hack instance's public address.
	InkBaseURL() string
	// IsCanonicalInstance reports whether this is simple-hack.app itself,
	// not a self-hosted or otherwise addressed instance.
	IsCanonicalInstance() bool
	// StaticFS is the embedded static/ tree.
	StaticFS() fs.FS
	// SkillsModTime is the one Last-Modified every skills/plugin download serves.
	SkillsModTime() time.Time
	// ServeStaticPage serves one embedded static/<name> with its chrome.
	ServeStaticPage(name string) http.Handler
	// ChromePage is one embedded static/<name> with its chrome, before the
	// response's script nonce is stamped (httpx.StampNonce).
	ChromePage(r *http.Request, name string) ([]byte, error)
	// RenderPage applies the instance chrome and text to a product-owned page.
	RenderPage(r *http.Request, name string, raw []byte) ([]byte, error)
	// AdminUICSP is the standard admin-app Content-Security-Policy wrapper.
	AdminUICSP(next http.Handler) http.Handler
	// UICSP is AdminUICSP with an extended frame-src.
	UICSP(next http.Handler, frameSrc string) http.Handler
	// RenderAppShell is static/hack/app.html with its chrome applied: the
	// kit's own presentation, not the product chrome, and the sign-in
	// script tag only when signin is true.
	RenderAppShell(r *http.Request, body []byte, signin bool) ([]byte, error)
	// LandingMarkdownHandler serves one landing page's copy as markdown.
	LandingMarkdownHandler(file, title, description, publicBaseURL string, hackAssets bool) http.HandlerFunc
	// WriteMessagePage writes the one-message page (showcase.go).
	WriteMessagePage(w http.ResponseWriter, r *http.Request, base string, status int, message, subtext, backURL, backLabel, form string)
	// SkillServedText rewrites one skill file's served text for this instance.
	SkillServedText(path string, data []byte) []byte
	// RegisterSkillsHub mounts the standard (non-Hack-prefixed) skills catalog.
	RegisterSkillsHub(mux *http.ServeMux, publicBaseURL string)
}

// hackSiteHooks is what the events API needs of the site side.
type hackSiteHooks interface {
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
	// TeamSiteFlatHostsOn reports whether TEAM_SITE_FLAT_HOSTS is on.
	TeamSiteFlatHostsOn() bool
}

// SetSites connects the team sites (hack_sites.go).
func (h *HackHandler) SetSites(s hackSiteHooks) { h.sites = s }

// ShareAssets makes h render through the site side's asset pipeline
// (SiteHandler.Pages()); wired right after construction.
func (h *HackHandler) ShareAssets(pages hackPages) { h.assets = pages }

// NewHackHandlerForTest builds a *HackHandler with a given archive-link key
// and limits, for host/handler's opaque token test.
func NewHackHandlerForTest(archiveLinkKey []byte, limits edition.Limits) *HackHandler {
	lim := productconfig.DefaultLimits()
	lim.Limits = limits
	return &HackHandler{archiveLinkKey: archiveLinkKey, hackBase: &hackBase{limits: lim}}
}

// NewHackHandler builds the hosted-events API. publicBaseURL is the apex
// (join/judge/manage links); siteDomain is the event host parent.
func NewHackHandler(database *sql.DB, publicBaseURL, siteDomain string, editions ...*edition.Edition) *HackHandler {
	var ed *edition.Edition
	if len(editions) > 0 {
		ed = editions[0]
	}
	lim := productconfig.DefaultLimits()
	lim.Limits = ed.Limits()
	b := &hackBase{
		database:           database,
		events:             events.New(database),
		teams:              teams.New(database),
		publicBaseURL:      strings.TrimRight(publicBaseURL, "/"),
		siteDomain:         strings.Trim(strings.ToLower(siteDomain), "."),
		limits:             lim,
		siteArchiveDefault: defaultSiteArchiveSize,
		codesIP:            httpx.NewRateLimiter(float64(lim.RateEventCodesIP.Burst), lim.RateEventCodesIP.PerSecond()),
		codesUser:          httpx.NewRateLimiter(float64(lim.RateEventCodesUser.Burst), lim.RateEventCodesUser.PerSecond()),
	}
	b.entries = entries.New(database, b.entrySiteLive)
	h := &HackHandler{
		hackBase:       b,
		namesUser:      httpx.NewRateLimiter(float64(lim.RateEventNamesUser.Burst), lim.RateEventNamesUser.PerSecond()),
		screenshotsIP:  httpx.NewRateLimiter(120, 5),
		archiveLinkKey: newHackArchiveKey(),
		judging:        &judgingHTTP{b},
		publicEvent:    &publicEventHTTP{b},
		participation: &participationHTTP{hackBase: b,
			entryWrites: httpx.NewRateLimiter(30, 0.5),
			shotWrites:  httpx.NewRateLimiter(5, 0.1),
			applyIP:     httpx.NewRateLimiter(float64(lim.RateEventApplyIP.Burst), lim.RateEventApplyIP.PerSecond()),
			applyUser:   httpx.NewRateLimiter(float64(lim.RateEventApplyUser.Burst), lim.RateEventApplyUser.PerSecond()),
		},
		// burst 40 (above the 30/day business limit and the 18-question eval
		// set), refills one every 5s: abuse defense only
		assistant: &assistantHTTP{hackBase: b, writes: httpx.NewRateLimiter(40, 0.2)},
	}
	b.members = members.New(database, h.archiveEventTx, h.deleteEventTx, func(ctx context.Context, ev hackmodel.Event) {
		paperRecorder(ctx).RecordHackStage(ctx, ev.ID, ev.Stage, ev.UpdatedAt)
		paperRecorder(ctx).HackPhasesChanged(ctx)
		b.pinDue(ctx, ev.ID)
		// requestEventCert is a no-op for archived events, as in setStage.
	}, h.removeEventFiles, b.removeOrphanTeamSites)
	return h
}

// SetAssistant wires the SIM-394 organiser assistant (main builds it: it
// needs host/llm and the docs text). Unset, every assistant route is off.
func (h *HackHandler) SetAssistant(a *events.AssistantService) { h.assistant.service = a }

// defaultSiteArchiveSize is the per-site cap before SetSiteArchiveDefault.
const defaultSiteArchiveSize int64 = 100 << 20

// SetSiteArchiveDefault records the instance's default per-site size cap
// (handler.SiteLimit(), after any MAX_ARCHIVE_MB override).
func (h *HackHandler) SetSiteArchiveDefault(bytes int64) {
	if bytes > 0 {
		h.siteArchiveDefault = bytes
	}
}

// newHackArchiveKey is 32 random bytes; it panics only without randomness.
func newHackArchiveKey() []byte {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		panic("hack archive links: no randomness: " + err.Error())
	}
	return key
}

// siteLimitFor is accounts.SiteLimit with siteArchiveDefault.
func (h *hackBase) siteLimitFor(ctx context.Context, database *sql.DB, user *model.User) (limit int64, override bool) {
	return accountapp.New(database).SiteLimit(ctx, user, h.limits.Limits, h.siteArchiveDefault)
}

// SetNamePeer: fn reports whether the peer instance holds name; nil = no peer.
// An error means the name is refused with 503 code name_check_unavailable.
func (h *HackHandler) SetNamePeer(fn func(ctx context.Context, name string) (bool, error)) {
	h.namePeer = fn
}

// SetInstanceUsage: fn returns bytes used by team sites on this instance; nil = 0.
func (h *HackHandler) SetInstanceUsage(fn func(ctx context.Context) (int64, error)) {
	h.usageFn = fn
}

// NameTaken is what this instance answers the peer: the name is an event slug,
// a handle or a handle alias here, or reserved.
func (h *HackHandler) NameTaken(ctx context.Context, name string) (bool, error) {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return false, nil
	}
	if eventNameReserved(name) {
		return true, nil
	}
	v, err := h.events.JudgeHandle(ctx, name)
	if err != nil {
		return false, err
	}
	if v.Status != 0 {
		if v.Code == "invalid_handle" {
			return false, nil
		}
		return true, nil
	}
	taken, err := h.events.EventSlugTakenLookup(ctx, name)
	return taken, err
}

// SetProductLimits installs the validated product snapshot before routes start.
func (h *HackHandler) SetProductLimits(limits productconfig.Limits) {
	limits.Limits = h.limits.Limits
	h.limits = limits
	h.codesIP = httpx.NewRateLimiter(float64(limits.RateEventCodesIP.Burst), limits.RateEventCodesIP.PerSecond())
	h.codesUser = httpx.NewRateLimiter(float64(limits.RateEventCodesUser.Burst), limits.RateEventCodesUser.PerSecond())
	h.namesUser = httpx.NewRateLimiter(float64(limits.RateEventNamesUser.Burst), limits.RateEventNamesUser.PerSecond())
	h.participation.applyIP = httpx.NewRateLimiter(float64(limits.RateEventApplyIP.Burst), limits.RateEventApplyIP.PerSecond())
	h.participation.applyUser = httpx.NewRateLimiter(float64(limits.RateEventApplyUser.Burst), limits.RateEventApplyUser.PerSecond())
}
