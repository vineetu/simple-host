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

	"github.com/vsriram/simple-host/host/edition"
	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/model"
	appsites "github.com/vsriram/simple-host/host/sites"
	"github.com/vsriram/simple-host/products/hack/app/entries"
	"github.com/vsriram/simple-host/products/hack/app/events"
	"github.com/vsriram/simple-host/products/hack/app/members"
	"github.com/vsriram/simple-host/products/hack/app/teams"
	productconfig "github.com/vsriram/simple-host/products/hack/config"
)

// HackDefaultCoC is the default code of conduct shown when an organiser has
// not written their own. Plain text; never turned into HTML.
const HackDefaultCoC = "This event is a respectful, harassment-free space. Treat everyone with care, whatever their background or experience.\n\nDo not post hateful, sexual or abusive content. Do not copy or interfere with another team's work.\n\nIf something is wrong, tell the organiser. Organisers may remove anyone who breaks this code."

const (
	hackJoinCodeLen  = 8
	hackJudgeCodeLen = 12
	hackMaxBody      = 64 << 10
)

var hackStages = map[string]bool{
	"draft": true, "open": true, "building": true, "closed": true,
	"judging": true, "results": true, "archived": true,
}

var hackStagesOfferedList = []string{"draft", "open", "building", "closed", "judging", "results", "archived"}

// HackHandler is the hosted-events API (EVENTS=hosted): event CRUD, the startup
// setters, workers and the route table.
type HackHandler struct {
	events  *events.Service
	entries *entries.Service
	teams   *teams.Service
	members *members.Service
	links   *siteLinks
	views   *views
	limits  *productconfig.Limits
	website *websiteLimits
	codes   *codeLimiter
	mailer  events.Mailer

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
	// noticeTiming overrides the notice email delay and window (tests only).
	noticeTiming *noticeMailPolicy
	// assets is the asset/chrome pipeline for RegisterDemo's pages
	// (hack_watch.go), set once by ShareAssets right after construction.
	assets hackPages

	judging       *judgingHTTP
	participation *participationHTTP
	publicEvent   *publicEventHTTP
	assistant     *assistantHTTP
}

// views builds the member-facing JSON: event view, member and team blocks, entry list items, checklist.
type views struct {
	database *sql.DB
	events   *events.Service
	entries  *entries.Service
	teams    *teams.Service
	members  *members.Service
	links    *siteLinks
	demoSlug string
}

type judgingHTTP struct {
	events  *events.Service
	entries *entries.Service
	teams   *teams.Service
	members *members.Service
	links   *siteLinks
	views   *views
}

type publicEventHTTP struct {
	database *sql.DB
	events   *events.Service
	entries  *entries.Service
	teams    *teams.Service
	members  *members.Service
	links    *siteLinks
	limits   *productconfig.Limits
	website  *websiteLimits
	canEmail bool
}

type participationHTTP struct {
	database    *sql.DB
	events      *events.Service
	entries     *entries.Service
	teams       *teams.Service
	members     *members.Service
	links       *siteLinks
	views       *views
	limits      *productconfig.Limits
	website     *websiteLimits
	codes       *codeLimiter
	entryWrites *httpx.RateLimiter // entry and screenshot writes, per account
	shotWrites  *httpx.RateLimiter // screenshot uploads, per account (2 MB each)
	applyIP     *httpx.RateLimiter // applications without a join link, per network address
	applyUser   *httpx.RateLimiter // the same, per account
}

type assistantHTTP struct {
	events  *events.Service
	entries *entries.Service
	members *members.Service
	views   *views
	limits  *productconfig.Limits
	website *websiteLimits
	service *events.AssistantService
	writes  *httpx.RateLimiter // questions, per account
}

// SetHandleChecker injects the Host handle check, which this package may not import.
func (h *HackHandler) SetHandleChecker(fn events.HandleChecker) {
	h.events.SetHandleChecker(fn)
}

// hackPages is what Hack pages need of the site's asset/chrome pipeline (*assetPipeline in host/handler, which this package may not import).
type hackPages interface {
	InkBaseURL() string
	// IsCanonicalInstance reports whether this is simple-hack.app itself, not a self-hosted instance.
	IsCanonicalInstance() bool
	StaticFS() fs.FS
	SkillsModTime() time.Time
	ServeStaticPage(name string) http.Handler
	// ChromePage is one embedded static/<name> with its chrome, before the script nonce is stamped.
	ChromePage(r *http.Request, name string) ([]byte, error)
	RenderPage(r *http.Request, name string, raw []byte) ([]byte, error)
	AdminUICSP(next http.Handler) http.Handler
	UICSP(next http.Handler, frameSrc string) http.Handler
	// RenderAppShell is static/hack/app.html with its chrome applied; the sign-in script tag only when signin is true.
	RenderAppShell(r *http.Request, body []byte, signin bool) ([]byte, error)
	LandingMarkdownHandler(file, title, description, publicBaseURL string, hackAssets bool) http.HandlerFunc
	WriteMessagePage(w http.ResponseWriter, r *http.Request, base string, status int, message, subtext, backURL, backLabel, form string)
	SkillServedText(path string, data []byte) []byte
	RegisterSkillsHub(mux *http.ServeMux, publicBaseURL string)
}

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
	TeamSiteFlatHostsOn() bool
}

func (h *HackHandler) SetSites(s hackSiteHooks) { h.links.hooks = s }

func (h *HackHandler) ShareAssets(pages hackPages) { h.assets = pages }

func NewHackHandler(database *sql.DB, publicBaseURL, siteDomain string, editions ...*edition.Edition) *HackHandler {
	var ed *edition.Edition
	if len(editions) > 0 {
		ed = editions[0]
	}
	lim := productconfig.DefaultLimits()
	lim.Limits = ed.Limits()
	codes := newCodeLimiter(lim)
	h := &HackHandler{
		teams:          teams.New(database),
		limits:         &lim,
		website:        &websiteLimits{database: database, limits: &lim, archiveDefault: defaultSiteArchiveSize},
		codes:          &codes,
		namesUser:      httpx.NewRateLimiter(float64(lim.RateEventNamesUser.Burst), lim.RateEventNamesUser.PerSecond()),
		screenshotsIP:  httpx.NewRateLimiter(120, 5),
		archiveLinkKey: newHackArchiveKey(),
	}
	h.links = &siteLinks{teams: h.teams, publicBaseURL: strings.TrimRight(publicBaseURL, "/"), siteDomain: strings.Trim(strings.ToLower(siteDomain), ".")}
	h.entries = entries.New(database, h.links.entrySiteLive)
	h.events = events.New(database, h.entries)
	h.members = members.New(database, h.events, h.entries)
	h.views = &views{database: database, events: h.events, entries: h.entries, teams: h.teams, members: h.members, links: h.links}
	h.judging = &judgingHTTP{events: h.events, entries: h.entries, teams: h.teams, members: h.members, links: h.links, views: h.views}
	h.publicEvent = &publicEventHTTP{database: database, events: h.events, entries: h.entries, teams: h.teams, members: h.members, links: h.links, limits: h.limits, website: h.website}
	h.participation = &participationHTTP{database: database, events: h.events, entries: h.entries, teams: h.teams, members: h.members, links: h.links, views: h.views, limits: h.limits, website: h.website, codes: h.codes,
		entryWrites: httpx.NewRateLimiter(30, 0.5),
		shotWrites:  httpx.NewRateLimiter(5, 0.1),
		applyIP:     httpx.NewRateLimiter(float64(lim.RateEventApplyIP.Burst), lim.RateEventApplyIP.PerSecond()),
		applyUser:   httpx.NewRateLimiter(float64(lim.RateEventApplyUser.Burst), lim.RateEventApplyUser.PerSecond()),
	}
	// Rate limit: burst 40 (above the 30/day business limit and the 18-question eval set), refills one every 5s; abuse defense only.
	h.assistant = &assistantHTTP{events: h.events, entries: h.entries, members: h.members, views: h.views, limits: h.limits, website: h.website, writes: httpx.NewRateLimiter(40, 0.2)}
	return h
}

// SetAssistant wires the organiser assistant (needs host/llm and the docs text). Unset, every assistant route is off.
func (h *HackHandler) SetAssistant(a *events.AssistantService) { h.assistant.service = a }

const defaultSiteArchiveSize int64 = 100 << 20

// SetSiteArchiveDefault records the instance's default per-site size cap.
func (h *HackHandler) SetSiteArchiveDefault(bytes int64) {
	if bytes > 0 {
		h.website.archiveDefault = bytes
	}
}

func newHackArchiveKey() []byte {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		panic("hack archive links: no randomness: " + err.Error())
	}
	return key
}

// SetNamePeer: fn reports whether the peer instance holds name; nil = no peer.
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
	if events.NameReserved(name) {
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

func (h *HackHandler) SetProductLimits(limits productconfig.Limits) {
	limits.Limits = h.limits.Limits
	*h.limits = limits
	*h.codes = newCodeLimiter(limits)
	h.namesUser = httpx.NewRateLimiter(float64(limits.RateEventNamesUser.Burst), limits.RateEventNamesUser.PerSecond())
	h.participation.applyIP = httpx.NewRateLimiter(float64(limits.RateEventApplyIP.Burst), limits.RateEventApplyIP.PerSecond())
	h.participation.applyUser = httpx.NewRateLimiter(float64(limits.RateEventApplyUser.Burst), limits.RateEventApplyUser.PerSecond())
}
