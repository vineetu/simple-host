package members

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"strings"

	model "github.com/vsriram/simple-host/products/hack/model"
	eventrepo "github.com/vsriram/simple-host/products/hack/repository/hackevents"
)

// Caller is who a signed-in account is in one event and what it may do there.
type Caller struct {
	Event  model.Event
	Member model.EventMember
	// Admin is a platform admin reading an event it is not a member of; it reads as an organiser and may not write.
	Admin bool
}

// ApprovalError refuses a participant whose application is not approved.
type ApprovalError struct{ Status string }

func (e *ApprovalError) Error() string { return "your application is " + e.Status }

func (e *ApprovalError) Code() string { return "approval_" + e.Status }

var (
	errEventNotFound = &Error{Code: "event_not_found", Message: "event not found"}
	errTakenDown     = &Error{Code: "event_taken_down", Message: "this event has been taken down"}
)

// Caller resolves the account in the event slug names.
func (s *Service) Caller(ctx context.Context, slug, userID string, isAdmin, write bool, roles ...string) (Caller, error) {
	var c Caller
	ev, err := eventrepo.GetEventBySlug(ctx, s.db, strings.ToLower(strings.TrimSpace(slug)))
	if err != nil {
		return c, notFound(err)
	}
	c.Event = ev
	m, err := eventrepo.GetEventMember(ctx, s.db, ev.ID, userID)
	if errors.Is(err, sql.ErrNoRows) && isAdmin && !write {
		c.Member = model.EventMember{EventID: ev.ID, UserID: userID, Role: "organiser"}
		c.Admin = true
		return c, nil
	}
	if err != nil {
		return c, notFound(err)
	}
	c.Member = m
	if err := s.memberMay(ctx, ev, m, write, roles); err != nil {
		return c, err
	}
	return c, nil
}

// memberMay checks an existing member against approval, role and take-down.
func (s *Service) memberMay(ctx context.Context, ev model.Event, m model.EventMember, write bool, roles []string) error {
	if m.Role == "participant" && (write || len(roles) > 0) {
		status, err := eventrepo.MemberApprovalStatus(ctx, s.db, ev.ID, m.UserID)
		if err != nil {
			return err
		}
		if status != "approved" {
			return &ApprovalError{Status: status}
		}
	}
	if len(roles) > 0 && !slices.Contains(roles, m.Role) {
		return errEventNotFound
	}
	if write && ev.TakenDown() {
		return errTakenDown
	}
	return nil
}

// notFound turns a missing row into event_not_found and keeps other errors.
func notFound(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return errEventNotFound
	}
	return err
}
