package hackteams

import (
	"context"
	"database/sql"
	"errors"

	hackmodel "github.com/vsriram/simple-host/products/hack/model"
	"github.com/vsriram/simple-host/products/hack/repository/hackevents"
)

// TeamWriteState is what decides whether a team may change its site or entry
// now. Read with TeamWriteStateFor.
type TeamWriteState = hackmodel.TeamWriteState

// TeamWriteStateFor reads a team's write state. In a transaction with share
// set, the team row is held FOR SHARE until the transaction ends, so the
// deadline pin (PinDueTeams, FOR UPDATE) waits for a deploy that was let in
// before the deadline and then pins what it made live. The time compared is
// clock_timestamp(), not the transaction's start.
func TeamWriteStateFor(ctx context.Context, q Querier, teamID string, share bool) (TeamWriteState, error) {
	lock := ""
	if share {
		lock = " FOR SHARE OF t"
	}
	return scanTeamWriteState(q.QueryRowContext(ctx, `SELECT `+teamWriteStateCols+`
		  FROM event_teams t JOIN events e ON e.id = t.event_id
		 WHERE t.id = $1::uuid`+lock, teamID))
}

// TeamWriteStateBySlug is TeamWriteStateFor for the event's team whose site
// is slug, holding the team's row FOR SHARE.
func TeamWriteStateBySlug(ctx context.Context, tx *sql.Tx, eventID, slug string) (TeamWriteState, error) {
	return scanTeamWriteState(tx.QueryRowContext(ctx, `SELECT `+teamWriteStateCols+`
		  FROM event_teams t JOIN events e ON e.id = t.event_id
		 WHERE e.id = $1::uuid AND t.slug = lower($2) FOR SHARE OF t`, eventID, slug))
}

// teamWriteStateCols over event_teams t and events e. A site the platform
// took down is refused by the platform itself.
const teamWriteStateCols = `t.id, e.id, e.slug, t.slug, e.account_id, e.stage, e.taken_down_at IS NOT NULL,
	t.site_taken_down_at IS NOT NULL,
	` + hackevents.EffectiveDeadlineSQL + `,
	clock_timestamp(), t.pinned_at, t.pinned_version,
	COALESCE(t.deadline_override > e.submission_deadline, false),
	e.submission_deadline, t.deadline_override`

func scanTeamWriteState(row interface{ Scan(...any) error }) (TeamWriteState, error) {
	var s TeamWriteState
	err := row.Scan(
		&s.TeamID, &s.EventID, &s.EventSlug, &s.TeamSlug, &s.AccountID, &s.Stage, &s.EventTakenDown,
		&s.SiteTakenDown, &s.Deadline, &s.Now, &s.PinnedAt, &s.PinnedVersion, &s.HasOverride,
		&s.EventDeadline, &s.TeamDeadlineSet)
	return s, err
}

// TeamWriteStatesForEvent is TeamWriteStateFor for every team of an event, by
// team id, in one query (the organiser's and judges' lists).
func TeamWriteStatesForEvent(ctx context.Context, q interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}, eventID string) (map[string]TeamWriteState, error) {
	rows, err := q.QueryContext(ctx, `SELECT `+teamWriteStateCols+`
		  FROM event_teams t JOIN events e ON e.id = t.event_id
		 WHERE e.id = $1::uuid`, eventID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]TeamWriteState{}
	for rows.Next() {
		st, err := scanTeamWriteState(rows)
		if err != nil {
			return nil, err
		}
		out[st.TeamID] = st
	}
	return out, rows.Err()
}

// PinDueTeams pins every team whose deadline has passed and that is not
// pinned yet: pinned_version is its site's live version at that moment (NULL
// when it has no live site). eventID "" means every event. Each team is
// pinned in its own transaction holding the team row FOR UPDATE, so a deploy
// already let in (holding it FOR SHARE) finishes first.
// liveVersion is the platform's answer (sites.LiveActiveVersion).
func PinDueTeams(ctx context.Context, database *sql.DB, eventID string, liveVersion func(context.Context, Querier, string, string) (sql.NullInt64, error), progress ...func()) error {
	rows, err := database.QueryContext(ctx, `
		SELECT t.id FROM event_teams t JOIN events e ON e.id = t.event_id
		 WHERE t.pinned_at IS NULL
		   AND (e.stage IN ('judging', 'results', 'archived') OR (CASE WHEN e.submission_deadline IS NULL THEN NULL ELSE GREATEST(e.submission_deadline, t.deadline_override) END <= clock_timestamp()))
		   AND ($1 = '' OR e.id::text = $1)`, eventID)
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, id := range ids {
		if err := pinTeam(ctx, database, id, liveVersion, progress...); err != nil {
			return err
		}
	}
	return nil
}

func pinTeam(ctx context.Context, database *sql.DB, teamID string, liveVersion func(context.Context, Querier, string, string) (sql.NullInt64, error), progress ...func()) error {
	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var (
		due       bool
		slug      string
		accountID string
	)
	err = tx.QueryRowContext(ctx, `
		SELECT t.pinned_at IS NULL AND (e.stage IN ('judging', 'results', 'archived') OR COALESCE(CASE WHEN e.submission_deadline IS NULL THEN NULL ELSE GREATEST(e.submission_deadline, t.deadline_override) END <= clock_timestamp(), false)),
		       t.slug, e.account_id
		  FROM event_teams t JOIN events e ON e.id = t.event_id
		 WHERE t.id = $1
		   FOR UPDATE OF t`, teamID).Scan(&due, &slug, &accountID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if !due {
		return nil
	}
	v, err := liveVersion(ctx, tx, accountID, slug)
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
	_, err := q.ExecContext(ctx, `
		UPDATE event_teams t SET pinned_version = NULL, pinned_at = NULL
		  FROM events e
		 WHERE e.id = t.event_id AND e.id = $1 AND t.pinned_at IS NOT NULL AND e.stage NOT IN ('judging', 'results', 'archived')
		   AND (CASE WHEN e.submission_deadline IS NULL THEN NULL ELSE GREATEST(e.submission_deadline, t.deadline_override) END IS NULL
		        OR CASE WHEN e.submission_deadline IS NULL THEN NULL ELSE GREATEST(e.submission_deadline, t.deadline_override) END > clock_timestamp())`, eventID)
	return err
}

// PinnedVersions returns the versions pinned for the holding account's team
// sites, by site name (for keeping them when old versions are pruned).
func PinnedVersionOfSite(ctx context.Context, q Querier, accountID, siteName string) (int, bool, error) {
	var v sql.NullInt64
	err := q.QueryRowContext(ctx, `
		SELECT t.pinned_version FROM event_teams t JOIN events e ON e.id = t.event_id
		 WHERE e.account_id = $1 AND t.slug = $2 AND t.pinned_version IS NOT NULL`, accountID, siteName).Scan(&v)
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

// TeamNameEverUsed: slug was a team of this event before (event_used_names)
// or the holding account has or had a site by that name (siteEverNamed, the
// platform's answer).
func TeamNameEverUsed(ctx context.Context, q Querier, eventSlug, accountID, slug string, siteEverNamed func(context.Context, Querier, string, string) (bool, error)) (bool, error) {
	var used bool
	if err := q.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM event_used_names WHERE event_slug = $1 AND team_slug = $2)`, eventSlug, slug).Scan(&used); err != nil || used {
		return used, err
	}
	return siteEverNamed(ctx, q, accountID, slug)
}

// RecordUsedTeamName keeps the event's name and a new team's name for good.
func RecordUsedTeamName(ctx context.Context, q Querier, eventSlug, teamSlug string) error {
	_, err := q.ExecContext(ctx, `
		INSERT INTO event_used_names (event_slug, team_slug) VALUES ($1, ''), ($1, $2)
		ON CONFLICT DO NOTHING`, eventSlug, teamSlug)
	return err
}

// DeleteEmptyOpenTeams removes teams nobody is on whose deadline has not
// passed and that hold no pin (a team kept for its submission that the
// organiser reopened). Their sites follow with the orphan sweep.
func DeleteEmptyOpenTeams(ctx context.Context, q Querier, progress ...func()) error {
	res, err := q.ExecContext(ctx, `
		DELETE FROM event_teams t USING events e
		 WHERE e.id = t.event_id AND `+TeamKeepableSQL+`
		   AND NOT EXISTS (SELECT 1 FROM event_members m WHERE m.team_id = t.id)`)
	if err == nil {
		if n, _ := res.RowsAffected(); n > 0 {
			for _, fn := range progress {
				fn()
			}
		}
	}
	return err
}

// MyTeam is one team a person is on, for the connector's team choice.
type MyTeam = hackmodel.MyTeam

// ListMyTeams lists the teams userID is on that can still publish: the event
// has not ended and is not taken down, the team's deadline has not passed and
// its site is not taken down.
func ListMyTeams(ctx context.Context, q interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}, userID string) ([]MyTeam, error) {
	rows, err := q.QueryContext(ctx, `
		SELECT t.id, t.name, t.slug, e.slug, e.title
		  FROM event_members m
		  JOIN event_teams t ON t.id = m.team_id
		  JOIN events e ON e.id = m.event_id
		 WHERE m.user_id = $1 AND m.role = 'participant'
		   AND e.stage <> 'archived' AND e.taken_down_at IS NULL
		   AND t.site_taken_down_at IS NULL
		   AND NOT COALESCE(`+hackevents.EffectiveDeadlineSQL+` <= clock_timestamp(), false)
		 ORDER BY e.created_at DESC, t.name`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []MyTeam{}
	for rows.Next() {
		var m MyTeam
		if err := rows.Scan(&m.TeamID, &m.TeamName, &m.TeamSlug, &m.EventSlug, &m.EventTitle); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
