// TODO(4.2): move SQL to repository/hackevents.
package events

import (
	"context"
	"database/sql"
	"errors"
	"github.com/vsriram/simple-host/internal/db"
	"github.com/vsriram/simple-host/internal/rules/hackentries"
	"github.com/vsriram/simple-host/internal/rules/hackevents"
	"time"
)

// SetStage changes the event stage and settles entries within the transaction.
func (s *Service) SetStage(ctx context.Context, event db.Event, stage string, settle func(context.Context, *sql.Tx, string) error) (db.Event, string, error) {
	if event.Stage == "archived" && stage != "archived" {
		return db.Event{}, "", &Error{409, "event_closed", "an ended event cannot be reopened"}
	}
	if stage == "archived" && event.Stage != "archived" {
		// An event nobody joined is deleted, not ended: ending one would keep
		// its name and page for good without it ever running.
		participants, _, _, err := db.CountEventMembers(ctx, s.db, event.ID)
		if err != nil {
			return db.Event{}, "", err
		}
		if participants == 0 {
			return db.Event{}, "", &Error{409, "archive_needs_participants", "nobody has joined, so delete the event instead"}
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return db.Event{}, "", err
	}
	defer tx.Rollback()
	var current string
	if err := tx.QueryRowContext(ctx, `SELECT stage FROM events WHERE id = $1 FOR UPDATE`, event.ID).Scan(&current); err != nil {
		return db.Event{}, "", err
	}
	if current == "archived" && stage != "archived" {
		return db.Event{}, "", &Error{409, "event_closed", "an ended event cannot be reopened"}
	}
	// Every team row too: a deploy let in holds its team FOR SHARE and
	// finishes before the deadline moves.
	if _, err := tx.ExecContext(ctx, `SELECT 1 FROM event_teams WHERE event_id = $1 FOR UPDATE`, event.ID); err != nil {
		return db.Event{}, "", err
	}
	var updated db.Event
	if stage == "archived" {
		updated, err = s.ArchiveEventTx(ctx, tx, event.ID, current, settle)
		if err != nil {
			return db.Event{}, "", err
		}
	} else {
		switch {
		case hackevents.SubmissionsClosedStage(stage) && !hackevents.SubmissionsClosedStage(current) && current != "archived":
			// Submissions close now: the deadline becomes now unless it
			// already passed. Teams given more time keep it.
			_, err = tx.ExecContext(ctx, `
				UPDATE events SET submission_deadline = clock_timestamp()
				 WHERE id = $1 AND (submission_deadline IS NULL OR submission_deadline > clock_timestamp())`, event.ID)
		case (stage == "open" || stage == "building") && current != "open" && current != "building":
			// Back to open or building from any other stage (closed, or draft
			// after closed): a passed deadline (and passed extensions) go, so
			// teams can publish again until the organiser sets a new one.
			if _, err = tx.ExecContext(ctx, `
				UPDATE events SET submission_deadline = NULL
				 WHERE id = $1 AND submission_deadline <= clock_timestamp()`, event.ID); err == nil {
				_, err = tx.ExecContext(ctx, `
					UPDATE event_teams SET deadline_override = NULL
					 WHERE event_id = $1 AND deadline_override <= clock_timestamp()`, event.ID)
			}
			if err == nil {
				// Section 7, "Unsettle": back to building reopens every
				// unsubmitted draft the sweep had already settled.
				err = db.UnsettleEventDrafts(ctx, tx, event.ID)
			}
		}
		if err != nil {
			return db.Event{}, "", err
		}
		updated, err = db.SetEventStage(ctx, tx, event.ID, stage)
		if err != nil {
			return db.Event{}, "", err
		}
		if err := db.UnpinReopened(ctx, tx, event.ID); err != nil {
			return db.Event{}, "", err
		}
		// Section 7: settle every due entry in the same transaction as a
		// move that freezes submissions, so judging never has to wait a
		// minute for the sweep to catch up.
		if hackevents.SubmissionsClosedStage(stage) {
			if err := settle(ctx, tx, event.ID); err != nil {
				return db.Event{}, "", err
			}
		}
		// Moving to results locks judging in the same transaction, when it
		// is not locked already: Results already stops scoring
		// (scoringStage), and locking here removes the dead end where the
		// Results screen refused to publish an unlocked event. Moving back
		// to judging leaves the lock on; the stage picker says so.
		if stage == "results" && current != "results" {
			if updated, err = db.SetJudgingLock(ctx, tx, event.ID, true, ""); err != nil {
				return db.Event{}, "", err
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return db.Event{}, "", err
	}
	return updated, current, nil
}

// DeleteEvent checks and deletes an event under its lock.
func (s *Service) DeleteEvent(ctx context.Context, event db.Event, userID string, confirm *string, trash func(context.Context, string, string) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Hold the event against joins and recheck current organiser membership.
	var stage, role string
	var takenDown bool
	err = tx.QueryRowContext(ctx, `SELECT e.stage, m.role, e.taken_down_at IS NOT NULL
		FROM events e JOIN event_members m ON m.event_id=e.id
		WHERE e.id=$1 AND m.user_id=$2 FOR UPDATE OF e FOR SHARE OF m`, event.ID, userID).Scan(&stage, &role, &takenDown)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && role != "organiser") {
		return sql.ErrNoRows
	}
	if err != nil {
		return err
	}
	if takenDown {
		return &Error{403, "event_taken_down", "this event has been taken down"}
	}
	participants, _, judges, err := db.CountEventMembers(ctx, tx, event.ID)
	if err != nil {
		return err
	}
	if (confirm != nil && *confirm != event.Slug) || (confirm == nil && (stage == "archived" || participants+judges > 0)) {
		return &Error{409, "delete_only_empty", "to permanently delete this event, send {\"confirm\":\"" + event.Slug + "\"} with the matching event address name"}
	}
	if err := s.DeleteEventTx(ctx, tx, event, trash); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return nil
}

// ArchiveEventTx archives an event in the caller's transaction.
func (s *Service) ArchiveEventTx(ctx context.Context, tx *sql.Tx, eventID, current string, settle func(context.Context, *sql.Tx, string) error) (db.Event, error) {
	if _, err := tx.ExecContext(ctx, `SELECT 1 FROM event_teams WHERE event_id = $1 FOR UPDATE`, eventID); err != nil {
		return db.Event{}, err
	}
	if !hackevents.SubmissionsClosedStage(current) {
		if _, err := tx.ExecContext(ctx, `
			UPDATE events SET submission_deadline = clock_timestamp()
			 WHERE id = $1 AND (submission_deadline IS NULL OR submission_deadline > clock_timestamp())`, eventID); err != nil {
			return db.Event{}, err
		}
	}
	updated, err := db.SetEventStage(ctx, tx, eventID, "archived")
	if err != nil {
		return db.Event{}, err
	}
	if err := db.UnpinReopened(ctx, tx, eventID); err != nil {
		return db.Event{}, err
	}
	// Section 7: an event ending always freezes every remaining draft.
	if err := settle(ctx, tx, eventID); err != nil {
		return db.Event{}, err
	}
	return updated, nil
}

// DeleteEventTx deletes an event in the caller's transaction.
func (s *Service) DeleteEventTx(ctx context.Context, tx *sql.Tx, ev db.Event, trash func(context.Context, string, string) error) error {
	if trash != nil {
		teams, err := db.ListEventTeams(ctx, tx, ev.ID)
		if err != nil {
			return err
		}
		for _, team := range teams {
			if err := trash(ctx, ev.AccountID, team.Slug); err != nil {
				return err
			}
		}
	}
	return db.DeleteEventAndAccount(ctx, tx, ev)
}

// ErrValidation indicates that the input validator rejected the request.
var ErrValidation = errors.New("invalid input")
var entryFields = []string{"title", "tagline", "description", "video_url", "code_url", "screenshot"}

// PatchEvent applies validated settings while holding the event lock.
func (s *Service) PatchEvent(ctx context.Context, eventID string, validate func(*db.Event) (bool, bool, bool)) (db.Event, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return db.Event{}, err
	}
	defer tx.Rollback()
	ev, err := db.GetEventForUpdate(ctx, tx, eventID)
	if err != nil {
		return db.Event{}, err
	}
	deadlineChanged, entryRequiredChanged, ok := validate(&ev)
	if !ok {
		return db.Event{}, ErrValidation
	}
	stage := ev.Stage
	if deadlineChanged {
		if stage == "archived" {
			return db.Event{}, &Error{409, "event_closed", "an ended event cannot be changed"}
		}
		// Submissions closed means the deadline has passed; reopening them
		// is moving the event back to Building, or one team's extension.
		var dbNow time.Time
		if err := tx.QueryRowContext(ctx, `SELECT clock_timestamp()`).Scan(&dbNow); err != nil {
			return db.Event{}, err
		}
		if hackevents.SubmissionsClosedStage(stage) && (!ev.SubmissionDeadline.Valid || ev.SubmissionDeadline.Time.After(dbNow)) {
			return db.Event{}, &Error{409, "submissions_closed_stage", "submissions are closed: move the event back to Building to set a new deadline, or give single teams more time"}
		}
	}
	updated, err := db.UpdateEventPatch(ctx, tx, ev)
	if err != nil {
		return db.Event{}, err
	}
	updated, err = db.UpdateEventEntrySettings(ctx, tx, ev.ID, ev.SubmissionDeadline, ev.EntryRequired, ev.GalleryOpen)
	if err != nil {
		return db.Event{}, err
	}
	// Deprecated entry_required alias (section 4.2): maps to "required" for
	// the listed fields, leaves every other field's mode as it is, and
	// demotes the stored preset to custom -- kept for one release.
	if entryRequiredChanged {
		fields := hackentries.LoadRules(updated.EntryRules)
		want := map[string]bool{}
		for _, f := range ev.EntryRequired {
			want[f] = true
		}
		for _, f := range entryFields {
			if want[f] {
				fields[f] = "required"
			}
		}
		updated, err = db.UpdateEventEntryRules(ctx, tx, ev.ID, "custom", hackentries.MarshalRules(fields), updated.JudgeFirst, ev.EntryRequired)
		if err != nil {
			return db.Event{}, err
		}
	}
	if deadlineChanged {
		// Lock every team row: a deploy let in before the old deadline
		// holds its team FOR SHARE and finishes first.
		if _, err := tx.ExecContext(ctx, `SELECT 1 FROM event_teams WHERE event_id = $1 FOR UPDATE`, ev.ID); err != nil {
			return db.Event{}, err
		}
		if err := db.UnpinReopened(ctx, tx, ev.ID); err != nil {
			return db.Event{}, err
		}
		// A deadline pushed later reopens drafts the sweep had already
		// settled as not submitted (section 7, "Unsettle").
		if err := db.UnsettleEventDrafts(ctx, tx, ev.ID); err != nil {
			return db.Event{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return db.Event{}, err
	}
	return updated, nil
}

func savepoint(ctx context.Context, q db.Querier, fn func() error) error {
	if _, err := q.ExecContext(ctx, "SAVEPOINT hack_try"); err != nil {
		return err
	}
	if err := fn(); err != nil {
		if _, rerr := q.ExecContext(ctx, "ROLLBACK TO SAVEPOINT hack_try"); rerr != nil {
			return rerr
		}
		return err
	}
	_, err := q.ExecContext(ctx, "RELEASE SAVEPOINT hack_try")
	return err
}
