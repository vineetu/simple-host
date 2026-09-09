package handler

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/lib/pq"

	"github.com/vsriram/simple-host/internal/auth"
	"github.com/vsriram/simple-host/internal/eventdns"
)

// eventTTL is how long a claimed event name lives before the sweep removes it.
//
// Long enough for a week-long event, short enough that a forgotten claim does
// not point at a recycled cloud address for months. An organiser can re-claim
// the same name to extend it.
const eventTTL = 21 * 24 * time.Hour

// recordTTL is deliberately short so teardown takes effect within minutes.
const recordTTL = 120

// maxClaimsPerAccount caps how many event names one account can hold at once.
//
// A name under our domain carrying a valid certificate is a phishing surface,
// so the number one account can mint is bounded and every claim is
// attributable. An organiser runs one event at a time; five is generous.
const maxClaimsPerAccount = 5

// EventDomainHandler hands an organiser two hostnames under a domain we own,
// pointing at their own server, so they never touch a registrar.
type EventDomainHandler struct {
	db      *sql.DB
	dns     eventdns.Provider
	domains []string // the domains we are willing to create names under
	limiter *rateLimiter
}

func NewEventDomainHandler(database *sql.DB, dns eventdns.Provider, domains []string) *EventDomainHandler {
	// Burst of 5, refilling one every two minutes. A real organiser claims one
	// name and occasionally re-claims to extend it; anything faster is either a
	// broken script or someone enumerating names.
	return &EventDomainHandler{db: database, dns: dns, domains: domains,
		limiter: newRateLimiter(5, 1.0/120.0)}
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
	name, err := eventdns.NormalizeName(req.Name)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
		return
	}
	req.Name = name
	if err := eventdns.ValidatePublicIP(req.IP); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
		return
	}
	if !h.allowed(req.Domain) {
		writeJSON(w, http.StatusBadRequest, errorResponse{
			Error: fmt.Sprintf("domain must be one of: %s", strings.Join(h.domains, ", "))})
		return
	}

	// Cap concurrent holdings, counting only names this account does not
	// already hold, so re-claiming to extend an existing event never trips it.
	var held int
	if err := h.db.QueryRowContext(r.Context(),
		`SELECT count(*) FROM event_domains WHERE user_id=$1 AND NOT (name=$2 AND domain=$3)`,
		user.ID, req.Name, req.Domain).Scan(&held); err == nil && held >= maxClaimsPerAccount {
		writeJSON(w, http.StatusConflict, errorResponse{
			Error: fmt.Sprintf("an account may hold %d event names at once; release one first", maxClaimsPerAccount)})
		return
	}

	// Claim the row first. Whoever wins this insert owns the name, so two
	// organisers racing for the same event name cannot both create records and
	// leave one set orphaned in DNS with nothing tracking them.
	var ownerID string
	var existingIDs []string
	err = h.db.QueryRowContext(r.Context(), `
		INSERT INTO event_domains (name, domain, user_id, ip, expires_at)
		VALUES ($1,$2,$3,$4, now() + $5::interval)
		ON CONFLICT (name, domain) DO UPDATE
		  SET ip = EXCLUDED.ip, expires_at = EXCLUDED.expires_at
		  WHERE event_domains.user_id = EXCLUDED.user_id
		RETURNING user_id, record_ids`,
		req.Name, req.Domain, user.ID, req.IP, fmt.Sprintf("%d hours", int(eventTTL.Hours())),
	).Scan(&ownerID, pq.Array(&existingIDs))
	if errors.Is(err, sql.ErrNoRows) {
		// The conflict clause refused: the row exists and belongs to somebody else.
		writeJSON(w, http.StatusConflict, errorResponse{Error: "that event name is taken"})
		return
	}
	if err != nil {
		log.Printf("event claim: %v", err)
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}

	// Re-claiming the same name replaces its records rather than accumulating
	// them, otherwise a moved event would leave the old address answering.
	for _, id := range existingIDs {
		if err := h.dns.DeleteRecord(r.Context(), req.Domain, id); err != nil {
			log.Printf("event claim: stale record %s: %v", id, err)
		}
	}

	host := req.Name + "." + req.Domain
	content := "sites." + req.Name + "." + req.Domain
	var ids []string
	for _, label := range []string{req.Name, "sites." + req.Name} {
		id, err := h.dns.CreateA(r.Context(), req.Domain, label, req.IP, recordTTL)
		if err != nil {
			// Roll back what we made, so a half-created claim never leaves a
			// record behind that nothing will clean up.
			for _, made := range ids {
				_ = h.dns.DeleteRecord(r.Context(), req.Domain, made)
			}
			_, _ = h.db.ExecContext(r.Context(), `DELETE FROM event_domains WHERE name=$1 AND domain=$2`, req.Name, req.Domain)
			log.Printf("event claim: %v", err)
			writeJSON(w, http.StatusBadGateway, errorResponse{Error: "could not create DNS records"})
			return
		}
		ids = append(ids, id)
	}
	if _, err := h.db.ExecContext(r.Context(),
		`UPDATE event_domains SET record_ids=$3 WHERE name=$1 AND domain=$2`,
		req.Name, req.Domain, pq.Array(ids)); err != nil {
		log.Printf("event claim: recording ids: %v", err)
	}

	// Attributable by design: a name under our domain carrying a valid
	// certificate is worth being able to trace back to an account.
	log.Printf("event claim: %s -> %s by user %s", host, req.IP, user.ID)

	writeJSON(w, http.StatusCreated, map[string]any{
		"host":         host,
		"content_host": content,
		"ip":           req.IP,
		"expires_at":   time.Now().Add(eventTTL).UTC().Format(time.RFC3339),
	})
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
	var ids []string
	var owner string
	err := h.db.QueryRowContext(r.Context(),
		`SELECT user_id, record_ids FROM event_domains WHERE name=$1 AND domain=$2`,
		name, domain).Scan(&owner, pq.Array(&ids))
	if errors.Is(err, sql.ErrNoRows) {
		writeJSON(w, http.StatusNotFound, errorResponse{Error: "not found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	if owner != user.ID && !user.IsAdmin {
		writeJSON(w, http.StatusNotFound, errorResponse{Error: "not found"})
		return
	}
	for _, id := range ids {
		if err := h.dns.DeleteRecord(r.Context(), domain, id); err != nil {
			log.Printf("event release: %v", err)
			writeJSON(w, http.StatusBadGateway, errorResponse{Error: "could not remove DNS records"})
			return
		}
	}
	// Only after the records are gone. Dropping the row first would lose the
	// only record of what still needs deleting.
	if _, err := h.db.ExecContext(r.Context(),
		`DELETE FROM event_domains WHERE name=$1 AND domain=$2`, name, domain); err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
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
	rows, err := h.db.QueryContext(r.Context(),
		`SELECT name, domain, ip, expires_at FROM event_domains WHERE user_id=$1 ORDER BY created_at DESC`, user.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var name, domain, ip string
		var exp time.Time
		if err := rows.Scan(&name, &domain, &ip, &exp); err != nil {
			continue
		}
		out = append(out, map[string]any{
			"host": name + "." + domain, "content_host": "sites." + name + "." + domain,
			"ip": ip, "expires_at": exp.UTC().Format(time.RFC3339),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": out})
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
	rows, err := h.db.QueryContext(ctx,
		`SELECT name, domain, record_ids FROM event_domains WHERE expires_at < now()`)
	if err != nil {
		return
	}
	type expired struct {
		name, domain string
		ids          []string
	}
	var list []expired
	for rows.Next() {
		var e expired
		if err := rows.Scan(&e.name, &e.domain, pq.Array(&e.ids)); err == nil {
			list = append(list, e)
		}
	}
	rows.Close()
	for _, e := range list {
		ok := true
		for _, id := range e.ids {
			if err := h.dns.DeleteRecord(ctx, e.domain, id); err != nil {
				log.Printf("event sweep: %s.%s: %v", e.name, e.domain, err)
				ok = false
			}
		}
		if !ok {
			continue // keep the row so the next sweep retries
		}
		if _, err := h.db.ExecContext(ctx,
			`DELETE FROM event_domains WHERE name=$1 AND domain=$2`, e.name, e.domain); err == nil {
			log.Printf("event sweep: released %s.%s", e.name, e.domain)
		}
	}
}
