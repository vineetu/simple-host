package members

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	accounts "github.com/vsriram/simple-host/host/access"
	"github.com/vsriram/simple-host/products/hack/app/events"
	model "github.com/vsriram/simple-host/products/hack/model"
	eventrepo "github.com/vsriram/simple-host/products/hack/repository/hackevents"
	"github.com/vsriram/simple-host/products/hack/repository/hackmembers"
	"github.com/vsriram/simple-host/products/hack/rules/hackevents"
)

// pendingPerAccount caps how many applications one account may have waiting.
const pendingPerAccount = 20

type ApplicationInput struct {
	Display, City, Country, AgeRange, Gender string
	Answers                                  map[string]string
}

type ApplyResult struct {
	Event  model.Event
	Member model.EventMember
}

type ApplyPage struct {
	Event        model.Event
	Open         bool
	ClosedReason any
	Questions    []model.SignupQuestion
	Me           *ApplyMe
}

type ApplyMe struct {
	Role           string `json:"role"`
	JoinedVia      string `json:"joined_via"`
	ApprovalStatus string `json:"approval_status,omitempty"`
}

func errAlreadyMember() error {
	return &Error{Code: "already_member", Message: "you already have a different role in this event"}
}

// applyHidden is true for an event whose apply page must look exactly like an unknown slug: a draft or a taken-down event.
func applyHidden(stage string, takenDown bool) bool { return stage == "draft" || takenDown }

// applyEvent finds the event at slug, refusing one applyHidden hides.
func (s *Service) applyEvent(ctx context.Context, slug string) (model.Event, error) {
	ev, err := eventrepo.GetEventBySlug(ctx, s.db, slug)
	if err == nil && applyHidden(ev.Stage, ev.TakenDown()) {
		err = errEventNotFound
	}
	return ev, err
}

// ApplyPage loads the public application page of the event at slug, plus the signed-in caller's questions and standing.
func (s *Service) ApplyPage(ctx context.Context, slug, caller string) (p ApplyPage, err error) {
	if p.Event, err = s.applyEvent(ctx, slug); err != nil {
		return p, err
	}
	ev := p.Event
	p.Open = events.ApplicationsOpenNow(ev.ApplicationsOpen, ev.DirectoryListed, ev.Stage, ev.TakenDown())
	p.ClosedReason = events.ApplicationsClosedReason(ev.ApplicationsOpen, ev.DirectoryListed, ev.Stage)
	if caller == "" {
		return p, nil
	}
	if p.Open {
		if p.Questions, _, err = eventrepo.RegistrationSettings(ctx, s.db, ev.ID); err != nil {
			return p, err
		}
	}
	status, err := hackmembers.Member(ctx, s.db, ev.ID, caller, hackmembers.NoLock)
	if errors.Is(err, sql.ErrNoRows) {
		return p, nil
	}
	p.Me = &ApplyMe{Role: status.Role, JoinedVia: status.JoinedVia}
	if status.Role == "participant" {
		p.Me.ApprovalStatus = status.ApprovalStatus
	}
	return p, err
}

// Apply checks an application and files it, pending, for the event at slug.
func (s *Service) Apply(ctx context.Context, slug, userID string, accept *bool, in ApplicationInput, capN int) (res ApplyResult, err error) {
	if in, err = validateApplication(accept, in); err != nil {
		return res, err
	}
	if res.Event, err = s.applyEvent(ctx, slug); err != nil {
		return res, err
	}
	res.Member, err = s.applyTx(ctx, res.Event.ID, userID, in, capN)
	if hackevents.UniqueError(err) {
		// A concurrent apply by the same caller won the insert.
		res.Member, err = hackmembers.Member(ctx, s.db, res.Event.ID, userID, hackmembers.NoLock)
		res.Member, err = hackmembers.WithDisplayName(ctx, s.db, res.Member, err)
		if err != nil || res.Member.Role != "participant" {
			err = errAlreadyMember()
		}
	}
	return res, err
}

// applyTx files or re-reads the caller's application in one transaction.
func (s *Service) applyTx(ctx context.Context, eventID, userID string, in ApplicationInput, capN int) (model.EventMember, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return model.EventMember{}, err
	}
	defer tx.Rollback()
	var stage string
	var open, takenDown bool
	if err := eventrepo.LockApplicationEvent(ctx, tx, eventID, &stage, &open, &takenDown); err != nil {
		return model.EventMember{}, err
	}
	if applyHidden(stage, takenDown) {
		return model.EventMember{}, errEventNotFound
	}
	member, err := hackmembers.Member(ctx, tx, eventID, userID, hackmembers.NoLock)
	member, err = hackmembers.WithDisplayName(ctx, tx, member, err)
	switch {
	case err == nil:
		member, err = reapply(ctx, tx, member, in.Display)
	case errors.Is(err, sql.ErrNoRows):
		member, err = fileApplication(ctx, tx, eventID, userID, stage, open, in, capN)
	}
	if err != nil {
		return model.EventMember{}, err
	}
	return member, tx.Commit()
}

// reapply answers an application from someone already in the event.
func reapply(ctx context.Context, tx *sql.Tx, existing model.EventMember, display string) (model.EventMember, error) {
	if existing.Role != "participant" {
		return model.EventMember{}, errAlreadyMember()
	}
	if err := events.EnsureAccountName(ctx, tx, existing.UserID, display); err != nil {
		return model.EventMember{}, err
	}
	m, err := hackmembers.Member(ctx, tx, existing.EventID, existing.UserID, hackmembers.NoLock)
	return hackmembers.WithDisplayName(ctx, tx, m, err)
}

// fileApplication inserts a new pending application once the event takes applications, the answers are valid and neither pending cap is reached, and tells the organisers.
func fileApplication(ctx context.Context, tx *sql.Tx, eventID, userID, stage string, open bool, in ApplicationInput, capN int) (model.EventMember, error) {
	if stage != "open" && stage != "building" {
		return model.EventMember{}, &Error{Code: "joining_closed", Message: "joining is closed for this event"}
	}
	if !open {
		return model.EventMember{}, &Error{Code: "applications_closed", Message: "this event isn't taking applications right now"}
	}
	questions, _, err := eventrepo.RegistrationSettings(ctx, tx, eventID)
	if err != nil {
		return model.EventMember{}, err
	}
	answers, invalid := hackevents.SignupAnswers(questions, in.Answers)
	if invalid != nil {
		return model.EventMember{}, &Error{Code: invalid.Code, Message: invalid.Message}
	}
	if err := checkPendingCaps(ctx, tx, eventID, userID, capN); err != nil {
		return model.EventMember{}, err
	}
	if err := events.EnsureAccountName(ctx, tx, userID, in.Display); err != nil {
		return model.EventMember{}, err
	}
	profile := model.ApplicantProfile{City: in.City, Country: in.Country, AgeRange: in.AgeRange, Gender: in.Gender}
	member, err := hackmembers.Apply(ctx, tx, eventID, userID, answers, profile)
	if err != nil {
		return model.EventMember{}, err
	}
	return member, hackmembers.NotifyOrganisers(ctx, tx, eventID, hackmembers.NoticeApplicationNew, userID)
}

// checkPendingCaps refuses an application past the account-wide or the per-event pending cap.
func checkPendingCaps(ctx context.Context, tx *sql.Tx, eventID, userID string, capN int) error {
	if err := accounts.LockAccount(ctx, tx, userID); err != nil {
		return err
	}
	pending, err := hackmembers.Count(ctx, tx, hackmembers.Filter{UserID: userID, Via: "application", Status: "pending"})
	if err != nil {
		return err
	}
	if pending >= pendingPerAccount {
		return &Error{Code: "too_many_pending", Message: fmt.Sprintf("You have %d applications waiting. Wait for answers before applying to more.", pendingPerAccount)}
	}
	eventPending, err := hackmembers.Count(ctx, tx, hackmembers.Filter{EventID: eventID, Via: "application", Status: "pending"})
	if err != nil {
		return err
	}
	if eventPending >= capN {
		return &Error{Code: "event_applications_full", Message: fmt.Sprintf("This event already has %d applications waiting for a decision. Try again once some are decided.", capN)}
	}
	return nil
}

// validateApplication checks and normalises the supplied application fields.
func validateApplication(accept *bool, in ApplicationInput) (ApplicationInput, error) {
	if accept == nil || !*accept {
		return in, &Error{Code: "coc_required", Message: "you must accept the code of conduct"}
	}
	city, e := hackevents.Line(in.City, "city", 0, 80)
	if e != nil {
		return in, &Error{Code: e.Code, Message: e.Message}
	}
	country, ok := hackevents.Country(in.Country)
	switch {
	case !ok:
		return in, &Error{Code: "invalid_country", Message: "country must be an ISO 3166-1 alpha-2 code"}
	case !hackevents.Enum(in.AgeRange, hackevents.AgeRangeOrder):
		return in, &Error{Code: "invalid_age_range", Message: "age_range is not a recognised value"}
	case !hackevents.Enum(in.Gender, hackevents.GenderOrder):
		return in, &Error{Code: "invalid_gender", Message: "gender is not a recognised value"}
	}
	in.City, in.Country = city, country
	return in, nil
}
