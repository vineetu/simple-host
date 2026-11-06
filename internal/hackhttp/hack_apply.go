package hackhttp

import (
	"database/sql"
	"errors"
	"net/http"
	"strings"

	"github.com/vsriram/simple-host/internal/app/hack/events"
	"github.com/vsriram/simple-host/internal/app/hack/members"
	"github.com/vsriram/simple-host/internal/httpx"
	"github.com/vsriram/simple-host/internal/model"
)

// Applying to a hackathon without a join link (zp-hack-applications.sql,
// owner 2026-10-31): GET/POST /v1/hack/events/{slug}/apply. D2: a separate
// route from /v1/hack/join/{code}, so the join code stays a private
// capability and this stays a public action keyed by the event's slug, with
// its own rules (the organiser's switch, and an application is always
// pending — D1).

// hackAgeRangeOrder/hackAgeRangeLabels and hackGenderOrder/hackGenderLabels
// moved to internal/rules/hackevents (AgeRangeOrder/AgeRangeLabels,
// GenderOrder/GenderLabels) in cleanup 3.2 slice 1c: fixed product choices,
// no handler dependency.

// hackUnder18Note is plain prose for non-browser consumers of GET .../apply;
// the public-apply.js form itself never renders this string -- it builds
// the same sentence with "Terms" as a real link to /terms (review item 6:
// the API's own copy of this sentence must not bake the URL into visible
// text either, since a client showing it verbatim would repeat the same
// mistake).
const hackUnder18Note = "If you're under 18, a parent or guardian needs to agree to Simple Hack's Terms for you."

// hackApplicationsOpenNow moved to internal/rules/hackevents
// (ApplicationsOpenNow) in cleanup 3.2 slice 4: fixed product decision, no
// handler dependency.

func hackApplyFields() map[string]any {
	ageRanges := make([]map[string]string, 0, len(events.AgeRangeOrder))
	for _, v := range events.AgeRangeOrder {
		ageRanges = append(ageRanges, map[string]string{"value": v, "label": events.AgeRangeLabels[v]})
	}
	genders := make([]map[string]string, 0, len(events.GenderOrder))
	for _, v := range events.GenderOrder {
		genders = append(genders, map[string]string{"value": v, "label": events.GenderLabels[v]})
	}
	return map[string]any{
		"city":          map[string]any{"max_length": 80},
		"country":       map[string]any{"format": "ISO 3166-1 alpha-2, e.g. ID, GB, US"},
		"age_range":     ageRanges,
		"gender":        genders,
		"under_18_note": hackUnder18Note,
	}
}

// getApply is GET /v1/hack/events/{slug}/apply. Signed out allowed; a valid
// X-API-Key meters the account instead of the shared network address, as
// getCodeInfo does for join/judge links.
func (h *HackHandler) getApply(w http.ResponseWriter, r *http.Request) {
	caller := ""
	accountName := ""
	if k := r.Header.Get("X-API-Key"); k != "" {
		if u, err := h.eventsSvc().UserByAPIKey(r.Context(), k, h.limits.KeyIdleExpiry); err == nil {
			caller = u.ID
			accountName = strings.TrimSpace(u.DisplayName.String)
		}
	}
	// The body carries the caller's own account name: never cache it.
	w.Header().Set("Cache-Control", "no-store")
	if h.rateLimited(w, r, caller) {
		return
	}
	slug := strings.ToLower(strings.TrimSpace(r.PathValue("slug")))
	ev, err := h.membersSvc().ApplyEvent(r.Context(), slug)
	if err == nil {
		paperHackEvent(r, ev)
	}
	if errors.Is(err, sql.ErrNoRows) {
		writeEventNotFound(w)
		return
	}
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	// A draft or taken-down event is 404, identical to an unknown slug: its
	// existence must not leak either way.
	if ev.Stage == "draft" || ev.TakenDown() {
		writeEventNotFound(w)
		return
	}
	openNow := events.ApplicationsOpenNow(ev.ApplicationsOpen, ev.DirectoryListed, ev.Stage, ev.TakenDown())
	out := map[string]any{
		"slug": ev.Slug, "title": ev.Title, "tagline": ev.Tagline,
		"icon_url": h.iconURL(ev), "color": hackEventAccent(ev),
		"organiser_name": ev.OrganiserName, "organisation": ev.Organisation,
		"stage":             ev.Stage,
		"starts_at":         rfc3339UTC(ev.StartsAt),
		"ends_at":           rfc3339UTC(ev.EndsAt),
		"time_zone":         ev.TimeZone,
		"event_url":         h.EventURL(ev.Slug),
		"applications_open": openNow,
		"account_name":      accountName,
		"closed_reason":     events.ApplicationsClosedReason(ev.ApplicationsOpen, ev.DirectoryListed, ev.Stage),
		"fields":            hackApplyFields(),
	}
	// Questions and the organiser's own code of conduct are for people they
	// invited to answer. They used to require the join code, including for a
	// signed-out caller (security review #5); keep that protection by
	// returning them only when applications are open right now, to a
	// signed-in caller -- not to every account for every non-draft slug.
	// `me` stays available to a member either way, open or closed.
	if caller != "" {
		questions, status, err := h.membersSvc().ApplyDetails(r.Context(), ev, caller, openNow)
		if err != nil {
			httpx.WriteInternal(w)
			return
		}
		if openNow {
			out["signup_questions"] = questions
			out["coc_default"] = HackDefaultCoC
			out["coc_text"] = ev.CocText
		}
		if status == nil {
			out["me"] = nil
		} else {
			me := map[string]any{"role": status.Role, "joined_via": status.JoinedVia}
			if status.Role == "participant" {
				me["approval_status"] = status.ApprovalStatus
			}
			out["me"] = me
		}

	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

// hackCountryName and hackJoinedViaLabel moved to internal/rules/hackevents
// (CountryName, JoinedViaLabel) in cleanup 3.2 slice 1c: pure lookups, no
// handler dependency.

type postApplyReq struct {
	AcceptCoC   *bool             `json:"accept_coc"`
	DisplayName string            `json:"display_name"`
	City        string            `json:"city"`
	Country     string            `json:"country"`
	AgeRange    string            `json:"age_range"`
	Gender      string            `json:"gender"`
	Answers     map[string]string `json:"answers"`
}

// postApply is POST /v1/hack/events/{slug}/apply: D1, every application is
// pending whatever approval_required says, and always joined_via='application'.
func (h *HackHandler) postApply(w http.ResponseWriter, r *http.Request) {
	user := hackNeedUser(w, r)
	if user == nil {
		return
	}
	if h.applyLimited(w, r, user.ID) {
		return
	}
	var req postApplyReq
	if !decodeHackJSON(w, r, &req) {
		return
	}
	input, err := h.membersSvc().ValidateApplication(req.AcceptCoC, members.ApplicationInput{Display: req.DisplayName, City: req.City, Country: req.Country, AgeRange: req.AgeRange, Gender: req.Gender, Answers: req.Answers})
	if err != nil {
		writeMemberError(w, err)
		return
	}

	slug := strings.ToLower(strings.TrimSpace(r.PathValue("slug")))
	ev, err := h.membersSvc().ApplyEvent(r.Context(), slug)
	if err == nil {
		paperHackEvent(r, ev)
	}
	if errors.Is(err, sql.ErrNoRows) {
		writeEventNotFound(w)
		return
	}
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	if ev.Stage == "draft" || ev.TakenDown() {
		writeEventNotFound(w)
		return
	}

	ctx := r.Context()
	member, existingView, err := h.membersSvc().Apply(ctx, ev, user.ID, input, h.limits.EventApplyPendingPerEvent, func(existing model.EventMember) any {
		return h.eventView(ctx, ev, existing, false)
	})
	if err != nil {
		writeMemberError(w, err)
		return
	}
	if existingView != nil {
		httpx.WriteJSON(w, http.StatusOK, existingView)
		return
	}

	httpx.WriteJSON(w, http.StatusOK, h.eventView(r.Context(), ev, member, false))
}
