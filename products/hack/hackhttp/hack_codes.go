package hackhttp

import (
	"net/http"
	"strings"

	"github.com/vsriram/simple-host/host/access/auth"
	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/products/hack/app/events"
)

func (h *HackHandler) getJoin(w http.ResponseWriter, r *http.Request) {
	h.getCodeInfo(w, r, false)
}

func (h *HackHandler) getJudge(w http.ResponseWriter, r *http.Request) {
	h.getCodeInfo(w, r, true)
}

func (h *HackHandler) getCodeInfo(w http.ResponseWriter, r *http.Request, judge bool) {
	// The route answers signed out, but a signed-in page sends its key: then
	// the account is metered, not the venue's shared address.
	caller, accountName := "", ""
	if u := auth.GetUser(r.Context()); u != nil {
		caller, accountName = u.ID, strings.TrimSpace(u.DisplayName.String)
	}
	// The body carries the caller's own account name: never cache it.
	w.Header().Set("Cache-Control", "no-store")
	if h.codes.limited(w, r, caller) {
		return
	}
	inv, err := h.events.InviteByCode(r.Context(), r.PathValue("code"), judge)
	if err != nil {
		writeHackError(w, err)
		return
	}
	ev := inv.Event
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"slug":         ev.Slug,
		"title":        ev.Title,
		"icon_url":     h.links.iconURL(ev),
		"accent_color": ev.AccentColor, "color": hackEventAccent(ev),
		"tagline":           ev.Tagline,
		"organiser_name":    ev.OrganiserName,
		"organisation":      ev.Organisation,
		"stage":             ev.Stage,
		"joinable":          inv.Joinable,
		"account_name":      accountName,
		"role":              inv.Role,
		"coc_default":       HackDefaultCoC,
		"coc_text":          ev.CocText,
		"starts_at":         rfc3339UTC(ev.StartsAt),
		"ends_at":           rfc3339UTC(ev.EndsAt),
		"time_zone":         ev.TimeZone,
		"signup_questions":  inv.Questions,
		"approval_required": inv.ApprovalRequired,
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
	if h.codes.limited(w, r, user.ID) {
		return
	}
	var req struct {
		AcceptCoC   *bool             `json:"accept_coc"`
		DisplayName string            `json:"display_name"`
		Answers     map[string]string `json:"answers"`
	}
	if !httpx.DecodeJSON(w, r, hackMaxBody, &req) {
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
	ev, member, organiser, err := h.events.JoinByCode(r.Context(), code, user.ID, display, judge, req.Answers, validateSignupAnswersValue)
	if err != nil {
		writeHackError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, h.views.eventView(r.Context(), ev, member, organiser))
}
