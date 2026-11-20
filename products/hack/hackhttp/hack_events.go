package hackhttp

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/rules/sites"
	"github.com/vsriram/simple-host/products/hack/app/events"
	hackmodel "github.com/vsriram/simple-host/products/hack/model"
)

func eventNameReserved(name string) bool {
	return extraEventReserved[name] || sites.LabelReservedForNew(name)
}

func (h *HackHandler) checkName(w http.ResponseWriter, r *http.Request) {
	user := hackNeedUser(w, r)
	if user == nil {
		return
	}
	if !h.namesUser.AllowHTTP(w, user.ID) {
		httpx.TooManyRequests(w)
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

// slugVerdict is the create/check result for an event slug. Empty code means
// free. "--" is refused outright (not just the usual shape check): an event
// slug is also a DNS label in a hosted event's own and team addresses, and
// TEAM_SITE_FLAT_HOSTS' <team>--<event> single label needs "--" to appear
// nowhere else in either slug to stay unambiguous.
func (h *HackHandler) slugVerdict(ctx context.Context, slug string) (code, errMsg string, err error) {
	if !sites.EventNameShape(slug) {
		return "invalid_name", "use 3 to 39 lowercase letters, numbers or hyphens, not starting or ending with a hyphen, and never two hyphens in a row", nil
	}
	if eventNameReserved(slug) {
		return "name_reserved", "that name is reserved", nil
	}
	v, err := h.events.JudgeHandle(ctx, slug)
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
	taken, err := h.events.EventSlugTakenLookup(ctx, slug)
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
	if !httpx.DecodeJSON(w, r, hackMaxBody, &req) {
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
	title, err := events.EventTitle(req.Title)
	var organiserName, organisation, contact, purpose, tz string
	var loc *time.Location
	var startsAt, endsAt sql.NullTime
	if err == nil {
		organiserName, err = events.EventOrganiserName(req.OrganiserName)
	}
	if err == nil {
		organisation, err = events.EventOrganisation(req.Organisation)
	}
	if err == nil {
		contact, err = events.EventContactEmail(req.ContactEmail)
	}
	if err == nil {
		purpose, err = events.EventPurpose(req.Purpose)
	}
	expected := 0 // absent is refused like 0
	if req.ExpectedParticipants != nil {
		expected = *req.ExpectedParticipants
	}
	if err == nil {
		err = events.EventExpectedParticipants(expected)
	}
	if err == nil {
		tz, loc, err = events.EventTimeZone(req.TimeZone)
	}
	if err == nil {
		startsAt, err = events.ParseEventTime(req.StartsAt, loc, "starts_at")
	}
	if err == nil && strings.TrimSpace(req.EndsAt) != "" {
		if endsAt, err = events.ParseEventTime(req.EndsAt, loc, "ends_at"); err == nil {
			err = events.EventEnds(startsAt, endsAt, tz)
		}
	}
	if err != nil {
		writeHackError(w, err)
		return
	}
	full, err := h.instanceFull(r.Context())
	if err != nil || full {
		httpx.WriteError(w, http.StatusInsufficientStorage, "instance_full", "this instance is at capacity")
		return
	}

	perDay, maxActive := h.limits.EventCreatePerDay, h.limits.EventMaxActivePerOrganiser
	if user.IsAdmin || h.limits.EventLimitExempt(user.Username) {
		perDay, maxActive = 0, 0
	}
	ctx := r.Context()
	ev, member, err := h.events.CreateEvent(ctx, user.ID, slug, title, organiserName, organisation, contact, purpose, tz, expected, startsAt, endsAt, perDay, maxActive, h.limits.EventTeamSizeDefault, hackJoinCodeLen, hackJudgeCodeLen)
	if err != nil {
		writeHackError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, h.eventView(ctx, ev, member, true))
}

func (h *HackHandler) listEvents(w http.ResponseWriter, r *http.Request) {
	user := hackNeedUser(w, r)
	if user == nil {
		return
	}
	eventList, roles, err := h.events.EventsForUser(r.Context(), user.ID)
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	out := make([]map[string]any, 0, len(eventList))
	for i, ev := range eventList {
		item := map[string]any{
			"slug":         ev.Slug,
			"title":        ev.Title,
			"stage":        ev.Stage,
			"role":         roles[i],
			"can":          h.events.MemberAbilities(r.Context(), ev.ID, hackmodel.EventMember{UserID: user.ID, Role: roles[i], JoinedAt: ev.CreatedAt}),
			"ended_at":     rfc3339UTC(ev.ClosedAt),
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
			if status, err := h.members.MemberApprovalStatusLookup(r.Context(), ev.ID, user.ID); err == nil {
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

// submissionsClosedStage: stages after submissions close.
func submissionsClosedStage(stage string) bool { return events.SubmissionsClosedStage(stage) }

func (h *HackHandler) setStage(w http.ResponseWriter, r *http.Request) {
	a, ok := h.loadMember(w, r, r.PathValue("slug"), true, "organiser")
	if !ok {
		return
	}
	var req struct {
		Stage string `json:"stage"`
	}
	if !httpx.DecodeJSON(w, r, hackMaxBody, &req) {
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
	updated, current, err := h.events.SetStage(r.Context(), a.event, stage, h.entries.SettleEventTx)
	if err != nil {
		writeHackError(w, err)
		return
	}
	if stage == "judging" && (current == "draft" || current == "open" || current == "building" || current == "closed") && updated.JudgeAssignmentMode == "automatic" {
		allTeams, assignErr := h.entries.TeamsWithMembers(r.Context(), updated.ID)
		if assignErr == nil {
			pool, poolErr := h.judgingPoolFor(r.Context(), updated, allTeams)
			assignErr = poolErr
			if assignErr == nil {
				_, _, assignErr = h.entries.GenerateAssignments(r.Context(), updated.ID, nil, pool.In, true)
			}
		}
		if assignErr != nil {
			log.Printf("hack: stage assignments %s: %v", updated.Slug, assignErr)
		}
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
func (h *HackHandler) archiveEventTx(ctx context.Context, tx *sql.Tx, eventID, current string) (hackmodel.Event, error) {
	return h.events.ArchiveEventTx(ctx, tx, eventID, current, h.entries.SettleEventTx)
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
	updated, err := h.events.RegenerateCode(r.Context(), a.event.ID, kind, n)
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
		if !httpx.DecodeJSON(w, r, hackMaxBody, &req) {
			return
		}
	}
	var trash func(context.Context, string, string) error
	if h.sites != nil {
		trash = h.sites.TrashTeamSite
	}
	if err := h.events.DeleteEvent(r.Context(), a.event, a.user.ID, req.Confirm, trash); err != nil {
		writeHackError(w, err)
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
func (h *HackHandler) deleteEventTx(ctx context.Context, tx *sql.Tx, ev hackmodel.Event) error {
	var trash func(context.Context, string, string) error
	if h.sites != nil {
		trash = h.sites.TrashTeamSite
	}
	return h.events.DeleteEventTx(ctx, tx, ev, trash)
}

func organiserTeamStages(stage string) bool {
	return stage != "archived"
}
