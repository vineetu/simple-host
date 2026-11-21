package hackteams

import (
	"context"
	"database/sql"
	"errors"

	"github.com/lib/pq"

	"github.com/vsriram/simple-host/host/sqlkit"
	"github.com/vsriram/simple-host/products/hack/repository/hackevents"
	"github.com/vsriram/simple-host/products/hack/repository/hackmembers"
)

// LeaveMembership is the shared core of a person losing their event
// membership, whether they chose it; the caller then drops what entries keep
// for them (hackentries.DropLeaver) in the same transaction.
func LeaveMembership(ctx context.Context, q sqlkit.Querier, eventID, userID string, selfLeave bool) (string, error) {
	m, err := lockMember(ctx, q, eventID, userID)
	if err != nil {
		return "", err
	}
	if m.TeamID.Valid {
		if _, err := LockTeam(ctx, q, m.TeamID.String); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return "", err
		}
	}
	if err := dropMembership(ctx, q, eventID, userID); err != nil || !m.TeamID.Valid {
		return "", err
	}
	team := m.TeamID.String
	if err := DeleteTeamIfEmpty(ctx, q, team); err != nil {
		return "", err
	}
	if selfLeave {
		if err := MarkTeamKeptIfEmpty(ctx, q, team); err != nil {
			return "", err
		}
	}
	if err := EnsureTeamAdmin(ctx, q, team); err != nil {
		return "", err
	}
	return team, nil
}

func RemoveCoOrganiser(ctx context.Context, q *sql.Tx, eventID, actorID, userID string) error {
	// Serialise removals so a legacy event without its creator cannot lose both remaining organisers through concurrent requests.
	ev, err := hackevents.GetEventForUpdate(ctx, q, eventID)
	if err != nil {
		return err
	}
	createdBy := ev.CreatedBy
	m, err := lockMember(ctx, q, eventID, userID)
	if err != nil {
		return err
	}
	if !m.Organiser() || (createdBy.Valid && createdBy.String == userID) {
		return sql.ErrNoRows
	}
	actor, err := hackmembers.Member(ctx, q, eventID, actorID, hackmembers.NoLock)
	if errors.Is(err, sql.ErrNoRows) || err == nil && !actor.Organiser() {
		return sql.ErrNoRows
	}
	if err != nil {
		return err
	}
	others, err := hackmembers.Count(ctx, q, hackmembers.Filter{EventID: eventID, Role: "organiser", Except: userID})
	if err != nil {
		return err
	}
	if others == 0 {
		return sql.ErrNoRows
	}
	// The caller drops their scores and receipts (hackentries.DropLeaver).
	_, err = LeaveMembership(ctx, q, eventID, userID, false)
	return err
}

// dropMembership drops everything still waiting on someone leaving (team
// requests, announcement emails, notices, checklist
// acknowledgements), takes them out of the organiser judges, then removes the
// membership; the event's name stays taken.
func dropMembership(ctx context.Context, q sqlkit.Querier, eventID, userID string) error {
	if _, err := q.ExecContext(ctx, `DELETE FROM event_team_requests WHERE event_id = $1 AND user_id = $2`, eventID, userID); err != nil {
		return err
	}
	if err := hackevents.DropQueuedForLeaver(ctx, q, eventID, userID); err != nil {
		return err
	}
	if err := hackmembers.DeleteNotices(ctx, q, eventID, userID); err != nil {
		return err
	}
	if err := hackevents.DeletePersonalChecklistAcknowledgements(ctx, q, eventID, userID); err != nil {
		return err
	}
	if err := hackevents.RemoveOrganiserJudge(ctx, q, eventID, userID); err != nil {
		return err
	}
	return hackmembers.Remove(ctx, q, eventID, userID)
}

// MarkTeamKeptIfEmpty marks a team nobody is on any more kept after leave
// (DeleteTeamIfEmpty just kept it instead of deleting it).
func MarkTeamKeptIfEmpty(ctx context.Context, q sqlkit.Querier, teamID string) error {
	n, err := CountTeamMembers(ctx, q, teamID)
	if err != nil || n > 0 {
		return err
	}
	return MarkTeamKeptAfterLeave(ctx, q, teamID)
}

// MarkTeamKeptAfterLeave keeps an emptied team for its submission and locks
// it for good (the caller checked nobody is on it).
func MarkTeamKeptAfterLeave(ctx context.Context, q sqlkit.Querier, teamID string) error {
	_, err := q.ExecContext(ctx, `
		UPDATE event_teams SET kept_after_leave = true, locked_at = COALESCE(locked_at, clock_timestamp())
		 WHERE id = $1`, teamID)
	return err
}

// TeamKeptIfEmptied reports whether, if leaving emptied this team of ev
// right now, it would be kept rather than deleted.
func TeamKeptIfEmptied(ctx context.Context, q sqlkit.Querier, ev hackevents.Event, teamID string) (bool, error) {
	var kept bool
	err := q.QueryRowContext(ctx, `
		SELECT NOT (`+hackevents.EmptyTeamDeletable("$2::timestamptz")+`)
		  FROM event_teams t WHERE t.id = $1`, teamID, ev.SubmissionDeadline).Scan(&kept)
	return kept, err
}

// DeleteSoleTeams deletes the teams a person whose account is being deleted
// is alone on, unless a team is kept for its submission (pinned or past its
// deadline): its code would otherwise stay live on an empty team.
func DeleteSoleTeams(ctx context.Context, q sqlkit.Querier, userID string, teamIDs []string) error {
	if len(teamIDs) == 0 {
		return nil
	}
	// Lock the teams, then keep those the person is still alone on: someone
	// may have joined since the caller read them.
	if _, err := q.ExecContext(ctx, `SELECT 1 FROM event_teams WHERE id = ANY($1::uuid[]) ORDER BY id FOR UPDATE`, pq.Array(teamIDs)); err != nil {
		return err
	}
	still, err := hackmembers.SoleMemberTeams(ctx, q, userID)
	if err != nil {
		return err
	}
	teamIDs = intersect(teamIDs, still)
	if len(teamIDs) == 0 {
		return nil
	}
	teams, err := TeamsByIDs(ctx, q, teamIDs)
	if err != nil {
		return err
	}
	var eventIDs []string
	for _, t := range teams {
		eventIDs = append(eventIDs, t.EventID)
	}
	byID, err := hackevents.EventsByIDs(ctx, q, eventIDs)
	if err != nil || len(byID) == 0 {
		return err
	}
	events := make([]Event, 0, len(byID))
	for _, e := range byID {
		events = append(events, e)
	}
	ids, deadlines, frozen := eventDeadlines(events)
	_, err = q.ExecContext(ctx, `
		DELETE FROM event_teams t USING unnest($1::uuid[], $2::text[], $3::bool[]) AS d(event_id, deadline, frozen)
		 WHERE d.event_id = t.event_id AND t.id = ANY($4::uuid[])
		   AND `+hackevents.EmptyTeamDeletable(eventDeadlineOfD), pq.Array(ids), pq.Array(deadlines), pq.Array(frozen), pq.Array(teamIDs))
	return err
}

// intersect is the ids of a that are also in b.
func intersect(a, b []string) []string {
	in := make(map[string]bool, len(b))
	for _, id := range b {
		in[id] = true
	}
	var out []string
	for _, id := range a {
		if in[id] {
			out = append(out, id)
		}
	}
	return out
}
