package handler

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/vsriram/simple-host/internal/app/sites"

	"github.com/vsriram/simple-host/internal/auth"
	"github.com/vsriram/simple-host/internal/edition"
	"github.com/vsriram/simple-host/internal/eventdns"
)

// eventTTL (EVENT_TTL_DAYS, default 21) is how long a claimed event name
// lives before the sweep removes it. Long enough for a week-long event, short
// enough that a forgotten claim does not point at a recycled cloud address for
// months. An organiser can re-claim the same name to extend it.

// maxClaimsPerAccount (EVENT_MAX_CLAIMS, default 5) caps how many event names
// one account can hold at once. A name under our domain carrying a valid
// certificate is a phishing surface, so the number one account can mint is
// bounded and every claim is attributable. An organiser runs one event at a
// time; five is generous.

// EventDomainHandler hands an organiser two hostnames under a domain we own,
// pointing at their own server, so they never touch a registrar.
type EventDomainHandler struct {
	limitSet
	db      *sql.DB
	dns     eventdns.Provider
	probe   *eventdns.LiveProbe
	domains []string // the domains we are willing to create names under
	limiter *rateLimiter
	// peer is the hackathon platform (EVENT_NAME_PEER): a name it holds
	// under the same zone is refused here (hack_mode.go). nil: no peer.
	peer func(ctx context.Context, zone, name string) (bool, error)
}

// SetNamePeer sets the peer instance asked before a name is claimed.
func (h *EventDomainHandler) SetNamePeer(fn func(ctx context.Context, zone, name string) (bool, error)) {
	h.peer = fn
}

// NameClaimed reports whether name is claimed under zone here: what this
// instance answers the peer.
func (h *EventDomainHandler) NameClaimed(ctx context.Context, zone, name string) (bool, error) {
	return h.eventSitesSvc().EventNameClaimed(ctx, sites.EventNameInput{Name: name, Zone: zone})
}

func NewEventDomainHandler(database *sql.DB, dns eventdns.Provider, domains []string, editions ...*edition.Edition) *EventDomainHandler {
	var ed *edition.Edition
	if len(editions) > 0 {
		ed = editions[0]
	}
	// Burst of 10, refilling one a minute.
	//
	// The limiter runs before validation on purpose, so a rejected request still
	// costs a token and nobody can enumerate which names are taken for free. That
	// means typos count too, which is why the burst is not tight: four mistyped
	// addresses followed by a real attempt must not lock an organiser out. The
	// hard ceiling on abuse is maxClaimsPerAccount, not this.
	return &EventDomainHandler{limitSet: newLimitSet(ed.Limits()), db: database, dns: dns, domains: domains,
		probe:   eventdns.NewLiveProbe(),
		limiter: newRateLimiter(10, 1.0/60.0)}
}

func (h *EventDomainHandler) Register(mux *http.ServeMux, authMiddleware func(http.Handler) http.Handler) {
	mux.Handle("GET /v1/events", authMiddleware(http.HandlerFunc(h.list)))
	mux.Handle("POST /v1/events", authMiddleware(http.HandlerFunc(h.claim)))
	mux.Handle("DELETE /v1/events/{name}", authMiddleware(http.HandlerFunc(h.release)))
}

type claimRequest struct {
	Name   string `json:"name"`
	IP     string `json:"ip"`
	Domain string `json:"domain"`
	// Move acknowledges that this claim points an existing event at a different
	// machine. Without it a re-claim with a new address silently repoints a
	// running event: the organiser's dashboard and every participant's page
	// start resolving to another server, under a valid certificate, with no
	// error anywhere to look at.
	Move bool `json:"move"`
}

func (h *EventDomainHandler) allowed(domain string) bool {
	for _, d := range h.domains {
		if d == domain {
			return true
		}
	}
	return false
}

func (h *EventDomainHandler) claim(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUser(r.Context())
	if user == nil {
		writeJSON(w, http.StatusUnauthorized, errorResponse{Error: "unauthorized"})
		return
	}
	if !h.limiter.allow(user.ID) {
		writeJSON(w, http.StatusTooManyRequests, errorResponse{Error: "too many event claims; wait a few minutes"})
		return
	}
	var req claimRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid request body"})
		return
	}
	req.IP = strings.TrimSpace(req.IP)
	if req.Domain == "" && len(h.domains) > 0 {
		req.Domain = h.domains[0]
	}
	name, nerr := eventdns.NormalizeName(req.Name)
	if nerr != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: nerr.Error()})
		return
	}
	req.Name = name
	canonicalIP, err := eventdns.ValidatePublicIP(req.IP)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
		return
	}
	req.IP = canonicalIP
	if !h.allowed(req.Domain) {
		writeJSON(w, http.StatusBadRequest, errorResponse{
			Error: fmt.Sprintf("domain must be one of: %s", strings.Join(h.domains, ", "))})
		return
	}

	result, err := h.eventSitesSvc().ClaimEventDomain(r.Context(), sites.EventClaimInput{UserID: user.ID, Name: req.Name, Domain: req.Domain, IP: req.IP, Move: req.Move})
	if writeEventDomainError(w, err) {
		return
	}
	writeJSON(w, http.StatusCreated, eventDomainResponse(result))
}

func (h *EventDomainHandler) release(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUser(r.Context())
	if user == nil {
		writeJSON(w, http.StatusUnauthorized, errorResponse{Error: "unauthorized"})
		return
	}
	name := strings.ToLower(strings.TrimSpace(r.PathValue("name")))
	domain := r.URL.Query().Get("domain")
	if domain == "" && len(h.domains) > 0 {
		domain = h.domains[0]
	}
	if writeEventDomainError(w, h.eventSitesSvc().ReleaseEventDomain(r.Context(), sites.EventReleaseInput{UserID: user.ID, IsAdmin: user.IsAdmin, Name: name, Domain: domain})) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *EventDomainHandler) list(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUser(r.Context())
	if user == nil {
		writeJSON(w, http.StatusUnauthorized, errorResponse{Error: "unauthorized"})
		return
	}
	list, err := h.eventSitesSvc().ListEventDomains(r.Context(), sites.EventListInput{UserID: user.ID})
	if writeEventDomainError(w, err) {
		return
	}
	out := []map[string]any{}
	for _, e := range list {
		out = append(out, eventDomainResponse(e))
	}
	// The configured domains ship with the listing so an agent can discover them
	// by asking rather than by provoking an error. The skill told agents to ask
	// the instance which domains it offers, and until now the only way to find
	// out was to submit a bad claim and read the rejection.
	writeJSON(w, http.StatusOK, map[string]any{"events": out, "domains": h.domains})
}

// StartSweep deletes records for claims nobody tore down.
//
// This is the whole reason expires_at exists. An organiser who forgets leaves a
// name pointing at a cloud address that provider will hand to someone else.
func (h *EventDomainHandler) StartSweep(every time.Duration) {
	go func() {
		for {
			time.Sleep(every)
			h.sweepOnce(context.Background())
		}
	}()
}

func (h *EventDomainHandler) sweepOnce(ctx context.Context) {
	h.eventSitesSvc().SweepEventDomains(ctx)
}

func (h *EventDomainHandler) eventSitesSvc() *sites.Service {
	return sites.New(h.db, nil, nil, 0, sites.Deps{EventDNS: h.dns, EventProbe: h.probe, EventNamePeer: h.peer, EventDomains: h.domains, EventTTL: h.eventTTL(), EventMaxClaims: h.maxClaimsPerAccount()})
}

func eventDomainResponse(e sites.EventDomainResult) map[string]any {
	return map[string]any{"host": e.Host, "content_host": e.ContentHost, "ip": e.IP, "expires_at": e.ExpiresAt.UTC().Format(time.RFC3339)}
}

func writeEventDomainError(w http.ResponseWriter, err error) bool {
	if err == nil {
		return false
	}
	status := http.StatusInternalServerError
	message, code := "internal server error", ""
	var e *sites.EventDomainError
	if errors.As(err, &e) {
		message, code = e.Message, e.WireCode
		switch e.Code {
		case "conflict":
			status = http.StatusConflict
		case "gateway":
			status = http.StatusBadGateway
		case "unavailable":
			status = http.StatusServiceUnavailable
		case "not_found":
			status = http.StatusNotFound
		}
	}
	writeJSON(w, status, errorResponse{Error: message, Code: code})
	return true
}
