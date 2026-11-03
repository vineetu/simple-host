package members

import (
	model "github.com/vsriram/simple-host/internal/model"
	eventrepo "github.com/vsriram/simple-host/internal/repository/hackevents"

	"context"
	"database/sql"
	"errors"
	"fmt"
	"github.com/vsriram/simple-host/internal/db"
	"github.com/vsriram/simple-host/internal/rules/hackevents"
)

// ApplicationInput contains normalized application fields.
type ApplicationInput struct {
	Display, City, Country, AgeRange, Gender string
	Answers                                  map[string]string
}

// Apply creates an application under the event and user locks.
func (s *Service) Apply(ctx context.Context, ev db.Event, userID string, input ApplicationInput, capN int, existingView func(db.EventMember) any) (memberResult db.EventMember, view any, errResult error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return db.EventMember{}, nil, err
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
	if err := eventrepo.LockApplicationEvent(ctx, tx, ev.ID, &stage, &applicationsOpen, &takenDownNow); err != nil {
		return db.EventMember{}, nil, err
	}
	if stage == "draft" || takenDownNow {
		return db.EventMember{}, nil, &Error{"event_not_found", "event not found"}
	}

	// Security review #12 (rejected/approved/pending caller): an existing
	// participant -- pending, approved or rejected -- gets the same
	// idempotent 200 view whether or not applications are currently open, so
	// a switch-off or a stage change never turns a status check into a
	// retry-able error for someone who already applied. This runs before the
	// joining_closed/applications_closed checks below, which only matter for
	// someone who is not yet a member.
	existing, err := eventrepo.GetEventMember(ctx, tx, ev.ID, userID)
	if err == nil {
		if existing.Role == "participant" {
			// Idempotent: applying again, an approved member re-applying or
			// a rejected member re-applying all return the same view
			// unchanged (no re-apply once rejected).
			return existing, existingView(existing), nil
		}
		return db.EventMember{}, nil, &Error{"already_member", "you already have a different role in this event"}
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return db.EventMember{}, nil, err
	}

	if stage != "open" && stage != "building" {
		return db.EventMember{}, nil, &Error{"joining_closed", "joining is closed for this event"}
	}
	if !applicationsOpen {
		return db.EventMember{}, nil, &Error{"applications_closed", "this event isn't taking applications right now"}
	}

	questions, _, err := eventrepo.RegistrationSettings(ctx, tx, ev.ID)
	if err != nil {
		return db.EventMember{}, nil, err
	}
	answers, validationErr := hackevents.SignupAnswers(questions, input.Answers)
	if validationErr != nil {
		return db.EventMember{}, nil, &Error{validationErr.Code, validationErr.Message}
	}

	// Security review #1/#2 (account-wide pending cap race): take a
	// transaction-scoped lock on the applicant's own users row before
	// counting their pending applications, so two concurrent applies to
	// different events serialise on this count instead of both reading the
	// same stale number and both inserting. Mirrors db.LockUserRow's
	// existing use for the per-organiser event-creation limit (hack.go:624).
	if err := db.LockUserRow(ctx, tx, userID); err != nil {
		return db.EventMember{}, nil, err
	}
	pending, err := eventrepo.CountPendingApplications(ctx, tx, userID)
	if err != nil {
		return db.EventMember{}, nil, err
	}
	if pending >= 20 {
		return db.EventMember{}, nil, &Error{"too_many_pending", "You have 20 applications waiting. Wait for answers before applying to more."}
	}

	// Security review (Grok 1): a per-event ceiling, so many accounts cannot
	// flood one event's queue. Counted under the same events-row FOR SHARE
	// lock as the rest of the gate, against the event-scoped partial index
	// (zp-hack-applications.sql).
	eventPending, err := eventrepo.CountPendingApplicationsForEvent(ctx, tx, ev.ID)
	if err != nil {
		return db.EventMember{}, nil, err
	}
	if eventPending >= capN {
		return db.EventMember{}, nil, &Error{"event_applications_full", fmt.Sprintf("This event already has %d applications waiting for a decision. Try again once some are decided.", capN)}
	}

	profile := model.ApplicantProfile{City: input.City, Country: input.Country, AgeRange: input.AgeRange, Gender: input.Gender}
	member, err := eventrepo.ApplyParticipant(ctx, tx, ev.ID, userID, input.Display, answers, profile)
	if err != nil {
		if hackevents.UniqueError(err) {
			existing2, err2 := eventrepo.GetEventMember(ctx, s.db, ev.ID, userID)
			if err2 == nil && existing2.Role == "participant" {
				return existing2, existingView(existing2), nil
			}
			return db.EventMember{}, nil, &Error{"already_member", "you already have a different role in this event"}
		}
		return db.EventMember{}, nil, err
	}
	// Every application waits on the organisers.
	if err := db.NotifyOrganisers(ctx, tx, ev.ID, db.NoticeApplicationNew, userID); err != nil {
		return db.EventMember{}, nil, err
	}
	if err := tx.Commit(); err != nil {
		return db.EventMember{}, nil, err
	}
	return member, nil, nil

}

// ApplyEvent finds an event before the public application checks.
func (s *Service) ApplyEvent(ctx context.Context, slug string) (db.Event, error) {
	ev, err := eventrepo.GetEventBySlug(ctx, s.db, slug)
	if err != nil {
		return ev, err
	}
	return ev, nil
}

// ApplyDetails loads private questions and membership when applicable.
func (s *Service) ApplyDetails(ctx context.Context, ev db.Event, caller string, open bool) ([]model.SignupQuestion, *model.MemberStatus, error) {
	var q []model.SignupQuestion
	var err error
	if open {
		q, _, err = eventrepo.RegistrationSettings(ctx, s.db, ev.ID)
		if err != nil {
			return nil, nil, err
		}
	}
	status, err := eventrepo.GetMemberStatus(ctx, s.db, ev.ID, caller)
	if errors.Is(err, sql.ErrNoRows) {
		return q, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	return q, &status, nil
}

// ValidateApplication checks and normalizes the supplied application fields.
func (s *Service) ValidateApplication(accept *bool, in ApplicationInput) (ApplicationInput, error) {
	if accept == nil || !*accept {
		return in, &Error{"coc_required", "you must accept the code of conduct"}
	}
	var e *hackevents.ValidationError
	in.Display, e = hackevents.Line(in.Display, "display_name", 1, 100)
	if e != nil {
		return in, &Error{e.Code, e.Message}
	}
	in.City, e = hackevents.Line(in.City, "city", 0, 80)
	if e != nil {
		return in, &Error{e.Code, e.Message}
	}
	var ok bool
	in.Country, ok = hackevents.Country(in.Country)
	if !ok {
		return in, &Error{"invalid_country", "country must be an ISO 3166-1 alpha-2 code"}
	}
	if !hackevents.Enum(in.AgeRange, hackevents.AgeRangeOrder) {
		return in, &Error{"invalid_age_range", "age_range is not a recognised value"}
	}
	if !hackevents.Enum(in.Gender, hackevents.GenderOrder) {
		return in, &Error{"invalid_gender", "gender is not a recognised value"}
	}
	return in, nil
}
