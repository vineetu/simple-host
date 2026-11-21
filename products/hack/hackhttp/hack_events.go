package hackhttp

import (
	"context"
	"errors"
	"log"
	"net/http"
	"strings"

	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/rules/sites"
	"github.com/vsriram/simple-host/products/hack/app/events"
	hackmodel "github.com/vsriram/simple-host/products/hack/model"
)

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
	out := map[string]any{"address": h.links.EventURL(slug)}
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

func (h *HackHandler) slugVerdict(ctx context.Context, slug string) (code, errMsg string, err error) {
	if !sites.EventNameShape(slug) {
		return "invalid_name", "use 3 to 39 lowercase letters, numbers or hyphens, not starting or ending with a hyphen, and never two hyphens in a row", nil
	}
	if events.NameReserved(slug) {
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

func (h *HackHandler) createEvent(w http.ResponseWriter, r *http.Request) {
	user := hackNeedUser(w, r)
	if user == nil {
		return
	}
	var req struct {
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
	if !httpx.DecodeJSON(w, r, hackMaxBody, &req) {
		return
	}
	slug := strings.ToLower(strings.TrimSpace(req.Slug))
	if !h.slugFree(w, r, slug) {
		return
	}
	in, err := events.CheckNewEvent(events.NewEventInput(req))
	if err != nil {
		writeHackError(w, err)
		return
	}
	if full, err := h.instanceFull(r.Context()); err != nil || full {
		httpx.WriteError(w, http.StatusInsufficientStorage, "instance_full", "this instance is at capacity")
		return
	}
	perDay, maxActive := h.limits.EventCreatePerDay, h.limits.EventMaxActivePerOrganiser
	if user.IsAdmin || h.limits.EventLimitExempt(user.Username) {
		perDay, maxActive = 0, 0
	}
	ev, member, err := h.events.CreateEvent(r.Context(), user.ID, slug, in.Title, in.OrganiserName, in.Organisation, in.ContactEmail, in.Purpose, in.TimeZone, in.ExpectedParticipants, in.StartsAt, in.EndsAt, perDay, maxActive, h.limits.EventTeamSizeDefault, hackJoinCodeLen, hackJudgeCodeLen)
	if err != nil {
		writeHackError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, h.views.eventView(r.Context(), ev, member, true))
}

func (h *HackHandler) slugFree(w http.ResponseWriter, r *http.Request, slug string) bool {
	code, errMsg, err := h.slugVerdict(r.Context(), slug)
	switch {
	case errors.Is(err, errNameCheckUnavailable):
		httpx.WriteError(w, http.StatusServiceUnavailable, "name_check_unavailable", "could not check whether this name is free; try again")
	case err != nil:
		httpx.WriteInternal(w)
	case code == "name_taken":
		httpx.WriteError(w, http.StatusConflict, code, errMsg)
	case code != "":
		httpx.WriteError(w, http.StatusBadRequest, code, errMsg)
	default:
		return true
	}
	return false
}

func (h *HackHandler) listEvents(w http.ResponseWriter, r *http.Request) {
	user := hackNeedUser(w, r)
	if user == nil {
		return
	}
	eventList, members, err := h.events.EventsForUser(r.Context(), user.ID)
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
			"role":         members[i].Role,
			"can":          h.events.MemberAbilities(r.Context(), ev.ID, hackmodel.EventMember{UserID: user.ID, Role: members[i].Role, JoinedAt: ev.CreatedAt}),
			"ended_at":     rfc3339UTC(ev.ClosedAt),
			"url":          h.links.EventURL(ev.Slug),
			"icon_url":     h.links.iconURL(ev),
			"accent_color": ev.AccentColor, "color": hackEventAccent(ev),
			"manage_url": h.manageURL(ev.Slug),
			"starts_at":  rfc3339UTC(ev.StartsAt),
			"ends_at":    rfc3339UTC(ev.EndsAt),
			"taken_down": ev.TakenDown(),
			"time_zone":  ev.TimeZone,
		}
		if members[i].Participant() {
			item["approval_status"] = members[i].ApprovalStatus
		}
		out = append(out, item)
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

func (h *HackHandler) getEvent(w http.ResponseWriter, r *http.Request) {
	a, ok := loadMember(w, r, h.members, r.PathValue("slug"), false)
	if !ok {
		return
	}
	h.links.pinDue(r.Context(), a.event.ID) // every read of pins pins first
	view := h.views.eventView(r.Context(), a.event, a.member, a.member.Role == "organiser" || a.admin)
	if a.admin && a.member.JoinedAt.IsZero() {
		// The platform admin reading an event it is not in: every write here
		// is refused, so the page shows it read-only.
		view["admin_view"] = true
	}
	httpx.WriteJSON(w, http.StatusOK, view)
}

func (h *HackHandler) setStage(w http.ResponseWriter, r *http.Request) {
	a, ok := loadMember(w, r, h.members, r.PathValue("slug"), true, "organiser")
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
	updated, current, err := h.events.SetStage(r.Context(), a.event, stage)
	if err != nil {
		writeHackError(w, err)
		return
	}
	if err := h.entries.AssignOnStartJudging(r.Context(), updated, current); err != nil {
		log.Printf("hack: stage assignments %s: %v", updated.Slug, err)
	}
	if current != updated.Stage {
		paperHackStage(r, updated)
	}
	paperHackTimes(r)
	h.links.pinDue(r.Context(), updated.ID)
	h.links.requestEventCert(updated)
	httpx.WriteJSON(w, http.StatusOK, h.views.eventView(r.Context(), updated, a.member, true))
}

func (h *HackHandler) regenCode(w http.ResponseWriter, r *http.Request) {
	a, ok := loadMember(w, r, h.members, r.PathValue("slug"), true, "organiser")
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
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"code": updated.JoinCode, "url": h.links.joinURL(updated.JoinCode)})
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"code": updated.JudgeCode, "url": h.links.judgeURL(updated.JudgeCode)})
}

func (h *HackHandler) deleteEvent(w http.ResponseWriter, r *http.Request) {
	a, ok := loadMember(w, r, h.members, r.PathValue("slug"), true, "organiser")
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
	if err := h.events.DeleteEvent(r.Context(), a.event, a.user.ID, req.Confirm, h.links.trash()); err != nil {
		writeHackError(w, err)
		return
	}
	h.links.removeEventFiles(a.event)
	w.WriteHeader(http.StatusNoContent)
}
