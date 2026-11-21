package hackteams

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"time"

	"github.com/lib/pq"
	"github.com/vsriram/simple-host/host/sites"
	hackmodel "github.com/vsriram/simple-host/products/hack/model"
	"github.com/vsriram/simple-host/products/hack/repository/hackevents"
	"github.com/vsriram/simple-host/products/hack/repository/hackmembers"
)

// TeamWriteState is what decides whether a team may change its site or entry
// now. Read with TeamWriteStateFor.
type TeamWriteState = hackmodel.TeamWriteState

// TeamWriteStateFor reads a team of the event's write state. In a
// transaction with share set, the team row is held FOR SHARE until the
// transaction ends, so the deadline pin (PinDueTeams, FOR UPDATE) waits for a
// deploy that was let in before the deadline and then pins what it made live.
// The time compared is clock_timestamp(), not the transaction's start. The
// event is read first, in the same transaction.
func TeamWriteStateFor(ctx context.Context, q Querier, eventID, teamID string, share bool) (TeamWriteState, error) {
	lock := ""
	if share {
		lock = " FOR SHARE OF t"
	}
	ev, err := hackevents.GetEventByID(ctx, q, eventID)
	if err != nil {
		return TeamWriteState{}, err
	}
	return scanTeamWriteState(ev, q.QueryRowContext(ctx, `SELECT `+teamWriteStateCols+`
		  FROM event_teams t
		 WHERE t.event_id = $2::uuid AND t.id = $3::uuid`+lock, ev.SubmissionDeadline, ev.ID, teamID))
}

// TeamWriteStateBySlug is TeamWriteStateFor for the event's team whose site
// is slug, holding the team's row FOR SHARE.
func TeamWriteStateBySlug(ctx context.Context, tx *sql.Tx, eventID, slug string) (TeamWriteState, error) {
	ev, err := hackevents.GetEventByID(ctx, tx, eventID)
	if err != nil {
		return TeamWriteState{}, err
	}
	return scanTeamWriteState(ev, tx.QueryRowContext(ctx, `SELECT `+teamWriteStateCols+`
		  FROM event_teams t
		 WHERE t.event_id = $2::uuid AND t.slug = lower($3) FOR SHARE OF t`, ev.SubmissionDeadline, ev.ID, slug))
}

// teamWriteStateCols over event_teams t, with $1 the event's submission
// deadline. A site the platform took down is refused by the platform itself.
var teamWriteStateCols = `t.id, t.slug, t.site_taken_down_at IS NOT NULL,
	` + hackevents.EffectiveDeadline("$1::timestamptz") + `,
	clock_timestamp(), t.pinned_at, t.pinned_version,
	COALESCE(t.deadline_override > $1::timestamptz, false), t.deadline_override`

func scanTeamWriteState(ev Event, row interface{ Scan(...any) error }) (TeamWriteState, error) {
	s := TeamWriteState{EventID: ev.ID, EventSlug: ev.Slug, AccountID: ev.AccountID, Stage: ev.Stage,
		EventTakenDown: ev.TakenDownAt.Valid, EventDeadline: ev.SubmissionDeadline}
	err := row.Scan(
		&s.TeamID, &s.TeamSlug, &s.SiteTakenDown, &s.Deadline, &s.Now, &s.PinnedAt, &s.PinnedVersion,
		&s.HasOverride, &s.TeamDeadlineSet)
	return s, err
}

// TeamWriteStatesForEvent is TeamWriteStateFor for every team of an event, by
// team id, in one query (the organiser's and judges' lists).
func TeamWriteStatesForEvent(ctx context.Context, q Querier, eventID string) (map[string]TeamWriteState, error) {
	ev, err := hackevents.GetEventByID(ctx, q, eventID)
	if err != nil {
		return nil, err
	}
	rows, err := queryContext(ctx, q, `SELECT `+teamWriteStateCols+`
		  FROM event_teams t
		 WHERE t.event_id = $2::uuid`, ev.SubmissionDeadline, ev.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]TeamWriteState{}
	for rows.Next() {
		st, err := scanTeamWriteState(ev, rows)
		if err != nil {
			return nil, err
		}
		out[st.TeamID] = st
	}
	return out, rows.Err()
}

// TeamDeadlinePassed reports whether the team's own effective deadline has
// passed (whatever the stage).
func TeamDeadlinePassed(ctx context.Context, q Querier, eventID, teamID string) (bool, error) {
	st, err := TeamWriteStateFor(ctx, q, eventID, teamID, false)
	return st.Deadline.Valid && !st.Now.Before(st.Deadline.Time), err
}

// eventDeadlines is the events' ids, submission deadlines (RFC 3339, "" for
// none) and whether their stage freezes every team, as three arrays a team
// query joins with unnest($n::uuid[], $n+1::text[], $n+2::bool[]) AS
// d(event_id, deadline, frozen) (eventDeadlinesJoin); eventDeadlineOfD reads
// the deadline back.
func eventDeadlines(events []Event) (ids, deadlines []string, frozen []bool) {
	for _, e := range events {
		ids = append(ids, e.ID)
		d := ""
		if e.SubmissionDeadline.Valid {
			d = e.SubmissionDeadline.Time.UTC().Format(time.RFC3339Nano)
		}
		deadlines = append(deadlines, d)
		frozen = append(frozen, hackevents.FrozenStage(e.Stage))
	}
	return ids, deadlines, frozen
}

const eventDeadlinesJoin = ` JOIN unnest($1::uuid[], $2::text[], $3::bool[]) AS d(event_id, deadline, frozen) ON d.event_id = t.event_id`

const eventDeadlineOfD = `NULLIF(d.deadline, '')::timestamptz`

// sweepEvents is the one event of eventID, or every event when it is "".
func sweepEvents(ctx context.Context, q Querier, eventID string) ([]Event, error) {
	if eventID == "" {
		return hackevents.AllEvents(ctx, q)
	}
	ev, err := hackevents.GetEventByID(ctx, q, eventID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return []Event{ev}, err
}

// PinDueTeams pins every team whose deadline has passed and that is not
// pinned yet: pinned_version is its site's live version at that moment (NULL
// when it has no live site). eventID "" means every event. Each team is
// pinned in its own transaction holding the team row FOR UPDATE, so a deploy
// already let in (holding it FOR SHARE) finishes first.
func PinDueTeams(ctx context.Context, database *sql.DB, eventID string, progress ...func()) error {
	events, err := sweepEvents(ctx, database, eventID)
	if err != nil || len(events) == 0 {
		return err
	}
	ids, deadlines, frozen := eventDeadlines(events)
	rows, err := database.QueryContext(ctx, `
		SELECT t.id, t.event_id FROM event_teams t`+eventDeadlinesJoin+`
		 WHERE t.pinned_at IS NULL
		   AND (d.frozen OR `+hackevents.EffectiveDeadline(eventDeadlineOfD)+` <= clock_timestamp())`,
		pq.Array(ids), pq.Array(deadlines), pq.Array(frozen))
	if err != nil {
		return err
	}
	type due struct{ team, event string }
	var list []due
	for rows.Next() {
		var d due
		if err := rows.Scan(&d.team, &d.event); err != nil {
			rows.Close()
			return err
		}
		list = append(list, d)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	byID := make(map[string]Event, len(events))
	for _, e := range events {
		byID[e.ID] = e
	}
	for _, d := range list {
		if err := pinTeam(ctx, database, byID[d.event], d.team, progress...); err != nil {
			return err
		}
	}
	return nil
}

// pinTeam rechecks the team is due under the event (FOR SHARE) and team
// (FOR UPDATE) row locks and pins its site's live version.
func pinTeam(ctx context.Context, database *sql.DB, ev Event, teamID string, progress ...func()) error {
	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// The event's deadline and stage as they are now, held until the pin
	// commits, so an extension or reopen that lands first is respected.
	ev, err = hackevents.GetEventForShare(ctx, tx, ev.ID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	var (
		due  bool
		slug string
	)
	err = tx.QueryRowContext(ctx, `
		SELECT t.pinned_at IS NULL AND ($3 OR COALESCE(`+hackevents.EffectiveDeadline("$2::timestamptz")+` <= clock_timestamp(), false)),
		       t.slug
		  FROM event_teams t
		 WHERE t.id = $1
		   FOR UPDATE OF t`, teamID, ev.SubmissionDeadline, hackevents.FrozenStage(ev.Stage)).Scan(&due, &slug)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if !due {
		return nil
	}
	v, err := sites.LiveActiveVersion(ctx, tx, ev.AccountID, slug)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE event_teams SET pinned_version = $2, pinned_at = clock_timestamp() WHERE id = $1`, teamID, v); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	for _, fn := range progress {
		fn()
	}
	return nil
}

// UnpinReopened clears the pin of every team of the event whose deadline is
// no longer passed (the organiser moved it). Call in the transaction that
// moved it.
func UnpinReopened(ctx context.Context, q Querier, eventID string) error {
	ev, err := hackevents.GetEventByID(ctx, q, eventID)
	if err != nil || hackevents.FrozenStage(ev.Stage) {
		return err
	}
	d := hackevents.EffectiveDeadline("$2::timestamptz")
	_, err = q.ExecContext(ctx, `
		UPDATE event_teams t SET pinned_version = NULL, pinned_at = NULL
		 WHERE t.event_id = $1 AND t.pinned_at IS NOT NULL
		   AND (`+d+` IS NULL OR `+d+` > clock_timestamp())`, eventID, ev.SubmissionDeadline)
	return err
}

// PinnedVersions returns the versions pinned for the holding account's team
// sites, by site name (for keeping them when old versions are pruned).
func PinnedVersionOfSite(ctx context.Context, q Querier, accountID, siteName string) (int, bool, error) {
	ev, ok, err := HoldingAccountEvent(ctx, q, accountID)
	if err != nil || !ok {
		return 0, false, err
	}
	var v sql.NullInt64
	err = q.QueryRowContext(ctx, `
		SELECT t.pinned_version FROM event_teams t
		 WHERE t.event_id = $1 AND t.slug = $2 AND t.pinned_version IS NOT NULL`, ev.ID, siteName).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return int(v.Int64), v.Valid, nil
}

// HoldingAccountEvent returns the event held by accountID, if it is one.
func HoldingAccountEvent(ctx context.Context, q Querier, accountID string) (Event, bool, error) {
	ev, err := hackevents.GetEventByAccount(ctx, q, accountID)
	if errors.Is(err, sql.ErrNoRows) {
		return Event{}, false, nil
	}
	return ev, err == nil, err
}

// TeamNameEverUsed: slug was a team of this event before or the holding
// account has or had a site by that name (the platform's answer).
func TeamNameEverUsed(ctx context.Context, q Querier, eventSlug, accountID, slug string) (bool, error) {
	used, err := hackevents.NameEverUsed(ctx, q, eventSlug, slug)
	if err != nil || used {
		return used, err
	}
	return sites.SiteEverNamed(ctx, q, accountID, slug)
}

// DeleteEmptyOpenTeams removes teams nobody is on whose deadline has not
// passed and that hold no pin (a team kept for its submission that the
// organiser reopened). Their sites follow with the orphan sweep. Each
// candidate is rechecked and deleted in its own transaction under the event
// (FOR SHARE) and team (FOR UPDATE) locks a join takes, so a team someone just
// joined is never deleted.
func DeleteEmptyOpenTeams(ctx context.Context, database *sql.DB, progress ...func()) error {
	occupied, err := hackmembers.TeamIDsWithMembers(ctx, database, "")
	if err != nil {
		return err
	}
	events, err := hackevents.AllEvents(ctx, database)
	if err != nil || len(events) == 0 {
		return err
	}
	ids, deadlines, frozen := eventDeadlines(events)
	rows, err := database.QueryContext(ctx, `
		SELECT t.id, t.event_id FROM event_teams t`+eventDeadlinesJoin+`
		 WHERE `+hackevents.EmptyTeamDeletable(eventDeadlineOfD)+`
		   AND NOT (t.id = ANY($4::uuid[])) ORDER BY t.id`, pq.Array(ids), pq.Array(deadlines), pq.Array(frozen), pq.Array(occupied))
	if err != nil {
		return err
	}
	var candidates [][2]string
	for rows.Next() {
		var c [2]string
		if err := rows.Scan(&c[0], &c[1]); err != nil {
			rows.Close()
			return err
		}
		candidates = append(candidates, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, c := range candidates {
		deleted, err := deleteEmptyTeam(ctx, database, c[1], c[0])
		if err != nil {
			return err
		}
		if deleted {
			for _, fn := range progress {
				fn()
			}
		}
	}
	return nil
}

// deleteEmptyTeam deletes one team if, under the event and team locks, it is
// still empty and deletable.
func deleteEmptyTeam(ctx context.Context, database *sql.DB, eventID, teamID string) (bool, error) {
	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	if _, err := hackevents.GetEventForShare(ctx, tx, eventID); errors.Is(err, sql.ErrNoRows) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	if err := LockTeamRow(ctx, tx, teamID); err != nil {
		return false, err
	}
	if err := DeleteTeamIfEmpty(ctx, tx, teamID); err != nil {
		return false, err
	}
	still, err := TeamExists(ctx, tx, teamID)
	if err != nil {
		return false, err
	}
	return !still, tx.Commit()
}

// MyTeam is one team a person is on, for the connector's team choice.
type MyTeam = hackmodel.MyTeam

// ListMyTeams lists the teams userID is on that can still publish: the event
// has not ended and is not taken down, the team's deadline has not passed and
// its site is not taken down.
func ListMyTeams(ctx context.Context, q Querier, userID string) ([]MyTeam, error) {
	members, err := hackmembers.Members(ctx, q, hackmembers.Filter{UserID: userID, Role: "participant", OnTeam: true})
	if err != nil {
		return nil, err
	}
	teamIDs, eventIDs := []string{}, []string{}
	for _, m := range members {
		teamIDs = append(teamIDs, m.TeamID.String)
		eventIDs = append(eventIDs, m.EventID)
	}
	byID, err := hackevents.EventsByIDs(ctx, q, eventIDs)
	if err != nil {
		return nil, err
	}
	// The events that can still publish, newest first.
	var events []Event
	for _, e := range byID {
		if e.Stage != "archived" && !e.TakenDownAt.Valid {
			events = append(events, e)
		}
	}
	sort.Slice(events, func(i, j int) bool { return events[i].CreatedAt.After(events[j].CreatedAt) })
	ids, deadlines, _ := eventDeadlines(events)
	order := make([]int, len(events))
	for i := range order {
		order[i] = i
	}
	rows, err := q.QueryContext(ctx, `
		SELECT t.id, t.name, t.slug, t.event_id
		  FROM event_teams t
		  JOIN unnest($1::uuid[], $2::text[], $3::int[]) AS d(event_id, deadline, n) ON d.event_id = t.event_id
		 WHERE t.id = ANY($4::uuid[])
		   AND t.site_taken_down_at IS NULL
		   AND NOT COALESCE(`+hackevents.EffectiveDeadline(eventDeadlineOfD)+` <= clock_timestamp(), false)
		 ORDER BY d.n, t.name`, pq.Array(ids), pq.Array(deadlines), pq.Array(order), pq.Array(teamIDs))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []MyTeam{}
	for rows.Next() {
		var m MyTeam
		var eventID string
		if err := rows.Scan(&m.TeamID, &m.TeamName, &m.TeamSlug, &eventID); err != nil {
			return nil, err
		}
		m.EventSlug, m.EventTitle = byID[eventID].Slug, byID[eventID].Title
		out = append(out, m)
	}
	return out, rows.Err()
}
