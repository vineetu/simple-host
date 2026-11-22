package messages

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/vsriram/simple-host/products/hack/app/events"

	"github.com/vsriram/simple-host/products/hack/model"
	eventrepo "github.com/vsriram/simple-host/products/hack/repository/hackevents"
	"github.com/vsriram/simple-host/products/hack/repository/hackmembers"
	teamrepo "github.com/vsriram/simple-host/products/hack/repository/hackteams"
)

// teamMessageRateWindow and teamMessageRateMax are the contract's DB-counted rate limit: at most this many message rows per event per rolling window.
const (
	teamMessageRateWindow = time.Hour
	teamMessageRateMax    = 50
	// A team writing to the organisers: at most this many per team per window.
	teamInboundRateWindow = time.Hour
	teamInboundRateMax    = 10
)

type TeamMessageSent struct {
	model.TeamMessage
	TeamName   string
	Recipients int
}

// sendTeamMessage sends one message to each of teamIDs' current members, in one transaction.
func (h *handler) sendTeamMessage(ctx context.Context, ev model.Event, teamIDs []string, text, senderID string) ([]TeamMessageSent, error) {
	tx, err := h.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('hack-team-message:' || $1, 0))`, ev.ID); err != nil {
		return nil, err
	}
	if err := lockOpenEvent(ctx, tx, ev.ID); err != nil {
		return nil, err
	}
	sender, err := hackmembers.Member(ctx, tx, ev.ID, senderID, hackmembers.NoLock)
	sender, err = hackmembers.WithDisplayName(ctx, tx, sender, err)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && sender.Role != "organiser") {
		return nil, errEventNotFound
	}
	if err != nil {
		return nil, err
	}
	count, oldest, err := teamrepo.CountRecentTeamMessages(ctx, tx, ev.ID, time.Now().Add(-teamMessageRateWindow))
	if err == nil {
		err = rateLimit(count+len(teamIDs), teamMessageRateMax, oldest, teamMessageRateWindow)
	}
	if err != nil {
		return nil, err
	}
	out := make([]TeamMessageSent, 0, len(teamIDs))
	for _, teamID := range teamIDs {
		sent, err := messageTeam(ctx, tx, ev.ID, teamID, senderID, sender.DisplayName, text)
		if err != nil {
			return nil, err
		}
		out = append(out, sent)
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return out, nil
}

var errEventNotFound = &events.Error{Code: "event_not_found", Message: "event not found"}

// lockOpenEvent holds the event row FOR SHARE and refuses an event that is gone, ended or taken down as it stands under that lock.
func lockOpenEvent(ctx context.Context, tx *sql.Tx, eventID string) error {
	fresh, err := eventrepo.GetEventForShare(ctx, tx, eventID)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return errEventNotFound
	case err != nil:
		return err
	case fresh.Stage == "archived":
		return &events.Error{Code: "event_closed", Message: "an ended event cannot be changed"}
	case fresh.TakenDown():
		return &events.Error{Code: "event_taken_down", Message: "this event has been taken down"}
	}
	return nil
}

// rateLimit refuses when used messages would exceed limit in the rolling window, asking the sender to wait until the oldest counted one ages out.
func rateLimit(used, limit int, oldest sql.NullTime, window time.Duration) error {
	if used <= limit {
		return nil
	}
	retry := window
	if oldest.Valid {
		retry = window - time.Since(oldest.Time)
	}
	retry = max(retry, time.Second)
	return &events.RateLimitError{RetryAfter: int(retry.Seconds()) + 1}
}

// messageTeam stores one organiser message in the team's history and notifies the team's current members.
func messageTeam(ctx context.Context, tx *sql.Tx, eventID, teamID, senderID, senderName, text string) (TeamMessageSent, error) {
	team, err := teamrepo.GetEventTeamByID(ctx, tx, teamID)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && team.EventID != eventID) {
		return TeamMessageSent{}, &events.Error{Code: "invalid_team", Message: "team_ids must be teams of this event"}
	}
	if err != nil {
		return TeamMessageSent{}, err
	}
	msg, err := teamrepo.InsertTeamMessage(ctx, tx, eventID, teamID, senderID, text, false)
	if err != nil {
		return TeamMessageSent{}, err
	}
	msg.SenderName = senderName
	ids, err := hackmembers.IDs(ctx, tx, hackmembers.Filter{EventID: eventID, TeamID: teamID})
	if err != nil {
		return TeamMessageSent{}, err
	}
	n, err := hackmembers.NotifyUsersCount(ctx, tx, eventID, hackmembers.NoticeTeamMessage, teamID, msg.ID, "", ids...)
	if err != nil {
		return TeamMessageSent{}, err
	}
	return TeamMessageSent{TeamMessage: msg, TeamName: team.Name, Recipients: int(n)}, nil
}

func (h *handler) listTeamMessages(ctx context.Context, teamID string) ([]model.TeamMessage, error) {
	return teamrepo.ListTeamMessages(ctx, h.DB, teamID, 100)
}

// sendToOrganisers is a team member's message to the event's organisers.
func (h *handler) sendToOrganisers(ctx context.Context, ev model.Event, senderID, text string) (TeamMessageSent, error) {
	tx, err := h.DB.BeginTx(ctx, nil)
	if err != nil {
		return TeamMessageSent{}, err
	}
	defer tx.Rollback()
	if err := lockOpenEvent(ctx, tx, ev.ID); err != nil {
		return TeamMessageSent{}, err
	}
	teamID, senderName, err := senderTeam(ctx, tx, ev.ID, senderID)
	if err != nil {
		return TeamMessageSent{}, err
	}
	count, oldest, err := teamrepo.CountRecentTeamInbound(ctx, tx, teamID, time.Now().Add(-teamInboundRateWindow))
	if err == nil {
		err = rateLimit(count+1, teamInboundRateMax, oldest, teamInboundRateWindow)
	}
	if err != nil {
		return TeamMessageSent{}, err
	}
	sent, err := messageOrganisers(ctx, tx, ev.ID, teamID, senderID, senderName, text)
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		return TeamMessageSent{}, err
	}
	return sent, nil
}

// senderTeam returns the team and display name of an approved participant and takes the team's inbound lock.
func senderTeam(ctx context.Context, tx *sql.Tx, eventID, senderID string) (string, string, error) {
	m, err := hackmembers.Member(ctx, tx, eventID, senderID, hackmembers.NoLock)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", errEventNotFound
	}
	if err != nil {
		return "", "", err
	}
	ev, err := eventrepo.GetEventByID(ctx, tx, eventID)
	teamID, status := m.TeamID, m.ApprovalStatus
	switch {
	case err != nil || ev.TakenDown() || !m.Participant():
		return "", "", errEventNotFound
	case !m.Approved():
		return "", "", &events.Error{Code: "approval_" + status, Message: "your application is " + status}
	case !teamID.Valid:
		return "", "", errNoTeam
	}
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('hack-team-inbound:' || $1, 0))`, teamID.String); err != nil {
		return "", "", err
	}
	// Still on that team now that the lock is held.
	now, err := hackmembers.Member(ctx, tx, eventID, senderID, hackmembers.NoLock)
	now, err = hackmembers.WithDisplayName(ctx, tx, now, err)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", "", err
	}
	if err != nil || now.TeamID != teamID {
		return "", "", errNoTeam
	}
	return teamID.String, now.DisplayName, nil
}

var errNoTeam = &events.Error{Code: "no_team", Message: "join or start a team first"}

// messageOrganisers stores a team's message in its thread and notifies every organiser.
func messageOrganisers(ctx context.Context, tx *sql.Tx, eventID, teamID, senderID, senderName, text string) (TeamMessageSent, error) {
	team, err := teamrepo.GetEventTeamByID(ctx, tx, teamID)
	if err != nil {
		return TeamMessageSent{}, err
	}
	msg, err := teamrepo.InsertTeamMessage(ctx, tx, eventID, teamID, senderID, text, true)
	if err != nil {
		return TeamMessageSent{}, err
	}
	msg.SenderName = senderName
	ids, err := hackmembers.OrganiserIDs(ctx, tx, eventID)
	if err != nil {
		return TeamMessageSent{}, err
	}
	n, err := hackmembers.NotifyUsersCount(ctx, tx, eventID, hackmembers.NoticeTeamInbound, teamID, msg.ID, "", ids...)
	if err != nil {
		return TeamMessageSent{}, err
	}
	return TeamMessageSent{TeamMessage: msg, TeamName: team.Name, Recipients: int(n)}, nil
}

// organiserOpenedThread marks the organiser's unread notices about the team's messages they were just shown seen.
func (h *handler) organiserOpenedThread(ctx context.Context, eventID, userID, teamID string, read []model.TeamMessage) error {
	if len(read) == 0 {
		return nil
	}
	// read is newest first: everything up to its newest row was shown.
	ids, err := teamrepo.TeamMessagesUpTo(ctx, h.DB, teamID, read[0].CreatedAt)
	if err != nil {
		return err
	}
	return hackmembers.MarkInboundSeen(ctx, h.DB, eventID, userID, teamID, ids)
}
