package hackhttp

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/vsriram/simple-host/internal/httpx"
	"github.com/vsriram/simple-host/internal/rules/sites"
	"io"
	"io/fs"
	"log"
	"net/http"
	"net/mail"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/vsriram/simple-host/internal/app/hack/entries"
	"github.com/vsriram/simple-host/internal/app/hack/events"
	"github.com/vsriram/simple-host/internal/auth"
	"github.com/vsriram/simple-host/internal/config"
	"github.com/vsriram/simple-host/internal/db"
	"github.com/vsriram/simple-host/internal/edition"
	"github.com/vsriram/simple-host/internal/email"
	"github.com/vsriram/simple-host/internal/model"
	"github.com/vsriram/simple-host/internal/papermetrics"
	"github.com/vsriram/simple-host/internal/rules/hackevents"
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

// HackHandler is the hosted-events API (EVENTS=hosted).
type HackHandler struct {
	// limits is this instance's edition.Limits() snapshot. A plain field,
	// not handler's shared limitSet embedding (cleanup 3.2 slice 1c: that
	// type's methods are unexported in another package once this type
	// moves to internal/hackhttp), so the three values this package reads
	// (siteLimitFor, exportLinkTTL, idleReplyTo below) are HackHandler's
	// own methods over the same config.Limits fields.
	limits config.Limits
	// siteArchiveDefault is the instance's default per-site size cap when
	// an account has no MAX_ARCHIVE_MB_OVERRIDES entry: handler.SiteLimit()
	// at construction (SetSiteArchiveDefault, wired from main). Falls back
	// to the same 100 MB default handler.site.go starts with when never set
	// (tests that build a bare &HackHandler{}).
	siteArchiveDefault int64
	demoSlug           string
	database           *sql.DB
	publicBaseURL      string
	siteDomain         string
	codesIP            *httpx.RateLimiter // join, judge and team codes, per network address
	codesUser          *httpx.RateLimiter // the same, per account
	namesUser          *httpx.RateLimiter // event address checks, per account
	entryWrites        *httpx.RateLimiter // entry and screenshot writes, per account
	shotWrites         *httpx.RateLimiter // screenshot uploads, per account (2 MB each)
	applyIP            *httpx.RateLimiter // applications without a join link, per network address
	applyUser          *httpx.RateLimiter // the same, per account
	namePeer           func(ctx context.Context, name string) (bool, error)
	usageFn            func(ctx context.Context) (int64, error)
	// sites reaches the team sites (the SiteHandler; hack_sites.go). nil in
	// tests that do not need it: team addresses then count as not ready.
	sites hackSiteHooks
	// mailer sends the one archive warning (hack_cleanup.go). nil, or a
	// sender that cannot send a notice, makes that sweep log and do nothing.
	mailer         email.Sender
	archiveLinkKey []byte
	// noticeTiming overrides the notice email delay and window (tests only;
	// hack_notices.go).
	noticeTiming *noticeMailPolicy
	// imageScan checks event icons and entry screenshots (imagescan.go);
	// nil (the zero value: SetImageScan was never called) until
	// PHOTODNA_API_KEY is set. A narrow interface, not *ImageScan directly
	// (cleanup 3.2 slice 1c): hackhttp must never import handler, even
	// though ImageScan is exported. Callers must check h.imageScan != nil
	// themselves (hack_icons.go, hack_entries.go) — a nil hackImageScanner
	// is a true nil interface, unlike the old nil-receiver-safe *ImageScan,
	// so calling a method on it directly would panic.
	imageScan hackImageScanner
	// assets is the asset/chrome pipeline for RegisterDemo's pages
	// (hack_watch.go); read it through pipeline(). HackHandler only exists
	// when EVENTS=hosted, so its edition is always Hack. Set once, by
	// ShareAssets (main wires it right after construction; tests do the
	// same in newHackApp/newTeamSiteApp).
	assets hackPages
	// handleChecker judges a candidate event slug against the Host
	// handle namespace (shape, reservation, occupancy): handlecheck.go's
	// judgeHandle, which HackHandler has no home for itself (the handle
	// namespace belongs to Host). Injected (cleanup 3.2 slice 3,
	// SetHandleChecker) since hackhttp may not import handler; nil means
	// "no handle conflicts" (tests that never wire it, e.g. the no-app
	// registrar-only fixtures, never reach a name check).
	handleChecker func(ctx context.Context, q db.Querier, handle string) (HandleVerdict, error)
	// events is the Hack events service (internal/app/hack/events,
	// cleanup 3.2 slice 4): today just the public directory listing
	// (getDirectory, hack_voting.go). Built in NewHackHandler from the
	// same *sql.DB; nil on a bare &HackHandler{} (NewHackHandlerForTest),
	// which never serves the directory route.
	events  *events.Service
	entries *entries.Service
}

// HandleVerdict is the outcome handlecheck.go's judgeHandle reaches for one
// candidate name: Status 0 means it can be had. Mirrors just the two fields
// slugVerdict/NameTaken need (handler's handleVerdict also carries Error and
// Address, which are Host-only concerns).
type HandleVerdict struct {
	Status int
	Code   string
}

// SetHandleChecker injects handlecheck.go's judgeHandle (the Host handle
// namespace: shape, reservation, who already holds it), which this package
// cannot reach itself (it may not import handler). Wired from
// cmd/server/main.go; test fixtures (hack_test.go, hack_m2_test.go) wire the
// same function, via their test-only handler import.
func (h *HackHandler) SetHandleChecker(fn func(ctx context.Context, q db.Querier, handle string) (HandleVerdict, error)) {
	h.handleChecker = fn
}

// judgeHandle is the event-name counterpart of handlecheck.go's judgeHandle,
// reached through h.handleChecker (SetHandleChecker above) since that
// function lives on the Host side. userID is always "" here: an event slug
// is never anyone's own handle.
func (h *HackHandler) judgeHandle(ctx context.Context, name string) (HandleVerdict, error) {
	if h.handleChecker == nil {
		return HandleVerdict{}, nil
	}
	return h.handleChecker(ctx, h.database, name)
}

// hackPages is what moving Hack code needs of the site's asset/chrome
// pipeline (assetPipeline, assets.go — stays in internal/handler this slice;
// its proposed eventual home is internal/webassets per
// docs/architecture/target.md §2, at which point this interface, and every
// hack-owned caller of sh.pipeline()/h.pipeline(), needs no interface at
// all). Every method here is exported on *assetPipeline for exactly this
// interface (cleanup 3.2 slice 2a); each is either a thin rename of an
// existing method with few other callers, or a new small method bundling a
// few of the pipeline's existing unexported ones (RenderAppShell,
// IsCanonicalInstance) so this interface's signatures stay to stdlib types,
// never assetPipeline or chromeData (both unexported, handler-only).
// Implemented by *assetPipeline (assets.go and friends).
type hackPages interface {
	// InkBaseURL is this Hack instance's public address.
	InkBaseURL() string
	// IsCanonicalInstance reports whether this is simple-hack.app itself,
	// not a self-hosted or otherwise addressed instance.
	IsCanonicalInstance() bool
	// StaticFS is the embedded static/ tree (ui.go's staticFiles), for
	// moving code that parses its own templates/pages from it.
	StaticFS() fs.FS
	// SkillsModTime is the one Last-Modified every skills/plugin download
	// serves (ui.go's skillsModTime, set once at process start).
	SkillsModTime() time.Time
	// ServeStaticPage serves one embedded static/<name> with its chrome.
	ServeStaticPage(name string) http.Handler
	// AdminUICSP is the standard admin-app Content-Security-Policy wrapper.
	AdminUICSP(next http.Handler) http.Handler
	// UICSP is AdminUICSP with an extended frame-src.
	UICSP(next http.Handler, frameSrc string) http.Handler
	// HackInstanceText rewrites simple-hack.app's own address to this
	// instance's, in served Hack text.
	HackInstanceText(data []byte) []byte
	// BuildHackSkillsZip is the cached hack-skills.zip body.
	BuildHackSkillsZip() ([]byte, error)
	// ServeHackSkillsZip serves BuildHackSkillsZip at /hack-skills.zip.
	ServeHackSkillsZip() http.HandlerFunc
	// RewriteHackPluginZip rewrites a downloaded plugin zip's text assets
	// (.md/.yaml/.json) for this instance.
	RewriteHackPluginZip(data []byte) ([]byte, error)
	// RenderAppShell is static/hack/app.html with its chrome applied: the
	// kit's own presentation, not the product chrome, and the sign-in
	// script tag only when signin is true.
	RenderAppShell(r *http.Request, body []byte, signin bool) ([]byte, error)
	// LandingMarkdownHandler serves one landing page's copy as markdown.
	LandingMarkdownHandler(file, title, description, publicBaseURL string, hackAssets bool) http.HandlerFunc
	// WriteMessagePage writes the one-message page (showcase.go).
	WriteMessagePage(w http.ResponseWriter, r *http.Request, base string, status int, message, subtext, backURL, backLabel, form string)
	// SkillServedText rewrites one skill file's served text for this
	// instance (skillshub.go's skillServedText). Exported for hackPages,
	// a test hook (cleanup 3.2 slice 3): the moved hack_instance_test.go
	// checks one skill file's rewritten text without building the whole
	// zip.
	SkillServedText(path string, data []byte) []byte
	// RegisterSkillsHub mounts the standard (non-Hack-prefixed) skills
	// catalog (skillshub.go's registerSkillsHub). Exported for hackPages,
	// needed by the moved hack_discovery_test.go and
	// hack_ink_browser_test.go, which registered it through
	// sh.pipeline() before the move.
	RegisterSkillsHub(mux *http.ServeMux, publicBaseURL string)
	// ServeSkillsZip is ui.go's serveSkillsZip, exported for hackPages:
	// the moved hack_get_started_test.go served the standard
	// (non-hosted) /skills.zip alias through it before the move.
	ServeSkillsZip(w http.ResponseWriter, r *http.Request, hackAssets bool)
}

// pipeline is h's asset pipeline, for the Hack edition at publicBaseURL.
// Production always has one by the time any route runs: main wires it with
// ShareAssets right after construction (cmd/server/main.go). A bare
// &HackHandler{} (most tests) must do the same (hack_test.go's newHackApp,
// hack_m2_test.go's newTeamSiteApp): hackhttp cannot build its own
// *assetPipeline (handler.NewPages), since that would need the forbidden
// handler import from non-test code (cleanup 3.2 slice 3; flagged by the
// slice 2a report as the one thing the real move would need to change).
func (h *HackHandler) pipeline() hackPages { return h.assets }

// hackSiteHooks is what the events API needs of the site side.
type hackSiteHooks interface {
	TeamSitesReady(eventSlug string) bool
	TeamSiteURL(eventSlug, teamSlug string) string
	TeamSiteInfo(ctx context.Context, accountID, teamSlug string) (model.TeamSite, db.Site, error)
	TeamPreviewLink(ctx context.Context, accountID, eventSlug, teamSlug string, n int) (string, time.Time, bool)
	TeamPreviewLinkFor(site db.Site, eventSlug string, n int) (string, bool)
	SetTeamSiteTakenDown(ctx context.Context, accountID, teamID, teamSlug string, on bool, reason string) error
	TrashTeamSite(ctx context.Context, accountID, name string) error
	RequestSiteCert(handle string)
	RemoveAccountFiles(userID, handle string) error
	SyncAccountMarkers(ctx context.Context, accountID string) error
}

// hackImageScanner is what moving Hack code needs of the image-scan worker
// (imagescan.go, staying in handler): enqueueing an event icon or entry
// screenshot for matching. A narrow interface, not *ImageScan directly
// (cleanup 3.2 slice 1c), satisfied by *ImageScan's existing exported
// methods, both already nil-receiver-safe.
type hackImageScanner interface {
	EnqueueEventIcon(eventID, userID string, raw []byte)
	EnqueueEntryScreenshot(eventID, teamID, userID string, raw []byte)
}

// syncEventMarkers puts the event's team sites down or back with the event;
// false (after logging) when a marker could not be written.
func (h *HackHandler) syncEventMarkers(ctx context.Context, ev db.Event) bool {
	if h.sites == nil {
		return true
	}
	if err := h.sites.SyncAccountMarkers(ctx, ev.AccountID); err != nil {
		log.Printf("hack: take-down markers of %s: %v", ev.Slug, err)
		return false
	}
	return true
}

// removeEventFiles deletes a deleted event's team site files.
func (h *HackHandler) removeEventFiles(ev db.Event) {
	if h.sites == nil {
		return
	}
	if err := h.sites.RemoveAccountFiles(ev.AccountID, ev.Slug); err != nil {
		log.Printf("hack: remove files of deleted event %s: %v", ev.Slug, err)
	}
}

// SetSites connects the team sites (hack_sites.go).
func (h *HackHandler) SetSites(s hackSiteHooks) { h.sites = s }

// ShareAssets makes h render through pages, the same asset pipeline the
// site side uses (SiteHandler.Pages()), instead of building its own from
// edition.ForKind(edition.Hack) on first use (pipeline() above). Moved from
// assets.go (cleanup 3.2 slice 1c); takes the hackPages interface, not
// *SiteHandler (cleanup 3.2 slice 2a: the only thing this ever did with sh
// was call sh.pipeline()).
func (h *HackHandler) ShareAssets(pages hackPages) { h.assets = pages }

// SetImageScan connects the worker to the events side (event icon and entry
// screenshot enqueueing). Moved from imagescan.go (cleanup 3.2 slice 1c): a
// method with a *HackHandler receiver must live in that type's package.
// Only hackImageScanner's two methods, so this takes the interface, not
// *ImageScan: main wires the take-down-marker sync the other way, with
// SyncEventMarkers below and ImageScan.SetHackTakeDownSync (imagescan.go),
// since that needs *ImageScan's own (unexported-to-hackhttp) method.
func (h *HackHandler) SetImageScan(s hackImageScanner) { h.imageScan = s }

// SyncEventMarkers is syncEventMarkers, exported so main can wire it into
// the image-scan worker's take-down path (ImageScan.SetHackTakeDownSync)
// without this package needing to know *ImageScan's type.
func (h *HackHandler) SyncEventMarkers(ctx context.Context, ev db.Event) bool {
	return h.syncEventMarkers(ctx, ev)
}

// teamSitesReady: the event's team addresses have their certificate.
func (h *HackHandler) teamSitesReady(eventSlug string) bool {
	return h.sites != nil && h.sites.TeamSitesReady(eventSlug)
}

// teamSiteURL is https://<team>.<event>.<SITE_DOMAIN>/.
func (h *HackHandler) teamSiteURL(eventSlug, teamSlug string) string {
	if h.sites != nil {
		return h.sites.TeamSiteURL(eventSlug, teamSlug)
	}
	return "https://" + teamSlug + "." + eventSlug + "." + h.siteDomain + "/"
}

// These hooks annotate the outer paper collector using resolutions the
// handler already performed. With collection off they are context lookups
// only. Moved from hack_paper_metrics.go (cleanup 3.2 slice 1c): free
// functions with no *SiteHandler dependency, used only by the event API.
func paperHackEvent(r *http.Request, ev db.Event) {
	papermetrics.ResolveHackEvent(r.Context(), ev.Slug, ev.Stage, "", ev.ResultsPublishedAt.Time)
	paperHackUser(r, auth.GetUser(r.Context()))
}

func paperHackUser(r *http.Request, user *db.User) {
	if user == nil {
		return
	}
	member := user.ID
	if user.Team != nil {
		member = user.Team.MemberID
	}
	if user.EventWebsite != nil {
		member = user.EventWebsite.OrganiserID
	}
	papermetrics.HackMember(r.Context(), member)
}

func paperHackKnownMember(r *http.Request, ev db.Event, member string) {
	papermetrics.ResolveHackEvent(r.Context(), ev.Slug, ev.Stage, member, ev.ResultsPublishedAt.Time)
}

func paperHackStage(r *http.Request, ev db.Event) {
	papermetrics.RecordHackStage(r.Context(), ev.ID, ev.Stage, ev.UpdatedAt)
}

// NewHackHandlerForTest builds a *HackHandler with a specific archive-link
// key and limits, bypassing NewHackHandler's random key and edition-derived
// limits. Test hook (cleanup 3.2 slice 3): opaque_tokens_test.go
// (internal/handler) checks that export/preview/file and hack archive
// tokens never cross-authenticate even when minted from the same underlying
// key — only the domain string tells them apart — so it needs the exact
// same key the SiteHandler side of that test uses.
func NewHackHandlerForTest(archiveLinkKey []byte, limits config.Limits) *HackHandler {
	return &HackHandler{archiveLinkKey: archiveLinkKey, limits: limits}
}

// NewHackHandler builds the hosted-events API. publicBaseURL is the apex
// (join/judge/manage links); siteDomain is the event host parent.
func NewHackHandler(database *sql.DB, publicBaseURL, siteDomain string, editions ...*edition.Edition) *HackHandler {
	var ed *edition.Edition
	if len(editions) > 0 {
		ed = editions[0]
	}
	lim := ed.Limits()
	h := &HackHandler{
		limits:             lim,
		siteArchiveDefault: defaultSiteArchiveSize,
		database:           database,
		publicBaseURL:      strings.TrimRight(publicBaseURL, "/"),
		siteDomain:         strings.Trim(strings.ToLower(siteDomain), "."),
		codesIP:            httpx.NewRateLimiter(float64(lim.RateEventCodesIP.Burst), lim.RateEventCodesIP.PerSecond()),
		codesUser:          httpx.NewRateLimiter(float64(lim.RateEventCodesUser.Burst), lim.RateEventCodesUser.PerSecond()),
		namesUser:          httpx.NewRateLimiter(float64(lim.RateEventNamesUser.Burst), lim.RateEventNamesUser.PerSecond()),
		entryWrites:        httpx.NewRateLimiter(30, 0.5),
		shotWrites:         httpx.NewRateLimiter(5, 0.1),
		applyIP:            httpx.NewRateLimiter(float64(lim.RateEventApplyIP.Burst), lim.RateEventApplyIP.PerSecond()),
		applyUser:          httpx.NewRateLimiter(float64(lim.RateEventApplyUser.Burst), lim.RateEventApplyUser.PerSecond()),
		archiveLinkKey:     newHackArchiveKey(),
		events:             events.New(database),
	}
	h.entries = entries.New(h.database, h.entrySiteLive)
	return h
}

// defaultSiteArchiveSize matches handler's own starting maxSiteArchiveSize
// (site.go) before any MAX_ARCHIVE_MB env override; SetSiteArchiveDefault
// overrides it with the instance's real value.
const defaultSiteArchiveSize int64 = 100 << 20

// SetSiteArchiveDefault records the instance's default per-site size cap
// (handler.SiteLimit(), wired once from main after SetSiteLimit has applied
// any MAX_ARCHIVE_MB env override) for siteLimitFor below.
func (h *HackHandler) SetSiteArchiveDefault(bytes int64) {
	if bytes > 0 {
		h.siteArchiveDefault = bytes
	}
}

// newHackArchiveKey is handler's newExportKey (exportlink.go), duplicated
// here so HackHandler's own package needs no cross-package call once it
// moves: 32 random bytes, panics only if the system has no randomness.
func newHackArchiveKey() []byte {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		panic("hack archive links: no randomness: " + err.Error())
	}
	return key
}

// siteLimitFor is the largest site this account may deploy, in bytes: its
// MAX_ARCHIVE_MB_OVERRIDES entry (matched against its current handle and
// then its earlier ones), else siteArchiveDefault. Mirrors handler's own
// limitSet.siteLimitFor (limits.go) so the values read stay identical;
// duplicated here (not shared) because that method is unexported in
// another package once this type moves to internal/hackhttp.
func (h *HackHandler) siteLimitFor(ctx context.Context, database *sql.DB, user *db.User) (limit int64, override bool) {
	if h.limits.MaxArchiveOverrides == "" || user == nil {
		return h.siteArchiveDefault, false
	}
	if mb, ok := h.limits.ArchiveOverrideMB(hackAccountHandles(ctx, database, user)...); ok {
		return int64(mb) << 20, true
	}
	return h.siteArchiveDefault, false
}

// hackAccountHandles is handler's own accountHandles (limits.go), duplicated
// for the same reason as newHackArchiveKey above.
func hackAccountHandles(ctx context.Context, database *sql.DB, user *db.User) []string {
	handles := []string{user.Handle.String}
	if aliases, err := db.ListHandleAliases(ctx, database, user.ID); err == nil {
		handles = append(handles, aliases...)
	}
	return handles
}

// exportLinkTTL is the shared EXPORT_LINK_TTL_MINUTES knob, which also
// controls event archive links (hack_exportlink.go).
func (h *HackHandler) exportLinkTTL() time.Duration { return h.limits.ExportLinkTTL }

// idleReplyTo is the Reply-To address on the one archive-removal warning
// (hack_cleanup.go).
func (h *HackHandler) idleReplyTo() string { return h.limits.IdleReplyTo }

// StartCleanup evicts idle rate-limit buckets; call once from the server.
func (h *HackHandler) StartCleanup() {
	for _, l := range []*httpx.RateLimiter{h.codesIP, h.codesUser, h.namesUser, h.entryWrites, h.shotWrites, h.applyIP, h.applyUser} {
		l.StartCleanup(10*time.Minute, 30*time.Minute)
	}
}

// Register adds every /v1/hack/... and /v1/admin/hack/... route in the spec.
// authMW is auth.Middleware (X-API-Key).
func (h *HackHandler) Register(mux *http.ServeMux, authMW func(http.Handler) http.Handler) {
	wrap := func(fn http.HandlerFunc) http.Handler {
		return authMW(http.HandlerFunc(fn))
	}
	mux.Handle("GET /v1/hack/directory", http.HandlerFunc(h.getDirectory))
	mux.HandleFunc("GET /v1/hack/events/{slug}/public", h.PublicEventJSON)
	mux.HandleFunc("OPTIONS /v1/hack/events/{slug}/public", h.PublicEventJSON)
	h.registerEventIcon(mux, wrap)
	mux.Handle("GET /v1/hack/events/{slug}/voting", wrap(h.getVotingSettings))
	mux.Handle("PUT /v1/hack/events/{slug}/voting", wrap(h.putVotingSettings))
	mux.Handle("PATCH /v1/hack/events/{slug}/directory", wrap(h.patchDirectoryListed))
	mux.Handle("GET /v1/hack/events/{slug}/vote", http.HandlerFunc(h.getVote))
	mux.Handle("GET /v1/hack/events/{slug}/my-vote", wrap(h.getMyVote))
	mux.Handle("PUT /v1/hack/events/{slug}/vote", wrap(h.putVote))
	mux.Handle("GET /v1/hack/events", wrap(h.listEvents))
	mux.Handle("POST /v1/hack/events", wrap(h.createEvent))
	mux.Handle("GET /v1/hack/names/{slug}", wrap(h.checkName))
	mux.Handle("GET /v1/hack/events/{slug}", wrap(h.getEvent))
	mux.Handle("PATCH /v1/hack/events/{slug}", wrap(h.patchEvent))
	mux.Handle("POST /v1/hack/events/{slug}/stage", wrap(h.setStage))
	mux.Handle("POST /v1/hack/events/{slug}/codes/{kind}", wrap(h.regenCode))
	mux.Handle("DELETE /v1/hack/events/{slug}", wrap(h.deleteEvent))
	mux.Handle("GET /v1/hack/events/{slug}/people", wrap(h.listPeople))
	mux.Handle("DELETE /v1/hack/events/{slug}/people/{user_id}", wrap(h.removePerson))
	mux.Handle("GET /v1/hack/events/{slug}/teams", wrap(h.listTeams))
	mux.Handle("GET /v1/hack/events/{slug}/teams/open", wrap(h.listOpenTeams))
	mux.Handle("PUT /v1/hack/events/{slug}/team/finding", wrap(h.setTeamFinding)) // deprecated, one release
	mux.Handle("PUT /v1/hack/events/{slug}/me/looking", wrap(h.setLooking))
	mux.Handle("POST /v1/hack/events/{slug}/teams/{team}/requests", wrap(h.createTeamRequest))
	mux.Handle("POST /v1/hack/events/{slug}/team/invites", wrap(h.createTeamRequest))
	mux.Handle("POST /v1/hack/events/{slug}/requests/{id}/{action}", wrap(h.resolveTeamRequest))
	mux.Handle("DELETE /v1/hack/events/{slug}/requests/{id}", wrap(h.resolveTeamRequest))
	mux.Handle("DELETE /v1/hack/events/{slug}/team/members/{user_id}", wrap(h.removeTeamMember))
	mux.Handle("POST /v1/hack/events/{slug}/teams/fill", wrap(h.fillTeams))
	mux.Handle("POST /v1/hack/events/{slug}/teams", wrap(h.createTeam))
	mux.Handle("POST /v1/hack/events/{slug}/teams/join", wrap(h.joinTeam))
	mux.Handle("POST /v1/hack/events/{slug}/teams/leave", wrap(h.leaveTeam))
	mux.Handle("POST /v1/hack/events/{slug}/teams/{team}/members", wrap(h.moveMember))
	mux.Handle("DELETE /v1/hack/events/{slug}/teams/{team}/members/{user_id}", wrap(h.removeTeamMember))
	mux.Handle("DELETE /v1/hack/events/{slug}/teams/{team}", wrap(h.deleteTeam))
	// Teams redesign (owner 2026-10-31): team admins, locking, the team link.
	mux.Handle("PATCH /v1/hack/events/{slug}/team", wrap(h.patchTeam))
	mux.Handle("PUT /v1/hack/events/{slug}/team/lock", wrap(h.setMyTeamLock))
	mux.Handle("PUT /v1/hack/events/{slug}/team/members/{user_id}/admin", wrap(h.setMyTeammateAdmin))
	mux.Handle("POST /v1/hack/events/{slug}/team/link", wrap(h.newMyTeamLink))
	mux.Handle("PUT /v1/hack/events/{slug}/teams/{team}/lock", wrap(h.lockOrganiserTeam))
	mux.Handle("PUT /v1/hack/events/{slug}/teams/{team}/members/{user_id}/admin", wrap(h.setOrganiserTeamAdmin))
	mux.Handle("POST /v1/hack/events/{slug}/teams/{team}/link", wrap(h.newOrganiserTeamLink))
	mux.Handle("GET /v1/hack/team-links/{code}", http.HandlerFunc(h.getTeamLink))
	mux.Handle("GET /v1/hack/join/{code}", http.HandlerFunc(h.getJoin))
	mux.Handle("POST /v1/hack/join/{code}", wrap(h.postJoin))
	mux.Handle("GET /v1/hack/judge/{code}", http.HandlerFunc(h.getJudge))
	mux.Handle("POST /v1/hack/judge/{code}", wrap(h.postJudge))
	mux.Handle("GET /v1/admin/hack/events", wrap(h.adminListEvents))
	mux.Handle("POST /v1/admin/hack/events/{slug}/takedown", wrap(h.adminTakeDown))
	mux.Handle("POST /v1/admin/hack/events/{slug}/keep-sites", wrap(h.adminKeepSites))
	mux.Handle("POST /v1/admin/hack/events/{slug}/restore", wrap(h.adminRestore))
	mux.Handle("DELETE /v1/admin/hack/events/{slug}", wrap(h.adminDelete))
	h.registerTeamSites(mux, wrap)
	h.registerEntries(mux, wrap)
	h.registerJudging(mux, wrap)
	h.registerAdministration(mux, wrap)
	h.registerContent(mux, wrap)
	h.registerRegistration(mux, wrap)
	h.registerEventWebsite(mux, wrap)
	h.registerHackPreferences(mux, wrap)
	h.registerLeave(mux, wrap)
	h.registerNotices(mux, wrap)
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
	v, err := h.judgeHandle(ctx, name)
	if err != nil {
		return false, err
	}
	if v.Status != 0 {
		if v.Code == "invalid_handle" {
			return false, nil
		}
		return true, nil
	}
	taken, err := db.EventSlugTaken(ctx, h.database, name)
	return taken, err
}

// EventURL is https://<slug>.<siteDomain>/
func (h *HackHandler) EventURL(slug string) string {
	return "https://" + slug + "." + h.siteDomain + "/"
}

func (h *HackHandler) joinURL(code string) string {
	return h.publicBaseURL + "/join/" + code
}

func (h *HackHandler) judgeURL(code string) string {
	return h.publicBaseURL + "/judge/" + code
}

// teamLinkURL is https://<hack host>/team/<code>: instant join, never event
// admission (checked at GET /v1/hack/team-links/{code} and at join).
func (h *HackHandler) teamLinkURL(code string) string {
	return h.publicBaseURL + "/team/" + code
}

func (h *HackHandler) manageURL(slug string) string {
	return h.publicBaseURL + "/e/" + slug + "/manage"
}

func eventNameReserved(name string) bool {
	return extraEventReserved[name] || sites.LabelReservedForNew(name)
}

// rateLimited meters code lookups and joins: per network address (roomy,
// since a venue shares one) and, when signed in, per account.
//
// A signed-in caller is metered by account only: an account costs an email
// round trip, and a venue's shared address must not let one stranger's
// anonymous lookups lock everyone there out of joining.
func (h *HackHandler) rateLimited(w http.ResponseWriter, r *http.Request, userID string) bool {
	if userID == "" && !h.codesIP.Allow(httpx.ClientIP(r)) {
		httpx.WriteJSON(w, http.StatusTooManyRequests, httpx.ErrorResponse{Error: "rate limit exceeded, slow down", Code: "rate_limited"})
		return true
	}
	if userID != "" && !h.codesUser.Allow(userID) {
		httpx.WriteJSON(w, http.StatusTooManyRequests, httpx.ErrorResponse{Error: "rate limit exceeded, slow down", Code: "rate_limited"})
		return true
	}
	return false
}

// applyLimited meters POST apply: a signed-in caller is metered by both its
// account and its network address (RATE_LIMIT_EVENT_APPLY_USER/_IP), so one
// stranger cannot burn through many accounts from one address either.
func (h *HackHandler) applyLimited(w http.ResponseWriter, r *http.Request, userID string) bool {
	if !h.applyIP.Allow(httpx.ClientIP(r)) {
		httpx.WriteJSON(w, http.StatusTooManyRequests, httpx.ErrorResponse{Error: "rate limit exceeded, slow down", Code: "rate_limited"})
		return true
	}
	if !h.applyUser.Allow(userID) {
		httpx.WriteJSON(w, http.StatusTooManyRequests, httpx.ErrorResponse{Error: "rate limit exceeded, slow down", Code: "rate_limited"})
		return true
	}
	return false
}

func decodeHackJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, hackMaxBody))
	if err := dec.Decode(dst); err != nil {
		if errors.Is(err, io.EOF) {
			httpx.WriteJSON(w, http.StatusBadRequest, httpx.ErrorResponse{Error: "JSON body required", Code: "invalid_request"})
			return false
		}
		httpx.WriteJSON(w, http.StatusBadRequest, httpx.ErrorResponse{Error: "invalid JSON body", Code: "invalid_request"})
		return false
	}
	return true
}

func hackNeedUser(w http.ResponseWriter, r *http.Request) *db.User {
	user := auth.GetUser(r.Context())
	if user == nil {
		httpx.WriteJSON(w, http.StatusUnauthorized, httpx.ErrorResponse{Error: "unauthorized"})
		return nil
	}
	return user
}

// writeHackErr and writeInternal moved to internal/httpx (httpx.WriteError,
// httpx.WriteInternal); see internal/handler/httpx_forward.go. (cleanup 3.2,
// slice 1a)

func writeEventNotFound(w http.ResponseWriter) {
	httpx.WriteError(w, http.StatusNotFound, "event_not_found", "event not found")
}

type hackAccess struct {
	event  db.Event
	member db.EventMember
	user   *db.User
	admin  bool
}

// loadMember loads the event and the caller's membership. Non-members get 404
// event_not_found (including a wrong-role member on a role-gated route). The
// admin key may read any event it is not in as organiser (admin is true only
// then); writes need membership. A platform admin who joined an event is an
// ordinary member there, with no admin bypass. write is true for mutating routes.
func (h *HackHandler) loadMember(w http.ResponseWriter, r *http.Request, slug string, write bool, roles ...string) (hackAccess, bool) {
	var z hackAccess
	user := hackNeedUser(w, r)
	if user == nil {
		return z, false
	}
	slug = strings.ToLower(strings.TrimSpace(slug))
	ev, err := db.GetEventBySlug(r.Context(), h.database, slug)
	if err == nil {
		paperHackEvent(r, ev)
	}
	if errors.Is(err, sql.ErrNoRows) {
		writeEventNotFound(w)
		return z, false
	}
	if err != nil {
		httpx.WriteInternal(w)
		return z, false
	}
	m, err := db.GetEventMember(r.Context(), h.database, ev.ID, user.ID)
	if err == nil {
		paperHackKnownMember(r, ev, user.ID)
	}
	if errors.Is(err, sql.ErrNoRows) {
		if user.IsAdmin && !write {
			z = hackAccess{event: ev, member: db.EventMember{EventID: ev.ID, UserID: user.ID, Role: "organiser"}, user: user, admin: true}
			return z, true
		}
		writeEventNotFound(w)
		return z, false
	}
	if err != nil {
		httpx.WriteInternal(w)
		return z, false
	}
	if m.Role == "participant" && (write || len(roles) > 0) {
		status, err := db.MemberApprovalStatus(r.Context(), h.database, ev.ID, user.ID)
		if err != nil {
			httpx.WriteInternal(w)
			return z, false
		}
		if status != "approved" {
			httpx.WriteError(w, http.StatusForbidden, "approval_"+status, "your application is "+status)
			return z, false
		}
	}
	if len(roles) > 0 {
		ok := false
		for _, role := range roles {
			if m.Role == role {
				ok = true
				break
			}
		}
		if !ok {
			writeEventNotFound(w)
			return z, false
		}
	}
	if write && ev.TakenDown() {
		httpx.WriteError(w, http.StatusForbidden, "event_taken_down", "this event has been taken down")
		return z, false
	}
	return hackAccess{event: ev, member: m, user: user}, true
}

func (h *HackHandler) checkName(w http.ResponseWriter, r *http.Request) {
	user := hackNeedUser(w, r)
	if user == nil {
		return
	}
	if !h.namesUser.Allow(user.ID) {
		httpx.WriteJSON(w, http.StatusTooManyRequests, httpx.ErrorResponse{Error: "rate limit exceeded, slow down", Code: "rate_limited"})
		return
	}
	slug := strings.ToLower(strings.TrimSpace(r.PathValue("slug")))
	out := map[string]any{"address": h.EventURL(slug)}
	code, errMsg, err := h.slugVerdict(r.Context(), slug)
	if err != nil {
		if errors.Is(err, errNameCheckUnavailable) {
			httpx.WriteError(w, http.StatusServiceUnavailable, "name_check_unavailable", "could not check whether this name is free; try again")
			return
		}
		httpx.WriteInternal(w)
		return
	}
	if code != "" {
		out["available"] = false
		out["code"] = code
		out["error"] = errMsg
		httpx.WriteJSON(w, http.StatusOK, out)
		return
	}
	out["available"] = true
	httpx.WriteJSON(w, http.StatusOK, out)
}

var errNameCheckUnavailable = errors.New("name check unavailable")

// slugVerdict is the create/check result for an event slug. Empty code means free.
func (h *HackHandler) slugVerdict(ctx context.Context, slug string) (code, errMsg string, err error) {
	if utf8.RuneCountInString(slug) < 3 || !sites.VisitorHandle.MatchString(slug) || !sites.HandleIsLabel(slug) {
		return "invalid_name", "use 3 to 39 lowercase letters, numbers or hyphens, not starting or ending with a hyphen", nil
	}
	if eventNameReserved(slug) {
		return "name_reserved", "that name is reserved", nil
	}
	v, err := h.judgeHandle(ctx, slug)
	if err != nil {
		return "", "", err
	}
	if v.Status != 0 {
		switch v.Code {
		case "invalid_handle":
			return "invalid_name", "use 3 to 39 lowercase letters, numbers or hyphens, not starting or ending with a hyphen", nil
		case "handle_reserved":
			return "name_reserved", "that name is reserved", nil
		default:
			return "name_taken", "that name is already in use", nil
		}
	}
	taken, err := db.EventSlugTaken(ctx, h.database, slug)
	if err != nil {
		return "", "", err
	}
	if taken {
		return "name_taken", "that name is already in use", nil
	}
	if h.namePeer != nil {
		peerTaken, err := h.namePeer(ctx, slug)
		if err != nil {
			return "", "", errNameCheckUnavailable
		}
		if peerTaken {
			return "name_taken", "that name is already in use", nil
		}
	}
	return "", "", nil
}

func (h *HackHandler) instanceFull(ctx context.Context) (bool, error) {
	budgetGB := h.limits.HackInstanceBudgetGB
	if budgetGB <= 0 {
		return false, nil
	}
	var used int64
	if h.usageFn != nil {
		var err error
		used, err = h.usageFn(ctx)
		if err != nil {
			return true, err
		}
	}
	limit := int64(budgetGB) * 1024 * 1024 * 1024 * 80 / 100
	return used >= limit, nil
}

type createEventReq struct {
	Slug                 string `json:"slug"`
	Title                string `json:"title"`
	OrganiserName        string `json:"organiser_name"`
	Organisation         string `json:"organisation"`
	ContactEmail         string `json:"contact_email"`
	Purpose              string `json:"purpose"`
	ExpectedParticipants *int   `json:"expected_participants"`
	StartsAt             string `json:"starts_at"`
	EndsAt               string `json:"ends_at"`
	TimeZone             string `json:"time_zone"`
}

func (h *HackHandler) createEvent(w http.ResponseWriter, r *http.Request) {
	user := hackNeedUser(w, r)
	if user == nil {
		return
	}
	var req createEventReq
	if !decodeHackJSON(w, r, &req) {
		return
	}
	slug := strings.ToLower(strings.TrimSpace(req.Slug))
	code, errMsg, err := h.slugVerdict(r.Context(), slug)
	if err != nil {
		if errors.Is(err, errNameCheckUnavailable) {
			httpx.WriteError(w, http.StatusServiceUnavailable, "name_check_unavailable", "could not check whether this name is free; try again")
			return
		}
		httpx.WriteInternal(w)
		return
	}
	if code != "" {
		status := http.StatusBadRequest
		if code == "name_taken" {
			status = http.StatusConflict
		}
		httpx.WriteError(w, status, code, errMsg)
		return
	}
	title, ok := checkHackLine(w, req.Title, "title", 1, 120)
	if !ok {
		return
	}
	organiserName, ok := checkHackLine(w, req.OrganiserName, "organiser_name", 1, 100)
	if !ok {
		return
	}
	organisation, ok := checkHackLine(w, req.Organisation, "organisation", 0, 120)
	if !ok {
		return
	}
	contact, ok := checkContactEmail(w, req.ContactEmail)
	if !ok {
		return
	}
	purpose, ok := checkHackText(w, req.Purpose, "purpose", 1, 1000)
	if !ok {
		return
	}
	if req.ExpectedParticipants == nil || *req.ExpectedParticipants < 1 || *req.ExpectedParticipants > 100000 {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_expected_participants", "expected_participants must be between 1 and 100000")
		return
	}
	tz := strings.TrimSpace(req.TimeZone)
	if tz == "" {
		tz = "UTC"
	}
	loc, ok := checkTimeZone(w, tz)
	if !ok {
		return
	}
	startsAt, ok := parseEventTime(w, req.StartsAt, loc, "starts_at", true)
	if !ok {
		return
	}
	var endsAt sql.NullTime
	if strings.TrimSpace(req.EndsAt) != "" {
		t, ok := parseEventTime(w, req.EndsAt, loc, "ends_at", true)
		if !ok {
			return
		}
		endsAt = t
		if t.Valid && startsAt.Valid && t.Time.Before(startsAt.Time) {
			httpx.WriteError(w, http.StatusBadRequest, "invalid_ends_at", "ends_at must not be before starts_at")
			return
		}
	}
	full, err := h.instanceFull(r.Context())
	if err != nil || full {
		httpx.WriteError(w, http.StatusInsufficientStorage, "instance_full", "this instance is at capacity")
		return
	}

	ctx := r.Context()
	ev, member, err := h.eventsSvc().CreateEvent(ctx, user.ID, slug, title, organiserName, organisation, contact, purpose, tz, *req.ExpectedParticipants, startsAt, endsAt, h.limits.EventCreatePerDay, h.limits.EventMaxActivePerOrganiser, h.limits.EventTeamSizeDefault, hackJoinCodeLen, hackJudgeCodeLen)
	if err != nil {
		writeEventsError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, h.eventView(ctx, ev, member, true))
}

func (h *HackHandler) listEvents(w http.ResponseWriter, r *http.Request) {
	user := hackNeedUser(w, r)
	if user == nil {
		return
	}
	events, roles, err := db.ListEventsForUser(r.Context(), h.database, user.ID)
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	out := make([]map[string]any, 0, len(events))
	for i, ev := range events {
		item := map[string]any{
			"slug":         ev.Slug,
			"title":        ev.Title,
			"stage":        ev.Stage,
			"role":         roles[i],
			"url":          h.EventURL(ev.Slug),
			"icon_url":     h.iconURL(ev),
			"accent_color": ev.AccentColor, "color": hackEventAccent(ev),
			"manage_url": h.manageURL(ev.Slug),
			"starts_at":  rfc3339UTC(ev.StartsAt),
			"ends_at":    rfc3339UTC(ev.EndsAt),
			"taken_down": ev.TakenDown(),
			"time_zone":  ev.TimeZone,
		}
		if roles[i] == "participant" {
			if status, err := db.MemberApprovalStatus(r.Context(), h.database, ev.ID, user.ID); err == nil {
				item["approval_status"] = status
			}
		}
		out = append(out, item)
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

func (h *HackHandler) getEvent(w http.ResponseWriter, r *http.Request) {
	a, ok := h.loadMember(w, r, r.PathValue("slug"), false)
	if !ok {
		return
	}
	h.pinDue(r.Context(), a.event.ID) // every read of pins pins first
	view := h.eventView(r.Context(), a.event, a.member, a.member.Role == "organiser" || a.admin)
	if a.admin && a.member.JoinedAt.IsZero() {
		// The platform admin reading an event it is not in: every write here
		// is refused, so the page shows it read-only.
		view["admin_view"] = true
	}
	httpx.WriteJSON(w, http.StatusOK, view)
}

func (h *HackHandler) eventView(ctx context.Context, ev db.Event, member db.EventMember, organiser bool) map[string]any {
	// The code of conduct is the default text plus the organiser's own.
	cocText := ev.CocText
	tracks, _ := db.ListEventTracks(ctx, h.database, ev.ID)
	resultState, _ := db.GetEventResults(ctx, h.database, ev.ID)
	body := map[string]any{
		"event": map[string]any{
			"slug":              ev.Slug,
			"title":             ev.Title,
			"tagline":           ev.Tagline,
			"about":             ev.About,
			"rules":             ev.Rules,
			"prizes":            ev.Prizes,
			"coc_text":          cocText,
			"coc_default":       HackDefaultCoC,
			"stage":             ev.Stage,
			"time_zone":         ev.TimeZone,
			"starts_at":         rfc3339UTC(ev.StartsAt),
			"ends_at":           rfc3339UTC(ev.EndsAt),
			"team_size_max":     ev.TeamSizeMax,
			"team_finding":      ev.TeamFinding,
			"applications_open": ev.ApplicationsOpen,
			"url":               h.EventURL(ev.Slug),
			"builtin_url":       h.publicBaseURL + "/e/" + ev.Slug,
			"website_mode":      ev.WebsiteMode,
			"icon_url":          h.iconURL(ev),
			"accent_color":      ev.AccentColor, "color": hackEventAccent(ev),
			"icon_media_type":      ev.IconMediaType,
			"taken_down":           ev.TakenDown(),
			"results_visibility":   ev.ResultsVisibility,
			"submission_deadline":  rfc3339UTC(ev.SubmissionDeadline),
			"entry_required":       entryRequiredList(ev.EntryRequired),
			"gallery_open":         ev.GalleryOpen,
			"team_sites_ready":     h.teamSitesReady(ev.Slug),
			"stages_offered":       hackStagesOfferedList,
			"judging_locked_at":    rfc3339UTC(ev.JudgingLockedAt),
			"scoring_open":         h.scoringStage(ctx, ev),
			"results_published":    resultState.Published,
			"results_published_at": rfc3339UTC(resultState.PublishedAt),
			"judging_lock_reason":  ev.JudgingLockReason,
			"tracks":               tracks,
		},
		"role": member.Role,
		"me":   h.meView(ctx, ev, member),
		// What waits on this person here (hack_notices.go): the sidebar
		// badges, Overview's Needs you card and hack_get_team_status.
		"needs_you": h.needsYou(ctx, ev, member),
	}
	// Newest first, for the participant's client-side unread badge on
	// Announcements (audit 2026-11-02): no read state is kept on the server.
	if times, err := db.ListAnnouncementTimes(ctx, h.database, ev.ID); err == nil {
		body["event"].(map[string]any)["announcement_times"] = times
	}
	if organiser {
		participants, teams, _, _ := db.CountEventMembers(ctx, h.database, ev.ID)
		judges, _ := db.CountEligibleJudges(ctx, h.database, ev.ID)
		onNone, _ := db.CountOnNoTeam(ctx, h.database, ev.ID)
		emailable, _ := db.CountEmailableParticipants(ctx, h.database, ev.ID)
		exp := any(nil)
		if ev.ExpectedParticipants.Valid {
			exp = ev.ExpectedParticipants.Int64
		}
		body["organiser"] = map[string]any{
			"organiser_name":        ev.OrganiserName,
			"organisation":          ev.Organisation,
			"contact_email":         ev.ContactEmail,
			"purpose":               ev.Purpose,
			"expected_participants": exp,
			"join_code":             ev.JoinCode,
			"join_url":              h.joinURL(ev.JoinCode),
			"judge_code":            ev.JudgeCode,
			"judge_url":             h.judgeURL(ev.JudgeCode),
			"counts": map[string]int{
				"participants":           participants,
				"teams":                  teams,
				"judges":                 judges,
				"on_no_team":             onNone,
				"emailable_participants": emailable,
			},
		}
		step := nextStepJSON(ev, resultState, participants)
		// cue: a schedule row lining up with this move has come (owner,
		// 2026-11-02). It only words the card. The move still waits for the
		// organiser's press.
		step["cue"] = h.scheduleCue(ctx, ev, step)
		body["next_step"] = step
	}
	if member.Role == "participant" {
		if status, err := db.MemberApprovalStatus(ctx, h.database, ev.ID, member.UserID); err == nil {
			body["me"].(map[string]any)["approval_status"] = status
		}
		// progress (section 4.3): the Home checklist, only once the person
		// has a team -- there is nothing to track before that.
		if member.TeamID.Valid {
			if progress, err := h.memberProgressJSON(ctx, ev, member); err == nil {
				body["progress"] = progress
			} else {
				log.Printf("hack: progress %s: %v", ev.Slug, err)
			}
		}
	}
	return body
}

// memberProgressJSON is eventView's "progress" for a team's participant:
// entryProgressJSON with the team and teammates loaded.
func (h *HackHandler) memberProgressJSON(ctx context.Context, ev db.Event, member db.EventMember) (map[string]any, error) {
	team, err := db.GetEventTeamByID(ctx, h.database, member.TeamID.String)
	if err != nil {
		return nil, err
	}
	st, err := db.TeamWriteStateFor(ctx, h.database, team.ID, false)
	if err != nil {
		return nil, err
	}
	people, err := db.ListTeamPeople(ctx, h.database, team.ID)
	if err != nil {
		return nil, err
	}
	var teammates []string
	for _, p := range people {
		if p.UserID != member.UserID {
			teammates = append(teammates, p.DisplayName)
		}
	}
	return h.entryProgressJSON(ctx, ev, team, teammates, st)
}

// nextStepJSON is the Overview's one forward move: the server computes it so
// the website and AI apps agree (parity rule 2026-10-27). kind is "stage"
// (set stage to the given stage), "publish_results" (POST .../results/publish
// {}) or "none" (archived: no button). blocked is nil or
// "archive_needs_participants" (End with nobody in the event): the button
// still shows, disabled, pointing to Settings -> Delete. eventView adds cue
// (hackScheduleCue): the schedule row that lines up with this move once its
// time has come, or nil.
func nextStepJSON(ev db.Event, results db.EventResultsRow, participants int) map[string]any {
	step := func(stage, label string, deadlinePassed bool, blocked any) map[string]any {
		return map[string]any{"kind": "stage", "stage": stage, "label": label, "deadline_passed": deadlinePassed, "blocked": blocked}
	}
	switch ev.Stage {
	case "draft":
		return step("open", "Open sign-up", false, nil)
	case "open":
		return step("building", "Start building", false, nil)
	case "building":
		deadlinePassed := ev.SubmissionDeadline.Valid && !ev.SubmissionDeadline.Time.After(time.Now())
		return step("closed", "Close entries", deadlinePassed, nil)
	case "closed":
		return step("judging", "Start judging", false, nil)
	case "judging":
		return step("results", "Finish judging", false, nil)
	case "results":
		if !results.Published {
			return map[string]any{"kind": "publish_results", "stage": "", "label": "Publish results", "deadline_passed": false, "blocked": nil}
		}
		var blocked any
		if participants == 0 {
			blocked = "archive_needs_participants"
		}
		return step("archived", "End event", false, blocked)
	default: // archived
		return map[string]any{"kind": "none", "stage": "", "label": "", "deadline_passed": false, "blocked": nil}
	}
}

func (h *HackHandler) meView(ctx context.Context, ev db.Event, member db.EventMember) map[string]any {
	me := map[string]any{
		// user_id: the signed-in person's own ID, same value the people
		// list already shows for everyone else on this event -- so
		// exposing it here reveals nothing a member couldn't already read
		// about themselves. Added for the MCP organiser app's own "(you)"
		// labels (hack_apps_organiser.go's hackShowOrganiserViewOnly),
		// which need to compare an event member's user_id against the
		// caller's own without a dedicated identity tool.
		"user_id":         member.UserID,
		"display_name":    member.DisplayName,
		"coc_accepted_at": rfc3339UTC(member.CocAcceptedAt),
		"team":            nil,
	}
	if member.Role == "participant" {
		me["team_key"] = nil
		if k, err := db.GetTeamKey(ctx, h.database, ev.ID, member.UserID); err == nil {
			me["team_key"] = map[string]any{
				"last4":        k.Last4,
				"created_at":   rfc3339Time(k.CreatedAt),
				"last_used_at": rfc3339Ptr(k.LastUsedAt),
			}
		}
	}
	if member.Role != "participant" || !member.TeamID.Valid {
		if member.Role == "participant" {
			var notice string
			if err := h.database.QueryRowContext(ctx, `SELECT team_removal_notice FROM event_members WHERE event_id=$1 AND user_id=$2`, ev.ID, member.UserID).Scan(&notice); err == nil {
				me["team_removal_notice"] = notice
			}
		}
		return me
	}
	team, err := db.GetEventTeamByID(ctx, h.database, member.TeamID.String)
	if err != nil {
		return me
	}
	people, err := db.ListTeamPeople(ctx, h.database, team.ID)
	if err != nil {
		return me
	}
	members := make([]map[string]any, 0, len(people))
	var youAdmin bool
	admins := 0
	for _, p := range people {
		you := p.UserID == member.UserID
		if p.Admin {
			admins++
			if you {
				youAdmin = true
			}
		}
		members = append(members, map[string]any{
			"user_id":      p.UserID,
			"display_name": p.DisplayName,
			"you":          you,
			"admin":        p.Admin,
		})
	}
	var lookingFor string
	_ = h.database.QueryRowContext(ctx, `SELECT looking_for FROM event_teams WHERE id = $1`, team.ID).Scan(&lookingFor)
	t := map[string]any{
		"slug":        team.Slug,
		"name":        team.Name,
		"code":        team.Code,
		"link":        h.teamLinkURL(team.Code),
		"admin":       youAdmin,
		"locked":      team.Locked(),
		"kept":        team.KeptAfterLeave,
		"looking_for": lookingFor,
		"members":     members,
		"site":        h.teamSiteJSON(ctx, ev, team),
	}
	// site.connection: the Website screen's state strip, mirrored for MCP
	// (hack_get_team_status) -- the caller's own connector/key status only
	// (build plan §4.4). Best-effort: a lookup failure here must not break
	// the whole team-status read.
	if site, ok := t["site"].(map[string]any); ok {
		if conn, err := h.connectionStatusJSON(ctx, ev.ID, team, member.UserID); err == nil {
			site["connection"] = conn
		}
	}
	// admin_successor: shown only when the caller is the team's one admin and
	// others remain, so they know who takes over if they leave or step down
	// (EnsureTeamAdmin's own tie-break: longest-standing, then user_id;
	// NULLS LAST, so a row with no team_joined_at never jumps the queue --
	// Grok security review, 2026-11-01).
	if youAdmin && admins == 1 {
		var successor string
		if err := h.database.QueryRowContext(ctx, `
			SELECT display_name FROM event_members WHERE team_id = $1 AND user_id <> $2
			 ORDER BY team_joined_at NULLS LAST, user_id LIMIT 1`, team.ID, member.UserID).Scan(&successor); err == nil {
			t["admin_successor"] = successor
		}
	}
	if youAdmin {
		var pending int
		_ = h.database.QueryRowContext(ctx, `SELECT COUNT(*) FROM event_team_requests WHERE team_id = $1 AND kind = 'request'`, team.ID).Scan(&pending)
		t["pending_requests"] = pending
	}
	if track, err := db.TeamTrack(ctx, h.database, team.ID); err == nil {
		t["track"] = track
	}
	h.teamDeadlineJSON(ctx, team, t)
	me["team"] = t
	return me
}

type patchEventReq struct {
	AccentColor          *string `json:"accent_color"`
	Title                *string `json:"title"`
	Tagline              *string `json:"tagline"`
	About                *string `json:"about"`
	Rules                *string `json:"rules"`
	Prizes               *string `json:"prizes"`
	CocText              *string `json:"coc_text"`
	TimeZone             *string `json:"time_zone"`
	StartsAt             *string `json:"starts_at"`
	EndsAt               *string `json:"ends_at"`
	TeamFinding          *bool   `json:"team_finding"`
	ApplicationsOpen     *bool   `json:"applications_open"`
	TeamSizeMax          *int    `json:"team_size_max"`
	OrganiserName        *string `json:"organiser_name"`
	Organisation         *string `json:"organisation"`
	ContactEmail         *string `json:"contact_email"`
	Purpose              *string `json:"purpose"`
	ExpectedParticipants *int    `json:"expected_participants"`
	// M2
	SubmissionDeadline *string   `json:"submission_deadline"`
	EntryRequired      *[]string `json:"entry_required"`
	GalleryOpen        *bool     `json:"gallery_open"`
}

func (h *HackHandler) patchEvent(w http.ResponseWriter, r *http.Request) {
	a, ok := h.loadMember(w, r, r.PathValue("slug"), true, "organiser")
	if !ok {
		return
	}
	var req patchEventReq
	if !decodeHackJSON(w, r, &req) {
		return
	}
	deadlineChanged, entryRequiredChanged := false, false
	updated, err := h.eventsSvc().PatchEvent(r.Context(), a.event.ID, func(ev *db.Event) (bool, bool, error) {
		if req.AccentColor != nil {
			if !hackColorValid(*req.AccentColor) {
				return false, false, &events.Error{Code: "invalid_accent_color", Message: "choose a crayon colour or an empty string for automatic"}
			}
			ev.AccentColor = *req.AccentColor
		}
		if req.Title != nil {
			s, validationErr := checkHackLineValue(*req.Title, "title", 1, 120)
			if validationErr != nil {
				return false, false, validationErr
			}
			ev.Title = s
		}
		if req.Tagline != nil {
			s, validationErr := checkHackLineValue(*req.Tagline, "tagline", 0, 160)
			if validationErr != nil {
				return false, false, validationErr
			}
			ev.Tagline = s
		}
		if req.About != nil {
			s, validationErr := checkHackTextValue(*req.About, "about", 0, 10000)
			if validationErr != nil {
				return false, false, validationErr
			}
			ev.About = s
		}
		if req.Rules != nil {
			s, validationErr := checkHackTextValue(*req.Rules, "rules", 0, 10000)
			if validationErr != nil {
				return false, false, validationErr
			}
			ev.Rules = s
		}
		if req.Prizes != nil {
			s, validationErr := checkHackTextValue(*req.Prizes, "prizes", 0, 5000)
			if validationErr != nil {
				return false, false, validationErr
			}
			ev.Prizes = s
		}
		if req.CocText != nil {
			s, validationErr := checkHackTextValue(*req.CocText, "coc_text", 0, 10000)
			if validationErr != nil {
				return false, false, validationErr
			}
			ev.CocText = s
		}
		tz := ev.TimeZone
		oldZone := ev.TimeZone
		if req.TimeZone != nil {
			tz = strings.TrimSpace(*req.TimeZone)
			if tz == "" {
				tz = "UTC"
			}
			if _, validationErr := checkTimeZoneValue(tz); validationErr != nil {
				return false, false, validationErr
			}
			ev.TimeZone = tz
		}
		loc, err := time.LoadLocation(ev.TimeZone)
		if err != nil {
			return false, false, &events.Error{Code: "invalid_time_zone", Message: "time_zone is not a valid IANA name"}
		}
		// A new zone without new dates keeps the same calendar dates: each
		// stored instant is moved to the same wall-clock time in the new zone.
		if ev.TimeZone != oldZone {
			if old, oerr := time.LoadLocation(oldZone); oerr == nil {
				reanchor := func(t sql.NullTime) sql.NullTime {
					if !t.Valid {
						return t
					}
					o := t.Time.In(old)
					return sql.NullTime{Time: time.Date(o.Year(), o.Month(), o.Day(), o.Hour(), o.Minute(), o.Second(), 0, loc), Valid: true}
				}
				if req.StartsAt == nil {
					ev.StartsAt = reanchor(ev.StartsAt)
				}
				if req.EndsAt == nil {
					ev.EndsAt = reanchor(ev.EndsAt)
				}
			}
		}
		if req.StartsAt != nil {
			t, validationErr := parseEventTimeValue(*req.StartsAt, loc, "starts_at", true)
			if validationErr != nil {
				return false, false, validationErr
			}
			ev.StartsAt = t
		}
		if req.EndsAt != nil {
			if strings.TrimSpace(*req.EndsAt) == "" {
				ev.EndsAt = sql.NullTime{}
			} else {
				t, validationErr := parseEventTimeValue(*req.EndsAt, loc, "ends_at", true)
				if validationErr != nil {
					return false, false, validationErr
				}
				ev.EndsAt = t
			}
		}
		if ev.EndsAt.Valid && ev.StartsAt.Valid && ev.EndsAt.Time.Before(ev.StartsAt.Time) {
			return false, false, &events.Error{Code: "invalid_ends_at", Message: "ends_at must not be before starts_at"}
		}
		if req.TeamFinding != nil {
			ev.TeamFinding = *req.TeamFinding
		}
		if req.ApplicationsOpen != nil {
			if ev.Stage == "archived" {
				return false, false, &events.Error{Code: "event_closed", Message: "an ended event cannot be changed"}
			}
			ev.ApplicationsOpen = *req.ApplicationsOpen
		}
		if req.TeamSizeMax != nil {
			if *req.TeamSizeMax < 1 || *req.TeamSizeMax > 50 {
				return false, false, &events.Error{Code: "invalid_team_size", Message: "team_size_max must be between 1 and 50"}
			}
			ev.TeamSizeMax = *req.TeamSizeMax
		}
		if req.OrganiserName != nil {
			s, validationErr := checkHackLineValue(*req.OrganiserName, "organiser_name", 1, 100)
			if validationErr != nil {
				return false, false, validationErr
			}
			ev.OrganiserName = s
		}
		if req.Organisation != nil {
			s, validationErr := checkHackLineValue(*req.Organisation, "organisation", 0, 120)
			if validationErr != nil {
				return false, false, validationErr
			}
			ev.Organisation = s
		}
		if req.ContactEmail != nil {
			s, validationErr := checkContactEmailValue(*req.ContactEmail)
			if validationErr != nil {
				return false, false, validationErr
			}
			ev.ContactEmail = s
		}
		if req.Purpose != nil {
			s, validationErr := checkHackTextValue(*req.Purpose, "purpose", 1, 1000)
			if validationErr != nil {
				return false, false, validationErr
			}
			ev.Purpose = s
		}
		if req.ExpectedParticipants != nil {
			if *req.ExpectedParticipants < 1 || *req.ExpectedParticipants > 100000 {
				return false, false, &events.Error{Code: "invalid_expected_participants", Message: "expected_participants must be between 1 and 100000"}
			}
			ev.ExpectedParticipants = sql.NullInt64{Int64: int64(*req.ExpectedParticipants), Valid: true}
		}
		// M2: the submission deadline, the entry's required fields, the gallery.
		deadlineChanged = false
		// The deadline is an instant: a new zone never moves it (dates of the
		// event move to keep their calendar day; a deadline that passed must
		// stay passed).
		if req.SubmissionDeadline != nil {
			if strings.TrimSpace(*req.SubmissionDeadline) == "" {
				ev.SubmissionDeadline = sql.NullTime{}
			} else {
				t, validationErr := parseEventTimeValue(*req.SubmissionDeadline, loc, "submission_deadline", true)
				if validationErr != nil {
					return false, false, validationErr
				}
				ev.SubmissionDeadline = t
			}
			deadlineChanged = true
		}
		entryRequiredChanged = false
		if req.EntryRequired != nil {
			fields, validationErr := checkEntryRequiredValue(*req.EntryRequired)
			if validationErr != nil {
				return false, false, validationErr
			}
			ev.EntryRequired = fields
			entryRequiredChanged = true
		}
		if req.GalleryOpen != nil {
			ev.GalleryOpen = *req.GalleryOpen
		}
		return deadlineChanged, entryRequiredChanged, nil
	})
	if err != nil {
		writeEventsError(w, err)
		return
	}
	if deadlineChanged {
		h.pinDue(r.Context(), a.event.ID)
	}
	httpx.WriteJSON(w, http.StatusOK, h.eventView(r.Context(), updated, a.member, true))
}

// submissionsClosedStage: stages after submissions close.
func submissionsClosedStage(stage string) bool { return hackevents.SubmissionsClosedStage(stage) }

// checkEntryRequired validates the organiser's required entry fields and
// returns them in the canonical order.
func checkEntryRequired(w http.ResponseWriter, in []string) ([]string, bool) {
	value, err := checkEntryRequiredValue(in)
	if err != nil {
		writeEventsError(w, err)
		return nil, false
	}
	return value, true
}

func keysInOrder(set map[string]bool) []string {
	out := []string{}
	for _, k := range hackevents.EntryFields {
		if set[k] {
			out = append(out, k)
		}
	}
	return out
}

// entryRequiredList is the required fields in canonical order, never nil.
func entryRequiredList(in []string) []string {
	set := map[string]bool{}
	for _, f := range in {
		set[f] = true
	}
	return keysInOrder(set)
}

func (h *HackHandler) setStage(w http.ResponseWriter, r *http.Request) {
	a, ok := h.loadMember(w, r, r.PathValue("slug"), true, "organiser")
	if !ok {
		return
	}
	var req struct {
		Stage string `json:"stage"`
	}
	if !decodeHackJSON(w, r, &req) {
		return
	}
	stage := strings.TrimSpace(req.Stage)
	if !hackStages[stage] {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_stage", "stage is not a known event stage")
		return
	}
	if !hackStagesOffered[stage] {
		httpx.WriteError(w, http.StatusConflict, "stage_not_available", "that stage is not available yet")
		return
	}
	updated, current, err := h.eventsSvc().SetStage(r.Context(), a.event, stage, h.entries.SettleEventTx)
	if err != nil {
		writeEventsError(w, err)
		return
	}
	if current != updated.Stage {
		paperHackStage(r, updated)
	}
	paperHackTimes(r)
	h.pinDue(r.Context(), updated.ID)
	h.requestEventCert(updated)
	httpx.WriteJSON(w, http.StatusOK, h.eventView(r.Context(), updated, a.member, true))
}

// archiveEventTx archives the event: locks event_teams (as every other
// deadline-affecting write does), closes the submission deadline now --
// unless it already passed, or submissions were already closed (closed,
// judging or results: Nit 3, restoring setStage's original condition,
// `!submissionsClosedStage(current)`, that archiveEventTx dropped when it
// was extracted) -- sets stage to archived, then clears any pin that no
// longer applies (UnpinReopened is a no-op once the stage read back is
// archived). Extracted from setStage so POST .../leave's last_organiser
// "end" case (hack_leave.go) shares it exactly; the caller already holds the
// event row FOR UPDATE and has checked there is at least one participant
// (archive_needs_participants is each caller's own 409). current is the
// event's stage right now, under that same lock.
func (h *HackHandler) archiveEventTx(ctx context.Context, tx *sql.Tx, eventID, current string) (db.Event, error) {
	return h.eventsSvc().ArchiveEventTx(ctx, tx, eventID, current, h.entries.SettleEventTx)
}

func (h *HackHandler) regenCode(w http.ResponseWriter, r *http.Request) {
	a, ok := h.loadMember(w, r, r.PathValue("slug"), true, "organiser")
	if !ok {
		return
	}
	if a.event.Stage == "archived" {
		httpx.WriteError(w, http.StatusConflict, "event_closed", "an ended event cannot be changed")
		return
	}
	kind := r.PathValue("kind")
	var n int
	switch kind {
	case "join":
		n = hackJoinCodeLen
	case "judge":
		n = hackJudgeCodeLen
	default:
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "kind must be join or judge")
		return
	}
	updated, err := h.eventsSvc().RegenerateCode(r.Context(), a.event.ID, kind, n)
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	if kind == "join" {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"code": updated.JoinCode, "url": h.joinURL(updated.JoinCode)})
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"code": updated.JudgeCode, "url": h.judgeURL(updated.JudgeCode)})
}

func (h *HackHandler) deleteEvent(w http.ResponseWriter, r *http.Request) {
	a, ok := h.loadMember(w, r, r.PathValue("slug"), true, "organiser")
	if !ok {
		return
	}
	var req struct {
		Confirm *string `json:"confirm"`
	}
	if r.Body != nil && r.ContentLength != 0 {
		if !decodeHackJSON(w, r, &req) {
			return
		}
	}
	var trash func(context.Context, string, string) error
	if h.sites != nil {
		trash = h.sites.TrashTeamSite
	}
	if err := h.eventsSvc().DeleteEvent(r.Context(), a.event, a.user.ID, req.Confirm, trash); err != nil {
		writeEventsError(w, err)
		return
	}
	h.removeEventFiles(a.event)
	w.WriteHeader(http.StatusNoContent)
}

// deleteEventTx trashes every team site (the same removal as deleting a
// team; the holding-account deletion below then purges its Recently deleted
// sites too, so there is no event undo), then deletes the event and its
// holding account. Extracted from deleteEvent so POST .../leave's
// last_organiser "delete" case (hack_leave.go) shares it exactly. The caller
// already holds the event row, and has checked confirm matches the slug.
func (h *HackHandler) deleteEventTx(ctx context.Context, tx *sql.Tx, ev db.Event) error {
	var trash func(context.Context, string, string) error
	if h.sites != nil {
		trash = h.sites.TrashTeamSite
	}
	return h.eventsSvc().DeleteEventTx(ctx, tx, ev, trash)
}

func (h *HackHandler) getJoin(w http.ResponseWriter, r *http.Request) {
	h.getCodeInfo(w, r, false)
}

func (h *HackHandler) getJudge(w http.ResponseWriter, r *http.Request) {
	h.getCodeInfo(w, r, true)
}

func (h *HackHandler) getCodeInfo(w http.ResponseWriter, r *http.Request, judge bool) {
	// The route answers signed out, but a signed-in page sends its key: then
	// the account is metered, not the venue's shared address.
	caller := ""
	if k := r.Header.Get("X-API-Key"); k != "" {
		if u, err := db.GetUserByAPIKey(r.Context(), h.database, k, h.limits.KeyIdleExpiry); err == nil {
			caller = u.ID
		}
	}
	if h.rateLimited(w, r, caller) {
		return
	}
	code := hackevents.NormalizeCode(r.PathValue("code"))
	if code == "" {
		httpx.WriteError(w, http.StatusNotFound, "invalid_code", "invalid code")
		return
	}
	var ev db.Event
	var err error
	if judge {
		ev, err = db.GetEventByJudgeCode(r.Context(), h.database, code)
	} else {
		ev, err = db.GetEventByJoinCode(r.Context(), h.database, code)
	}
	if errors.Is(err, sql.ErrNoRows) {
		httpx.WriteError(w, http.StatusNotFound, "invalid_code", "invalid code")
		return
	}
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	if ev.TakenDown() {
		httpx.WriteError(w, http.StatusGone, "event_taken_down", "this event has been taken down")
		return
	}
	role := "participant"
	joinable := ev.Stage == "open" || ev.Stage == "building"
	if judge {
		role = "judge"
		joinable = ev.Stage != "results" && ev.Stage != "archived"
	}
	// The code of conduct is the default text plus the organiser's own.
	cocText := ev.CocText
	questions := []db.SignupQuestion{}
	approvalRequired := false
	if !judge {
		questions, approvalRequired, err = db.RegistrationSettings(r.Context(), h.database, ev.ID)
		if err != nil {
			httpx.WriteInternal(w)
			return
		}
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"slug":         ev.Slug,
		"title":        ev.Title,
		"icon_url":     h.iconURL(ev),
		"accent_color": ev.AccentColor, "color": hackEventAccent(ev),
		"tagline":           ev.Tagline,
		"organiser_name":    ev.OrganiserName,
		"organisation":      ev.Organisation,
		"stage":             ev.Stage,
		"joinable":          joinable,
		"role":              role,
		"coc_default":       HackDefaultCoC,
		"coc_text":          cocText,
		"starts_at":         rfc3339UTC(ev.StartsAt),
		"ends_at":           rfc3339UTC(ev.EndsAt),
		"time_zone":         ev.TimeZone,
		"signup_questions":  questions,
		"approval_required": approvalRequired,
	})
}

func (h *HackHandler) postJoin(w http.ResponseWriter, r *http.Request) {
	h.postCodeJoin(w, r, false)
}

func (h *HackHandler) postJudge(w http.ResponseWriter, r *http.Request) {
	h.postCodeJoin(w, r, true)
}

func (h *HackHandler) postCodeJoin(w http.ResponseWriter, r *http.Request, judge bool) {
	user := hackNeedUser(w, r)
	if user == nil {
		return
	}
	if h.rateLimited(w, r, user.ID) {
		return
	}
	var req struct {
		AcceptCoC   *bool             `json:"accept_coc"`
		DisplayName string            `json:"display_name"`
		Answers     map[string]string `json:"answers"`
	}
	if !decodeHackJSON(w, r, &req) {
		return
	}
	if req.AcceptCoC == nil || !*req.AcceptCoC {
		httpx.WriteError(w, http.StatusBadRequest, "coc_required", "you must accept the code of conduct")
		return
	}
	display, ok := checkHackLine(w, req.DisplayName, "display_name", 1, 100)
	if !ok {
		return
	}
	code := hackevents.NormalizeCode(r.PathValue("code"))
	if code == "" {
		httpx.WriteError(w, http.StatusNotFound, "invalid_code", "invalid code")
		return
	}
	ev, member, organiser, err := h.eventsSvc().JoinByCode(r.Context(), code, user.ID, display, judge, req.Answers, validateSignupAnswersValue)
	if err != nil {
		writeEventsError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, h.eventView(r.Context(), ev, member, organiser))
}

// validateSignupAnswers writes the shared sign-up validation error for join links.
func validateSignupAnswers(w http.ResponseWriter, questions []db.SignupQuestion, in map[string]string) ([]db.SignupAnswer, bool) {
	answers, e := hackevents.SignupAnswers(questions, in)
	if e != nil {
		httpx.WriteError(w, 400, e.Code, e.Message)
		return nil, false
	}
	return answers, true
}

func checkHackText(w http.ResponseWriter, s, field string, min, max int) (string, bool) {
	value, e := hackevents.Text(s, field, min, max)
	if e != nil {
		httpx.WriteError(w, 400, e.Code, e.Message)
		return "", false
	}
	return value, true
}

func checkHackLine(w http.ResponseWriter, s, field string, min, max int) (string, bool) {
	value, e := hackevents.Line(s, field, min, max)
	if e != nil {
		httpx.WriteError(w, 400, e.Code, e.Message)
		return "", false
	}
	return value, true
}

func checkContactEmail(w http.ResponseWriter, s string) (string, bool) {
	value, err := checkContactEmailValue(s)
	if err != nil {
		writeEventsError(w, err)
		return "", false
	}
	return value, true
}

func checkTimeZone(w http.ResponseWriter, tz string) (*time.Location, bool) {
	value, err := checkTimeZoneValue(tz)
	if err != nil {
		writeEventsError(w, err)
		return nil, false
	}
	return value, true
}

func parseEventTime(w http.ResponseWriter, s string, loc *time.Location, field string, required bool) (sql.NullTime, bool) {
	value, err := parseEventTimeValue(s, loc, field, required)
	if err != nil {
		writeEventsError(w, err)
		return sql.NullTime{}, false
	}
	return value, true
}

func rfc3339UTC(t sql.NullTime) any {
	if !t.Valid {
		return nil
	}
	return t.Time.UTC().Format(time.RFC3339)
}

func rfc3339Time(t time.Time) string {
	return t.UTC().Format(time.RFC3339)
}

// randomHackCode and normalizeHackCode moved to
// internal/rules/hackevents (RandomCode, NormalizeCode) in cleanup 3.2
// slice 1c: pure Hack code generation/matching, no handler dependency.

// teamCodeFromInput accepts either a bare team code or a pasted team link
// (https://<hack host>/team/<code>): when s parses as a URL on this host,
// or simply contains "/team/" (a bare path, or a link previewed/pasted
// before TLS terms are known), the last path segment is taken as the code.
// Either way the result is normalised like any other code, so a link and
// its code land on the same team.
func (h *HackHandler) teamCodeFromInput(s string) string {
	s = strings.TrimSpace(s)
	useLastSegment := strings.Contains(s, "/team/")
	if !useLastSegment {
		if u, err := url.Parse(s); err == nil && u.Host != "" && strings.EqualFold(u.Host, h.teamLinkHost()) {
			useLastSegment = true
		}
	}
	if useLastSegment {
		// Strip the query/fragment before trimming trailing slashes, not
		// after: ".../abcd1234/?source=chat" has no trailing slash to trim
		// yet, but removing "?source=chat" first leaves one behind
		// (".../abcd1234/"), which then makes the last segment empty
		// unless it is trimmed too (Codex review, 2026-11-01).
		trimmed := s
		if i := strings.IndexAny(trimmed, "?#"); i >= 0 {
			trimmed = trimmed[:i]
		}
		trimmed = strings.TrimRight(trimmed, "/")
		if i := strings.LastIndex(trimmed, "/"); i >= 0 {
			s = trimmed[i+1:]
		} else {
			s = trimmed
		}
	}
	return hackevents.NormalizeCode(s)
}

// teamLinkHost is the host team links are served from.
func (h *HackHandler) teamLinkHost() string {
	if u, err := url.Parse(h.publicBaseURL); err == nil {
		return u.Host
	}
	return ""
}

func organiserTeamStages(stage string) bool {
	return stage != "archived"
}

// trySavepoint runs fn inside a savepoint of tx, rolling back to it when fn
// fails: a unique violation aborts the whole transaction otherwise, and a
// retry after it could only fail.
func trySavepoint(ctx context.Context, q db.Querier, fn func() error) error {
	if _, err := q.ExecContext(ctx, "SAVEPOINT hack_try"); err != nil {
		return err
	}
	if err := fn(); err != nil {
		if _, rerr := q.ExecContext(ctx, "ROLLBACK TO SAVEPOINT hack_try"); rerr != nil {
			return rerr
		}
		return err
	}
	_, err := q.ExecContext(ctx, "RELEASE SAVEPOINT hack_try")
	return err
}

func checkEntryRequiredValue(in []string) ([]string, error) {
	want := map[string]bool{}
	for _, f := range in {
		f = strings.TrimSpace(f)
		known := false
		for _, k := range hackevents.EntryFields {
			if f == k {
				known = true
			}
		}
		if !known {
			return nil, &events.Error{Code: "invalid_entry_field", Message: "entry_required takes any of: " + strings.Join(hackevents.EntryFields, ", ")}
		}
		want[f] = true
	}
	return entryRequiredList(keysInOrder(want)), nil
}

func checkContactEmailValue(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" || utf8.RuneCountInString(s) > 254 {
		return "", &events.Error{Code: "invalid_contact_email", Message: "contact_email must be a valid email address"}
	}
	addr, err := mail.ParseAddress(s)
	if err != nil || addr.Name != "" || !strings.EqualFold(addr.Address, s) {
		return "", &events.Error{Code: "invalid_contact_email", Message: "contact_email must be a valid email address"}
	}
	if utf8.RuneCountInString(addr.Address) > 254 {
		return "", &events.Error{Code: "invalid_contact_email", Message: "contact_email must be a valid email address"}
	}
	return addr.Address, nil
}

func checkTimeZoneValue(tz string) (*time.Location, error) {
	if tz == "Local" {
		return nil, &events.Error{Code: "invalid_time_zone", Message: "time_zone is not a valid IANA name"}
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return nil, &events.Error{Code: "invalid_time_zone", Message: "time_zone is not a valid IANA name"}
	}
	return loc, nil
}

func parseEventTimeValue(s string, loc *time.Location, field string, required bool) (sql.NullTime, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		if required {
			return sql.NullTime{}, &events.Error{Code: "invalid_" + field, Message: field + " is required"}
		}
		return sql.NullTime{}, nil
	}
	if len(s) == 10 && s[4] == '-' && s[7] == '-' {
		t, err := time.ParseInLocation("2006-01-02", s, loc)
		if err != nil {
			return sql.NullTime{}, &events.Error{Code: "invalid_" + field, Message: field + " must be RFC 3339 or YYYY-MM-DD"}
		}
		return sql.NullTime{Time: t, Valid: true}, nil
	}
	// A wall-clock time in the event's zone, as a datetime-local field sends it.
	for _, layout := range []string{"2006-01-02T15:04", "2006-01-02T15:04:05"} {
		if len(s) == len(layout) {
			if t, err := time.ParseInLocation(layout, s, loc); err == nil {
				// A wall-clock time the clocks skip (a daylight-saving gap)
				// does not exist in that zone: say so rather than move it.
				if t.In(loc).Format(layout) != s {
					return sql.NullTime{}, &events.Error{Code: "invalid_" + field, Message: field + " is a time the clocks skip in " + loc.String() + "; pick another"}
				}
				return sql.NullTime{Time: t, Valid: true}, nil
			}
		}
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t, err = time.Parse(time.RFC3339Nano, s)
	}
	if err != nil {
		return sql.NullTime{}, &events.Error{Code: "invalid_" + field, Message: field + " must be RFC 3339, YYYY-MM-DDTHH:MM in the event's time zone, or YYYY-MM-DD"}
	}
	return sql.NullTime{Time: t, Valid: true}, nil
}

// validateSignupAnswersValue is hackevents.SignupAnswers with the error as an
// events.Error, for validators the events service runs under its lock.
func validateSignupAnswersValue(questions []db.SignupQuestion, in map[string]string) ([]db.SignupAnswer, error) {
	answers, e := hackevents.SignupAnswers(questions, in)
	if e != nil {
		return nil, &events.Error{Code: e.Code, Message: e.Message}
	}
	return answers, nil
}

// checkHackTextValue is hackevents.Text with the error as an events.Error.
func checkHackTextValue(s, field string, min, max int) (string, error) {
	value, e := hackevents.Text(s, field, min, max)
	if e != nil {
		return "", &events.Error{Code: e.Code, Message: e.Message}
	}
	return value, nil
}

// checkHackLineValue is hackevents.Line with the error as an events.Error.
func checkHackLineValue(s, field string, min, max int) (string, error) {
	value, e := hackevents.Line(s, field, min, max)
	if e != nil {
		return "", &events.Error{Code: e.Code, Message: e.Message}
	}
	return value, nil
}
