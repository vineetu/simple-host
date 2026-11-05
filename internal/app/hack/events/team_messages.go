package events

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/vsriram/simple-host/internal/model"
	eventrepo "github.com/vsriram/simple-host/internal/repository/hackevents"
	teamrepo "github.com/vsriram/simple-host/internal/repository/hackteams"
)

// Organiser team messages (0003-hack-notices-judges-messages.sql): a notice
// kind (team_message) like any other, with a durable history row
// (event_team_messages) since notices are pruned after 30 days and exist
// only per recipient.

// teamMessageRateWindow and teamMessageRateMax are the contract's DB-counted
// rate limit: at most this many message rows per event per rolling window.
// Counting the durable history rows is the "DB-counted" mechanism -- it
// survives a restart without a separate counter table.
const (
	teamMessageRateWindow = time.Hour
	teamMessageRateMax    = 50
)

// TeamMessageSent is one team's message, as the POST response shows it.
type TeamMessageSent struct {
	model.TeamMessage
	TeamName   string
	Recipients int
}

// SendTeamMessage sends one message to each of teamIDs' current members, in
// one transaction: the durable history row, then the notice to every
// current member (team-scoped, so it stops reaching anyone who has since
// left). teamIDs must all belong to ev and be distinct; the caller validates
// shape and count (1..25) and text length before calling this.
func (s *Service) SendTeamMessage(ctx context.Context, ev model.Event, teamIDs []string, text, senderID, senderName string) ([]TeamMessageSent, error) {
	if ev.Stage == "archived" {
		return nil, &Error{"event_closed", "an ended event cannot be changed"}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	count, oldest, err := eventrepo.CountRecentTeamMessages(ctx, tx, ev.ID, time.Now().Add(-teamMessageRateWindow))
	if err != nil {
		return nil, err
	}
	if count+len(teamIDs) > teamMessageRateMax {
		retry := teamMessageRateWindow
		if oldest.Valid {
			retry = teamMessageRateWindow - time.Since(oldest.Time)
		}
		if retry < time.Second {
			retry = time.Second
		}
		return nil, &RateLimitError{RetryAfter: int(retry.Seconds()) + 1}
	}
	out := make([]TeamMessageSent, 0, len(teamIDs))
	for _, teamID := range teamIDs {
		team, err := teamrepo.GetEventTeamByID(ctx, tx, teamID)
		if errors.Is(err, sql.ErrNoRows) || (err == nil && team.EventID != ev.ID) {
			return nil, &Error{"invalid_team", "team_ids must be teams of this event"}
		}
		if err != nil {
			return nil, err
		}
		msg, err := eventrepo.InsertTeamMessage(ctx, tx, ev.ID, teamID, senderID, senderName, text)
		if err != nil {
			return nil, err
		}
		ids, err := eventrepo.TeamMemberIDs(ctx, tx, ev.ID, teamID, "")
		if err != nil {
			return nil, err
		}
		n, err := eventrepo.NotifyUsersCount(ctx, tx, ev.ID, eventrepo.NoticeTeamMessage, teamID, msg.ID, "", ids...)
		if err != nil {
			return nil, err
		}
		out = append(out, TeamMessageSent{TeamMessage: msg, TeamName: team.Name, Recipients: int(n)})
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return out, nil
}

// ListTeamMessages is the organiser's GET .../teams/{team}/messages.
func (s *Service) ListTeamMessages(ctx context.Context, teamID string) ([]model.TeamMessage, error) {
	return eventrepo.ListTeamMessages(ctx, s.db, teamID, 100)
}
