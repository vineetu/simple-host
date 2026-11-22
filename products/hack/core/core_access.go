package core

import (
	"net/http"

	productconfig "github.com/vsriram/simple-host/products/hack/config"

	"github.com/vsriram/simple-host/host/access/auth"
	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/model"
	"github.com/vsriram/simple-host/products/hack/app/members"
	hackmodel "github.com/vsriram/simple-host/products/hack/model"
)

// These hooks annotate the outer paper collector using resolutions the handler
// already performed.
func PaperEvent(r *http.Request, ev hackmodel.Event) {
	paper(r.Context()).ResolveHackEvent(r.Context(), ev.Slug, ev.Stage, "", ev.ResultsPublishedAt.Time)
	paperUser(r, auth.GetUser(r.Context()))
}

func paperUser(r *http.Request, user *model.User) {
	if user == nil {
		return
	}
	member := user.ID
	if user.Member != nil {
		member = user.Member.PersonID
	}
	paper(r.Context()).HackMember(r.Context(), member)
}

func PaperMember(r *http.Request, ev hackmodel.Event, member string) {
	paper(r.Context()).ResolveHackEvent(r.Context(), ev.Slug, ev.Stage, member, ev.ResultsPublishedAt.Time)
}

func PaperStage(r *http.Request, ev hackmodel.Event) {
	paper(r.Context()).RecordHackStage(r.Context(), ev.ID, ev.Stage, ev.UpdatedAt)
}

// codeLimiter meters code lookups and joins: per network address (roomy, since
// a venue shares one) and, when signed in, per account.
type CodeLimiter struct {
	IP, User *httpx.RateLimiter
}

func NewCodeLimiter(limits productconfig.Limits) CodeLimiter {
	return CodeLimiter{
		IP:   httpx.NewRateLimiter(float64(limits.RateEventCodesIP.Burst), limits.RateEventCodesIP.PerSecond()),
		User: httpx.NewRateLimiter(float64(limits.RateEventCodesUser.Burst), limits.RateEventCodesUser.PerSecond()),
	}
}

// limited answers 429 and reports true when the caller is over its limit.
func (c *CodeLimiter) Limited(w http.ResponseWriter, r *http.Request, userID string) bool {
	var allowed bool
	if userID == "" {
		allowed = c.IP.AllowHTTP(w, httpx.ClientIP(r))
	} else {
		allowed = c.User.AllowHTTP(w, userID)
	}
	if !allowed {
		httpx.TooManyRequests(w)
	}
	return !allowed
}

func NeedUser(w http.ResponseWriter, r *http.Request) *model.User {
	user := auth.GetUser(r.Context())
	if user == nil {
		httpx.WriteJSON(w, http.StatusUnauthorized, httpx.ErrorResponse{Error: "unauthorized"})
		return nil
	}
	return user
}

func WriteEventNotFound(w http.ResponseWriter) {
	httpx.WriteError(w, http.StatusNotFound, "event_not_found", "event not found")
}

type Access struct {
	Event  hackmodel.Event
	Member hackmodel.EventMember
	User   *model.User
	Admin  bool
}

// loadMember resolves the signed-in caller in the event (members.Caller owns
// the decision) and writes the refusal when there is one.
func LoadMember(w http.ResponseWriter, r *http.Request, svc *members.Service, slug string, write bool, roles ...string) (Access, bool) {
	user := NeedUser(w, r)
	if user == nil {
		return Access{}, false
	}
	c, err := svc.Caller(r.Context(), slug, user.ID, user.IsAdmin, write, roles...)
	if c.Event.ID != "" {
		PaperEvent(r, c.Event)
	}
	if c.Member.Role != "" && !c.Admin {
		PaperMember(r, c.Event, user.ID)
	}
	if err != nil {
		WriteError(w, err)
		return Access{}, false
	}
	return Access{Event: c.Event, Member: c.Member, User: user, Admin: c.Admin}, true
}
