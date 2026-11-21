package entries

import (
	"context"
	"database/sql"
	"errors"
	"time"

	hackeventsrules "github.com/vsriram/simple-host/products/hack/rules/hackevents"

	"github.com/vsriram/simple-host/products/hack/model"
	entryrepo "github.com/vsriram/simple-host/products/hack/repository/hackentries"
	eventrepo "github.com/vsriram/simple-host/products/hack/repository/hackevents"
	teamrepo "github.com/vsriram/simple-host/products/hack/repository/hackteams"
)

func (s *Service) SetExcluded(ctx context.Context, eventID, userID, slug string, excluded bool) (model.EventTeam, error) {
	team, err := teamrepo.GetEventTeamBySlug(ctx, s.db, eventID, slug)
	if errors.Is(err, sql.ErrNoRows) {
		return team, &Error{Code: "team_not_found", Message: "team not found"}
	}
	if err != nil {
		return team, err
	}
	if _, err := entryrepo.GetEventEntry(ctx, s.db, team.ID); errors.Is(err, sql.ErrNoRows) {
		return team, &Error{Code: "no_entry", Message: "this team has no entry"}
	} else if err != nil {
		return team, err
	}
	_, err = entryrepo.SetEntryExcluded(ctx, s.db, team.ID, userID, excluded)
	return team, err
}

type WriteRequest struct {
	Event        model.Event
	UserID, Kind string
	UpdatedAt    *string
	Fields       map[string]string
	Tools        *[]string
	Track        *string
	Data         []byte
	MIME         string
}

// Write applies a participant's entry change under the existing membership and team locks.
// Missing contains the fields needed when an incomplete entry is rejected.
func (s *Service) Write(ctx context.Context, req WriteRequest) (string, []string, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", nil, err
	}
	defer tx.Rollback()
	id, st, err := lockWritableTeam(ctx, tx, req.Event.ID, req.UserID)
	if err != nil {
		return id, nil, err
	}
	var missing []string
	switch req.Kind {
	case "text":
		missing, err = s.writeText(ctx, tx, req, st, id)
	case "submit":
		missing, err = s.submitEntry(ctx, tx, req, st, id)
	case "unsubmit":
		_, err = entryrepo.UnsubmitEventEntry(ctx, tx, id)
	case "screenshot":
		missing, err = s.writeScreenshot(ctx, tx, req, st, id)
	}
	if err != nil {
		return id, missing, err
	}
	return id, nil, tx.Commit()
}

// lockWritableTeam share-locks the participant's membership, locks their team
// and refuses when the team may not change its entry now.
func lockWritableTeam(ctx context.Context, tx *sql.Tx, eventID, userID string) (string, model.TeamWriteState, error) {
	var teamID sql.NullString
	err := tx.QueryRowContext(ctx, `SELECT team_id FROM event_members WHERE event_id = $1 AND user_id = $2 AND role = 'participant' FOR SHARE`, eventID, userID).Scan(&teamID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", model.TeamWriteState{}, &Error{Code: "event_not_found", Message: "event not found"}
	}
	if err != nil {
		return "", model.TeamWriteState{}, err
	}
	if teamID.String == "" {
		return "", model.TeamWriteState{}, &Error{Code: "no_team", Message: "join or start a team first"}
	}
	id := teamID.String
	if _, err = tx.ExecContext(ctx, `SELECT 1 FROM event_teams WHERE id=$1 FOR UPDATE`, id); err != nil {
		return id, model.TeamWriteState{}, err
	}
	st, err := teamrepo.TeamWriteStateFor(ctx, tx, id, true)
	if errors.Is(err, sql.ErrNoRows) {
		return id, st, &Error{Code: "no_team", Message: "join or start a team first"}
	}
	if err != nil {
		return id, st, err
	}
	if code := st.WriteRefusal(); code != "" {
		return id, st, &Error{Code: code, Message: hackeventsrules.TeamRefusalMessage(code)}
	}
	return id, st, nil
}

// writeText saves text fields and the track choice, refusing a stale edit
// and an edit that would leave a submitted entry incomplete.
func (s *Service) writeText(ctx context.Context, tx *sql.Tx, req WriteRequest, st model.TeamWriteState, id string) ([]string, error) {
	before, err := entryOrNone(entryrepo.GetEventEntry(ctx, tx, id))
	if err != nil {
		return nil, err
	}
	if err := checkEntryStamp(req.UpdatedAt, before); err != nil {
		return nil, err
	}
	if err := setEntryTrack(ctx, tx, req, st, id); err != nil {
		return nil, err
	}
	if err := entryrepo.UpsertEventEntryText(ctx, tx, req.Event.ID, id, req.UserID, req.Fields, req.Tools); err != nil {
		return nil, err
	}
	return s.keepComplete(ctx, tx, req.Event, st, id, before.SubmittedAt.Valid)
}

// setEntryTrack records the team's track choice while team choices are open.
func setEntryTrack(ctx context.Context, tx *sql.Tx, req WriteRequest, st model.TeamWriteState, id string) error {
	if req.Track == nil {
		return nil
	}
	if st.Stage != "open" && st.Stage != "building" {
		return &Error{Code: "team_choice_closed", Message: "team choices are closed"}
	}
	_, err := eventrepo.SetTeamTrack(ctx, tx, req.Event.ID, id, *req.Track)
	if errors.Is(err, sql.ErrNoRows) {
		return &Error{Code: "track_not_found", Message: "track not found"}
	}
	return err
}

// submitEntry submits a complete entry; an already submitted entry is left as is.
func (s *Service) submitEntry(ctx context.Context, tx *sql.Tx, req WriteRequest, st model.TeamWriteState, id string) ([]string, error) {
	e, err := entryOrNone(entryrepo.GetEventEntryForUpdate(ctx, tx, id))
	if err != nil {
		return nil, err
	}
	if !e.Exists {
		e = model.EventEntry{TeamID: id, EventID: req.Event.ID}
	}
	if err := checkEntryStamp(req.UpdatedAt, e); err != nil || e.SubmittedAt.Valid {
		return nil, err
	}
	check, err := s.EvaluateEntryTx(ctx, tx, req.Event, st, id, e)
	if err != nil {
		return nil, err
	}
	if !check.Complete {
		return check.Missing, &Error{Code: "entry_incomplete"}
	}
	_, err = entryrepo.SubmitEventEntry(ctx, tx, id, req.UserID)
	return nil, err
}

// writeScreenshot stores the screenshot, refusing a change that would leave
// a submitted entry incomplete.
func (s *Service) writeScreenshot(ctx context.Context, tx *sql.Tx, req WriteRequest, st model.TeamWriteState, id string) ([]string, error) {
	before, err := entryOrNone(entryrepo.GetEventEntry(ctx, tx, id))
	if err != nil {
		return nil, err
	}
	if err := entryrepo.UpsertEventEntryScreenshot(ctx, tx, req.Event.ID, id, req.UserID, req.Data, req.MIME); err != nil {
		return nil, err
	}
	return s.keepComplete(ctx, tx, req.Event, st, id, before.SubmittedAt.Valid)
}

func entryOrNone(e model.EventEntry, err error) (model.EventEntry, error) {
	if errors.Is(err, sql.ErrNoRows) {
		return model.EventEntry{}, nil
	}
	return e, err
}

// checkEntryStamp refuses a change when the entry moved on since the writer
// read it; a nil stamp skips the check and "" stands for no entry.
func checkEntryStamp(want *string, e model.EventEntry) error {
	if want == nil {
		return nil
	}
	stamp := ""
	if e.Exists {
		stamp = e.UpdatedAt.UTC().Format(time.RFC3339Nano)
	}
	if *want != stamp {
		return &Error{Code: "entry_changed", Message: "A teammate changed this since you opened it"}
	}
	return nil
}

// keepComplete refuses a change that leaves a submitted entry incomplete.
func (s *Service) keepComplete(ctx context.Context, tx *sql.Tx, ev model.Event, st model.TeamWriteState, id string, submitted bool) ([]string, error) {
	if !submitted {
		return nil, nil
	}
	after, err := entryrepo.GetEventEntry(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	check, err := s.EvaluateEntryTx(ctx, tx, ev, st, id, after)
	if err != nil {
		return nil, err
	}
	if !check.Complete {
		return check.Missing, &Error{Code: "entry_would_be_incomplete"}
	}
	return nil, nil
}

func (s *Service) Screenshot(ctx context.Context, teamID string) ([]byte, string, error) {
	return entryrepo.GetEventScreenshot(ctx, s.db, teamID)
}

func (s *Service) Entries(ctx context.Context, eventID string) ([]model.EventEntry, error) {
	return entryrepo.ListEventEntries(ctx, s.db, eventID)
}

func (s *Service) Entry(ctx context.Context, teamID string) (model.EventEntry, error) {
	return entryrepo.GetEventEntry(ctx, s.db, teamID)
}

func (s *Service) JudgeTeamEligible(ctx context.Context, eventID, userID, teamID string) (bool, error) {
	return entryrepo.JudgeTeamEligible(ctx, s.db, eventID, userID, teamID)
}

func (s *Service) EligibleJudgeTeams(ctx context.Context, eventID, userID string) (map[string]bool, error) {
	return entryrepo.EligibleJudgeTeams(ctx, s.db, eventID, userID)
}
