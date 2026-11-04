package sites

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"time"

	reposite "github.com/vsriram/simple-host/internal/repository/sites"
)

// recordTTL is deliberately short so teardown takes effect within minutes.
const recordTTL = 120

// EventDomainError describes a workflow failure and its optional public code.
type EventDomainError struct{ Code, Message, WireCode string }

func (e *EventDomainError) Error() string { return e.Message }

// EventClaimInput contains a validated event-domain claim.
type EventClaimInput struct {
	UserID, Name, IP, Domain string
	Move                     bool
}

// EventDomainResult describes a claimed address without transport formatting.
type EventDomainResult struct {
	Host, ContentHost, IP string
	ExpiresAt             time.Time
}

// EventReleaseInput identifies the claim and the caller releasing it.
type EventReleaseInput struct {
	UserID, Name, Domain string
	IsAdmin              bool
}

// EventNameInput identifies a name under a zone.
type EventNameInput struct{ Name, Zone string }

// EventListInput identifies the account whose claims are listed.
type EventListInput struct{ UserID string }

// EventNameClaimed reports an unexpired claim held by this instance.
func (s *Service) EventNameClaimed(ctx context.Context, in EventNameInput) (bool, error) {
	return reposite.EventNameClaimed(ctx, s.database, in.Name, in.Zone)
}

// ClaimEventDomain claims a row, replaces DNS records and compensates failures in order.
func (s *Service) ClaimEventDomain(ctx context.Context, req EventClaimInput) (EventDomainResult, error) {
	// A static reserved list cannot know what the zone already serves. Live
	// products answered by the wildcard have no explicit record of their own, so
	// without this check anyone could claim one of those exact names, override
	// the wildcard, obtain a certificate and serve content under it.
	taken, terr := s.deps.EventDNS.TakenNames(ctx, req.Domain)
	if terr != nil {
		log.Printf("event claim: listing %s: %v", req.Domain, terr)
		return EventDomainResult{}, &EventDomainError{Code: "gateway", Message: "could not check the domain; try again", WireCode: ""}
	}
	// The instance knows its own names authoritatively, and they are the ones a
	// probe is least likely to catch: a handle or a site name may answer no
	// differently from an unknown host today, yet claiming it would still take
	// an explicit record over a name this service is responsible for.
	ours, err := reposite.EventNameLocal(ctx, s.database, req.Name)
	if err != nil {
		return EventDomainResult{}, &EventDomainError{Code: "internal", Message: "internal server error", WireCode: ""}
	}
	if ours {
		return EventDomainResult{}, &EventDomainError{Code: "conflict", Message: "that name belongs to an account or a site on this instance", WireCode: ""}
	}

	// The hackathon platform hands out names under the same zone from its
	// own database; it is asked, and a failure to answer refuses the name.
	if s.deps.EventNamePeer != nil {
		held, perr := s.deps.EventNamePeer(ctx, req.Domain, req.Name)
		if perr != nil {
			log.Printf("event claim: name peer %s.%s: %v", req.Name, req.Domain, perr)
			return EventDomainResult{}, &EventDomainError{Code: "unavailable", Message: "could not check that name; try again", WireCode: "name_check_unavailable"}
		}
		if held {
			return EventDomainResult{}, &EventDomainError{Code: "conflict", Message: "that name is an event on " + req.Domain, WireCode: "name_taken"}
		}
	}

	// Listing the zone only sees explicit records. A live product served through
	// the wildcard by name has none, so it would look free and claiming it would
	// take a running product off the internet. Ask the internet as well.
	if !taken[req.Name] && !taken["sites."+req.Name] {
		if inUse, perr := s.deps.EventProbe.InUse(ctx, req.Domain, req.Name); perr != nil || inUse {
			mine, _ := reposite.EventNameOwned(ctx, s.database, req.Name, req.Domain, req.UserID)
			if !mine {
				if perr != nil {
					log.Printf("event claim: probe %s.%s: %v", req.Name, req.Domain, perr)
					return EventDomainResult{}, &EventDomainError{Code: "gateway", Message: "could not check that name; try again", WireCode: ""}
				}
				return EventDomainResult{}, &EventDomainError{Code: "conflict", Message: "that name is already serving something on this domain", WireCode: ""}
			}
		}
	}
	if taken[req.Name] || taken["sites."+req.Name] {
		// Unless it is already ours: re-claiming to extend must still work.
		mine, _ := reposite.EventNameOwned(ctx, s.database, req.Name, req.Domain, req.UserID)
		if !mine {
			return EventDomainResult{}, &EventDomainError{Code: "conflict", Message: "that name is already in use on this domain", WireCode: ""}
		}
	}

	// Refuse to move a live event unless the caller says that is the intent.
	currentIP, _ := reposite.EventCurrentIP(ctx, s.database, req.Name, req.Domain, req.UserID)
	if currentIP != "" && currentIP != req.IP && !req.Move {
		return EventDomainResult{}, &EventDomainError{Code: "conflict", Message: fmt.Sprintf(
			"%s.%s already points at %s. Re-claiming with a different address moves a running event, and anyone using it would silently reach the new machine. Send \"move\": true if that is what you want.",
			req.Name, req.Domain, currentIP), WireCode: ""}
	}

	// Cap concurrent holdings, counting only names this account does not
	// already hold, so re-claiming to extend an existing event never trips it.
	if held, err := reposite.EventHoldingCount(ctx, s.database, req.UserID, req.Name, req.Domain); err == nil && held >= s.deps.EventMaxClaims {
		return EventDomainResult{}, &EventDomainError{Code: "conflict", Message: fmt.Sprintf("an account may hold %d event names at once; release one first", s.deps.EventMaxClaims), WireCode: ""}
	}

	// Claim the row first. Whoever wins this insert owns the name, so two
	// organisers racing for the same event name cannot both create records and
	// leave one set orphaned in DNS with nothing tracking them.
	_, existingIDs, wasNew, err := reposite.ClaimEventDomain(ctx, s.database, req.Name, req.Domain, req.UserID, req.IP, fmt.Sprintf("%d hours", int(s.deps.EventTTL.Hours())))
	if errors.Is(err, sql.ErrNoRows) {
		// The conflict clause refused: the row exists and belongs to somebody else.
		return EventDomainResult{}, &EventDomainError{Code: "conflict", Message: "that event name is taken", WireCode: ""}
	}
	if err != nil {
		log.Printf("event claim: %v", err)
		return EventDomainResult{}, &EventDomainError{Code: "internal", Message: "internal server error", WireCode: ""}
	}

	// Re-claiming the same name replaces its records rather than accumulating
	// them, otherwise a moved event would leave the old address answering.
	for _, id := range existingIDs {
		if err := s.deps.EventDNS.DeleteRecord(ctx, req.Domain, id); err != nil {
			log.Printf("event claim: stale record %s: %v", id, err)
		}
	}

	host := req.Name + "." + req.Domain
	content := "sites." + req.Name + "." + req.Domain
	var ids []string
	for _, label := range []string{req.Name, "sites." + req.Name} {
		id, err := s.deps.EventDNS.CreateA(ctx, req.Domain, label, req.IP, recordTTL)
		if err != nil {
			// Roll back what we made, so a half-created claim never leaves a
			// record behind that nothing will clean up.
			for _, made := range ids {
				_ = s.deps.EventDNS.DeleteRecord(ctx, req.Domain, made)
			}
			// Only drop the row if this request created it. A renewal that fails
			// must leave the existing claim standing, or the organiser's running
			// event loses its name and anyone can take it.
			if wasNew {
				_, _ = reposite.DeleteEventDomain(ctx, s.database, req.Name, req.Domain)
			}
			log.Printf("event claim: %v", err)
			return EventDomainResult{}, &EventDomainError{Code: "gateway", Message: "could not create DNS records", WireCode: ""}
		}
		ids = append(ids, id)
	}
	// If this fails the records exist but nothing tracks them, so neither release
	// nor the sweep would ever remove them. Undo rather than report success.
	if _, err := reposite.SetEventRecordIDs(ctx, s.database, req.Name, req.Domain, ids); err != nil {
		log.Printf("event claim: recording ids: %v", err)
		for _, made := range ids {
			_ = s.deps.EventDNS.DeleteRecord(ctx, req.Domain, made)
		}
		if wasNew {
			_, _ = reposite.DeleteEventDomain(ctx, s.database, req.Name, req.Domain)
		}
		return EventDomainResult{}, &EventDomainError{Code: "internal", Message: "internal server error", WireCode: ""}
	}

	// Attributable by design: a name under our domain carrying a valid
	// certificate is worth being able to trace back to an account.
	log.Printf("event claim: %s -> %s by user %s", host, req.IP, req.UserID)

	return EventDomainResult{Host: host, ContentHost: content, IP: req.IP, ExpiresAt: time.Now().Add(s.deps.EventTTL)}, nil
}

// ReleaseEventDomain authorizes the caller and deletes DNS records before the row.
func (s *Service) ReleaseEventDomain(ctx context.Context, in EventReleaseInput) error {
	name := in.Name
	domain := in.Domain
	if domain == "" && len(s.deps.EventDomains) > 0 {
		domain = s.deps.EventDomains[0]
	}
	owner, ids, err := reposite.EventDomainOwner(ctx, s.database, name, domain)
	if errors.Is(err, sql.ErrNoRows) {
		return &EventDomainError{Code: "not_found", Message: "not found", WireCode: ""}
	}
	if err != nil {
		return &EventDomainError{Code: "internal", Message: "internal server error", WireCode: ""}
	}
	if owner != in.UserID && !in.IsAdmin {
		return &EventDomainError{Code: "not_found", Message: "not found", WireCode: ""}
	}
	for _, id := range ids {
		if err := s.deps.EventDNS.DeleteRecord(ctx, domain, id); err != nil {
			log.Printf("event release: %v", err)
			return &EventDomainError{Code: "gateway", Message: "could not remove DNS records", WireCode: ""}
		}
	}
	// Only after the records are gone. Dropping the row first would lose the
	// only record of what still needs deleting.
	if _, err := reposite.DeleteEventDomain(ctx, s.database, name, domain); err != nil {
		return &EventDomainError{Code: "internal", Message: "internal server error", WireCode: ""}
	}
	return nil
}

// ListEventDomains returns claims in creation order.
func (s *Service) ListEventDomains(ctx context.Context, in EventListInput) ([]EventDomainResult, error) {
	list, err := reposite.ListEventDomains(ctx, s.database, in.UserID)
	if err != nil {
		return nil, &Error{Code: "internal", Message: "internal server error"}
	}
	out := []EventDomainResult{}
	for _, e := range list {
		out = append(out, EventDomainResult{Host: e.Name + "." + e.Domain, ContentHost: "sites." + e.Name + "." + e.Domain, IP: e.IP, ExpiresAt: e.Expiry})
	}
	return out, nil
}

// SweepEventDomains removes expired DNS records and unchanged claim snapshots.
func (s *Service) SweepEventDomains(ctx context.Context) {
	list, err := reposite.ExpiredEventDomains(ctx, s.database)
	if err != nil {
		ReportWorkerFailure(ctx, "list expired claims", err)
		return
	}
	for _, e := range list {
		ok := true
		for _, id := range e.IDs {
			if err := s.deps.EventDNS.DeleteRecord(ctx, e.Domain, id); err != nil {
				ReportWorkerFailure(ctx, "delete DNS record", err)
				ok = false
			}
		}
		if !ok {
			continue // keep the row so the next sweep retries
		}
		// Delete only if the row is still the one we snapshotted. Deleting the
		// records takes network calls, and an owner can renew in that window: a
		// renewal replaces record_ids and pushes expires_at out. Deleting
		// unconditionally would discard the renewal and leave its freshly
		// created records live with nothing tracking them, which is exactly the
		// orphaned-record case this sweep exists to prevent.
		res, err := reposite.DeleteExpiredEventDomain(ctx, s.database, e)
		if err != nil {
			ReportWorkerFailure(ctx, "delete expired claim", err)
			continue
		}
		if n, _ := res.RowsAffected(); n == 0 {
			// Renewed underneath us. The records we just deleted were the old
			// ones, which the renewal had already replaced, so nothing is lost.
			log.Printf("event sweep: %s.%s was renewed mid-sweep, left alone", e.Name, e.Domain)
			continue
		}
		ReportWorkerProgress(ctx)
		log.Printf("event sweep: released %s.%s", e.Name, e.Domain)
	}
}
