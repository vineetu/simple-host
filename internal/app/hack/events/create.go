package events

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"github.com/vsriram/simple-host/internal/db"
	"github.com/vsriram/simple-host/internal/rules/hackentries"
	"github.com/vsriram/simple-host/internal/rules/hackevents"
)

func unique(err error) bool {
	return err != nil && (strings.Contains(err.Error(), "23505") || strings.Contains(strings.ToLower(err.Error()), "unique"))
}

// CreateEvent creates an event and its organiser in one transaction.
func (s *Service) CreateEvent(ctx context.Context, userID, slug, title, organiserName, organisation, contact, purpose, tz string, expected int, startsAt, endsAt sql.NullTime, perDay, maxActive, teamSize, joinLen, judgeLen int) (db.Event, db.EventMember, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return db.Event{}, db.EventMember{}, err
	}
	defer tx.Rollback()

	if err := db.LockUserRow(ctx, tx, userID); err != nil {
		return db.Event{}, db.EventMember{}, err
	}
	since := time.Now().Add(-24 * time.Hour)
	n, err := db.CountEventCreatesSince(ctx, tx, userID, since)
	if err != nil {
		return db.Event{}, db.EventMember{}, err
	}
	if n >= perDay {
		return db.Event{}, db.EventMember{}, &Error{429, "too_many_events_today", "you have created too many events today"}
	}
	active, err := db.CountActiveEventsByOrganiser(ctx, tx, userID)
	if err != nil {
		return db.Event{}, db.EventMember{}, err
	}
	if active >= maxActive {
		return db.Event{}, db.EventMember{}, &Error{409, "too_many_active_events", "you already have as many active events as this instance allows"}
	}

	eventID, err := db.NewUUID(ctx, tx)
	if err != nil {
		return db.Event{}, db.EventMember{}, err
	}
	holding, err := db.CreateUser(ctx, tx, "event+"+eventID+"@events.invalid", "", false)
	if err != nil {
		return db.Event{}, db.EventMember{}, err
	}
	claimed, err := db.ClaimHandle(ctx, tx, holding.ID, slug)
	if err != nil {
		return db.Event{}, db.EventMember{}, err
	}
	if !claimed {
		return db.Event{}, db.EventMember{}, &Error{409, "name_taken", "that name is already in use"}
	}
	if err := db.MarkEventAccount(ctx, tx, holding.ID); err != nil {
		return db.Event{}, db.EventMember{}, err
	}

	var ev db.Event
	for attempt := 0; attempt < 8; attempt++ {
		joinCode, err := hackevents.RandomCode(joinLen)
		if err != nil {
			return db.Event{}, db.EventMember{}, err
		}
		judgeCode, err := hackevents.RandomCode(judgeLen)
		if err != nil {
			return db.Event{}, db.EventMember{}, err
		}
		err = savepoint(ctx, tx, func() error {
			var ierr error
			ev, ierr = db.InsertEvent(ctx, tx, db.Event{
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
				// New events start on the AI-build preset (section 2); a Go
				// table, not a DB default, so the preset's field list lives
				// in one place (hack_entry_rules.go).
				EntryPreset: "build",
				EntryRules:  hackentries.MarshalRules(hackentries.ExpandPreset("build")),
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
			return db.Event{}, db.EventMember{}, &Error{409, "name_taken", "that name is already in use"}
		}
		return db.Event{}, db.EventMember{}, err
	}
	// Starter rubric, only for this new row. Nothing else writes one onto an
	// event that does not already have it.
	if err := db.ReplaceRubric(ctx, tx, ev.ID, hackentries.DefaultEventRubric()); err != nil {
		return db.Event{}, db.EventMember{}, err
	}
	if _, err := db.InsertEventMember(ctx, tx, ev.ID, userID, "organiser", organiserName); err != nil {
		return db.Event{}, db.EventMember{}, err
	}
	if err := tx.Commit(); err != nil {
		return db.Event{}, db.EventMember{}, err
	}
	member, err := db.GetEventMember(ctx, s.db, ev.ID, userID)
	if err != nil {
		return db.Event{}, db.EventMember{}, err
	}
	return ev, member, nil
}
