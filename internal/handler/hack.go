package handler

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/mail"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/vsriram/simple-host/internal/auth"
	"github.com/vsriram/simple-host/internal/config"
	"github.com/vsriram/simple-host/internal/db"
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
}

const (
	hackCodeAlphabet = "abcdefghjkmnpqrstuvwxyz23456789"
	hackJoinCodeLen  = 8
	hackJudgeCodeLen = 12
	hackTeamCodeLen  = 6
	hackMaxBody      = 64 << 10
)

var hackStages = map[string]bool{
	"draft": true, "open": true, "building": true, "closed": true,
	"judging": true, "results": true, "archived": true,
}

// HackHandler is the hosted-events API (EVENTS=hosted).
type HackHandler struct {
	database      *sql.DB
	publicBaseURL string
	siteDomain    string
	limiter       *rateLimiter
	namePeer      func(ctx context.Context, name string) (bool, error)
	usageFn       func(ctx context.Context) (int64, error)
}

// NewHackHandler builds the hosted-events API. publicBaseURL is the apex
// (join/judge/manage links); siteDomain is the event host parent.
func NewHackHandler(database *sql.DB, publicBaseURL, siteDomain string) *HackHandler {
	return &HackHandler{
		database:      database,
		publicBaseURL: strings.TrimRight(publicBaseURL, "/"),
		siteDomain:    strings.Trim(strings.ToLower(siteDomain), "."),
		limiter:       newRateLimiter(20, 1.0/3.0),
	}
}

// Register adds every /v1/hack/... and /v1/admin/hack/... route in the spec.
// authMW is auth.Middleware (X-API-Key).
func (h *HackHandler) Register(mux *http.ServeMux, authMW func(http.Handler) http.Handler) {
	wrap := func(fn http.HandlerFunc) http.Handler {
		return authMW(http.HandlerFunc(fn))
	}
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
	mux.Handle("POST /v1/admin/hack/events/{slug}/restore", wrap(h.adminRestore))
	mux.Handle("DELETE /v1/admin/hack/events/{slug}", wrap(h.adminDelete))
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

func (h *HackHandler) rateLimited(w http.ResponseWriter, r *http.Request, userID string) bool {
	if !h.limiter.allow("ip:" + clientIP(r)) {
		writeJSON(w, http.StatusTooManyRequests, errorResponse{Error: "rate limit exceeded, slow down", Code: "rate_limited"})
		return true
	}
	if userID != "" && !h.limiter.allow("user:"+userID) {
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
	if h.rateLimited(w, r, user.ID) {
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
	title, ok := checkHackText(w, req.Title, "title", 1, 120)
	if !ok {
		return
	}
	organiserName, ok := checkHackText(w, req.OrganiserName, "organiser_name", 1, 100)
	if !ok {
		return
	}
	organisation, ok := checkHackText(w, req.Organisation, "organisation", 0, 120)
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
		ev, err = db.InsertEvent(ctx, tx, db.Event{
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
			"slug":       ev.Slug,
			"title":      ev.Title,
			"stage":      ev.Stage,
			"role":       roles[i],
			"url":        h.EventURL(ev.Slug),
			"manage_url": h.manageURL(ev.Slug),
			"starts_at":  rfc3339UTC(ev.StartsAt),
			"ends_at":    rfc3339UTC(ev.EndsAt),
			"taken_down": ev.TakenDown(),
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
	writeJSON(w, http.StatusOK, h.eventView(r.Context(), a.event, a.member, a.member.Role == "organiser" || a.admin))
}

func (h *HackHandler) eventView(ctx context.Context, ev db.Event, member db.EventMember, organiser bool) map[string]any {
	// The code of conduct is the default text plus the organiser's own.
	cocText := ev.CocText
	body := map[string]any{
		"event": map[string]any{
			"slug":               ev.Slug,
			"title":              ev.Title,
			"tagline":            ev.Tagline,
			"about":              ev.About,
			"rules":              ev.Rules,
			"prizes":             ev.Prizes,
			"coc_text":           cocText,
			"coc_default":        HackDefaultCoC,
			"stage":              ev.Stage,
			"time_zone":          ev.TimeZone,
			"starts_at":          rfc3339UTC(ev.StartsAt),
			"ends_at":            rfc3339UTC(ev.EndsAt),
			"team_size_max":      ev.TeamSizeMax,
			"url":                h.EventURL(ev.Slug),
			"taken_down":         ev.TakenDown(),
			"results_visibility": ev.ResultsVisibility,
		},
		"role": member.Role,
		"me":   h.meView(ctx, ev, member),
	}
	if organiser {
		participants, teams, judges, _ := db.CountEventMembers(ctx, h.database, ev.ID)
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
	return body
}

func (h *HackHandler) meView(ctx context.Context, ev db.Event, member db.EventMember) map[string]any {
	me := map[string]any{
		"display_name":    member.DisplayName,
		"coc_accepted_at": rfc3339UTC(member.CocAcceptedAt),
		"team":            nil,
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
	me["team"] = map[string]any{
		"slug":    team.Slug,
		"name":    team.Name,
		"code":    team.Code,
		"members": members,
	}
	return me
}

type patchEventReq struct {
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
	ev := a.event
	if req.Title != nil {
		s, ok := checkHackText(w, *req.Title, "title", 1, 120)
		if !ok {
			return
		}
		ev.Title = s
	}
	if req.Tagline != nil {
		s, ok := checkHackText(w, *req.Tagline, "tagline", 0, 160)
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
		s, ok := checkHackText(w, *req.OrganiserName, "organiser_name", 1, 100)
		if !ok {
			return
		}
		ev.OrganiserName = s
	}
	if req.Organisation != nil {
		s, ok := checkHackText(w, *req.Organisation, "organisation", 0, 120)
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
	updated, err := db.UpdateEventPatch(r.Context(), h.database, ev)
	if err != nil {
		writeInternal(w)
		return
	}
	writeJSON(w, http.StatusOK, h.eventView(r.Context(), updated, a.member, true))
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
	if a.event.Stage == "archived" && stage != "archived" {
		writeHackErr(w, http.StatusConflict, "event_closed", "an archived event cannot be reopened")
		return
	}
	updated, err := db.SetEventStage(r.Context(), h.database, a.event.ID, stage)
	if err != nil {
		writeInternal(w)
		return
	}
	writeJSON(w, http.StatusOK, h.eventView(r.Context(), updated, a.member, true))
}

func (h *HackHandler) regenCode(w http.ResponseWriter, r *http.Request) {
	a, ok := h.loadMember(w, r, r.PathValue("slug"), true, "organiser")
	if !ok {
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
	if a.event.Stage != "draft" {
		writeHackErr(w, http.StatusConflict, "delete_only_draft", "only a draft event can be deleted")
		return
	}
	tx, err := h.database.BeginTx(r.Context(), nil)
	if err != nil {
		writeInternal(w)
		return
	}
	defer tx.Rollback()
	if err := db.DeleteEventAndAccount(r.Context(), tx, a.event); err != nil {
		writeInternal(w)
		return
	}
	if err := tx.Commit(); err != nil {
		writeInternal(w)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *HackHandler) getJoin(w http.ResponseWriter, r *http.Request) {
	h.getCodeInfo(w, r, false)
}

func (h *HackHandler) getJudge(w http.ResponseWriter, r *http.Request) {
	h.getCodeInfo(w, r, true)
}

func (h *HackHandler) getCodeInfo(w http.ResponseWriter, r *http.Request, judge bool) {
	if h.rateLimited(w, r, "") {
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
	writeJSON(w, http.StatusOK, map[string]any{
		"slug":           ev.Slug,
		"title":          ev.Title,
		"tagline":        ev.Tagline,
		"organiser_name": ev.OrganiserName,
		"organisation":   ev.Organisation,
		"stage":          ev.Stage,
		"joinable":       joinable,
		"role":           role,
		"coc_default":    HackDefaultCoC,
		"coc_text":       cocText,
		"starts_at":      rfc3339UTC(ev.StartsAt),
		"ends_at":        rfc3339UTC(ev.EndsAt),
		"time_zone":      ev.TimeZone,
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
		AcceptCoC   *bool  `json:"accept_coc"`
		DisplayName string `json:"display_name"`
	}
	if !decodeHackJSON(w, r, &req) {
		return
	}
	if req.AcceptCoC == nil || !*req.AcceptCoC {
		writeHackErr(w, http.StatusBadRequest, "coc_required", "you must accept the code of conduct")
		return
	}
	display, ok := checkHackText(w, req.DisplayName, "display_name", 1, 100)
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
	member, err := db.InsertEventMember(r.Context(), h.database, ev.ID, user.ID, role, display)
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
	s = strings.TrimSpace(s)
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

func hasBadControls(s string) bool {
	for _, r := range s {
		if r == '\n' || r == '\t' {
			continue
		}
		if unicode.IsControl(r) {
			return true
		}
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
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t, err = time.Parse(time.RFC3339Nano, s)
	}
	if err != nil {
		writeHackErr(w, http.StatusBadRequest, "invalid_"+field, field+" must be RFC 3339 or YYYY-MM-DD")
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
