package events

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"math"
	"strings"
	"time"

	"github.com/vsriram/simple-host/products/hack/model"
	entryrepo "github.com/vsriram/simple-host/products/hack/repository/hackentries"
	eventrepo "github.com/vsriram/simple-host/products/hack/repository/hackevents"
	"github.com/vsriram/simple-host/products/hack/repository/hackmembers"
	teamrepo "github.com/vsriram/simple-host/products/hack/repository/hackteams"
	"github.com/vsriram/simple-host/products/hack/rules/hackentries"
)

// QueueEntryReminders claims each due team once per reminder per deadline, then queues its members in the same transaction.
func (s *Service) QueueEntryReminders(ctx context.Context) (written int64, firstErr error) {
	withMembers, err := hackmembers.TeamIDsWithMembers(ctx, s.db, "")
	if err != nil {
		log.Printf("hack: entry reminders: %v", err)
		return 0, err
	}
	teams, err := teamrepo.TeamsByIDs(ctx, s.db, withMembers)
	if err != nil {
		log.Printf("hack: entry reminders: %v", err)
		return 0, err
	}
	candidates := make([]model.EventTeam, 0, len(teams))
	for _, t := range teams {
		if !t.SiteTakenDownAt.Valid {
			candidates = append(candidates, t)
		}
	}
	due, err := eventrepo.DueEntryReminders(ctx, s.db, candidates)
	if err == nil {
		due, err = entryrepo.UnsentEntryReminders(ctx, s.db, due)
	}
	if err != nil {
		log.Printf("hack: entry reminders: %v", err)
		return 0, err
	}
	for _, r := range due {
		n, err := s.queueEntryReminder(ctx, r)
		written += n
		if err != nil {
			log.Printf("hack: entry reminder %s: %v", r.TeamID, err)
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	return
}

// queueEntryReminder re-reads the team's effective deadline under lock before claiming.
func (s *Service) queueEntryReminder(ctx context.Context, r model.EntryReminder) (int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	if _, err := eventrepo.GetEventForShare(ctx, tx, r.EventID); errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	} else if err != nil {
		return 0, err
	}
	st, err := teamrepo.TeamWriteStateFor(ctx, tx, r.EventID, r.TeamID, true)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	if !st.Deadline.Valid || !st.Deadline.Time.Equal(r.Deadline) {
		return 0, nil
	}
	claimed, err := entryrepo.ClaimEntryReminder(ctx, tx, r)
	if err != nil || !claimed {
		return 0, err
	}
	ids, err := hackmembers.IDs(ctx, tx, hackmembers.Filter{EventID: r.EventID, TeamID: r.TeamID})
	if err != nil {
		return 0, err
	}
	n, err := hackmembers.NotifyUsersCount(ctx, tx, r.EventID, hackmembers.NoticeEntryReminder, r.TeamID, "", reminderDetail(r.Kind, r.Deadline), ids...)
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	// The durable reminder claim is also a write when there are no emailable members.
	return n + 1, nil
}

// EntryEvaluator is the events service's small interface over the entries service, wired from hackhttp so this package never imports app/hack/entries directly.
type EntryEvaluator interface {
	EvaluateEntryTx(context.Context, *sql.Tx, model.Event, model.TeamWriteState, string, model.EventEntry) (hackentries.Check, error)
	UnscoredTeams(ctx context.Context, ev model.Event, judgeID string) (int, error)
	QueueCounts(ctx context.Context, ev model.Event, judgeID string) (total, unscored int, err error)
}

func reminderDetail(label string, deadline time.Time) string {
	return label + "@" + deadline.UTC().Format(time.RFC3339)
}

// reminderOffsetStale reports whether a queued reminder's offset no longer matches the current deadline.
func reminderOffsetStale(offsets []int64, detail string, currentDeadline time.Time, remaining time.Duration) bool {
	label, deadlineStr, hasDeadline := strings.Cut(detail, "@")
	for _, o := range offsets {
		if ReminderLabel(int(o)) != label {
			continue
		}
		if !hasDeadline {
			return remaining > time.Duration(o)*time.Minute+30*time.Minute
		}
		stored, err := time.Parse(time.RFC3339, deadlineStr)
		if err != nil {
			return true
		}
		return !stored.Truncate(time.Second).Equal(currentDeadline.Truncate(time.Second))
	}
	return true
}

// entryReminderLine re-reads the team and its entry and writes the reminder's line.
func (s *Service) entryReminderLine(ctx context.Context, tx *sql.Tx, n model.MailNotice, evaluator EntryEvaluator) (string, bool, error) {
	st, err := teamrepo.TeamWriteStateFor(ctx, tx, n.EventID, n.TeamID, false)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	if st.WriteRefusal() != "" || !st.Deadline.Valid || !st.Now.Before(st.Deadline.Time) {
		return "", false, nil
	}
	ev, err := eventrepo.GetEventByID(ctx, tx, st.EventID)
	if err != nil {
		return "", false, err
	}
	if reminderOffsetStale(ev.ReminderOffsets, n.Detail, st.Deadline.Time, st.Deadline.Time.Sub(st.Now)) {
		return "", false, nil
	}
	closes := "Entries close for your team " + hackNoticeTime(st.Deadline.Time, n.EventTimeZone) +
		" (" + ReminderIn(st.Deadline.Time.Sub(st.Now)) + ")."
	e, err := entryrepo.GetEventEntry(ctx, tx, n.TeamID)
	if errors.Is(err, sql.ErrNoRows) {
		return "Your team hasn't started its entry. " + closes + " A team with no entry isn't judged.", true, nil
	}
	if err != nil {
		return "", false, err
	}
	if e.SubmittedAt.Valid {
		return "", false, nil
	}
	check, err := evaluator.EvaluateEntryTx(ctx, tx, ev, st, n.TeamID, e)
	if err != nil {
		return "", false, err
	}
	if check.Complete {
		return "Your team's entry is complete but not submitted yet. " + closes +
			" Submit it now, or it is submitted for you at the deadline.", true, nil
	}
	return "Your team's entry isn't submitted yet. " + closes + " Still missing: " +
		oneLine(hackentries.MissingMessage(check.Missing, hackentries.LoadRules(ev.EntryRules))) +
		". If it isn't complete by then, it is still judged, marked incomplete.", true, nil
}

// ReminderIn is the plain "in about 3 hours" for the time left.
func ReminderIn(d time.Duration) string {
	switch {
	case d < 45*time.Minute:
		return "in less than an hour"
	case d < 90*time.Minute:
		return "in about an hour"
	}
	return fmt.Sprintf("in about %d hours", int(math.Round(d.Hours())))
}
