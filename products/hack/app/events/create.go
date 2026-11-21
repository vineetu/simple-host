package events

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"time"

	accounts "github.com/vsriram/simple-host/host/access"
	"github.com/vsriram/simple-host/host/sites"
	"github.com/vsriram/simple-host/host/sqlkit"
	model "github.com/vsriram/simple-host/products/hack/model"
	entryrepo "github.com/vsriram/simple-host/products/hack/repository/hackentries"
	eventrepo "github.com/vsriram/simple-host/products/hack/repository/hackevents"
	"github.com/vsriram/simple-host/products/hack/repository/hackmembers"
	teamrepo "github.com/vsriram/simple-host/products/hack/repository/hackteams"
	"github.com/vsriram/simple-host/products/hack/rules/hackentries"
	"github.com/vsriram/simple-host/products/hack/rules/hackevents"
)

func unique(err error) bool { return hackevents.UniqueError(err) }

func (s *Service) CreateEvent(ctx context.Context, userID, slug, title, organiserName, organisation, contact, purpose, tz string, expected int, startsAt, endsAt sql.NullTime, perDay, maxActive, teamSize, joinLen, judgeLen int) (model.Event, model.EventMember, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return model.Event{}, model.EventMember{}, err
	}
	defer tx.Rollback()
	if err := checkCreateLimits(ctx, tx, userID, perDay, maxActive); err != nil {
		return model.Event{}, model.EventMember{}, err
	}
	eventID, err := eventrepo.NewUUID(ctx, tx)
	if err != nil {
		return model.Event{}, model.EventMember{}, err
	}
	// The event's account is a team whose members are the organisers (SyncAccess): its sites are the team sites and the event website.
	accountID, claimed, err := accounts.CreateProductTeam(ctx, tx, "event+"+eventID+"@events.invalid", slug)
	if err == nil && !claimed {
		err = errNameTaken
	}
	if err != nil {
		return model.Event{}, model.EventMember{}, err
	}
	ev, err := insertWithCodes(ctx, tx, model.Event{
		ID: eventID, Slug: slug, AccountID: accountID, CreatedBy: sql.NullString{String: userID, Valid: true}, Stage: "draft",
		Title: title, OrganiserName: organiserName, Organisation: organisation, ContactEmail: contact, Purpose: purpose,
		ExpectedParticipants: sql.NullInt64{Int64: int64(expected), Valid: true}, TimeZone: tz, StartsAt: startsAt, EndsAt: endsAt, TeamSizeMax: teamSize,
		EntryPreset: "video_or_website",
		EntryRules:  hackentries.MarshalRules(hackentries.ExpandPreset("video_or_website")),
	}, joinLen, judgeLen)
	if err == nil {
		err = seedEvent(ctx, tx, ev.ID, userID, organiserName)
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		return model.Event{}, model.EventMember{}, err
	}
	member, err := hackmembers.Member(ctx, s.db, ev.ID, userID, hackmembers.NoLock)
	member, err = hackmembers.WithDisplayName(ctx, s.db, member, err)
	if err != nil {
		return model.Event{}, model.EventMember{}, err
	}
	return ev, member, nil
}

var errNameTaken = &Error{Code: "name_taken", Message: "that name is already in use"}

// checkCreateLimits locks the user's row, then refuses a creator over the per-day or active-event limits (0 means no limit).
func checkCreateLimits(ctx context.Context, tx *sql.Tx, userID string, perDay, maxActive int) error {
	if err := accounts.LockAccount(ctx, tx, userID); err != nil {
		return err
	}
	n, err := eventrepo.CountEventCreatesSince(ctx, tx, userID, time.Now().Add(-24*time.Hour))
	if err != nil {
		return err
	}
	if perDay > 0 && n >= perDay {
		return &Error{Code: "too_many_events_today", Message: "you have created too many events today"}
	}
	active, err := eventrepo.CountActiveEventsByOrganiser(ctx, tx, userID)
	if err != nil || maxActive <= 0 || active < maxActive {
		return err
	}
	noun := "active events"
	if maxActive == 1 {
		noun = "active event"
	}
	return &Error{Code: "too_many_active_events", Message: fmt.Sprintf("You already have %d %s, the most this site allows. Events count until they're ended: end one from its Home, or delete it in Settings.", maxActive, noun)}
}

// insertWithCodes inserts ev with fresh join and judge codes, retrying a collision up to 8 times; a collision every time reads as name_taken.
func insertWithCodes(ctx context.Context, tx *sql.Tx, ev model.Event, joinLen, judgeLen int) (model.Event, error) {
	var err error
	for attempt := 0; attempt < 8; attempt++ {
		if ev.JoinCode, err = hackevents.RandomCode(joinLen); err != nil {
			return model.Event{}, err
		}
		if ev.JudgeCode, err = hackevents.RandomCode(judgeLen); err != nil {
			return model.Event{}, err
		}
		var inserted model.Event
		err = savepoint(ctx, tx, func() error {
			var ierr error
			inserted, ierr = eventrepo.InsertEvent(ctx, tx, ev)
			return ierr
		})
		if !unique(err) {
			return inserted, err
		}
	}
	return model.Event{}, errNameTaken
}

func seedEvent(ctx context.Context, tx *sql.Tx, eventID, userID, organiserName string) error {
	if err := entryrepo.ReplaceRubric(ctx, tx, eventID, hackentries.DefaultEventRubric()); err != nil {
		return err
	}
	if organiserName != "" {
		if err := accounts.FillEmptyDisplayName(ctx, tx, userID, organiserName); err != nil {
			return err
		}
	}
	if _, err := hackmembers.Join(ctx, tx, eventID, userID, "organiser"); err != nil {
		return err
	}
	return SyncAccess(ctx, tx, eventID)
}

func (s *Service) SetStage(ctx context.Context, event model.Event, stage string) (model.Event, string, error) {
	if event.Stage == "archived" && stage != "archived" {
		return model.Event{}, "", &Error{Code: "event_closed", Message: "an ended event cannot be reopened"}
	}
	if stage == "archived" && event.Stage != "archived" {
		// An event nobody joined is deleted, not ended: ending one would keep its name and page for good without it ever running.
		participants, err := hackmembers.Count(ctx, s.db, hackmembers.Filter{EventID: event.ID, Role: "participant", Status: "approved"})
		if err != nil {
			return model.Event{}, "", err
		}
		if participants == 0 {
			return model.Event{}, "", &Error{Code: "archive_needs_participants", Message: "nobody has joined, so delete the event instead"}
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return model.Event{}, "", err
	}
	defer tx.Rollback()
	updated, current, err := s.SetStageTx(ctx, tx, event, stage)
	if err != nil {
		return model.Event{}, "", err
	}
	if err := tx.Commit(); err != nil {
		return model.Event{}, "", err
	}
	return updated, current, nil
}

func (s *Service) SetStageTx(ctx context.Context, tx *sql.Tx, event model.Event, stage string) (model.Event, string, error) {
	var current string
	if err := eventrepo.LockWorkflowStage(ctx, tx, event.ID).Scan(&current); err != nil {
		return model.Event{}, "", err
	}
	if current == "archived" && stage != "archived" {
		return model.Event{}, "", &Error{Code: "event_closed", Message: "an ended event cannot be reopened"}
	}
	// Every team row too: a deploy let in holds its team FOR SHARE and finishes before the deadline moves.
	if err := teamrepo.LockEventTeams(ctx, tx, event.ID); err != nil {
		return model.Event{}, "", err
	}
	if stage == "archived" {
		updated, err := s.ArchiveEventTx(ctx, tx, event.ID, current)
		if err != nil {
			return model.Event{}, "", err
		}
		return updated, current, nil
	}
	updated, err := s.moveStage(ctx, tx, event.ID, current, stage)
	if err == nil && current != stage {
		err = hackmembers.DismissTeamMessages(ctx, tx, event.ID)
	}
	if err != nil {
		return model.Event{}, "", err
	}
	return updated, current, nil
}

func (s *Service) moveStage(ctx context.Context, tx *sql.Tx, eventID, current, stage string) (model.Event, error) {
	if err := moveSubmissionDeadline(ctx, tx, eventID, current, stage); err != nil {
		return model.Event{}, err
	}
	updated, err := eventrepo.SetEventStage(ctx, tx, eventID, stage)
	if err != nil {
		return model.Event{}, err
	}
	if err := teamrepo.UnpinReopened(ctx, tx, eventID); err != nil {
		return model.Event{}, err
	}
	// Settle every due entry in the same transaction as a move that freezes submissions, so judging never waits for the sweep.
	if hackevents.SubmissionsClosedStage(stage) {
		if err := s.entries.SettleEventTx(ctx, tx, eventID); err != nil {
			return model.Event{}, err
		}
	}
	// Judging opens now, from anything but results -- after settle, so a judge's first reminder/queue read already sees the settled entries.
	if stage == "judging" && current != "judging" && current != "results" {
		if err := hackmembers.NotifyJudgingOpen(ctx, tx, eventID); err != nil {
			return model.Event{}, err
		}
	}
	return syncJudgingLock(ctx, tx, eventID, current, stage, updated)
}

// moveSubmissionDeadline closes submissions on a move that freezes them.
func moveSubmissionDeadline(ctx context.Context, tx *sql.Tx, eventID, current, stage string) error {
	forming := func(st string) bool { return st == "open" || st == "building" }
	switch {
	case hackevents.SubmissionsClosedStage(stage) && !hackevents.SubmissionsClosedStage(current):
		_, err := eventrepo.CloseSubmissionDeadline(ctx, tx, eventID)
		return err
	case forming(stage) && !forming(current):
		if _, err := eventrepo.ClearPassedSubmissionDeadline(ctx, tx, eventID); err != nil {
			return err
		}
		if err := teamrepo.ClearPassedTeamDeadlines(ctx, tx, eventID); err != nil {
			return err
		}
		return entryrepo.UnsettleEventDrafts(ctx, tx, eventID)
	}
	return nil
}

// syncJudgingLock makes the judging lock follow the stage: moving to Results locks it, moving back to Judging or earlier clears it.
func syncJudgingLock(ctx context.Context, tx *sql.Tx, eventID, current, stage string, updated model.Event) (model.Event, error) {
	if stage == "results" && current != "results" {
		return eventrepo.SetJudgingLock(ctx, tx, eventID, true, "")
	}
	if stage != "results" && updated.JudgingLockedAt.Valid {
		return eventrepo.SetJudgingLock(ctx, tx, eventID, false, "stage moved back")
	}
	return updated, nil
}

func (s *Service) DeleteEvent(ctx context.Context, event model.Event, userID string, confirm *string, trash func(context.Context, string, string) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Hold the event against joins and recheck current organiser membership.
	var stage string
	var takenDown bool
	err = eventrepo.LockStageTakenDown(ctx, tx, event.ID).Scan(&stage, &takenDown)
	var m model.EventMember
	if err == nil {
		m, err = hackmembers.Member(ctx, tx, event.ID, userID, hackmembers.ForShare)
	}
	if errors.Is(err, sql.ErrNoRows) || (err == nil && !m.Organiser()) {
		return &Error{Code: "event_not_found", Message: "event not found"}
	}
	if err != nil {
		return err
	}
	current, err := eventrepo.GetEventByID(ctx, tx, event.ID)
	if err != nil {
		return err
	}
	if takenDown {
		return &Error{Code: "event_taken_down", Message: "this event has been taken down"}
	}
	participants, _, judges, err := countPeople(ctx, tx, current)
	if err != nil {
		return err
	}
	if (confirm != nil && *confirm != event.Slug) || (confirm == nil && (stage == "archived" || participants+judges > 0)) {
		return &Error{Code: "delete_only_empty", Message: "to permanently delete this event, send {\"confirm\":\"" + event.Slug + "\"} with the matching event address name"}
	}
	if err := s.DeleteEventTx(ctx, tx, event, trash); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return nil
}

// ArchiveEventTx locks teams and closes the deadline unless it already passed or submissions were closed, then archives, unpins and settles entries.
func (s *Service) ArchiveEventTx(ctx context.Context, tx *sql.Tx, eventID, current string) (model.Event, error) {
	if err := teamrepo.LockEventTeams(ctx, tx, eventID); err != nil {
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
	// An event ending always freezes every remaining draft.
	if err := s.entries.SettleEventTx(ctx, tx, eventID); err != nil {
		return model.Event{}, err
	}
	if current != "archived" {
		if err := hackmembers.DismissTeamMessages(ctx, tx, eventID); err != nil {
			return model.Event{}, err
		}
	}
	return updated, nil
}

// DeleteEventTx trashes team sites, then deletes the event and its holding account, including recently deleted sites; there is no event undo.
func (s *Service) DeleteEventTx(ctx context.Context, tx *sql.Tx, ev model.Event, trash func(context.Context, string, string) error) error {
	if down, err := sites.AccountSiteTakenDown(ctx, tx, ev.AccountID); err != nil || down {
		if err == nil {
			err = &Error{Code: "site_taken_down", Message: "a team site is taken down; it stays until the take-down is lifted"}
		}
		return err
	}
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
	return deleteEventAndAccount(ctx, tx, ev)
}

func (s *Service) PatchEvent(ctx context.Context, eventID string, p EventPatch) (updated model.Event, deadlineChanged bool, err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return model.Event{}, false, err
	}
	defer tx.Rollback()
	// Re-read under the lock: the event loaded before the transaction may be stale.
	ev, err := eventrepo.GetEventForUpdate(ctx, tx, eventID)
	if err != nil {
		return model.Event{}, false, err
	}
	deadlineChanged, entryRequiredChanged, err := p.apply(&ev)
	if err == nil && deadlineChanged {
		err = checkDeadlineChange(ctx, tx, ev)
	}
	if err == nil {
		updated, err = eventrepo.UpdateEventPatch(ctx, tx, ev)
	}
	if err == nil {
		updated, err = eventrepo.UpdateEventEntrySettings(ctx, tx, ev.ID, ev.SubmissionDeadline, ev.EntryRequired, ev.GalleryOpen)
	}
	if err == nil && entryRequiredChanged {
		updated, err = applyEntryRequiredAlias(ctx, tx, ev, updated)
	}
	if err == nil && deadlineChanged {
		err = reopenForDeadline(ctx, tx, ev.ID)
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		return model.Event{}, false, err
	}
	return updated, deadlineChanged, nil
}

// checkDeadlineChange refuses a new deadline on an ended event, or while submissions are closed and ev's deadline is unset or still ahead.
func checkDeadlineChange(ctx context.Context, tx *sql.Tx, ev model.Event) error {
	if ev.Stage == "archived" {
		return &Error{Code: "event_closed", Message: "an ended event cannot be changed"}
	}
	var dbNow time.Time
	if err := eventrepo.DatabaseNow(ctx, tx).Scan(&dbNow); err != nil {
		return err
	}
	if hackevents.SubmissionsClosedStage(ev.Stage) && (!ev.SubmissionDeadline.Valid || ev.SubmissionDeadline.Time.After(dbNow)) {
		return &Error{Code: "submissions_closed_stage", Message: "submissions are closed: move the event back to Building to set a new deadline, or give single teams more time"}
	}
	return nil
}

// applyEntryRequiredAlias handles the deprecated entry_required alias.
func applyEntryRequiredAlias(ctx context.Context, tx *sql.Tx, ev, updated model.Event) (model.Event, error) {
	fields := hackentries.LoadRules(updated.EntryRules)
	for _, f := range hackevents.EntryFields {
		if slices.Contains(ev.EntryRequired, f) {
			fields[f] = "required"
		}
	}
	return eventrepo.UpdateEventEntryRules(ctx, tx, ev.ID, "custom", hackentries.MarshalRules(fields), updated.JudgeFirst, ev.EntryRequired)
}

// reopenForDeadline lets a moved deadline take effect.
func reopenForDeadline(ctx context.Context, tx *sql.Tx, eventID string) error {
	if err := teamrepo.LockEventTeams(ctx, tx, eventID); err != nil {
		return err
	}
	if err := teamrepo.UnpinReopened(ctx, tx, eventID); err != nil {
		return err
	}
	return entryrepo.UnsettleEventDrafts(ctx, tx, eventID)
}

// savepoint rolls back a failed attempt so a unique violation does not abort the transaction and prevent a retry.
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
