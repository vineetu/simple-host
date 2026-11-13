package events

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"

	"github.com/vsriram/simple-host/internal/model"
	eventrepo "github.com/vsriram/simple-host/products/hack/repository/hackevents"
)

// Reminder offset settings (0003-hack-notices-judges-messages.sql). One list
// per event drives both audiences: entry reminders to participants (before
// their deadline) and judging reminders to judges (before judging_ends_at).

const (
	// ReminderOffsetMin and ReminderOffsetMax are the allowed minutes
	// before a deadline: 15 minutes to 14 days.
	ReminderOffsetMin   = 15
	ReminderOffsetMax   = 14 * 24 * 60
	ReminderOffsetCount = 5
)

// DefaultReminderOffsets is the factory default both audiences start with
// (24h, 1h), the same pair the fixed entry reminder used before this.
var DefaultReminderOffsets = []int{1440, 60}

// ReminderLabel is the kind stored for an offset of m minutes: m%60==0 ?
// (m/60)+"h" : m+"m" (1440->"24h", 60->"1h", 4320->"72h", 90->"90m"). Mirrors
// the SQL CASE expression DueEntryReminders/DueJudgeReminders use, so a
// label computed in Go for a test or a display always agrees with the one a
// sweep claims.
func ReminderLabel(m int) string {
	if m%60 == 0 {
		return fmt.Sprintf("%dh", m/60)
	}
	return fmt.Sprintf("%dm", m)
}

// ValidateReminderOffsets checks and normalises offsets: at most
// ReminderOffsetCount entries, each between ReminderOffsetMin and
// ReminderOffsetMax minutes, all distinct, returned sorted descending.
func ValidateReminderOffsets(in []int) ([]int, error) {
	if len(in) > ReminderOffsetCount {
		return nil, &Error{"invalid_offsets", fmt.Sprintf("send at most %d reminder offsets", ReminderOffsetCount)}
	}
	out := append([]int{}, in...)
	seen := map[int]bool{}
	for _, m := range out {
		if m < ReminderOffsetMin || m > ReminderOffsetMax {
			return nil, &Error{"invalid_offsets", fmt.Sprintf("each reminder offset must be between %d and %d minutes", ReminderOffsetMin, ReminderOffsetMax)}
		}
		if seen[m] {
			return nil, &Error{"invalid_offsets", "reminder offsets must be distinct"}
		}
		seen[m] = true
	}
	sort.Sort(sort.Reverse(sort.IntSlice(out)))
	return out, nil
}

// SetReminderOffsets validates and saves the event's reminder offsets.
//
// The handler's own checks (organiser membership, archived) ran before the
// request body was read, which can take arbitrarily long (a slow client, a
// large body); by the time this runs, actorID may have been removed as
// organiser, or the event may have been archived or taken down meanwhile.
// Inside the transaction, before writing anything, this re-reads the event
// under its own row lock (GetEventForUpdate, stronger than the FOR SHARE
// the team-message sender uses, since this writes the event row itself)
// and actorID's membership, and refuses with the same codes SendTeamMessage
// does: event_not_found for an actor who is no longer an organiser (the
// handler's own non-member/wrong-role treatment), event_closed for
// archived, event_taken_down for taken down -- rather than trusting the
// pre-body-read snapshot the handler checked against.
func (s *Service) SetReminderOffsets(ctx context.Context, eventID, actorID string, offsets []int) (model.Event, error) {
	valid, err := ValidateReminderOffsets(offsets)
	if err != nil {
		return model.Event{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return model.Event{}, err
	}
	defer tx.Rollback()
	ev, err := eventrepo.GetEventForUpdate(ctx, tx, eventID)
	if errors.Is(err, sql.ErrNoRows) {
		return model.Event{}, &Error{"event_not_found", "event not found"}
	}
	if err != nil {
		return model.Event{}, err
	}
	if ev.Stage == "archived" {
		return model.Event{}, &Error{"event_closed", "an ended event cannot be changed"}
	}
	if ev.TakenDown() {
		return model.Event{}, &Error{"event_taken_down", "this event has been taken down"}
	}
	actor, err := eventrepo.GetEventMember(ctx, tx, eventID, actorID)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && actor.Role != "organiser") {
		return model.Event{}, &Error{"event_not_found", "event not found"}
	}
	if err != nil {
		return model.Event{}, err
	}
	valid64 := make([]int64, len(valid))
	for i, m := range valid {
		valid64[i] = int64(m)
	}
	updated, err := eventrepo.SetEventReminderOffsets(ctx, tx, eventID, valid64)
	if err != nil {
		return model.Event{}, err
	}
	if err := tx.Commit(); err != nil {
		return model.Event{}, err
	}
	return updated, nil
}
