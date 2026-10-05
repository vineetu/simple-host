package handler

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"net/mail"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/vsriram/simple-host/internal/auth"
	"github.com/vsriram/simple-host/internal/config"
	"github.com/vsriram/simple-host/internal/db"
	"github.com/vsriram/simple-host/internal/email"
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
	hackCodeAlphabet = "abcdefghjkmnpqrstuvwxyz23456789"
	hackJoinCodeLen  = 8
	hackJudgeCodeLen = 12
	hackTeamCodeLen  = 8
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

// hackEntryFields are the entry fields an organiser can require, in order.
var hackEntryFields = []string{"title", "tagline", "description", "video_url", "code_url", "screenshot"}

// HackHandler is the hosted-events API (EVENTS=hosted).
type HackHandler struct {
	database      *sql.DB
	publicBaseURL string
	siteDomain    string
	codesIP       *rateLimiter // join, judge and team codes, per network address
	codesUser     *rateLimiter // the same, per account
	namesUser     *rateLimiter // event address checks, per account
	entryWrites   *rateLimiter // entry and screenshot writes, per account
	shotWrites    *rateLimiter // screenshot uploads, per account (2 MB each)
	namePeer      func(ctx context.Context, name string) (bool, error)
	usageFn       func(ctx context.Context) (int64, error)
	// sites reaches the team sites (the SiteHandler; hack_sites.go). nil in
	// tests that do not need it: team addresses then count as not ready.
	sites hackSiteHooks
	// mailer sends the one archive warning (hack_cleanup.go). nil, or a
	// sender that cannot send a notice, makes that sweep log and do nothing.
	mailer         email.Sender
	archiveLinkKey []byte
}

// hackSiteHooks is what the events API needs of the site side.
type hackSiteHooks interface {
	TeamSitesReady(eventSlug string) bool
	TeamSiteURL(eventSlug, teamSlug string) string
	TeamSiteInfo(ctx context.Context, accountID, teamSlug string) (TeamSite, db.Site, error)
	TeamPreviewLink(ctx context.Context, accountID, eventSlug, teamSlug string, n int) (string, time.Time, bool)
	TeamPreviewLinkFor(site db.Site, eventSlug string, n int) (string, bool)
	SetTeamSiteTakenDown(ctx context.Context, accountID, teamID, teamSlug string, on bool, reason string) error
	TrashTeamSite(ctx context.Context, accountID, name string) error
	RequestSiteCert(handle string)
	RemoveAccountFiles(userID, handle string) error
	SyncAccountMarkers(ctx context.Context, accountID string) error
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

// NewHackHandler builds the hosted-events API. publicBaseURL is the apex
// (join/judge/manage links); siteDomain is the event host parent.
func NewHackHandler(database *sql.DB, publicBaseURL, siteDomain string) *HackHandler {
	h := &HackHandler{
		database:       database,
		publicBaseURL:  strings.TrimRight(publicBaseURL, "/"),
		siteDomain:     strings.Trim(strings.ToLower(siteDomain), "."),
		codesIP:        newRateLimiterFor(config.Active().RateEventCodesIP),
		codesUser:      newRateLimiterFor(config.Active().RateEventCodesUser),
		namesUser:      newRateLimiterFor(config.Active().RateEventNamesUser),
		entryWrites:    newRateLimiter(30, 0.5),
		shotWrites:     newRateLimiter(5, 0.1),
		archiveLinkKey: newExportKey(),
	}
	return h
}

// StartCleanup evicts idle rate-limit buckets; call once from the server.
func (h *HackHandler) StartCleanup() {
	for _, l := range []*rateLimiter{h.codesIP, h.codesUser, h.namesUser, h.entryWrites, h.shotWrites} {
		l.startCleanup(10*time.Minute, 30*time.Minute)
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
	mux.Handle("POST /v1/hack/events/{slug}/teams", wrap(h.createTeam))
	mux.Handle("POST /v1/hack/events/{slug}/teams/join", wrap(h.joinTeam))
	mux.Handle("POST /v1/hack/events/{slug}/teams/leave", wrap(h.leaveTeam))
	mux.Handle("POST /v1/hack/events/{slug}/teams/{team}/members", wrap(h.moveMember))
	mux.Handle("DELETE /v1/hack/events/{slug}/teams/{team}/members/{user_id}", wrap(h.removeTeamMember))
	mux.Handle("DELETE /v1/hack/events/{slug}/teams/{team}", wrap(h.deleteTeam))
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
	v, err := judgeHandle(ctx, h.database, "", name)
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

func (h *HackHandler) manageURL(slug string) string {
	return h.publicBaseURL + "/e/" + slug + "/manage"
}

func eventNameReserved(name string) bool {
	return extraEventReserved[name] || labelReservedForNew(name)
}

// rateLimited meters code lookups and joins: per network address (roomy,
// since a venue shares one) and, when signed in, per account.
//
// A signed-in caller is metered by account only: an account costs an email
// round trip, and a venue's shared address must not let one stranger's
// anonymous lookups lock everyone there out of joining.
func (h *HackHandler) rateLimited(w http.ResponseWriter, r *http.Request, userID string) bool {
	if userID == "" && !h.codesIP.allow(clientIP(r)) {
		writeJSON(w, http.StatusTooManyRequests, errorResponse{Error: "rate limit exceeded, slow down", Code: "rate_limited"})
		return true
	}
	if userID != "" && !h.codesUser.allow(userID) {
		writeJSON(w, http.StatusTooManyRequests, errorResponse{Error: "rate limit exceeded, slow down", Code: "rate_limited"})
		return true
	}
	return false
}

func decodeHackJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, hackMaxBody))
	if err := dec.Decode(dst); err != nil {
		if errors.Is(err, io.EOF) {
			writeJSON(w, http.StatusBadRequest, errorResponse{Error: "JSON body required", Code: "invalid_request"})
			return false
		}
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid JSON body", Code: "invalid_request"})
		return false
	}
	return true
}

func hackNeedUser(w http.ResponseWriter, r *http.Request) *db.User {
	user := auth.GetUser(r.Context())
	if user == nil {
		writeJSON(w, http.StatusUnauthorized, errorResponse{Error: "unauthorized"})
		return nil
	}
	return user
}

func writeHackErr(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, errorResponse{Error: msg, Code: code})
}

func writeEventNotFound(w http.ResponseWriter) {
	writeHackErr(w, http.StatusNotFound, "event_not_found", "event not found")
}

func writeInternal(w http.ResponseWriter) {
	writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
}

type hackAccess struct {
	event  db.Event
	member db.EventMember
	user   *db.User
	admin  bool
}

// loadMember loads the event and the caller's membership. Non-members get 404
// event_not_found (including a wrong-role member on a role-gated route). The
// admin key may read any event as organiser; writes still need membership
// unless adminRead is the only path. write is true for mutating routes.
func (h *HackHandler) loadMember(w http.ResponseWriter, r *http.Request, slug string, write bool, roles ...string) (hackAccess, bool) {
	var z hackAccess
	user := hackNeedUser(w, r)
	if user == nil {
		return z, false
	}
	slug = strings.ToLower(strings.TrimSpace(slug))
	ev, err := db.GetEventBySlug(r.Context(), h.database, slug)
	if errors.Is(err, sql.ErrNoRows) {
		writeEventNotFound(w)
		return z, false
	}
	if err != nil {
		writeInternal(w)
		return z, false
	}
	m, err := db.GetEventMember(r.Context(), h.database, ev.ID, user.ID)
	admin := user.IsAdmin
	if errors.Is(err, sql.ErrNoRows) {
		if admin && !write {
			z = hackAccess{event: ev, member: db.EventMember{EventID: ev.ID, UserID: user.ID, Role: "organiser"}, user: user, admin: true}
			return z, true
		}
		writeEventNotFound(w)
		return z, false
	}
	if err != nil {
		writeInternal(w)
		return z, false
	}
	if m.Role == "participant" && (write || len(roles) > 0) {
		status, err := db.MemberApprovalStatus(r.Context(), h.database, ev.ID, user.ID)
		if err != nil {
			writeInternal(w)
			return z, false
		}
		if status != "approved" {
			writeHackErr(w, http.StatusForbidden, "approval_"+status, "your application is "+status)
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
	if write && ev.TakenDown() && !admin {
		writeHackErr(w, http.StatusForbidden, "event_taken_down", "this event has been taken down")
		return z, false
	}
	return hackAccess{event: ev, member: m, user: user, admin: admin}, true
}

func (h *HackHandler) checkName(w http.ResponseWriter, r *http.Request) {
	user := hackNeedUser(w, r)
	if user == nil {
		return
	}
	if !h.namesUser.allow(user.ID) {
		writeJSON(w, http.StatusTooManyRequests, errorResponse{Error: "rate limit exceeded, slow down", Code: "rate_limited"})
		return
	}
	slug := strings.ToLower(strings.TrimSpace(r.PathValue("slug")))
	out := map[string]any{"address": h.EventURL(slug)}
	code, errMsg, err := h.slugVerdict(r.Context(), slug)
	if err != nil {
		if errors.Is(err, errNameCheckUnavailable) {
			writeHackErr(w, http.StatusServiceUnavailable, "name_check_unavailable", "could not check whether this name is free; try again")
			return
		}
		writeInternal(w)
		return
	}
	if code != "" {
		out["available"] = false
		out["code"] = code
		out["error"] = errMsg
		writeJSON(w, http.StatusOK, out)
		return
	}
	out["available"] = true
	writeJSON(w, http.StatusOK, out)
}

var errNameCheckUnavailable = errors.New("name check unavailable")

// slugVerdict is the create/check result for an event slug. Empty code means free.
func (h *HackHandler) slugVerdict(ctx context.Context, slug string) (code, errMsg string, err error) {
	if utf8.RuneCountInString(slug) < 3 || !visitorHandleRe.MatchString(slug) || !handleIsLabel(slug) {
		return "invalid_name", "use 3 to 39 lowercase letters, numbers or hyphens, not starting or ending with a hyphen", nil
	}
	if eventNameReserved(slug) {
		return "name_reserved", "that name is reserved", nil
	}
	v, err := judgeHandle(ctx, h.database, "", slug)
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
	budgetGB := config.Active().HackInstanceBudgetGB
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
			writeHackErr(w, http.StatusServiceUnavailable, "name_check_unavailable", "could not check whether this name is free; try again")
			return
		}
		writeInternal(w)
		return
	}
	if code != "" {
		status := http.StatusBadRequest
		if code == "name_taken" {
			status = http.StatusConflict
		}
		writeHackErr(w, status, code, errMsg)
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
		writeHackErr(w, http.StatusBadRequest, "invalid_expected_participants", "expected_participants must be between 1 and 100000")
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
			writeHackErr(w, http.StatusBadRequest, "invalid_ends_at", "ends_at must not be before starts_at")
			return
		}
	}
	full, err := h.instanceFull(r.Context())
	if err != nil || full {
		writeHackErr(w, http.StatusInsufficientStorage, "instance_full", "this instance is at capacity")
		return
	}

	ctx := r.Context()
	tx, err := h.database.BeginTx(ctx, nil)
	if err != nil {
		writeInternal(w)
		return
	}
	defer tx.Rollback()

	if err := db.LockUserRow(ctx, tx, user.ID); err != nil {
		writeInternal(w)
		return
	}
	since := time.Now().Add(-24 * time.Hour)
	n, err := db.CountEventCreatesSince(ctx, tx, user.ID, since)
	if err != nil {
		writeInternal(w)
		return
	}
	if n >= config.Active().EventCreatePerDay {
		writeHackErr(w, http.StatusTooManyRequests, "too_many_events_today", "you have created too many events today")
		return
	}
	active, err := db.CountActiveEventsByOrganiser(ctx, tx, user.ID)
	if err != nil {
		writeInternal(w)
		return
	}
	if active >= config.Active().EventMaxActivePerOrganiser {
		writeHackErr(w, http.StatusConflict, "too_many_active_events", "you already have as many active events as this instance allows")
		return
	}

	eventID, err := db.NewUUID(ctx, tx)
	if err != nil {
		writeInternal(w)
		return
	}
	holding, err := db.CreateUser(ctx, tx, "event+"+eventID+"@events.invalid", "", false)
	if err != nil {
		writeInternal(w)
		return
	}
	claimed, err := db.ClaimHandle(ctx, tx, holding.ID, slug)
	if err != nil {
		writeInternal(w)
		return
	}
	if !claimed {
		writeHackErr(w, http.StatusConflict, "name_taken", "that name is already in use")
		return
	}
	if err := db.MarkEventAccount(ctx, tx, holding.ID); err != nil {
		writeInternal(w)
		return
	}

	var ev db.Event
	for attempt := 0; attempt < 8; attempt++ {
		joinCode, err := randomHackCode(hackJoinCodeLen)
		if err != nil {
			writeInternal(w)
			return
		}
		judgeCode, err := randomHackCode(hackJudgeCodeLen)
		if err != nil {
			writeInternal(w)
			return
		}
		err = trySavepoint(ctx, tx, func() error {
			var ierr error
			ev, ierr = db.InsertEvent(ctx, tx, db.Event{
				ID:                   eventID,
				Slug:                 slug,
				AccountID:            holding.ID,
				CreatedBy:            sql.NullString{String: user.ID, Valid: true},
				Title:                title,
				Stage:                "draft",
				OrganiserName:        organiserName,
				Organisation:         organisation,
				ContactEmail:         contact,
				Purpose:              purpose,
				ExpectedParticipants: sql.NullInt64{Int64: int64(*req.ExpectedParticipants), Valid: true},
				TimeZone:             tz,
				StartsAt:             startsAt,
				EndsAt:               endsAt,
				TeamSizeMax:          config.Active().EventTeamSizeDefault,
				JoinCode:             joinCode,
				JudgeCode:            judgeCode,
			})
			return ierr
		})
		if err == nil {
			break
		}
		if dbUnique(err) && attempt < 7 {
			continue
		}
		if dbUnique(err) {
			writeHackErr(w, http.StatusConflict, "name_taken", "that name is already in use")
			return
		}
		writeInternal(w)
		return
	}
	// Starter rubric, only for this new row. Nothing else writes one onto an
	// event that does not already have it.
	if err := db.ReplaceRubric(ctx, tx, ev.ID, defaultEventRubric()); err != nil {
		writeInternal(w)
		return
	}
	if _, err := db.InsertEventMember(ctx, tx, ev.ID, user.ID, "organiser", organiserName); err != nil {
		writeInternal(w)
		return
	}
	if err := db.InsertEventCreateLog(ctx, tx, user.ID); err != nil {
		writeInternal(w)
		return
	}
	if err := tx.Commit(); err != nil {
		writeInternal(w)
		return
	}
	member, err := db.GetEventMember(ctx, h.database, ev.ID, user.ID)
	if err != nil {
		writeInternal(w)
		return
	}
	writeJSON(w, http.StatusCreated, h.eventView(ctx, ev, member, true))
}

func dbUnique(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "23505") || strings.Contains(strings.ToLower(msg), "unique")
}

func (h *HackHandler) listEvents(w http.ResponseWriter, r *http.Request) {
	user := hackNeedUser(w, r)
	if user == nil {
		return
	}
	events, roles, err := db.ListEventsForUser(r.Context(), h.database, user.ID)
	if err != nil {
		writeInternal(w)
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
		out = append(out, item)
	}
	writeJSON(w, http.StatusOK, out)
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
	writeJSON(w, http.StatusOK, view)
}

func (h *HackHandler) eventView(ctx context.Context, ev db.Event, member db.EventMember, organiser bool) map[string]any {
	// The code of conduct is the default text plus the organiser's own.
	cocText := ev.CocText
	tracks, _ := db.ListEventTracks(ctx, h.database, ev.ID)
	resultState, _ := db.GetEventResults(ctx, h.database, ev.ID)
	body := map[string]any{
		"event": map[string]any{
			"slug":          ev.Slug,
			"title":         ev.Title,
			"tagline":       ev.Tagline,
			"about":         ev.About,
			"rules":         ev.Rules,
			"prizes":        ev.Prizes,
			"coc_text":      cocText,
			"coc_default":   HackDefaultCoC,
			"stage":         ev.Stage,
			"time_zone":     ev.TimeZone,
			"starts_at":     rfc3339UTC(ev.StartsAt),
			"ends_at":       rfc3339UTC(ev.EndsAt),
			"team_size_max": ev.TeamSizeMax,
			"url":           h.EventURL(ev.Slug),
			"builtin_url":   h.publicBaseURL + "/e/" + ev.Slug,
			"website_mode":  ev.WebsiteMode,
			"icon_url":      h.iconURL(ev),
			"accent_color":  ev.AccentColor, "color": hackEventAccent(ev),
			"icon_media_type":      ev.IconMediaType,
			"taken_down":           ev.TakenDown(),
			"results_visibility":   ev.ResultsVisibility,
			"submission_deadline":  rfc3339UTC(ev.SubmissionDeadline),
			"entry_required":       entryRequiredList(ev.EntryRequired),
			"gallery_open":         ev.GalleryOpen,
			"team_sites_ready":     h.teamSitesReady(ev.Slug),
			"stages_offered":       hackStagesOfferedList,
			"judging_locked_at":    rfc3339UTC(ev.JudgingLockedAt),
			"results_published":    resultState.Published,
			"results_published_at": rfc3339UTC(resultState.PublishedAt),
			"judging_lock_reason":  ev.JudgingLockReason,
			"tracks":               tracks,
		},
		"role": member.Role,
		"me":   h.meView(ctx, ev, member),
	}
	if organiser {
		participants, teams, _, _ := db.CountEventMembers(ctx, h.database, ev.ID)
		judges, _ := db.CountEligibleJudges(ctx, h.database, ev.ID)
		onNone, _ := db.CountOnNoTeam(ctx, h.database, ev.ID)
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
				"participants": participants,
				"teams":        teams,
				"judges":       judges,
				"on_no_team":   onNone,
			},
		}
	}
	if member.Role == "participant" {
		if status, err := db.MemberApprovalStatus(ctx, h.database, ev.ID, member.UserID); err == nil {
			body["me"].(map[string]any)["approval_status"] = status
		}
	}
	return body
}

func (h *HackHandler) meView(ctx context.Context, ev db.Event, member db.EventMember) map[string]any {
	me := map[string]any{
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
	for _, p := range people {
		members = append(members, map[string]any{
			"display_name": p.DisplayName,
			"you":          p.UserID == member.UserID,
		})
	}
	t := map[string]any{
		"slug":    team.Slug,
		"name":    team.Name,
		"code":    team.Code,
		"members": members,
		"site":    h.teamSiteJSON(ctx, ev, team),
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
	// The event row is locked and read again: the patch applies to what is
	// there now, never to a copy read before a concurrent stage change (a
	// stale deadline written back would reopen or refreeze submissions).
	tx, err := h.database.BeginTx(r.Context(), nil)
	if err != nil {
		writeInternal(w)
		return
	}
	defer tx.Rollback()
	ev, err := db.GetEventForUpdate(r.Context(), tx, a.event.ID)
	if err != nil {
		writeInternal(w)
		return
	}
	if req.AccentColor != nil {
		if !hackColorValid(*req.AccentColor) {
			writeHackErr(w, http.StatusBadRequest, "invalid_accent_color", "choose a crayon colour or an empty string for automatic")
			return
		}
		ev.AccentColor = *req.AccentColor
	}
	if req.Title != nil {
		s, ok := checkHackLine(w, *req.Title, "title", 1, 120)
		if !ok {
			return
		}
		ev.Title = s
	}
	if req.Tagline != nil {
		s, ok := checkHackLine(w, *req.Tagline, "tagline", 0, 160)
		if !ok {
			return
		}
		ev.Tagline = s
	}
	if req.About != nil {
		s, ok := checkHackText(w, *req.About, "about", 0, 10000)
		if !ok {
			return
		}
		ev.About = s
	}
	if req.Rules != nil {
		s, ok := checkHackText(w, *req.Rules, "rules", 0, 10000)
		if !ok {
			return
		}
		ev.Rules = s
	}
	if req.Prizes != nil {
		s, ok := checkHackText(w, *req.Prizes, "prizes", 0, 5000)
		if !ok {
			return
		}
		ev.Prizes = s
	}
	if req.CocText != nil {
		s, ok := checkHackText(w, *req.CocText, "coc_text", 0, 10000)
		if !ok {
			return
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
		if _, ok := checkTimeZone(w, tz); !ok {
			return
		}
		ev.TimeZone = tz
	}
	loc, err := time.LoadLocation(ev.TimeZone)
	if err != nil {
		writeHackErr(w, http.StatusBadRequest, "invalid_time_zone", "time_zone is not a valid IANA name")
		return
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
		t, ok := parseEventTime(w, *req.StartsAt, loc, "starts_at", true)
		if !ok {
			return
		}
		ev.StartsAt = t
	}
	if req.EndsAt != nil {
		if strings.TrimSpace(*req.EndsAt) == "" {
			ev.EndsAt = sql.NullTime{}
		} else {
			t, ok := parseEventTime(w, *req.EndsAt, loc, "ends_at", true)
			if !ok {
				return
			}
			ev.EndsAt = t
		}
	}
	if ev.EndsAt.Valid && ev.StartsAt.Valid && ev.EndsAt.Time.Before(ev.StartsAt.Time) {
		writeHackErr(w, http.StatusBadRequest, "invalid_ends_at", "ends_at must not be before starts_at")
		return
	}
	if req.TeamSizeMax != nil {
		if *req.TeamSizeMax < 1 || *req.TeamSizeMax > 50 {
			writeHackErr(w, http.StatusBadRequest, "invalid_team_size", "team_size_max must be between 1 and 50")
			return
		}
		ev.TeamSizeMax = *req.TeamSizeMax
	}
	if req.OrganiserName != nil {
		s, ok := checkHackLine(w, *req.OrganiserName, "organiser_name", 1, 100)
		if !ok {
			return
		}
		ev.OrganiserName = s
	}
	if req.Organisation != nil {
		s, ok := checkHackLine(w, *req.Organisation, "organisation", 0, 120)
		if !ok {
			return
		}
		ev.Organisation = s
	}
	if req.ContactEmail != nil {
		s, ok := checkContactEmail(w, *req.ContactEmail)
		if !ok {
			return
		}
		ev.ContactEmail = s
	}
	if req.Purpose != nil {
		s, ok := checkHackText(w, *req.Purpose, "purpose", 1, 1000)
		if !ok {
			return
		}
		ev.Purpose = s
	}
	if req.ExpectedParticipants != nil {
		if *req.ExpectedParticipants < 1 || *req.ExpectedParticipants > 100000 {
			writeHackErr(w, http.StatusBadRequest, "invalid_expected_participants", "expected_participants must be between 1 and 100000")
			return
		}
		ev.ExpectedParticipants = sql.NullInt64{Int64: int64(*req.ExpectedParticipants), Valid: true}
	}
	// M2: the submission deadline, the entry's required fields, the gallery.
	deadlineChanged := false
	// The deadline is an instant: a new zone never moves it (dates of the
	// event move to keep their calendar day; a deadline that passed must
	// stay passed).
	if req.SubmissionDeadline != nil {
		if strings.TrimSpace(*req.SubmissionDeadline) == "" {
			ev.SubmissionDeadline = sql.NullTime{}
		} else {
			t, ok := parseEventTime(w, *req.SubmissionDeadline, loc, "submission_deadline", true)
			if !ok {
				return
			}
			ev.SubmissionDeadline = t
		}
		deadlineChanged = true
	}
	if req.EntryRequired != nil {
		fields, ok := checkEntryRequired(w, *req.EntryRequired)
		if !ok {
			return
		}
		ev.EntryRequired = fields
	}
	if req.GalleryOpen != nil {
		ev.GalleryOpen = *req.GalleryOpen
	}
	stage := ev.Stage
	if deadlineChanged {
		if stage == "archived" {
			writeHackErr(w, http.StatusConflict, "event_closed", "an ended event cannot be changed")
			return
		}
		// Submissions closed means the deadline has passed; reopening them
		// is moving the event back to Building, or one team's extension.
		var dbNow time.Time
		if err := tx.QueryRowContext(r.Context(), `SELECT clock_timestamp()`).Scan(&dbNow); err != nil {
			writeInternal(w)
			return
		}
		if submissionsClosedStage(stage) && (!ev.SubmissionDeadline.Valid || ev.SubmissionDeadline.Time.After(dbNow)) {
			writeHackErr(w, http.StatusConflict, "submissions_closed_stage",
				"submissions are closed: move the event back to Building to set a new deadline, or give single teams more time")
			return
		}
	}
	updated, err := db.UpdateEventPatch(r.Context(), tx, ev)
	if err != nil {
		writeInternal(w)
		return
	}
	updated, err = db.UpdateEventEntrySettings(r.Context(), tx, ev.ID, ev.SubmissionDeadline, ev.EntryRequired, ev.GalleryOpen)
	if err != nil {
		writeInternal(w)
		return
	}
	if deadlineChanged {
		// Lock every team row: a deploy let in before the old deadline
		// holds its team FOR SHARE and finishes first.
		if _, err := tx.ExecContext(r.Context(), `SELECT 1 FROM event_teams WHERE event_id = $1 FOR UPDATE`, ev.ID); err != nil {
			writeInternal(w)
			return
		}
		if err := db.UnpinReopened(r.Context(), tx, ev.ID); err != nil {
			writeInternal(w)
			return
		}
	}
	if err := tx.Commit(); err != nil {
		writeInternal(w)
		return
	}
	if deadlineChanged {
		h.pinDue(r.Context(), ev.ID)
	}
	writeJSON(w, http.StatusOK, h.eventView(r.Context(), updated, a.member, true))
}

// submissionsClosedStage: stages after submissions close.
func submissionsClosedStage(stage string) bool {
	return stage == "closed" || stage == "judging" || stage == "results"
}

// checkEntryRequired validates the organiser's required entry fields and
// returns them in the canonical order.
func checkEntryRequired(w http.ResponseWriter, in []string) ([]string, bool) {
	want := map[string]bool{}
	for _, f := range in {
		f = strings.TrimSpace(f)
		known := false
		for _, k := range hackEntryFields {
			if f == k {
				known = true
			}
		}
		if !known {
			writeHackErr(w, http.StatusBadRequest, "invalid_entry_field", "entry_required takes any of: "+strings.Join(hackEntryFields, ", "))
			return nil, false
		}
		want[f] = true
	}
	return entryRequiredList(keysInOrder(want)), true
}

func keysInOrder(set map[string]bool) []string {
	out := []string{}
	for _, k := range hackEntryFields {
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
		writeHackErr(w, http.StatusBadRequest, "invalid_stage", "stage is not a known event stage")
		return
	}
	if !hackStagesOffered[stage] {
		writeHackErr(w, http.StatusConflict, "stage_not_available", "that stage is not available yet")
		return
	}
	if a.event.Stage == "archived" && stage != "archived" {
		writeHackErr(w, http.StatusConflict, "event_closed", "an ended event cannot be reopened")
		return
	}
	if stage == "archived" && a.event.Stage != "archived" {
		// An event nobody joined is deleted, not ended: ending one would keep
		// its name and page for good without it ever running.
		participants, _, _, err := db.CountEventMembers(r.Context(), h.database, a.event.ID)
		if err != nil {
			writeInternal(w)
			return
		}
		if participants == 0 {
			writeHackErr(w, http.StatusConflict, "archive_needs_participants", "nobody has joined, so delete the event instead")
			return
		}
	}
	tx, err := h.database.BeginTx(r.Context(), nil)
	if err != nil {
		writeInternal(w)
		return
	}
	defer tx.Rollback()
	var current string
	if err := tx.QueryRowContext(r.Context(), `SELECT stage FROM events WHERE id = $1 FOR UPDATE`, a.event.ID).Scan(&current); err != nil {
		writeInternal(w)
		return
	}
	if current == "archived" && stage != "archived" {
		writeHackErr(w, http.StatusConflict, "event_closed", "an ended event cannot be reopened")
		return
	}
	// Every team row too: a deploy let in holds its team FOR SHARE and
	// finishes before the deadline moves.
	if _, err := tx.ExecContext(r.Context(), `SELECT 1 FROM event_teams WHERE event_id = $1 FOR UPDATE`, a.event.ID); err != nil {
		writeInternal(w)
		return
	}
	switch {
	case (submissionsClosedStage(stage) || stage == "archived") && !submissionsClosedStage(current) && current != "archived":
		// Submissions close now (or the event ends): the deadline becomes
		// now unless it already passed. Teams given more time keep it.
		_, err = tx.ExecContext(r.Context(), `
			UPDATE events SET submission_deadline = clock_timestamp()
			 WHERE id = $1 AND (submission_deadline IS NULL OR submission_deadline > clock_timestamp())`, a.event.ID)
	case (stage == "open" || stage == "building") && current != "open" && current != "building":
		// Back to open or building from any other stage (closed, or draft
		// after closed): a passed deadline (and passed extensions) go, so
		// teams can publish again until the organiser sets a new one.
		if _, err = tx.ExecContext(r.Context(), `
			UPDATE events SET submission_deadline = NULL
			 WHERE id = $1 AND submission_deadline <= clock_timestamp()`, a.event.ID); err == nil {
			_, err = tx.ExecContext(r.Context(), `
				UPDATE event_teams SET deadline_override = NULL
				 WHERE event_id = $1 AND deadline_override <= clock_timestamp()`, a.event.ID)
		}
	}
	if err == nil {
		err = db.UnpinReopened(r.Context(), tx, a.event.ID)
	}
	if err != nil {
		writeInternal(w)
		return
	}
	updated, err := db.SetEventStage(r.Context(), tx, a.event.ID, stage)
	if err != nil {
		writeInternal(w)
		return
	}
	if err := tx.Commit(); err != nil {
		writeInternal(w)
		return
	}
	h.pinDue(r.Context(), updated.ID)
	h.requestEventCert(updated)
	writeJSON(w, http.StatusOK, h.eventView(r.Context(), updated, a.member, true))
}

func (h *HackHandler) regenCode(w http.ResponseWriter, r *http.Request) {
	a, ok := h.loadMember(w, r, r.PathValue("slug"), true, "organiser")
	if !ok {
		return
	}
	if a.event.Stage == "archived" {
		writeHackErr(w, http.StatusConflict, "event_closed", "an ended event cannot be changed")
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
		writeHackErr(w, http.StatusBadRequest, "invalid_request", "kind must be join or judge")
		return
	}
	var updated db.Event
	var err error
	for attempt := 0; attempt < 8; attempt++ {
		code, genErr := randomHackCode(n)
		if genErr != nil {
			writeInternal(w)
			return
		}
		if kind == "join" {
			updated, err = db.SetEventJoinCode(r.Context(), h.database, a.event.ID, code)
		} else {
			updated, err = db.SetEventJudgeCode(r.Context(), h.database, a.event.ID, code)
		}
		if err == nil {
			break
		}
		if dbUnique(err) && attempt < 7 {
			continue
		}
		writeInternal(w)
		return
	}
	if kind == "join" {
		writeJSON(w, http.StatusOK, map[string]any{"code": updated.JoinCode, "url": h.joinURL(updated.JoinCode)})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"code": updated.JudgeCode, "url": h.judgeURL(updated.JudgeCode)})
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
	tx, err := h.database.BeginTx(r.Context(), nil)
	if err != nil {
		writeInternal(w)
		return
	}
	defer tx.Rollback()
	// Hold the event against joins and recheck current organiser membership.
	var stage, role string
	var takenDown bool
	err = tx.QueryRowContext(r.Context(), `SELECT e.stage, m.role, e.taken_down_at IS NOT NULL
		FROM events e JOIN event_members m ON m.event_id=e.id
		WHERE e.id=$1 AND m.user_id=$2 FOR UPDATE OF e FOR SHARE OF m`, a.event.ID, a.user.ID).Scan(&stage, &role, &takenDown)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && role != "organiser") {
		writeEventNotFound(w)
		return
	}
	if err != nil {
		writeInternal(w)
		return
	}
	if takenDown && !a.admin {
		writeHackErr(w, http.StatusForbidden, "event_taken_down", "this event has been taken down")
		return
	}
	participants, _, judges, err := db.CountEventMembers(r.Context(), tx, a.event.ID)
	if err != nil {
		writeInternal(w)
		return
	}
	if (req.Confirm != nil && *req.Confirm != a.event.Slug) || (req.Confirm == nil && (stage == "archived" || participants+judges > 0)) {
		writeHackErr(w, http.StatusConflict, "delete_only_empty", "to permanently delete this event, send {\"confirm\":\""+a.event.Slug+"\"} with the matching event address name")
		return
	}
	// Use the same site removal as deleting a team. The holding-account
	// deletion below then purges its Recently deleted sites too; no event undo.
	if h.sites != nil {
		teams, err := db.ListEventTeams(r.Context(), tx, a.event.ID)
		if err != nil {
			writeInternal(w)
			return
		}
		for _, team := range teams {
			if err := h.sites.TrashTeamSite(r.Context(), a.event.AccountID, team.Slug); err != nil {
				writeInternal(w)
				return
			}
		}
	}
	if err := db.DeleteEventAndAccount(r.Context(), tx, a.event); err != nil {
		writeInternal(w)
		return
	}
	if err := tx.Commit(); err != nil {
		writeInternal(w)
		return
	}
	h.removeEventFiles(a.event)
	w.WriteHeader(http.StatusNoContent)
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
		if u, err := db.GetUserByAPIKey(r.Context(), h.database, k); err == nil {
			caller = u.ID
		}
	}
	if h.rateLimited(w, r, caller) {
		return
	}
	code := normalizeHackCode(r.PathValue("code"))
	if code == "" {
		writeHackErr(w, http.StatusNotFound, "invalid_code", "invalid code")
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
		writeHackErr(w, http.StatusNotFound, "invalid_code", "invalid code")
		return
	}
	if err != nil {
		writeInternal(w)
		return
	}
	if ev.TakenDown() {
		writeHackErr(w, http.StatusGone, "event_taken_down", "this event has been taken down")
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
			writeInternal(w)
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
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
	if user.IsAdmin {
		writeHackErr(w, http.StatusConflict, "admin_cannot_join", "the platform admin cannot join an event")
		return
	}
	if req.AcceptCoC == nil || !*req.AcceptCoC {
		writeHackErr(w, http.StatusBadRequest, "coc_required", "you must accept the code of conduct")
		return
	}
	display, ok := checkHackLine(w, req.DisplayName, "display_name", 1, 100)
	if !ok {
		return
	}
	code := normalizeHackCode(r.PathValue("code"))
	if code == "" {
		writeHackErr(w, http.StatusNotFound, "invalid_code", "invalid code")
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
		writeHackErr(w, http.StatusNotFound, "invalid_code", "invalid code")
		return
	}
	if err != nil {
		writeInternal(w)
		return
	}
	if ev.TakenDown() && !user.IsAdmin {
		writeHackErr(w, http.StatusForbidden, "event_taken_down", "this event has been taken down")
		return
	}
	role := "participant"
	if judge {
		role = "judge"
		if ev.Stage == "results" || ev.Stage == "archived" {
			writeHackErr(w, http.StatusConflict, "judging_closed", "judging is closed for this event")
			return
		}
	} else if ev.Stage != "open" && ev.Stage != "building" {
		writeHackErr(w, http.StatusConflict, "joining_closed", "joining is closed for this event")
		return
	}
	existing, err := db.GetEventMember(r.Context(), h.database, ev.ID, user.ID)
	if err == nil {
		if existing.Role == role {
			writeJSON(w, http.StatusOK, h.eventView(r.Context(), ev, existing, existing.Role == "organiser"))
			return
		}
		writeHackErr(w, http.StatusConflict, "already_member", "you already have a different role in this event")
		return
	}
	if !errors.Is(err, sql.ErrNoRows) {
		writeInternal(w)
		return
	}
	var member db.EventMember
	if judge {
		member, err = db.InsertEventMember(r.Context(), h.database, ev.ID, user.ID, role, display)
	} else {
		tx, txerr := h.database.BeginTx(r.Context(), nil)
		if txerr != nil {
			writeInternal(w)
			return
		}
		defer tx.Rollback()
		if _, txerr = tx.ExecContext(r.Context(), `SELECT 1 FROM events WHERE id=$1 FOR SHARE`, ev.ID); txerr != nil {
			writeInternal(w)
			return
		}
		questions, required, qerr := db.RegistrationSettings(r.Context(), tx, ev.ID)
		if qerr != nil {
			writeInternal(w)
			return
		}
		if len(req.Answers) > len(questions) {
			writeHackErr(w, 400, "invalid_answers", "answer only the listed questions")
			return
		}
		answers := make([]db.SignupAnswer, 0, len(questions))
		allowed := map[string]bool{}
		for _, q := range questions {
			allowed[q.ID] = true
			answer, valid := checkHackText(w, req.Answers[q.ID], "answer", 0, 500)
			if !valid {
				return
			}
			if q.Required && answer == "" {
				writeHackErr(w, 400, "answer_required", "answer every required question")
				return
			}
			answers = append(answers, db.SignupAnswer{ID: q.ID, Prompt: q.Prompt, Answer: answer})
		}
		for id := range req.Answers {
			if !allowed[id] {
				writeHackErr(w, 400, "invalid_answers", "answer only the listed questions")
				return
			}
		}
		member, err = db.JoinParticipantRegistration(r.Context(), tx, ev.ID, user.ID, display, answers, required)
		if err == nil {
			err = tx.Commit()
		}
	}
	if err != nil {
		if dbUnique(err) {
			existing, err2 := db.GetEventMember(r.Context(), h.database, ev.ID, user.ID)
			if err2 == nil && existing.Role == role {
				writeJSON(w, http.StatusOK, h.eventView(r.Context(), ev, existing, false))
				return
			}
			writeHackErr(w, http.StatusConflict, "already_member", "you already have a different role in this event")
			return
		}
		writeInternal(w)
		return
	}
	writeJSON(w, http.StatusOK, h.eventView(r.Context(), ev, member, false))
}

func checkHackText(w http.ResponseWriter, s, field string, min, max int) (string, bool) {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\r\n", "\n"))
	if hasBadControls(s) {
		writeHackErr(w, http.StatusBadRequest, "invalid_"+field, field+" contains characters that are not allowed")
		return "", false
	}
	n := utf8.RuneCountInString(s)
	if n < min || n > max {
		if min > 0 && n < min {
			writeHackErr(w, http.StatusBadRequest, "invalid_"+field, field+" is required")
			return "", false
		}
		writeHackErr(w, http.StatusBadRequest, "invalid_"+field, field+" is too long")
		return "", false
	}
	return s, true
}

// checkHackLine is checkHackText for a one-line field (a title, a name): no
// line breaks, runs of spaces collapsed (so "A  B" is not a second "A B"),
// and something visible in it.
func checkHackLine(w http.ResponseWriter, s, field string, min, max int) (string, bool) {
	if strings.ContainsAny(strings.TrimSpace(s), "\n\r\t\u0085\u2028\u2029") {
		writeHackErr(w, http.StatusBadRequest, "invalid_"+field, field+" must be one line")
		return "", false
	}
	s = strings.Join(strings.Fields(s), " ")
	if min > 0 && !hasVisible(s) {
		writeHackErr(w, http.StatusBadRequest, "invalid_"+field, field+" is required")
		return "", false
	}
	return checkHackText(w, s, field, min, max)
}

// hasBadControls: control characters other than newline and tab, the bidi
// embedding, override and isolate controls (they make a name read as
// something else), the zero-width space and the byte-order mark. Joiners,
// direction marks, soft hyphens and emoji tag sequences stay: real text
// needs them.
func hasBadControls(s string) bool {
	for _, r := range s {
		if r == '\n' || r == '\t' {
			continue
		}
		if unicode.IsControl(r) {
			return true
		}
		switch {
		case r >= 0x202A && r <= 0x202E, r >= 0x2066 && r <= 0x2069, r == 0x200B, r == 0xFEFF:
			return true
		}
	}
	return false
}

// hasVisible: s has at least one character that shows (not only spaces,
// marks or blank-looking letters such as the Hangul filler).
func hasVisible(s string) bool {
	for _, r := range s {
		switch r {
		case 0x3164, 0x115F, 0x1160, 0xFFA0, 0x2800, 0x180E:
			continue
		}
		if unicode.IsSpace(r) || unicode.Is(unicode.Cf, r) || unicode.Is(unicode.Mn, r) {
			continue
		}
		return true
	}
	return false
}

func checkContactEmail(w http.ResponseWriter, s string) (string, bool) {
	s = strings.TrimSpace(s)
	if s == "" || utf8.RuneCountInString(s) > 254 {
		writeHackErr(w, http.StatusBadRequest, "invalid_contact_email", "contact_email must be a valid email address")
		return "", false
	}
	addr, err := mail.ParseAddress(s)
	if err != nil || addr.Name != "" || !strings.EqualFold(addr.Address, s) {
		writeHackErr(w, http.StatusBadRequest, "invalid_contact_email", "contact_email must be a valid email address")
		return "", false
	}
	if utf8.RuneCountInString(addr.Address) > 254 {
		writeHackErr(w, http.StatusBadRequest, "invalid_contact_email", "contact_email must be a valid email address")
		return "", false
	}
	return addr.Address, true
}

func checkTimeZone(w http.ResponseWriter, tz string) (*time.Location, bool) {
	if tz == "Local" {
		writeHackErr(w, http.StatusBadRequest, "invalid_time_zone", "time_zone is not a valid IANA name")
		return nil, false
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		writeHackErr(w, http.StatusBadRequest, "invalid_time_zone", "time_zone is not a valid IANA name")
		return nil, false
	}
	return loc, true
}

func parseEventTime(w http.ResponseWriter, s string, loc *time.Location, field string, required bool) (sql.NullTime, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		if required {
			writeHackErr(w, http.StatusBadRequest, "invalid_"+field, field+" is required")
			return sql.NullTime{}, false
		}
		return sql.NullTime{}, true
	}
	if len(s) == 10 && s[4] == '-' && s[7] == '-' {
		t, err := time.ParseInLocation("2006-01-02", s, loc)
		if err != nil {
			writeHackErr(w, http.StatusBadRequest, "invalid_"+field, field+" must be RFC 3339 or YYYY-MM-DD")
			return sql.NullTime{}, false
		}
		return sql.NullTime{Time: t, Valid: true}, true
	}
	// A wall-clock time in the event's zone, as a datetime-local field sends it.
	for _, layout := range []string{"2006-01-02T15:04", "2006-01-02T15:04:05"} {
		if len(s) == len(layout) {
			if t, err := time.ParseInLocation(layout, s, loc); err == nil {
				// A wall-clock time the clocks skip (a daylight-saving gap)
				// does not exist in that zone: say so rather than move it.
				if t.In(loc).Format(layout) != s {
					writeHackErr(w, http.StatusBadRequest, "invalid_"+field, field+" is a time the clocks skip in "+loc.String()+"; pick another")
					return sql.NullTime{}, false
				}
				return sql.NullTime{Time: t, Valid: true}, true
			}
		}
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t, err = time.Parse(time.RFC3339Nano, s)
	}
	if err != nil {
		writeHackErr(w, http.StatusBadRequest, "invalid_"+field, field+" must be RFC 3339, YYYY-MM-DDTHH:MM in the event's time zone, or YYYY-MM-DD")
		return sql.NullTime{}, false
	}
	return sql.NullTime{Time: t, Valid: true}, true
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

func randomHackCode(n int) (string, error) {
	alpha := hackCodeAlphabet
	b := make([]byte, n)
	max := 256 - (256 % len(alpha))
	for i := 0; i < n; i++ {
		var buf [1]byte
		for {
			if _, err := rand.Read(buf[:]); err != nil {
				return "", err
			}
			if int(buf[0]) < max {
				b[i] = alpha[int(buf[0])%len(alpha)]
				break
			}
		}
	}
	return string(b), nil
}

func normalizeHackCode(s string) string {
	s = strings.ToLower(s)
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if r == ' ' || r == '-' {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func participantTeamStages(stage string) bool {
	return stage == "open" || stage == "building"
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
