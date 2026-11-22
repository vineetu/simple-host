package lifecycle

import (
	"context"
	"errors"
	"log"
	"net/http"
	"strings"

	core "github.com/vsriram/simple-host/products/hack/core"

	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/rules/sites"
	"github.com/vsriram/simple-host/products/hack/app/events"
	hackmodel "github.com/vsriram/simple-host/products/hack/model"
)

func (h *Handler) checkName(w http.ResponseWriter, r *http.Request) {
	user := core.NeedUser(w, r)
	if user == nil {
		return
	}
	if !h.NamesUser.AllowHTTP(w, user.ID) {
		httpx.TooManyRequests(w)
		return
	}
	slug := strings.ToLower(strings.TrimSpace(r.PathValue("slug")))
	out := map[string]any{"address": h.Links.EventURL(slug)}
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

func (h *Handler) slugVerdict(ctx context.Context, slug string) (code, errMsg string, err error) {
	if !sites.EventNameShape(slug) {
		return "invalid_name", "use 3 to 39 lowercase letters, numbers or hyphens, not starting or ending with a hyphen, and never two hyphens in a row", nil
	}
	if events.NameReserved(slug) {
		return "name_reserved", "that name is reserved", nil
	}
	v, err := h.Events.JudgeHandle(ctx, slug)
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
	taken, err := h.Events.EventSlugTakenLookup(ctx, slug)
	if err != nil {
		return "", "", err
	}
	if taken {
		return "name_taken", "that name is already in use", nil
	}
	if h.NamePeer != nil {
		peerTaken, err := h.NamePeer(ctx, slug)
		if err != nil {
			return "", "", errNameCheckUnavailable
		}
		if peerTaken {
			return "name_taken", "that name is already in use", nil
		}
	}
	return "", "", nil
}

func (h *Handler) instanceFull(ctx context.Context) (bool, error) {
	budgetGB := h.Limits.HackInstanceBudgetGB
	if budgetGB <= 0 {
		return false, nil
	}
	var used int64
	if h.InstanceUsage != nil {
		var err error
		used, err = h.InstanceUsage(ctx)
		if err != nil {
			return true, err
		}
	}
	limit := int64(budgetGB) * 1024 * 1024 * 1024 * 80 / 100
	return used >= limit, nil
}

func (h *Handler) createEvent(w http.ResponseWriter, r *http.Request) {
	user := core.NeedUser(w, r)
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
	if !httpx.DecodeJSON(w, r, core.MaxBody, &req) {
		return
	}
	slug := strings.ToLower(strings.TrimSpace(req.Slug))
	if !h.slugFree(w, r, slug) {
		return
	}
	in, err := events.CheckNewEvent(events.NewEventInput(req))
	if err != nil {
		core.WriteError(w, err)
		return
	}
	if full, err := h.instanceFull(r.Context()); err != nil || full {
		httpx.WriteError(w, http.StatusInsufficientStorage, "instance_full", "this instance is at capacity")
		return
	}
	perDay, maxActive := h.Limits.EventCreatePerDay, h.Limits.EventMaxActivePerOrganiser
	if user.IsAdmin || h.Limits.EventLimitExempt(user.Username) {
		perDay, maxActive = 0, 0
	}
	ev, member, err := h.Events.CreateEvent(r.Context(), user.ID, slug, in.Title, in.OrganiserName, in.Organisation, in.ContactEmail, in.Purpose, in.TimeZone, in.ExpectedParticipants, in.StartsAt, in.EndsAt, perDay, maxActive, h.Limits.EventTeamSizeDefault, events.JoinCodeLen, events.JudgeCodeLen)
	if err != nil {
		core.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, h.EventView(r.Context(), ev, member, true))
}

func (h *Handler) slugFree(w http.ResponseWriter, r *http.Request, slug string) bool {
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

func (h *Handler) listEvents(w http.ResponseWriter, r *http.Request) {
	user := core.NeedUser(w, r)
	if user == nil {
		return
	}
	eventList, members, err := h.Events.EventsForUser(r.Context(), user.ID)
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
			"can":          h.Events.MemberAbilities(r.Context(), ev.ID, hackmodel.EventMember{UserID: user.ID, Role: members[i].Role, JoinedAt: ev.CreatedAt}),
			"ended_at":     core.RFC3339(ev.ClosedAt),
			"url":          h.Links.EventURL(ev.Slug),
			"icon_url":     h.Links.IconURL(ev),
			"accent_color": ev.AccentColor, "color": core.EventAccent(ev),
			"manage_url": h.manageURL(ev.Slug),
			"starts_at":  core.RFC3339(ev.StartsAt),
			"ends_at":    core.RFC3339(ev.EndsAt),
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

func (h *Handler) getEvent(w http.ResponseWriter, r *http.Request) {
	a, ok := core.LoadMember(w, r, h.Members, r.PathValue("slug"), false)
	if !ok {
		return
	}
	h.Links.PinDue(r.Context(), a.Event.ID) // every read of pins pins first
	view := h.EventView(r.Context(), a.Event, a.Member, a.Member.Role == "organiser" || a.Admin)
	if a.Admin && a.Member.JoinedAt.IsZero() {
		// The platform admin reading an event it is not in: every write here
		// is refused, so the page shows it read-only.
		view["admin_view"] = true
	}
	httpx.WriteJSON(w, http.StatusOK, view)
}

func (h *Handler) setStage(w http.ResponseWriter, r *http.Request) {
	a, ok := core.LoadMember(w, r, h.Members, r.PathValue("slug"), true, "organiser")
	if !ok {
		return
	}
	var req struct {
		Stage string `json:"stage"`
	}
	if !httpx.DecodeJSON(w, r, core.MaxBody, &req) {
		return
	}
	stage := strings.TrimSpace(req.Stage)
	if !hackStages[stage] {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_stage", "stage is not a known event stage")
		return
	}
	updated, current, err := h.Events.SetStage(r.Context(), a.Event, stage)
	if err != nil {
		core.WriteError(w, err)
		return
	}
	if err := h.Entries.AssignOnStartJudging(r.Context(), updated, current); err != nil {
		log.Printf("hack: stage assignments %s: %v", updated.Slug, err)
	}
	if current != updated.Stage {
		core.PaperStage(r, updated)
	}
	core.PaperTimes(r)
	h.Links.PinDue(r.Context(), updated.ID)
	h.Links.RequestEventCert(updated)
	httpx.WriteJSON(w, http.StatusOK, h.EventView(r.Context(), updated, a.Member, true))
}

func (h *Handler) regenCode(w http.ResponseWriter, r *http.Request) {
	a, ok := core.LoadMember(w, r, h.Members, r.PathValue("slug"), true, "organiser")
	if !ok {
		return
	}
	if a.Event.Stage == "archived" {
		httpx.WriteError(w, http.StatusConflict, "event_closed", "an ended event cannot be changed")
		return
	}
	kind := r.PathValue("kind")
	var n int
	switch kind {
	case "join":
		n = events.JoinCodeLen
	case "judge":
		n = events.JudgeCodeLen
	default:
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "kind must be join or judge")
		return
	}
	updated, err := h.Events.RegenerateCode(r.Context(), a.Event.ID, kind, n)
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	if kind == "join" {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"code": updated.JoinCode, "url": h.Links.JoinURL(updated.JoinCode)})
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"code": updated.JudgeCode, "url": h.Links.JudgeURL(updated.JudgeCode)})
}

func (h *Handler) deleteEvent(w http.ResponseWriter, r *http.Request) {
	a, ok := core.LoadMember(w, r, h.Members, r.PathValue("slug"), true, "organiser")
	if !ok {
		return
	}
	var req struct {
		Confirm *string `json:"confirm"`
	}
	if r.Body != nil && r.ContentLength != 0 {
		if !httpx.DecodeJSON(w, r, core.MaxBody, &req) {
			return
		}
	}
	if err := h.Events.DeleteEvent(r.Context(), a.Event, a.User.ID, req.Confirm, h.Links.Trash()); err != nil {
		core.WriteError(w, err)
		return
	}
	h.Links.RemoveEventFiles(a.Event)
	w.WriteHeader(http.StatusNoContent)
}
