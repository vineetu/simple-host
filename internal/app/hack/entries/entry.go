package entries

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/vsriram/simple-host/internal/model"
	entryrepo "github.com/vsriram/simple-host/internal/repository/hackentries"
	eventrepo "github.com/vsriram/simple-host/internal/repository/hackevents"
	teamrepo "github.com/vsriram/simple-host/internal/repository/hackteams"
)

// SetExcluded changes the organiser's exclusion of an existing team entry.
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

// WriteRequest describes a participant's entry change.
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
	var teamID sql.NullString
	err = tx.QueryRowContext(ctx, `SELECT team_id FROM event_members WHERE event_id = $1 AND user_id = $2 AND role = 'participant' FOR SHARE`, req.Event.ID, req.UserID).Scan(&teamID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil, &Error{Code: "event_not_found"}
	}
	if err != nil {
		return "", nil, err
	}
	if !teamID.Valid || teamID.String == "" {
		return "", nil, &Error{Code: "no_team", Message: "join or start a team first"}
	}
	id := teamID.String
	if _, err = tx.ExecContext(ctx, `SELECT 1 FROM event_teams WHERE id=$1 FOR UPDATE`, id); err != nil {
		return id, nil, err
	}
	st, err := teamrepo.TeamWriteStateFor(ctx, tx, id, true)
	if errors.Is(err, sql.ErrNoRows) {
		return id, nil, &Error{Code: "no_team", Message: "join or start a team first"}
	}
	if err != nil {
		return id, nil, err
	}
	if code := st.WriteRefusal(); code != "" {
		return id, nil, &Error{Code: code}
	}
	var missing []string
	switch req.Kind {
	case "text":
		if req.UpdatedAt != nil {
			current, e := entryrepo.GetEventEntry(ctx, tx, id)
			if e != nil && !errors.Is(e, sql.ErrNoRows) {
				return id, nil, e
			}
			stamp := ""
			if current.Exists {
				stamp = current.UpdatedAt.UTC().Format(time.RFC3339Nano)
			}
			if *req.UpdatedAt != stamp {
				return id, nil, &Error{Code: "entry_changed"}
			}
		}
		before, e := entryrepo.GetEventEntry(ctx, tx, id)
		if e != nil && !errors.Is(e, sql.ErrNoRows) {
			return id, nil, e
		}
		if req.Track != nil {
			if st.Stage != "open" && st.Stage != "building" {
				return id, nil, &Error{Code: "team_choice_closed", Message: "team choices are closed"}
			}
			if _, e = eventrepo.SetTeamTrack(ctx, tx, req.Event.ID, id, *req.Track); e != nil {
				if errors.Is(e, sql.ErrNoRows) {
					return id, nil, &Error{Code: "track_not_found", Message: "track not found"}
				}
				return id, nil, e
			}
		}
		if e = entryrepo.UpsertEventEntryText(ctx, tx, req.Event.ID, id, req.UserID, req.Fields, req.Tools); e != nil {
			return id, nil, e
		}
		missing, err = s.keepComplete(ctx, tx, req.Event, st, id, before.SubmittedAt.Valid)
	case "submit":
		e, er := entryrepo.GetEventEntryForUpdate(ctx, tx, id)
		if errors.Is(er, sql.ErrNoRows) {
			e = model.EventEntry{TeamID: id, EventID: req.Event.ID}
		} else if er != nil {
			return id, nil, er
		}
		if req.UpdatedAt != nil {
			stamp := ""
			if e.Exists {
				stamp = e.UpdatedAt.UTC().Format(time.RFC3339Nano)
			}
			if *req.UpdatedAt != stamp {
				return id, nil, &Error{Code: "entry_changed"}
			}
		}
		if !e.SubmittedAt.Valid {
			check, er := s.EvaluateEntryTx(ctx, tx, req.Event, st, id, e)
			if er != nil {
				return id, nil, er
			}
			if !check.Complete {
				return id, check.Missing, &Error{Code: "entry_incomplete"}
			}
			_, err = entryrepo.SubmitEventEntry(ctx, tx, id, req.UserID)
		}
	case "unsubmit":
		_, err = entryrepo.UnsubmitEventEntry(ctx, tx, id)
	case "screenshot":
		before, er := entryrepo.GetEventEntry(ctx, tx, id)
		if er != nil && !errors.Is(er, sql.ErrNoRows) {
			return id, nil, er
		}
		if er = entryrepo.UpsertEventEntryScreenshot(ctx, tx, req.Event.ID, id, req.UserID, req.Data, req.MIME); er != nil {
			return id, nil, er
		}
		missing, err = s.keepComplete(ctx, tx, req.Event, st, id, before.SubmittedAt.Valid)
	}
	if err != nil {
		return id, missing, err
	}
	if err = tx.Commit(); err != nil {
		return id, nil, err
	}
	return id, nil, nil
}

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

// Screenshot reads an entry's stored screenshot.
func (s *Service) Screenshot(ctx context.Context, teamID string) ([]byte, string, error) {
	return entryrepo.GetEventScreenshot(ctx, s.db, teamID)
}

// Entries lists the stored entries for an event.
func (s *Service) Entries(ctx context.Context, eventID string) ([]model.EventEntry, error) {
	return entryrepo.ListEventEntries(ctx, s.db, eventID)
}

// Entry reads a team's entry.
func (s *Service) Entry(ctx context.Context, teamID string) (model.EventEntry, error) {
	return entryrepo.GetEventEntry(ctx, s.db, teamID)
}

// JudgeTeamEligible checks whether the judge can see the team's entry.
func (s *Service) JudgeTeamEligible(ctx context.Context, eventID, userID, teamID string) (bool, error) {
	return entryrepo.JudgeTeamEligible(ctx, s.db, eventID, userID, teamID)
}

// EligibleJudgeTeams returns the teams available to a judge.
func (s *Service) EligibleJudgeTeams(ctx context.Context, eventID, userID string) (map[string]bool, error) {
	return entryrepo.EligibleJudgeTeams(ctx, s.db, eventID, userID)
}
