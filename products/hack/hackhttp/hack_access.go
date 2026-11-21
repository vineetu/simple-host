package hackhttp

import (
	"net/http"

	"github.com/vsriram/simple-host/host/access/auth"
	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/model"
	"github.com/vsriram/simple-host/products/hack/app/members"
	productconfig "github.com/vsriram/simple-host/products/hack/config"
	hackmodel "github.com/vsriram/simple-host/products/hack/model"
)

// These hooks annotate the outer paper collector using resolutions the handler
// already performed.
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

// codeLimiter meters code lookups and joins: per network address (roomy, since
// a venue shares one) and, when signed in, per account.
type codeLimiter struct {
	ip, user *httpx.RateLimiter
}

func newCodeLimiter(limits productconfig.Limits) codeLimiter {
	return codeLimiter{
		ip:   httpx.NewRateLimiter(float64(limits.RateEventCodesIP.Burst), limits.RateEventCodesIP.PerSecond()),
		user: httpx.NewRateLimiter(float64(limits.RateEventCodesUser.Burst), limits.RateEventCodesUser.PerSecond()),
	}
}

// limited answers 429 and reports true when the caller is over its limit.
func (c *codeLimiter) limited(w http.ResponseWriter, r *http.Request, userID string) bool {
	var allowed bool
	if userID == "" {
		allowed = c.ip.AllowHTTP(w, httpx.ClientIP(r))
	} else {
		allowed = c.user.AllowHTTP(w, userID)
	}
	if !allowed {
		httpx.TooManyRequests(w)
	}
	return !allowed
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

// loadMember resolves the signed-in caller in the event (members.Caller owns
// the decision) and writes the refusal when there is one.
func loadMember(w http.ResponseWriter, r *http.Request, svc *members.Service, slug string, write bool, roles ...string) (hackAccess, bool) {
	user := hackNeedUser(w, r)
	if user == nil {
		return hackAccess{}, false
	}
	c, err := svc.Caller(r.Context(), slug, user.ID, user.IsAdmin, write, roles...)
	if c.Event.ID != "" {
		paperHackEvent(r, c.Event)
	}
	if c.Member.Role != "" && !c.Admin {
		paperHackKnownMember(r, c.Event, user.ID)
	}
	if err != nil {
		writeHackError(w, err)
		return hackAccess{}, false
	}
	return hackAccess{event: c.Event, member: c.Member, user: user, admin: c.Admin}, true
}
