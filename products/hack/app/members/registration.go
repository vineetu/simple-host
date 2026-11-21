package members

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	model "github.com/vsriram/simple-host/products/hack/model"
	eventrepo "github.com/vsriram/simple-host/products/hack/repository/hackevents"
	teamrepo "github.com/vsriram/simple-host/products/hack/repository/hackteams"
	"github.com/vsriram/simple-host/products/hack/rules/hackevents"
	"github.com/vsriram/simple-host/products/hack/rules/hackteams"
)

func (s *Service) Registration(ctx context.Context, eventID string) ([]model.SignupQuestion, bool, error) {
	return eventrepo.RegistrationSettings(ctx, s.db, eventID)
}

func (s *Service) Applications(ctx context.Context, eventID string) ([]model.RegistrationApplication, error) {
	return eventrepo.ListRegistrationApplications(ctx, s.db, eventID)
}

func (s *Service) Tracks(ctx context.Context, eventID string) ([]model.EventTrack, error) {
	return eventrepo.ListEventTracks(ctx, s.db, eventID)
}

func (s *Service) SetRegistration(ctx context.Context, eventID, userID string, questions []model.SignupQuestion, approval, admit bool) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var stage string
	if err = eventrepo.LockStage(ctx, tx, eventID, &stage); err != nil {
		return err
	}
	if stage == "archived" {
		return &Error{Code: "event_closed", Message: "an ended event cannot be changed"}
	}
	if err = eventrepo.SetRegistrationSettings(ctx, tx, eventID, questions, approval); err != nil {
		return err
	}
	if !approval && admit {
		// Approval settings govern link sign-ups, not applications.
		if _, err = eventrepo.AdmitPending(ctx, tx, eventID, userID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Service) DecideApplication(ctx context.Context, eventID, userID, actorID, decision string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var stage string
	if err = eventrepo.LockStage(ctx, tx, eventID, &stage); err != nil {
		return err
	}
	if stage == "archived" {
		return &Error{Code: "event_closed", Message: "an ended event cannot be changed"}
	}
	updated, err := eventrepo.DecideRegistration(ctx, tx, eventID, userID, actorID, decision)
	if err != nil {
		return err
	}
	if !updated {
		return &Error{Code: "application_not_pending", Message: "only pending applications can be decided"}
	}
	// Decision notices reach the app and email.
	if err = eventrepo.NotifyUsers(ctx, tx, eventID, eventrepo.NoticeApplicationDecided, "", "", decision, userID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Service) SetTracks(ctx context.Context, eventID string, tracks []model.EventTrack) ([]model.EventTrack, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var stage string
	if err = eventrepo.LockStage(ctx, tx, eventID, &stage); err != nil {
		return nil, err
	}
	if stage == "archived" {
		return nil, &Error{Code: "event_closed", Message: "an ended event cannot be changed"}
	}
	if err = eventrepo.ReplaceEventTracks(ctx, tx, eventID, tracks); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return eventrepo.ListEventTracks(ctx, s.db, eventID)
}

func (s *Service) ChooseTrack(ctx context.Context, eventID, teamID, slug string, organiser ...bool) (*model.EventTrack, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err := lockTrackChoice(ctx, tx, eventID, teamID, len(organiser) > 0 && organiser[0]); err != nil {
		return nil, err
	}
	if _, err = eventrepo.SetTeamTrack(ctx, tx, eventID, teamID, slug); errors.Is(err, sql.ErrNoRows) {
		return nil, &Error{Code: "track_not_found", Message: "track not found"}
	} else if err != nil {
		return nil, err
	}
	track, err := eventrepo.TeamTrack(ctx, tx, teamID)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return track, nil
}

func lockTrackChoice(ctx context.Context, tx *sql.Tx, eventID, teamID string, managing bool) error {
	ev, err := eventrepo.GetEventForUpdate(ctx, tx, eventID)
	if err != nil {
		return err
	}
	if managing && (ev.JudgingLockedAt.Valid || ev.Stage == "judging" || ev.Stage == "results" || ev.Stage == "archived") {
		return &Error{Code: "judging_setup_locked", Message: "Move back to Entries closed to change this."}
	}
	if _, err = eventrepo.LockTeam(ctx, tx, teamID); err != nil {
		return err
	}
	st, err := teamrepo.TeamWriteStateFor(ctx, tx, teamID, true)
	if err != nil {
		return err
	}
	if !managing && st.WriteRefusal() != "" {
		return &Error{Code: "team_choice_closed", Message: "team choices are closed"}
	}
	return nil
}

func (s *Service) ValidateQuestions(in []model.SignupQuestion) ([]model.SignupQuestion, error) {
	out, e := hackevents.Questions(in)
	if e != nil {
		return nil, &Error{Code: e.Code, Message: e.Message}
	}
	return out, nil
}

func (s *Service) ValidateTracks(in []model.EventTrack) ([]model.EventTrack, error) {
	out, e := hackevents.Tracks(in)
	if e != nil {
		return nil, &Error{Code: e.Code, Message: e.Message}
	}
	return out, nil
}

func (s *Service) ValidateDecision(decision string) error {
	if decision != "approved" && decision != "rejected" {
		return &Error{Code: "invalid_decision", Message: "decision must be approved or rejected"}
	}
	return nil
}

func (s *Service) ValidateTrackStage(stage string) error {
	if !hackteams.ParticipantTeamStages(stage) {
		return &Error{Code: "teams_locked", Message: "team choices are closed"}
	}
	return nil
}

func (s *Service) ValidateTrackChoice(choice string) (string, error) {
	slug := strings.TrimSpace(choice)
	if slug != "" && !hackevents.TrackSlug(slug) {
		return "", &Error{Code: "invalid_track", Message: "choose a listed track"}
	}
	return slug, nil
}
