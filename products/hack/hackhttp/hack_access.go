package hackhttp

import (
	"database/sql"
	"errors"
	"net/http"
	"strings"

	"github.com/vsriram/simple-host/host/access/auth"
	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/model"
	hackmodel "github.com/vsriram/simple-host/products/hack/model"
)

// These hooks annotate the outer paper collector using resolutions the
// handler already performed. With collection off they are context lookups
// only.
func paperHackEvent(r *http.Request, ev hackmodel.Event) {
	paperRecorder(r.Context()).ResolveHackEvent(r.Context(), ev.Slug, ev.Stage, "", ev.ResultsPublishedAt.Time)
	paperHackUser(r, auth.GetUser(r.Context()))
}

func paperHackUser(r *http.Request, user *model.User) {
	if user == nil {
		return
	}
	member := user.ID
	if user.Member != nil {
		member = user.Member.PersonID
	}
	paperRecorder(r.Context()).HackMember(r.Context(), member)
}

func paperHackKnownMember(r *http.Request, ev hackmodel.Event, member string) {
	paperRecorder(r.Context()).ResolveHackEvent(r.Context(), ev.Slug, ev.Stage, member, ev.ResultsPublishedAt.Time)
}

func paperHackStage(r *http.Request, ev hackmodel.Event) {
	paperRecorder(r.Context()).RecordHackStage(r.Context(), ev.ID, ev.Stage, ev.UpdatedAt)
}

// rateLimited meters code lookups and joins: per network address (roomy,
// since a venue shares one) and, when signed in, per account.
//
// A signed-in caller is metered by account only: an account costs an email
// round trip, and a venue's shared address must not let one stranger's
// anonymous lookups lock everyone there out of joining.
func (h *hackBase) rateLimited(w http.ResponseWriter, r *http.Request, userID string) bool {
	if userID == "" && !h.codesIP.AllowHTTP(w, httpx.ClientIP(r)) {
		httpx.TooManyRequests(w)
		return true
	}
	if userID != "" && !h.codesUser.AllowHTTP(w, userID) {
		httpx.TooManyRequests(w)
		return true
	}
	return false
}

// applyLimited meters POST apply: a signed-in caller is metered by both its
// account and its network address (RATE_LIMIT_EVENT_APPLY_USER/_IP), so one
// stranger cannot burn through many accounts from one address either.
func (h *participationHTTP) applyLimited(w http.ResponseWriter, r *http.Request, userID string) bool {
	if !h.applyIP.AllowHTTP(w, httpx.ClientIP(r)) {
		httpx.TooManyRequests(w)
		return true
	}
	if !h.applyUser.AllowHTTP(w, userID) {
		httpx.TooManyRequests(w)
		return true
	}
	return false
}

func hackNeedUser(w http.ResponseWriter, r *http.Request) *model.User {
	user := auth.GetUser(r.Context())
	if user == nil {
		httpx.WriteJSON(w, http.StatusUnauthorized, httpx.ErrorResponse{Error: "unauthorized"})
		return nil
	}
	return user
}

func writeEventNotFound(w http.ResponseWriter) {
	httpx.WriteError(w, http.StatusNotFound, "event_not_found", "event not found")
}

type hackAccess struct {
	event  hackmodel.Event
	member hackmodel.EventMember
	user   *model.User
	admin  bool
}

// loadMember loads the event and the caller's membership. Non-members get 404
// event_not_found (including a wrong-role member on a role-gated route). The
// admin key may read any event it is not in as organiser (admin is true only
// then); writes need membership. A platform admin who joined an event is an
// ordinary member there, with no admin bypass. write is true for mutating routes.
func (h *hackBase) loadMember(w http.ResponseWriter, r *http.Request, slug string, write bool, roles ...string) (hackAccess, bool) {
	var z hackAccess
	user := hackNeedUser(w, r)
	if user == nil {
		return z, false
	}
	slug = strings.ToLower(strings.TrimSpace(slug))
	ev, err := h.events.EventBySlug(r.Context(), slug)
	if err == nil {
		paperHackEvent(r, ev)
	}
	if errors.Is(err, sql.ErrNoRows) {
		writeEventNotFound(w)
		return z, false
	}
	if err != nil {
		httpx.WriteInternal(w)
		return z, false
	}
	m, err := h.events.EventMember(r.Context(), ev.ID, user.ID)
	if err == nil {
		paperHackKnownMember(r, ev, user.ID)
	}
	if errors.Is(err, sql.ErrNoRows) {
		if user.IsAdmin && !write {
			z = hackAccess{event: ev, member: hackmodel.EventMember{EventID: ev.ID, UserID: user.ID, Role: "organiser"}, user: user, admin: true}
			return z, true
		}
		writeEventNotFound(w)
		return z, false
	}
	if err != nil {
		httpx.WriteInternal(w)
		return z, false
	}
	if m.Role == "participant" && (write || len(roles) > 0) {
		status, err := h.members.MemberApprovalStatusLookup(r.Context(), ev.ID, user.ID)
		if err != nil {
			httpx.WriteInternal(w)
			return z, false
		}
		if status != "approved" {
			httpx.WriteError(w, http.StatusForbidden, "approval_"+status, "your application is "+status)
			return z, false
		}
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
	if write && ev.TakenDown() {
		httpx.WriteError(w, http.StatusForbidden, "event_taken_down", "this event has been taken down")
		return z, false
	}
	return hackAccess{event: ev, member: m, user: user}, true
}
