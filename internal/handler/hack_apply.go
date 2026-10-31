package handler

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"golang.org/x/text/language"
	"golang.org/x/text/language/display"

	"github.com/vsriram/simple-host/internal/config"
	"github.com/vsriram/simple-host/internal/db"
)

// Applying to a hackathon without a join link (zp-hack-applications.sql,
// owner 2026-10-31): GET/POST /v1/hack/events/{slug}/apply. D2: a separate
// route from /v1/hack/join/{code}, so the join code stays a private
// capability and this stays a public action keyed by the event's slug, with
// its own rules (the organiser's switch, and an application is always
// pending — D1).

// hackAgeRangeOrder and hackGenderOrder are the fixed product choices GET
// .../apply's "fields" lists, in display order; "" (not shared) is always
// accepted too and is not listed.
var (
	hackAgeRangeOrder  = []string{"under_18", "18_24", "25_34", "35_plus"}
	hackAgeRangeLabels = map[string]string{
		"under_18": "Under 18",
		"18_24":    "18-24",
		"25_34":    "25-34",
		"35_plus":  "35+",
	}
	hackGenderOrder  = []string{"woman", "man", "non_binary", "another", "prefer_not_to_say"}
	hackGenderLabels = map[string]string{
		"woman":             "Woman or girl",
		"man":               "Man or boy",
		"non_binary":        "Non-binary",
		"another":           "Another gender",
		"prefer_not_to_say": "Prefer not to say",
	}
)

// hackUnder18Note is plain prose for non-browser consumers of GET .../apply;
// the public-apply.js form itself never renders this string -- it builds
// the same sentence with "Terms" as a real link to /terms (review item 6:
// the API's own copy of this sentence must not bake the URL into visible
// text either, since a client showing it verbatim would repeat the same
// mistake).
const hackUnder18Note = "If you're under 18, a parent or guardian needs to agree to Simple Hack's Terms for you."

// hackApplicationsOpenNow is the applications_open rule shared by GET/POST
// .../apply, the directory and the public event JSON: the organiser's
// switch, the right stage, and not taken down (a draft or taken-down event
// never reaches this: callers 404 first).
func hackApplicationsOpenNow(applicationsOpen, listed bool, stage string, takenDown bool) bool {
	if takenDown || !applicationsOpen || !listed {
		return false
	}
	return stage == "open" || stage == "building"
}

// hackApplicationsClosedReason is GET .../apply's closed_reason: nil when
// open, else "applications_off" or "signup_over". "not_open" never applies:
// a draft is 404 before this runs. ev must not be taken down.
func hackApplicationsClosedReason(ev db.Event) any {
	if !ev.ApplicationsOpen {
		return "applications_off"
	}
	if !ev.DirectoryListed {
		return "not_listed"
	}
	if ev.Stage == "open" || ev.Stage == "building" {
		return nil
	}
	return "signup_over"
}

func hackApplyFields() map[string]any {
	ageRanges := make([]map[string]string, 0, len(hackAgeRangeOrder))
	for _, v := range hackAgeRangeOrder {
		ageRanges = append(ageRanges, map[string]string{"value": v, "label": hackAgeRangeLabels[v]})
	}
	genders := make([]map[string]string, 0, len(hackGenderOrder))
	for _, v := range hackGenderOrder {
		genders = append(genders, map[string]string{"value": v, "label": hackGenderLabels[v]})
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
	if k := r.Header.Get("X-API-Key"); k != "" {
		if u, err := db.GetUserByAPIKey(r.Context(), h.database, k); err == nil {
			caller = u.ID
		}
	}
	if h.rateLimited(w, r, caller) {
		return
	}
	slug := strings.ToLower(strings.TrimSpace(r.PathValue("slug")))
	ev, err := db.GetEventBySlug(r.Context(), h.database, slug)
	if errors.Is(err, sql.ErrNoRows) {
		writeEventNotFound(w)
		return
	}
	if err != nil {
		writeInternal(w)
		return
	}
	// A draft or taken-down event is 404, identical to an unknown slug: its
	// existence must not leak either way.
	if ev.Stage == "draft" || ev.TakenDown() {
		writeEventNotFound(w)
		return
	}
	openNow := hackApplicationsOpenNow(ev.ApplicationsOpen, ev.DirectoryListed, ev.Stage, ev.TakenDown())
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
		"closed_reason":     hackApplicationsClosedReason(ev),
		"fields":            hackApplyFields(),
	}
	// Questions and the organiser's own code of conduct are for people they
	// invited to answer. They used to require the join code, including for a
	// signed-out caller (security review #5); keep that protection by
	// returning them only when applications are open right now, to a
	// signed-in caller -- not to every account for every non-draft slug.
	// `me` stays available to a member either way, open or closed.
	if caller != "" {
		if openNow {
			questions, _, err := db.RegistrationSettings(r.Context(), h.database, ev.ID)
			if err != nil {
				writeInternal(w)
				return
			}
			out["signup_questions"] = questions
			out["coc_default"] = HackDefaultCoC
			out["coc_text"] = ev.CocText
		}
		status, err := db.GetMemberStatus(r.Context(), h.database, ev.ID, caller)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			out["me"] = nil
		case err != nil:
			writeInternal(w)
			return
		default:
			me := map[string]any{"role": status.Role, "joined_via": status.JoinedVia}
			if status.Role == "participant" {
				me["approval_status"] = status.ApprovalStatus
			}
			out["me"] = me
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// hackCountryName is the participants CSV's country cell: the English
// region name for a stored ISO 3166-1 alpha-2 code, blank when blank.
func hackCountryName(code string) string {
	if code == "" {
		return ""
	}
	region, err := language.ParseRegion(code)
	if err != nil {
		return ""
	}
	return display.English.Regions().Name(region)
}

// hackJoinedViaLabel is the participants CSV's joined_via cell.
func hackJoinedViaLabel(joinedVia string) string {
	if joinedVia == "application" {
		return "application"
	}
	return "join link"
}

// hackCountryCodeShape is two ASCII letters: the only shape ParseRegion is
// asked to validate (it also accepts 3-letter and numeric UN M49 codes,
// which this field does not take).
var hackCountryCodeShape = regexp.MustCompile(`^[A-Z]{2}$`)

// hackCountryExcluded is the gap between language.Region.IsCountry() plus
// "is its own canonical form" and an actual, choosable country, matched to
// the fixed list screens/public-apply.js offers (build-plan.md §3.2, §8
// item 25). Canonicalize() already turns away most legacy aliases (UK ->
// GB, TP -> TL, ZR -> CD, ...: rejected because they are not their own
// canonical form), but a handful canonicalize to themselves while still not
// being a real, nameable country: historical/non-country codes (AN, CS, EZ,
// FQ, NT, PC, SU, UN, YU), postal-only exceptional reservations with no
// population of their own (AC, CP, DG, EA, IC, TA), and CQ (parses, but
// CLDR gives it no English name). XK (Kosovo) is not in this list: no
// official ISO entry, but a real, nameable, day-to-day code, and the one
// the UI offers.
var hackCountryExcluded = map[string]bool{
	"AN": true, "CS": true, "EZ": true, "FQ": true, "NT": true, "PC": true, "SU": true, "UN": true, "YU": true,
	"AC": true, "CP": true, "DG": true, "EA": true, "IC": true, "TA": true,
	"CQ": true,
}

// checkHackCountry validates country (D6): empty, or an ISO 3166-1 alpha-2
// code.
func checkHackCountry(w http.ResponseWriter, s string) (string, bool) {
	s = strings.ToUpper(strings.TrimSpace(s))
	if s == "" {
		return "", true
	}
	if !hackCountryCodeShape.MatchString(s) {
		writeHackErr(w, http.StatusBadRequest, "invalid_country", "country must be an ISO 3166-1 alpha-2 code")
		return "", false
	}
	region, err := language.ParseRegion(s)
	if err != nil || !region.IsCountry() || region.Canonicalize() != region || hackCountryExcluded[s] {
		writeHackErr(w, http.StatusBadRequest, "invalid_country", "country must be an ISO 3166-1 alpha-2 code")
		return "", false
	}
	return region.String(), true
}

// checkHackEnum validates s is empty or one of allowed, else 400 invalid_<field>.
func checkHackEnum(w http.ResponseWriter, s, field string, allowed []string) (string, bool) {
	if s == "" {
		return "", true
	}
	for _, v := range allowed {
		if s == v {
			return s, true
		}
	}
	writeHackErr(w, http.StatusBadRequest, "invalid_"+field, field+" is not a recognised value")
	return "", false
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
	if req.AcceptCoC == nil || !*req.AcceptCoC {
		writeHackErr(w, http.StatusBadRequest, "coc_required", "you must accept the code of conduct")
		return
	}
	display, ok := checkHackLine(w, req.DisplayName, "display_name", 1, 100)
	if !ok {
		return
	}
	city, ok := checkHackLine(w, req.City, "city", 0, 80)
	if !ok {
		return
	}
	country, ok := checkHackCountry(w, req.Country)
	if !ok {
		return
	}
	ageRange, ok := checkHackEnum(w, req.AgeRange, "age_range", hackAgeRangeOrder)
	if !ok {
		return
	}
	gender, ok := checkHackEnum(w, req.Gender, "gender", hackGenderOrder)
	if !ok {
		return
	}
	slug := strings.ToLower(strings.TrimSpace(r.PathValue("slug")))
	ev, err := db.GetEventBySlug(r.Context(), h.database, slug)
	if errors.Is(err, sql.ErrNoRows) {
		writeEventNotFound(w)
		return
	}
	if err != nil {
		writeInternal(w)
		return
	}
	if ev.Stage == "draft" || ev.TakenDown() {
		writeEventNotFound(w)
		return
	}

	tx, err := h.database.BeginTx(r.Context(), nil)
	if err != nil {
		writeInternal(w)
		return
	}
	defer tx.Rollback()

	// The atomic gate (security review #2): stage, applications_open and
	// whether the event is taken down are read again inside the same
	// transaction as the insert, so a concurrent switch-off, stage change or
	// takedown cannot be bypassed. Security review #4 (takedown race): the
	// unlocked lookup above can be stale by the time this runs -- a takedown
	// that commits in that window must still produce the same 404 as an
	// unknown slug, with nothing inserted, so taken_down_at is read here too
	// rather than trusting the pre-transaction ev.TakenDown().
	//
	// Pre-ship review B2: this used to take the row FOR SHARE, which lets any
	// number of concurrent applicants hold the lock at once -- they don't
	// serialize on it, so they can all read the same eventPending count below
	// and all insert, overshooting EVENT_APPLY_PENDING_PER_EVENT (20
	// concurrent applicants against a cap of 3 produced 10 rows). FOR UPDATE
	// instead serializes every apply to this event on this one row, event
	// first and the applicant's own users row second (db.LockUserRow,
	// further down) -- the same order every other path through this
	// transaction takes, so two applies to different events by the same user
	// (the cross-event pending-cap race) still only ever contend on the one
	// shared resource, the user row, and can't deadlock.
	var stage string
	var applicationsOpen, takenDownNow bool
	if err := tx.QueryRowContext(r.Context(), `SELECT stage, applications_open AND directory_listed, taken_down_at IS NOT NULL FROM events WHERE id=$1 FOR UPDATE`, ev.ID).Scan(&stage, &applicationsOpen, &takenDownNow); err != nil {
		writeInternal(w)
		return
	}
	if stage == "draft" || takenDownNow {
		writeEventNotFound(w)
		return
	}

	// Security review #12 (rejected/approved/pending caller): an existing
	// participant -- pending, approved or rejected -- gets the same
	// idempotent 200 view whether or not applications are currently open, so
	// a switch-off or a stage change never turns a status check into a
	// retry-able error for someone who already applied. This runs before the
	// joining_closed/applications_closed checks below, which only matter for
	// someone who is not yet a member.
	existing, err := db.GetEventMember(r.Context(), tx, ev.ID, user.ID)
	if err == nil {
		if existing.Role == "participant" {
			// Idempotent: applying again, an approved member re-applying or
			// a rejected member re-applying all return the same view
			// unchanged (no re-apply once rejected).
			writeJSON(w, http.StatusOK, h.eventView(r.Context(), ev, existing, false))
			return
		}
		writeHackErr(w, http.StatusConflict, "already_member", "you already have a different role in this event")
		return
	}
	if !errors.Is(err, sql.ErrNoRows) {
		writeInternal(w)
		return
	}

	if stage != "open" && stage != "building" {
		writeHackErr(w, http.StatusConflict, "joining_closed", "joining is closed for this event")
		return
	}
	if !applicationsOpen {
		writeHackErr(w, http.StatusConflict, "applications_closed", "this event isn't taking applications right now")
		return
	}

	questions, _, err := db.RegistrationSettings(r.Context(), tx, ev.ID)
	if err != nil {
		writeInternal(w)
		return
	}
	answers, ok := validateSignupAnswers(w, questions, req.Answers)
	if !ok {
		return
	}

	// Security review #1/#2 (account-wide pending cap race): take a
	// transaction-scoped lock on the applicant's own users row before
	// counting their pending applications, so two concurrent applies to
	// different events serialise on this count instead of both reading the
	// same stale number and both inserting. Mirrors db.LockUserRow's
	// existing use for the per-organiser event-creation limit (hack.go:624).
	if err := db.LockUserRow(r.Context(), tx, user.ID); err != nil {
		writeInternal(w)
		return
	}
	pending, err := db.CountPendingApplications(r.Context(), tx, user.ID)
	if err != nil {
		writeInternal(w)
		return
	}
	if pending >= 20 {
		writeHackErr(w, http.StatusConflict, "too_many_pending", "You have 20 applications waiting. Wait for answers before applying to more.")
		return
	}

	// Security review (Grok 1): a per-event ceiling, so many accounts cannot
	// flood one event's queue. Counted under the same events-row FOR SHARE
	// lock as the rest of the gate, against the event-scoped partial index
	// (zp-hack-applications.sql).
	eventPending, err := db.CountPendingApplicationsForEvent(r.Context(), tx, ev.ID)
	if err != nil {
		writeInternal(w)
		return
	}
	if capN := config.Active().EventApplyPendingPerEvent; eventPending >= capN {
		writeHackErr(w, http.StatusConflict, "event_applications_full",
			fmt.Sprintf("This event already has %d applications waiting for a decision. Try again once some are decided.", capN))
		return
	}

	profile := db.ApplicantProfile{City: city, Country: country, AgeRange: ageRange, Gender: gender}
	member, err := db.ApplyParticipant(r.Context(), tx, ev.ID, user.ID, display, answers, profile)
	if err != nil {
		if dbUnique(err) {
			existing2, err2 := db.GetEventMember(r.Context(), h.database, ev.ID, user.ID)
			if err2 == nil && existing2.Role == "participant" {
				writeJSON(w, http.StatusOK, h.eventView(r.Context(), ev, existing2, false))
				return
			}
			writeHackErr(w, http.StatusConflict, "already_member", "you already have a different role in this event")
			return
		}
		writeInternal(w)
		return
	}
	if err := tx.Commit(); err != nil {
		writeInternal(w)
		return
	}
	writeJSON(w, http.StatusOK, h.eventView(r.Context(), ev, member, false))
}
