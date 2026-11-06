package events

import (
	"context"
	"database/sql"
	"errors"
	"time"

	model "github.com/vsriram/simple-host/internal/model"
	accounts "github.com/vsriram/simple-host/internal/repository/accounts"
	entryrepo "github.com/vsriram/simple-host/internal/repository/hackentries"
	eventrepo "github.com/vsriram/simple-host/internal/repository/hackevents"
	teamrepo "github.com/vsriram/simple-host/internal/repository/hackteams"
	sqlkit "github.com/vsriram/simple-host/internal/repository/sqlkit"
	"github.com/vsriram/simple-host/internal/rules/hackentries"
	"github.com/vsriram/simple-host/internal/rules/hackevents"
)

func unique(err error) bool { return hackevents.UniqueError(err) }

// CreateEvent creates an event and its organiser in one transaction.
func (s *Service) CreateEvent(ctx context.Context, userID, slug, title, organiserName, organisation, contact, purpose, tz string, expected int, startsAt, endsAt sql.NullTime, perDay, maxActive, teamSize, joinLen, judgeLen int) (model.Event, model.EventMember, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return model.Event{}, model.EventMember{}, err
	}
	defer tx.Rollback()

	if err := eventrepo.LockUserRow(ctx, tx, userID); err != nil {
		return model.Event{}, model.EventMember{}, err
	}
	since := time.Now().Add(-24 * time.Hour)
	n, err := eventrepo.CountEventCreatesSince(ctx, tx, userID, since)
	if err != nil {
		return model.Event{}, model.EventMember{}, err
	}
	if n >= perDay {
		return model.Event{}, model.EventMember{}, &Error{"too_many_events_today", "you have created too many events today"}
	}
	active, err := eventrepo.CountActiveEventsByOrganiser(ctx, tx, userID)
	if err != nil {
		return model.Event{}, model.EventMember{}, err
	}
	if active >= maxActive {
		return model.Event{}, model.EventMember{}, &Error{"too_many_active_events", "you already have as many active events as this instance allows"}
	}

	eventID, err := eventrepo.NewUUID(ctx, tx)
	if err != nil {
		return model.Event{}, model.EventMember{}, err
	}
	holding, err := accounts.CreateUser(ctx, tx, "event+"+eventID+"@events.invalid", "", false)
	if err != nil {
		return model.Event{}, model.EventMember{}, err
	}
	claimed, err := accounts.ClaimHandle(ctx, tx, holding.ID, slug)
	if err != nil {
		return model.Event{}, model.EventMember{}, err
	}
	if !claimed {
		return model.Event{}, model.EventMember{}, &Error{"name_taken", "that name is already in use"}
	}
	if err := accounts.MarkEventAccount(ctx, tx, holding.ID); err != nil {
		return model.Event{}, model.EventMember{}, err
	}

	var ev model.Event
	for attempt := 0; attempt < 8; attempt++ {
		joinCode, err := hackevents.RandomCode(joinLen)
		if err != nil {
			return model.Event{}, model.EventMember{}, err
		}
		judgeCode, err := hackevents.RandomCode(judgeLen)
		if err != nil {
			return model.Event{}, model.EventMember{}, err
		}
		err = savepoint(ctx, tx, func() error {
			var ierr error
			ev, ierr = eventrepo.InsertEvent(ctx, tx, model.Event{
				ID:                   eventID,
				Slug:                 slug,
				AccountID:            holding.ID,
				CreatedBy:            sql.NullString{String: userID, Valid: true},
				Title:                title,
				Stage:                "draft",
				OrganiserName:        organiserName,
				Organisation:         organisation,
				ContactEmail:         contact,
				Purpose:              purpose,
				ExpectedParticipants: sql.NullInt64{Int64: int64(expected), Valid: true},
				TimeZone:             tz,
				StartsAt:             startsAt,
				EndsAt:               endsAt,
				TeamSizeMax:          teamSize,
				JoinCode:             joinCode,
				JudgeCode:            judgeCode,
				// New events start on the "video or website" preset
				// (section 2); a Go table, not a DB default, so the
				// preset's field list lives in one place
				// (rules/hackentries). EntryRulesChosen false until the
				// organiser saves a choice on Entries (SIM-382,
				// 0005-hack-entry-rules-chosen.sql) -- that's what gates
				// Overview's "Choose what teams hand in" first step.
				EntryPreset:      "video_or_website",
				EntryRules:       hackentries.MarshalRules(hackentries.ExpandPreset("video_or_website")),
				EntryRulesChosen: false,
			})
			return ierr
		})
		if err == nil {
			break
		}
		if unique(err) && attempt < 7 {
			continue
		}
		if unique(err) {
			return model.Event{}, model.EventMember{}, &Error{"name_taken", "that name is already in use"}
		}
		return model.Event{}, model.EventMember{}, err
	}
	// Starter rubric, only for this new row. Nothing else writes one onto an
	// event that does not already have it.
	if err := entryrepo.ReplaceRubric(ctx, tx, ev.ID, hackentries.DefaultEventRubric()); err != nil {
		return model.Event{}, model.EventMember{}, err
	}
	if organiserName != "" {
		if err := accounts.FillEmptyDisplayName(ctx, tx, userID, organiserName); err != nil {
			return model.Event{}, model.EventMember{}, err
		}
	}
	if _, err := eventrepo.InsertEventMember(ctx, tx, ev.ID, userID, "organiser"); err != nil {
		return model.Event{}, model.EventMember{}, err
	}
	if err := tx.Commit(); err != nil {
		return model.Event{}, model.EventMember{}, err
	}
	member, err := eventrepo.GetEventMember(ctx, s.db, ev.ID, userID)
	if err != nil {
		return model.Event{}, model.EventMember{}, err
	}
	return ev, member, nil
}

// SetStage changes the event stage and settles entries within the transaction.
func (s *Service) SetStage(ctx context.Context, event model.Event, stage string, settle func(context.Context, *sql.Tx, string) error) (model.Event, string, error) {
	if event.Stage == "archived" && stage != "archived" {
		return model.Event{}, "", &Error{"event_closed", "an ended event cannot be reopened"}
	}
	if stage == "archived" && event.Stage != "archived" {
		// An event nobody joined is deleted, not ended: ending one would keep
		// its name and page for good without it ever running.
		participants, _, _, err := eventrepo.CountEventMembers(ctx, s.db, event.ID)
		if err != nil {
			return model.Event{}, "", err
		}
		if participants == 0 {
			return model.Event{}, "", &Error{"archive_needs_participants", "nobody has joined, so delete the event instead"}
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return model.Event{}, "", err
	}
	defer tx.Rollback()
	updated, current, err := s.SetStageTx(ctx, tx, event, stage, settle)
	if err != nil {
		return model.Event{}, "", err
	}
	if err := tx.Commit(); err != nil {
		return model.Event{}, "", err
	}
	return updated, current, nil
}

// SetStageTx is SetStage's work inside the caller's transaction: it locks
// the event's stage and team rows, moves the stage with every side effect
// SetStage has, and returns the stage it found under that lock. Publishing
// results uses it so the move to Results and the snapshot commit together
// (SIM-271). The archived path still needs SetStage's participant check.
func (s *Service) SetStageTx(ctx context.Context, tx *sql.Tx, event model.Event, stage string, settle func(context.Context, *sql.Tx, string) error) (model.Event, string, error) {
	var current string
	var err error
	if err = eventrepo.LockWorkflowStage(ctx, tx, event.ID).Scan(&current); err != nil {
		return model.Event{}, "", err
	}
	if current == "archived" && stage != "archived" {
		return model.Event{}, "", &Error{"event_closed", "an ended event cannot be reopened"}
	}
	// Every team row too: a deploy let in holds its team FOR SHARE and
	// finishes before the deadline moves.
	if _, err := eventrepo.LockEventTeams(ctx, tx, event.ID); err != nil {
		return model.Event{}, "", err
	}
	var updated model.Event
	if stage == "archived" {
		updated, err = s.ArchiveEventTx(ctx, tx, event.ID, current, settle)
		if err != nil {
			return model.Event{}, "", err
		}
	} else {
		switch {
		case hackevents.SubmissionsClosedStage(stage) && !hackevents.SubmissionsClosedStage(current) && current != "archived":
			// Submissions close now: the deadline becomes now unless it
			// already passed. Teams given more time keep it.
			_, err = eventrepo.CloseSubmissionDeadline(ctx, tx, event.ID)
		case (stage == "open" || stage == "building") && current != "open" && current != "building":
			// Back to open or building from any other stage (closed, or draft
			// after closed): a passed deadline (and passed extensions) go, so
			// teams can publish again until the organiser sets a new one.
			if _, err = eventrepo.ClearPassedSubmissionDeadline(ctx, tx, event.ID); err == nil {
				_, err = eventrepo.ClearPassedTeamDeadlines(ctx, tx, event.ID)
			}
			if err == nil {
				// Section 7, "Unsettle": back to building reopens every
				// unsubmitted draft the sweep had already settled.
				err = entryrepo.UnsettleEventDrafts(ctx, tx, event.ID)
			}
		}
		if err != nil {
			return model.Event{}, "", err
		}
		updated, err = eventrepo.SetEventStage(ctx, tx, event.ID, stage)
		if err != nil {
			return model.Event{}, "", err
		}
		if err := teamrepo.UnpinReopened(ctx, tx, event.ID); err != nil {
			return model.Event{}, "", err
		}
		// Section 7: settle every due entry in the same transaction as a
		// move that freezes submissions, so judging never has to wait a
		// minute for the sweep to catch up.
		if hackevents.SubmissionsClosedStage(stage) {
			if err := settle(ctx, tx, event.ID); err != nil {
				return model.Event{}, "", err
			}
		}
		// 0003-hack-notices-judges-messages.sql: judging opens now, from
		// anything but results -- after settle, so a judge's first
		// reminder/queue read already sees the settled entries.
		if stage == "judging" && current != "judging" && current != "results" {
			if err := eventrepo.NotifyJudges(ctx, tx, event.ID, eventrepo.NoticeJudgingOpen); err != nil {
				return model.Event{}, "", err
			}
		}
		// Moving to results locks judging in the same transaction, when it
		// is not locked already: Results already stops scoring
		// (scoringStage), and locking here removes the dead end where the
		// Results screen refused to publish an unlocked event. Moving back
		// to judging leaves the lock on; the stage picker says so.
		if stage == "results" && current != "results" {
			if updated, err = eventrepo.SetJudgingLock(ctx, tx, event.ID, true, ""); err != nil {
				return model.Event{}, "", err
			}
		}
	}
	return updated, current, nil
}

// DeleteEvent checks and deletes an event under its lock.
func (s *Service) DeleteEvent(ctx context.Context, event model.Event, userID string, confirm *string, trash func(context.Context, string, string) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Hold the event against joins and recheck current organiser membership.
	var stage, role string
	var takenDown bool
	err = eventrepo.LockStageAndMemberRole(ctx, tx, event.ID, userID).Scan(&stage, &role, &takenDown)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && role != "organiser") {
		return &Error{"event_not_found", "event not found"}
	}
	if err != nil {
		return err
	}
	if takenDown {
		return &Error{"event_taken_down", "this event has been taken down"}
	}
	participants, _, judges, err := eventrepo.CountEventMembers(ctx, tx, event.ID)
	if err != nil {
		return err
	}
	if (confirm != nil && *confirm != event.Slug) || (confirm == nil && (stage == "archived" || participants+judges > 0)) {
		return &Error{"delete_only_empty", "to permanently delete this event, send {\"confirm\":\"" + event.Slug + "\"} with the matching event address name"}
	}
	if err := s.DeleteEventTx(ctx, tx, event, trash); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return nil
}

// ArchiveEventTx locks teams and closes the deadline unless it already passed or
// submissions were closed, then archives, unpins and settles entries. The caller
// holds the event row FOR UPDATE, has checked that a participant exists, and
// supplies the current stage read under that lock.
func (s *Service) ArchiveEventTx(ctx context.Context, tx *sql.Tx, eventID, current string, settle func(context.Context, *sql.Tx, string) error) (model.Event, error) {
	if _, err := eventrepo.LockEventTeams(ctx, tx, eventID); err != nil {
		return model.Event{}, err
	}
	if !hackevents.SubmissionsClosedStage(current) {
		if _, err := eventrepo.CloseDeadlineForStage(ctx, tx, eventID); err != nil {
			return model.Event{}, err
		}
	}
	updated, err := eventrepo.SetEventStage(ctx, tx, eventID, "archived")
	if err != nil {
		return model.Event{}, err
	}
	if err := teamrepo.UnpinReopened(ctx, tx, eventID); err != nil {
		return model.Event{}, err
	}
	// Section 7: an event ending always freezes every remaining draft.
	if err := settle(ctx, tx, eventID); err != nil {
		return model.Event{}, err
	}
	return updated, nil
}

// DeleteEventTx trashes team sites, then deletes the event and its holding
// account, including recently deleted sites; there is no event undo. The caller
// holds the event row and has checked confirmation against the slug.
func (s *Service) DeleteEventTx(ctx context.Context, tx *sql.Tx, ev model.Event, trash func(context.Context, string, string) error) error {
	if trash != nil {
		teams, err := teamrepo.ListEventTeams(ctx, tx, ev.ID)
		if err != nil {
			return err
		}
		for _, team := range teams {
			if err := trash(ctx, ev.AccountID, team.Slug); err != nil {
				return err
			}
		}
	}
	return eventrepo.DeleteEventAndAccount(ctx, tx, ev)
}

// PatchEvent applies validated settings while holding the event lock.
func (s *Service) PatchEvent(ctx context.Context, eventID string, validate func(*model.Event) (bool, bool, error)) (model.Event, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return model.Event{}, err
	}
	defer tx.Rollback()
	// Re-read under the lock: the event loaded before the transaction may be stale.
	ev, err := eventrepo.GetEventForUpdate(ctx, tx, eventID)
	if err != nil {
		return model.Event{}, err
	}
	deadlineChanged, entryRequiredChanged, validationErr := validate(&ev)
	if validationErr != nil {
		return model.Event{}, validationErr
	}
	stage := ev.Stage
	if deadlineChanged {
		if stage == "archived" {
			return model.Event{}, &Error{"event_closed", "an ended event cannot be changed"}
		}
		// Submissions closed means the deadline has passed; reopening them
		// is moving the event back to Building, or one team's extension.
		var dbNow time.Time
		if err := eventrepo.DatabaseNow(ctx, tx).Scan(&dbNow); err != nil {
			return model.Event{}, err
		}
		if hackevents.SubmissionsClosedStage(stage) && (!ev.SubmissionDeadline.Valid || ev.SubmissionDeadline.Time.After(dbNow)) {
			return model.Event{}, &Error{"submissions_closed_stage", "submissions are closed: move the event back to Building to set a new deadline, or give single teams more time"}
		}
	}
	updated, err := eventrepo.UpdateEventPatch(ctx, tx, ev)
	if err != nil {
		return model.Event{}, err
	}
	updated, err = eventrepo.UpdateEventEntrySettings(ctx, tx, ev.ID, ev.SubmissionDeadline, ev.EntryRequired, ev.GalleryOpen)
	if err != nil {
		return model.Event{}, err
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
		for _, f := range hackevents.EntryFields {
			if want[f] {
				fields[f] = "required"
			}
		}
		updated, err = eventrepo.UpdateEventEntryRules(ctx, tx, ev.ID, "custom", hackentries.MarshalRules(fields), updated.JudgeFirst, ev.EntryRequired)
		if err != nil {
			return model.Event{}, err
		}
	}
	if deadlineChanged {
		// Lock every team row: a deploy let in before the old deadline
		// holds its team FOR SHARE and finishes first.
		if _, err := eventrepo.LockEventTeams(ctx, tx, ev.ID); err != nil {
			return model.Event{}, err
		}
		if err := teamrepo.UnpinReopened(ctx, tx, ev.ID); err != nil {
			return model.Event{}, err
		}
		// A deadline pushed later reopens drafts the sweep had already
		// settled as not submitted (section 7, "Unsettle").
		if err := entryrepo.UnsettleEventDrafts(ctx, tx, ev.ID); err != nil {
			return model.Event{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return model.Event{}, err
	}
	return updated, nil
}

// savepoint rolls back a failed attempt so a unique violation does not abort
// the transaction and prevent a retry.
func savepoint(ctx context.Context, q sqlkit.Querier, fn func() error) error {
	if _, err := eventrepo.StartWorkflowSavepoint(ctx, q); err != nil {
		return err
	}
	if err := fn(); err != nil {
		if _, rerr := eventrepo.RollbackWorkflowSavepoint(ctx, q); rerr != nil {
			return rerr
		}
		return err
	}
	_, err := eventrepo.ReleaseWorkflowSavepoint(ctx, q)
	return err
}

func eventLeaveOperations() eventrepo.LeaveOperations {
	return eventrepo.LeaveOperations{LockMember: teamrepo.LockMember, LockTeam: teamrepo.LockTeam, DeleteTeamIfEmpty: teamrepo.DeleteTeamIfEmpty, EnsureTeamAdmin: teamrepo.EnsureTeamAdmin}
}
