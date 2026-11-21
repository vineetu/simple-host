package hackhttp

import (
	"net/http"
	"strings"

	"github.com/vsriram/simple-host/host/access/auth"
	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/products/hack/app/events"
	"github.com/vsriram/simple-host/products/hack/app/members"
)

// Applying to a hackathon without a join link: GET/POST /v1/hack/events/{slug}/apply. A separate route from /v1/hack/join/{code}, so the join code stays a private capability and this stays a public action keyed by the event's slug, with its own rules (the organiser's switch; an application is always pending).

// hackUnder18Note is plain prose for non-browser consumers of GET .../apply; the public-apply.js form builds the same sentence with "Terms" as a real link to /terms, so the API's copy must not bake the URL into visible text.
const hackUnder18Note = "If you're under 18, a parent or guardian needs to agree to Simple Hack's Terms for you."

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

func (h *participationHTTP) getApply(w http.ResponseWriter, r *http.Request) {
	caller := ""
	accountName := ""
	if u := auth.GetUser(r.Context()); u != nil {
		caller = u.ID
		accountName = strings.TrimSpace(u.DisplayName.String)
	}
	// The body carries the caller's own account name: never cache it.
	w.Header().Set("Cache-Control", "no-store")
	if h.codes.limited(w, r, caller) {
		return
	}
	page, err := h.members.ApplyPage(r.Context(), eventSlug(r), caller)
	if page.Event.ID != "" {
		paperHackEvent(r, page.Event)
	}
	if err != nil {
		writeHackError(w, err)
		return
	}
	ev := page.Event
	out := map[string]any{
		"slug": ev.Slug, "title": ev.Title, "tagline": ev.Tagline,
		"icon_url": h.links.iconURL(ev), "color": hackEventAccent(ev),
		"organiser_name": ev.OrganiserName, "organisation": ev.Organisation,
		"stage":             ev.Stage,
		"starts_at":         rfc3339UTC(ev.StartsAt),
		"ends_at":           rfc3339UTC(ev.EndsAt),
		"time_zone":         ev.TimeZone,
		"event_url":         h.links.EventURL(ev.Slug),
		"applications_open": page.Open,
		"account_name":      accountName,
		"closed_reason":     page.ClosedReason,
		"fields":            hackApplyFields(),
	}
	// Questions and the organiser's own code of conduct are only for a signed-in caller while applications are open; `me` is shown to any signed-in caller.
	if caller != "" {
		if page.Open {
			out["signup_questions"] = page.Questions
			out["coc_default"] = HackDefaultCoC
			out["coc_text"] = ev.CocText
		}
		out["me"] = page.Me
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

type postApplyReq struct {
	AcceptCoC   *bool             `json:"accept_coc"`
	DisplayName string            `json:"display_name"`
	City        string            `json:"city"`
	Country     string            `json:"country"`
	AgeRange    string            `json:"age_range"`
	Gender      string            `json:"gender"`
	Answers     map[string]string `json:"answers"`
}

// postApply is POST .../apply: every application is pending whatever approval_required says, and always joined_via='application'.
func (h *participationHTTP) postApply(w http.ResponseWriter, r *http.Request) {
	user := hackNeedUser(w, r)
	if user == nil {
		return
	}
	if h.applyLimited(w, r, user.ID) {
		return
	}
	var req postApplyReq
	if !httpx.DecodeJSON(w, r, hackMaxBody, &req) {
		return
	}
	input := members.ApplicationInput{Display: req.DisplayName, City: req.City, Country: req.Country, AgeRange: req.AgeRange, Gender: req.Gender, Answers: req.Answers}
	res, err := h.members.Apply(r.Context(), eventSlug(r), user.ID, req.AcceptCoC, input, h.limits.EventApplyPendingPerEvent)
	if res.Event.ID != "" {
		paperHackEvent(r, res.Event)
	}
	if err != nil {
		writeHackError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, h.views.eventView(r.Context(), res.Event, res.Member, false))
}
