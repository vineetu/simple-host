package events

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"

	"github.com/vsriram/simple-host/products/hack/model"
	eventrepo "github.com/vsriram/simple-host/products/hack/repository/hackevents"
)

// Reminder offset settings (0003-hack-notices-judges-messages.sql).

const (
	// ReminderOffsetMin and ReminderOffsetMax are the allowed minutes before a deadline: 15 minutes to 14 days.
	ReminderOffsetMin   = 15
	ReminderOffsetMax   = 14 * 24 * 60
	ReminderOffsetCount = 5
)

// DefaultReminderOffsets is the factory default both audiences start with (24h, 1h), the same pair the fixed entry reminder used before this.
var DefaultReminderOffsets = []int{1440, 60}

// ReminderLabel is the kind stored for an offset of m minutes: m%60==0 ? (m/60)+"h" : m+"m" (1440->"24h", 60->"1h", 4320->"72h", 90->"90m").
func ReminderLabel(m int) string {
	if m%60 == 0 {
		return fmt.Sprintf("%dh", m/60)
	}
	return fmt.Sprintf("%dm", m)
}

// ValidateReminderOffsets checks and normalises offsets.
func ValidateReminderOffsets(in []int) ([]int, error) {
	if len(in) > ReminderOffsetCount {
		return nil, &Error{Code: "invalid_offsets", Message: fmt.Sprintf("send at most %d reminder offsets", ReminderOffsetCount)}
	}
	out := append([]int{}, in...)
	seen := map[int]bool{}
	for _, m := range out {
		if m < ReminderOffsetMin || m > ReminderOffsetMax {
			return nil, &Error{Code: "invalid_offsets", Message: fmt.Sprintf("each reminder offset must be between %d and %d minutes", ReminderOffsetMin, ReminderOffsetMax)}
		}
		if seen[m] {
			return nil, &Error{Code: "invalid_offsets", Message: "reminder offsets must be distinct"}
		}
		seen[m] = true
	}
	sort.Sort(sort.Reverse(sort.IntSlice(out)))
	return out, nil
}

// SetReminderOffsets validates and saves the event's reminder offsets.
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
		return model.Event{}, &Error{Code: "event_not_found", Message: "event not found"}
	}
	if err != nil {
		return model.Event{}, err
	}
	if ev.Stage == "archived" {
		return model.Event{}, &Error{Code: "event_closed", Message: "an ended event cannot be changed"}
	}
	if ev.TakenDown() {
		return model.Event{}, &Error{Code: "event_taken_down", Message: "this event has been taken down"}
	}
	actor, err := eventrepo.GetEventMember(ctx, tx, eventID, actorID)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && actor.Role != "organiser") {
		return model.Event{}, &Error{Code: "event_not_found", Message: "event not found"}
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
