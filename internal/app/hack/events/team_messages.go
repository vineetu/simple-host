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

// SendTeamMessage sends one message to each of teamIDs' current members, in
// one transaction: the durable history row, then the notice to every
// current member (team-scoped, so it stops reaching anyone who has since
// left). teamIDs must all belong to ev and be distinct; the caller validates
// shape and count (1..25) and text length before calling this.
//
// The handler's own checks (organiser membership, archived) ran before the
// request body was read, which can take arbitrarily long (a slow client, a
// large body up to the cap); by the time this runs, the sender may have
// been removed as organiser or the event may have been archived or taken
// down meanwhile. Inside the transaction, before anything else, this
// re-reads both under the event row's FOR SHARE lock and refuses with the
// same errors the handler would have (event_not_found for a sender who is
// no longer an organiser -- the handler's own non-member/wrong-role
// treatment -- event_closed for archived, event_taken_down for taken
// down), rather than trusting the pre-body-read snapshot in ev.
//
// The same lock also serialises concurrent sends for this event: without
// it, two sends arriving together could each count the rows already
// there, each see themselves as under the 50/hour limit, and both insert,
// letting the total cross it. pg_advisory_xact_lock (same mechanism
// NextMailBatch uses, hashtextextended) makes the count-then-insert below
// atomic across concurrent senders for this event, held until the
// transaction ends.
func (s *Service) SendTeamMessage(ctx context.Context, ev model.Event, teamIDs []string, text, senderID, senderName string) ([]TeamMessageSent, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('hack-team-message:' || $1, 0))`, ev.ID); err != nil {
		return nil, err
	}
	fresh, err := eventrepo.GetEventForShare(ctx, tx, ev.ID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, &Error{"event_not_found", "event not found"}
	}
	if err != nil {
		return nil, err
	}
	if fresh.Stage == "archived" {
		return nil, &Error{"event_closed", "an ended event cannot be changed"}
	}
	if fresh.TakenDown() {
		return nil, &Error{"event_taken_down", "this event has been taken down"}
	}
	sender, err := eventrepo.GetEventMember(ctx, tx, ev.ID, senderID)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && sender.Role != "organiser") {
		return nil, &Error{"event_not_found", "event not found"}
	}
	if err != nil {
		return nil, err
	}
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
		msg, err := eventrepo.InsertTeamMessage(ctx, tx, ev.ID, teamID, senderID, senderName, text, false)
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

// SendToOrganisers is a team member's message to the event's organisers
// (SIM-443): one history row in the team's thread (from_team), then a
// team_inbound notice to every organiser of the event. Like SendTeamMessage,
// membership, event state and the rate limit are checked inside the
// transaction, under the event row's lock and a per-team advisory lock, so a
// member who has just left the team, an archived or taken-down event and two
// concurrent sends are all handled at the moment of writing. Email goes only
// to organisers whose "Email me when something waits on me" is on (the
// notice queue's own rule, noticeEmails/emailableSQL); everyone else sees the
// message in the app.
func (s *Service) SendToOrganisers(ctx context.Context, ev model.Event, senderID, senderName, text string) (TeamMessageSent, error) {
	var zero TeamMessageSent
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return zero, err
	}
	defer tx.Rollback()
	fresh, err := eventrepo.GetEventForShare(ctx, tx, ev.ID)
	if errors.Is(err, sql.ErrNoRows) {
		return zero, &Error{"event_not_found", "event not found"}
	}
	if err != nil {
		return zero, err
	}
	if fresh.Stage == "archived" {
		return zero, &Error{"event_closed", "an ended event cannot be changed"}
	}
	if fresh.TakenDown() {
		return zero, &Error{"event_taken_down", "this event has been taken down"}
	}
	status, teamID, _, err := eventrepo.NoticeMemberStatus(ctx, tx, ev.ID, senderID)
	if errors.Is(err, sql.ErrNoRows) {
		return zero, &Error{"event_not_found", "event not found"}
	}
	if err != nil {
		return zero, err
	}
	role, _, _, _, _, err := eventrepo.MailMember(ctx, tx, ev.ID, senderID)
	if err != nil || role != "participant" {
		return zero, &Error{"event_not_found", "event not found"}
	}
	if status != "approved" {
		return zero, &Error{"approval_" + status, "your application is " + status}
	}
	if !teamID.Valid {
		return zero, &Error{"no_team", "join or start a team first"}
	}
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('hack-team-inbound:' || $1, 0))`, teamID.String); err != nil {
		return zero, err
	}
	// Re-read the team after waiting on the lock: someone who left or moved
	// meanwhile must not write into their former team's thread. (No member
	// row lock: leave and move lock the team before other members' rows.)
	if _, now, _, err := eventrepo.NoticeMemberStatus(ctx, tx, ev.ID, senderID); err != nil || now != teamID {
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return zero, err
		}
		return zero, &Error{"no_team", "join or start a team first"}
	}
	count, oldest, err := eventrepo.CountRecentTeamInbound(ctx, tx, teamID.String, time.Now().Add(-teamInboundRateWindow))
	if err != nil {
		return zero, err
	}
	if count >= teamInboundRateMax {
		retry := teamInboundRateWindow
		if oldest.Valid {
			retry = teamInboundRateWindow - time.Since(oldest.Time)
		}
		if retry < time.Second {
			retry = time.Second
		}
		return zero, &RateLimitError{RetryAfter: int(retry.Seconds()) + 1}
	}
	team, err := teamrepo.GetEventTeamByID(ctx, tx, teamID.String)
	if err != nil {
		return zero, err
	}
	msg, err := eventrepo.InsertTeamMessage(ctx, tx, ev.ID, teamID.String, senderID, senderName, text, true)
	if err != nil {
		return zero, err
	}
	ids, err := eventrepo.OrganiserIDs(ctx, tx, ev.ID)
	if err != nil {
		return zero, err
	}
	n, err := eventrepo.NotifyUsersCount(ctx, tx, ev.ID, eventrepo.NoticeTeamInbound, teamID.String, msg.ID, "", ids...)
	if err != nil {
		return zero, err
	}
	if err := tx.Commit(); err != nil {
		return zero, err
	}
	return TeamMessageSent{TeamMessage: msg, TeamName: team.Name, Recipients: int(n)}, nil
}

// OrganiserOpenedThread marks the organiser's unread notices about the
// team's messages they were just shown seen; one that arrived after the read
// keeps its Home line.
func (s *Service) OrganiserOpenedThread(ctx context.Context, eventID, userID, teamID string, read []model.TeamMessage) error {
	if len(read) == 0 {
		return nil
	}
	// read is newest first: everything up to its newest row was shown.
	return eventrepo.MarkTeamInboundSeen(ctx, s.db, eventID, userID, teamID, read[0].CreatedAt)
}
