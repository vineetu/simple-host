package hackhttp

import (
	"database/sql"
	"errors"
	"net/http"
	"strings"

	"github.com/vsriram/simple-host/host/access/auth"
	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/products/hack/app/events"
	hackmodel "github.com/vsriram/simple-host/products/hack/model"
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
	caller := ""
	accountName := ""
	if u := auth.GetUser(r.Context()); u != nil {
		caller = u.ID
		accountName = strings.TrimSpace(u.DisplayName.String)
	}
	// The body carries the caller's own account name: never cache it.
	w.Header().Set("Cache-Control", "no-store")
	if h.rateLimited(w, r, caller) {
		return
	}
	code := events.NormalizeCode(r.PathValue("code"))
	if code == "" {
		httpx.WriteError(w, http.StatusNotFound, "invalid_code", "invalid code")
		return
	}
	var ev hackmodel.Event
	var err error
	if judge {
		ev, err = h.events.EventByJudgeCode(r.Context(), code)
	} else {
		ev, err = h.events.EventByJoinCode(r.Context(), code)
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
		httpx.WriteError(w, http.StatusForbidden, "event_taken_down", "this event has been taken down")
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
	questions := []hackmodel.SignupQuestion{}
	approvalRequired := false
	if !judge {
		questions, approvalRequired, err = h.members.RegistrationSettingsLookup(r.Context(), ev.ID)
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
		"account_name":      accountName,
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
	httpx.WriteJSON(w, http.StatusOK, h.eventView(r.Context(), ev, member, organiser))
}
