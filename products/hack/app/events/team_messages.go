package events

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/vsriram/simple-host/products/hack/model"
	eventrepo "github.com/vsriram/simple-host/products/hack/repository/hackevents"
	teamrepo "github.com/vsriram/simple-host/products/hack/repository/hackteams"
)

// Organiser team messages (0003-hack-notices-judges-messages.sql).

// teamMessageRateWindow and teamMessageRateMax are the contract's DB-counted rate limit: at most this many message rows per event per rolling window.
const (
	teamMessageRateWindow = time.Hour
	teamMessageRateMax    = 50
	// A team writing to the organisers: at most this many per team per window.
	teamInboundRateWindow = time.Hour
	teamInboundRateMax    = 10
)

// TeamMessageSent is one team's message, as the POST response shows it.
type TeamMessageSent struct {
	model.TeamMessage
	TeamName   string
	Recipients int
}

// SendTeamMessage sends one message to each of teamIDs' current members, in one transaction.
func (s *Service) SendTeamMessage(ctx context.Context, ev model.Event, teamIDs []string, text, senderID, senderName string) ([]TeamMessageSent, error) {
	tx, err := s.db.BeginTx(ctx, nil)
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
	sender, err := eventrepo.GetEventMember(ctx, tx, ev.ID, senderID)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && sender.Role != "organiser") {
		return nil, errEventNotFound
	}
	if err != nil {
		return nil, err
	}
	count, oldest, err := eventrepo.CountRecentTeamMessages(ctx, tx, ev.ID, time.Now().Add(-teamMessageRateWindow))
	if err == nil {
		err = rateLimit(count+len(teamIDs), teamMessageRateMax, oldest, teamMessageRateWindow)
	}
	if err != nil {
		return nil, err
	}
	out := make([]TeamMessageSent, 0, len(teamIDs))
	for _, teamID := range teamIDs {
		sent, err := messageTeam(ctx, tx, ev.ID, teamID, senderID, senderName, text)
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

var errEventNotFound = &Error{Code: "event_not_found", Message: "event not found"}

// lockOpenEvent holds the event row FOR SHARE and refuses an event that is gone, ended or taken down as it stands under that lock.
func lockOpenEvent(ctx context.Context, tx *sql.Tx, eventID string) error {
	fresh, err := eventrepo.GetEventForShare(ctx, tx, eventID)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return errEventNotFound
	case err != nil:
		return err
	case fresh.Stage == "archived":
		return &Error{Code: "event_closed", Message: "an ended event cannot be changed"}
	case fresh.TakenDown():
		return &Error{Code: "event_taken_down", Message: "this event has been taken down"}
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
	return &RateLimitError{RetryAfter: int(retry.Seconds()) + 1}
}

// messageTeam stores one organiser message in the team's history and notifies the team's current members.
func messageTeam(ctx context.Context, tx *sql.Tx, eventID, teamID, senderID, senderName, text string) (TeamMessageSent, error) {
	team, err := teamrepo.GetEventTeamByID(ctx, tx, teamID)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && team.EventID != eventID) {
		return TeamMessageSent{}, &Error{Code: "invalid_team", Message: "team_ids must be teams of this event"}
	}
	if err != nil {
		return TeamMessageSent{}, err
	}
	msg, err := eventrepo.InsertTeamMessage(ctx, tx, eventID, teamID, senderID, senderName, text, false)
	if err != nil {
		return TeamMessageSent{}, err
	}
	ids, err := eventrepo.TeamMemberIDs(ctx, tx, eventID, teamID, "")
	if err != nil {
		return TeamMessageSent{}, err
	}
	n, err := eventrepo.NotifyUsersCount(ctx, tx, eventID, eventrepo.NoticeTeamMessage, teamID, msg.ID, "", ids...)
	if err != nil {
		return TeamMessageSent{}, err
	}
	return TeamMessageSent{TeamMessage: msg, TeamName: team.Name, Recipients: int(n)}, nil
}

// ListTeamMessages is the organiser's GET .../teams/{team}/messages.
func (s *Service) ListTeamMessages(ctx context.Context, teamID string) ([]model.TeamMessage, error) {
	return eventrepo.ListTeamMessages(ctx, s.db, teamID, 100)
}

// SendToOrganisers is a team member's message to the event's organisers.
func (s *Service) SendToOrganisers(ctx context.Context, ev model.Event, senderID, senderName, text string) (TeamMessageSent, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return TeamMessageSent{}, err
	}
	defer tx.Rollback()
	if err := lockOpenEvent(ctx, tx, ev.ID); err != nil {
		return TeamMessageSent{}, err
	}
	teamID, err := senderTeam(ctx, tx, ev.ID, senderID)
	if err != nil {
		return TeamMessageSent{}, err
	}
	count, oldest, err := eventrepo.CountRecentTeamInbound(ctx, tx, teamID, time.Now().Add(-teamInboundRateWindow))
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

// senderTeam returns the team of an approved participant and takes the team's inbound lock.
func senderTeam(ctx context.Context, tx *sql.Tx, eventID, senderID string) (string, error) {
	status, teamID, _, err := eventrepo.NoticeMemberStatus(ctx, tx, eventID, senderID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", errEventNotFound
	}
	if err != nil {
		return "", err
	}
	role, _, _, _, _, err := eventrepo.MailMember(ctx, tx, eventID, senderID)
	switch {
	case err != nil || role != "participant":
		return "", errEventNotFound
	case status != "approved":
		return "", &Error{Code: "approval_" + status, Message: "your application is " + status}
	case !teamID.Valid:
		return "", errNoTeam
	}
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('hack-team-inbound:' || $1, 0))`, teamID.String); err != nil {
		return "", err
	}
	_, now, _, err := eventrepo.NoticeMemberStatus(ctx, tx, eventID, senderID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	if err != nil || now != teamID {
		return "", errNoTeam
	}
	return teamID.String, nil
}

var errNoTeam = &Error{Code: "no_team", Message: "join or start a team first"}

// messageOrganisers stores a team's message in its thread and notifies every organiser.
func messageOrganisers(ctx context.Context, tx *sql.Tx, eventID, teamID, senderID, senderName, text string) (TeamMessageSent, error) {
	team, err := teamrepo.GetEventTeamByID(ctx, tx, teamID)
	if err != nil {
		return TeamMessageSent{}, err
	}
	msg, err := eventrepo.InsertTeamMessage(ctx, tx, eventID, teamID, senderID, senderName, text, true)
	if err != nil {
		return TeamMessageSent{}, err
	}
	ids, err := eventrepo.OrganiserIDs(ctx, tx, eventID)
	if err != nil {
		return TeamMessageSent{}, err
	}
	n, err := eventrepo.NotifyUsersCount(ctx, tx, eventID, eventrepo.NoticeTeamInbound, teamID, msg.ID, "", ids...)
	if err != nil {
		return TeamMessageSent{}, err
	}
	return TeamMessageSent{TeamMessage: msg, TeamName: team.Name, Recipients: int(n)}, nil
}

// OrganiserOpenedThread marks the organiser's unread notices about the team's messages they were just shown seen.
func (s *Service) OrganiserOpenedThread(ctx context.Context, eventID, userID, teamID string, read []model.TeamMessage) error {
	if len(read) == 0 {
		return nil
	}
	// read is newest first: everything up to its newest row was shown.
	return eventrepo.MarkTeamInboundSeen(ctx, s.db, eventID, userID, teamID, read[0].CreatedAt)
}
