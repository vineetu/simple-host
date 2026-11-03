package members

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	model "github.com/vsriram/simple-host/internal/model"
	eventrepo "github.com/vsriram/simple-host/internal/repository/hackevents"
	teamrepo "github.com/vsriram/simple-host/internal/repository/hackteams"
	"github.com/vsriram/simple-host/internal/rules/hackevents"
	"github.com/vsriram/simple-host/internal/rules/hackteams"
)

// Registration returns an event's registration settings.
func (s *Service) Registration(ctx context.Context, eventID string) ([]model.SignupQuestion, bool, error) {
	return eventrepo.RegistrationSettings(ctx, s.db, eventID)
}

// Applications lists registration applications.
func (s *Service) Applications(ctx context.Context, eventID string) ([]model.RegistrationApplication, error) {
	return eventrepo.ListRegistrationApplications(ctx, s.db, eventID)
}

// Tracks lists event tracks.
func (s *Service) Tracks(ctx context.Context, eventID string) ([]model.EventTrack, error) {
	return eventrepo.ListEventTracks(ctx, s.db, eventID)
}

// SetRegistration changes settings and optionally admits pending link sign-ups.
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
		return &Error{"event_closed", "an ended event cannot be changed"}
	}
	if err = eventrepo.SetRegistrationSettings(ctx, tx, eventID, questions, approval); err != nil {
		return err
	}
	if !approval && admit {
		// D5: Approval settings govern link sign-ups, not applications. A stranger's
		// application always waits for an organiser's decision.
		if _, err = eventrepo.AdmitPending(ctx, tx, eventID, userID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// DecideApplication decides a pending application and queues its notice.
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
		return &Error{"event_closed", "an ended event cannot be changed"}
	}
	updated, err := eventrepo.DecideRegistration(ctx, tx, eventID, userID, actorID, decision)
	if err != nil {
		return err
	}
	if !updated {
		return &Error{"application_not_pending", "only pending applications can be decided"}
	}
	// Decision notices reach the app and email (INTENT 2026-11-02),
	// superseding the earlier no-email decision.
	if err = eventrepo.NotifyUsers(ctx, tx, eventID, eventrepo.NoticeApplicationDecided, "", "", decision, userID); err != nil {
		return err
	}
	return tx.Commit()
}

// SetTracks replaces an event's tracks.
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
		return nil, &Error{"event_closed", "an ended event cannot be changed"}
	}
	if err = eventrepo.ReplaceEventTracks(ctx, tx, eventID, tracks); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return eventrepo.ListEventTracks(ctx, s.db, eventID)
}

// ChooseTrack updates a team's selected track.
func (s *Service) ChooseTrack(ctx context.Context, eventID, teamID, slug string) (*model.EventTrack, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err = eventrepo.LockEventShared(ctx, tx, eventID); err != nil {
		return nil, err
	}
	if _, err = eventrepo.LockTeam(ctx, tx, teamID); err != nil {
		return nil, err
	}
	st, err := teamrepo.TeamWriteStateFor(ctx, tx, teamID, true)
	if err != nil {
		return nil, err
	}
	if st.WriteRefusal() != "" {
		return nil, &Error{"team_choice_closed", "team choices are closed"}
	}
	if _, err = eventrepo.SetTeamTrack(ctx, tx, eventID, teamID, slug); errors.Is(err, sql.ErrNoRows) {
		return nil, &Error{"track_not_found", "track not found"}
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

// ValidateQuestions validates and normalizes sign-up questions.
func (s *Service) ValidateQuestions(in []model.SignupQuestion) ([]model.SignupQuestion, error) {
	out, e := hackevents.Questions(in)
	if e != nil {
		return nil, &Error{e.Code, e.Message}
	}
	return out, nil
}

// ValidateTracks validates and normalizes tracks.
func (s *Service) ValidateTracks(in []model.EventTrack) ([]model.EventTrack, error) {
	out, e := hackevents.Tracks(in)
	if e != nil {
		return nil, &Error{e.Code, e.Message}
	}
	return out, nil
}

// ValidateDecision checks an application decision.
func (s *Service) ValidateDecision(decision string) error {
	if decision != "approved" && decision != "rejected" {
		return &Error{"invalid_decision", "decision must be approved or rejected"}
	}
	return nil
}

// ValidateTrackStage checks whether team choices are open.
func (s *Service) ValidateTrackStage(stage string) error {
	if !hackteams.ParticipantTeamStages(stage) {
		return &Error{"teams_locked", "team choices are closed"}
	}
	return nil
}

// ValidateTrackChoice checks and normalizes a selected track.
func (s *Service) ValidateTrackChoice(choice string) (string, error) {
	slug := strings.TrimSpace(choice)
	if slug != "" && !hackevents.TrackSlug(slug) {
		return "", &Error{"invalid_track", "choose a listed track"}
	}
	return slug, nil
}
