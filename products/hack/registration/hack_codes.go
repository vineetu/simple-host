package registration

import (
	"net/http"
	"strings"

	core "github.com/vsriram/simple-host/products/hack/core"

	"github.com/vsriram/simple-host/host/access/auth"
	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/products/hack/app/events"
)

func (h *handler) getJoin(w http.ResponseWriter, r *http.Request) {
	h.getCodeInfo(w, r, false)
}

func (h *handler) getJudge(w http.ResponseWriter, r *http.Request) {
	h.getCodeInfo(w, r, true)
}

func (h *handler) getCodeInfo(w http.ResponseWriter, r *http.Request, judge bool) {
	// The route answers signed out, but a signed-in page sends its key: then
	// the account is metered, not the venue's shared address.
	caller, accountName := "", ""
	if u := auth.GetUser(r.Context()); u != nil {
		caller, accountName = u.ID, strings.TrimSpace(u.DisplayName.String)
	}
	// The body carries the caller's own account name: never cache it.
	w.Header().Set("Cache-Control", "no-store")
	if h.Codes.Limited(w, r, caller) {
		return
	}
	inv, err := h.Events.InviteByCode(r.Context(), r.PathValue("code"), judge)
	if err != nil {
		core.WriteError(w, err)
		return
	}
	ev := inv.Event
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"slug":         ev.Slug,
		"title":        ev.Title,
		"icon_url":     h.Links.IconURL(ev),
		"accent_color": ev.AccentColor, "color": core.EventAccent(ev),
		"tagline":           ev.Tagline,
		"organiser_name":    ev.OrganiserName,
		"organisation":      ev.Organisation,
		"stage":             ev.Stage,
		"joinable":          inv.Joinable,
		"account_name":      accountName,
		"role":              inv.Role,
		"coc_default":       core.DefaultCoC,
		"coc_text":          ev.CocText,
		"starts_at":         core.RFC3339(ev.StartsAt),
		"ends_at":           core.RFC3339(ev.EndsAt),
		"time_zone":         ev.TimeZone,
		"signup_questions":  inv.Questions,
		"approval_required": inv.ApprovalRequired,
	})
}

func (h *handler) postJoin(w http.ResponseWriter, r *http.Request) {
	h.postCodeJoin(w, r, false)
}

func (h *handler) postJudge(w http.ResponseWriter, r *http.Request) {
	h.postCodeJoin(w, r, true)
}

func (h *handler) postCodeJoin(w http.ResponseWriter, r *http.Request, judge bool) {
	user := core.NeedUser(w, r)
	if user == nil {
		return
	}
	if h.Codes.Limited(w, r, user.ID) {
		return
	}
	var req struct {
		AcceptCoC   *bool             `json:"accept_coc"`
		DisplayName string            `json:"display_name"`
		Answers     map[string]string `json:"answers"`
	}
	if !httpx.DecodeJSON(w, r, core.MaxBody, &req) {
		return
	}
	if req.AcceptCoC == nil || !*req.AcceptCoC {
		httpx.WriteError(w, http.StatusBadRequest, "coc_required", "you must accept the code of conduct")
		return
	}
	display := req.DisplayName
	code := events.NormalizeCode(r.PathValue("code"))
	if code == "" {
		httpx.WriteError(w, http.StatusNotFound, "invalid_code", "invalid code")
		return
	}
	ev, member, organiser, err := h.Events.JoinByCode(r.Context(), code, user.ID, display, judge, req.Answers, events.ValidateSignupAnswers)
	if err != nil {
		core.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, h.EventView(r.Context(), ev, member, organiser))
}
