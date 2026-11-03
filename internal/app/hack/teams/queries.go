package teams

// TODO(4.2): move to repository/hackteams.

import (
	"context"
	"database/sql"
	"github.com/vsriram/simple-host/internal/db"
)

func (s *Service) teamDeadlinePassed(ctx context.Context, teamID string) (bool, error) {
	var passed bool
	err := s.db.QueryRowContext(ctx, `
		SELECT COALESCE(`+db.EffectiveDeadlineSQL+` <= clock_timestamp(), false)
		  FROM event_teams t JOIN events e ON e.id = t.event_id WHERE t.id = $1`, teamID).Scan(&passed)
	return passed, err
}

const (
	adminSQL1  = `UPDATE event_teams SET looking_for = $2 WHERE id = $1`
	createSQL1 = `SELECT EXISTS (SELECT 1 FROM event_teams WHERE event_id = $1 AND lower(name) = lower($2))`
	createSQL2 = `
		SELECT COALESCE(submission_deadline <= clock_timestamp(), false) FROM events WHERE id = $1 FOR SHARE`
	createSQL3  = `UPDATE event_teams SET looking_for=$2 WHERE id=$1`
	findingSQL1 = `SELECT team_id, team_admin FROM event_members WHERE event_id=$1 AND user_id=$2 AND approval_status='approved' FOR UPDATE`
	findingSQL2 = `SELECT 1 FROM event_teams WHERE id=$1 FOR UPDATE`
	findingSQL3 = `UPDATE event_teams SET looking_for=$2 WHERE id=$1`
	findingSQL4 = `UPDATE event_members SET looking=$3,looking_intro=$4 WHERE event_id=$1 AND user_id=$2 AND role='participant' AND approval_status='approved' AND team_id IS NULL`
	findingSQL5 = `SELECT team_id, user_id, kind FROM event_team_requests WHERE event_id = $1 AND id = $2`
	findingSQL6 = `SELECT id FROM event_teams WHERE event_id = $1 AND slug = $2`
	findingSQL7 = `SELECT id FROM event_team_requests WHERE team_id = $1 AND user_id = $2`
	findingSQL8 = `SELECT 1 FROM event_members WHERE event_id=$1 ORDER BY user_id FOR UPDATE`
	findingSQL9 = `SELECT 1 FROM event_teams WHERE event_id=$1 ORDER BY id FOR UPDATE`
	locksSQL1   = `SELECT stage FROM events WHERE id = $1 FOR SHARE`
	locksSQL2   = `
		SELECT team_id, team_admin FROM event_members
		 WHERE event_id = $1 AND user_id = $2 AND approval_status = 'approved' FOR UPDATE`
	locksSQL3 = `SELECT 1 FROM event_teams WHERE id = $1 FOR UPDATE`
	locksSQL4 = `SELECT 1 FROM event_members WHERE event_id = $1 AND user_id = $2 FOR UPDATE`
	locksSQL5 = `SELECT 1 FROM event_members WHERE event_id = $1 AND user_id IN ($2, $3) ORDER BY user_id FOR UPDATE`
	locksSQL6 = `
		SELECT team_id, team_admin FROM event_members
		 WHERE event_id = $1 AND user_id = $2 AND approval_status = 'approved'`
	locksSQL7      = `SELECT 1 FROM event_teams WHERE id = $1 FOR UPDATE`
	membershipSQL1 = `DELETE FROM api_keys WHERE id IN (SELECT key_id FROM event_team_keys WHERE team_id = $1)`
	membershipSQL2 = `UPDATE event_members SET team_removal_notice=$2 WHERE team_id=$1`
	openSQL1       = `SELECT t.id,t.slug,t.name,t.looking_for,
 COALESCE(x.id::text,''),COALESCE(x.slug,''),COALESCE(x.name,''),COALESCE(x.challenge,''),COALESCE(x.prize,'')
 FROM event_teams t JOIN events e ON e.id=t.event_id LEFT JOIN event_tracks x ON x.id=t.track_id
 WHERE t.event_id=$1 AND t.locked_at IS NULL
   AND e.stage NOT IN ('judging','results','archived')
   AND NOT COALESCE(` + db.EffectiveDeadlineSQL + ` <= clock_timestamp(), false)
 ORDER BY t.created_at`
	openSQL2 = `SELECT m.team_id,m.display_name FROM event_members m JOIN event_teams t ON t.id=m.team_id WHERE t.event_id=$1 AND t.locked_at IS NULL AND m.approval_status='approved' ORDER BY m.joined_at,m.user_id`
	openSQL3 = `SELECT user_id,display_name,looking_intro FROM event_members WHERE event_id=$1 AND role='participant' AND approval_status='approved' AND team_id IS NULL AND looking ORDER BY joined_at,user_id`
	openSQL4 = `SELECT r.id,r.team_id,t.slug,t.name,r.user_id,m.display_name,r.kind,r.created_at FROM event_team_requests r JOIN event_teams t ON t.id=r.team_id JOIN event_members m ON m.event_id=r.event_id AND m.user_id=r.user_id WHERE r.event_id=$1 AND (r.user_id=$2 OR r.team_id=$3 OR $4) ORDER BY r.created_at`
	openSQL5 = `SELECT looking,looking_intro FROM event_members WHERE event_id=$1 AND user_id=$2`
	openSQL6 = `
			SELECT t.locked_at IS NOT NULL, t.looking_for, m.team_admin
			  FROM event_teams t JOIN event_members m ON m.team_id = t.id
			 WHERE t.id = $1 AND m.event_id = $2 AND m.user_id = $3`
	organiserSQL1 = `SELECT team_id FROM event_members WHERE event_id = $1 AND user_id = $2 FOR UPDATE`
	organiserSQL2 = `UPDATE event_teams SET code = $2 WHERE id = $1`
	organiserSQL3 = `SELECT EXISTS (SELECT 1 FROM event_teams WHERE id = $1)`
	organiserSQL4 = `SELECT 1 FROM event_members WHERE event_id=$1 AND user_id IN ($2,$3) ORDER BY user_id FOR UPDATE`
	organiserSQL5 = `SELECT team_id, team_admin FROM event_members WHERE event_id=$1 AND user_id=$2 AND approval_status='approved'`
	organiserSQL6 = `SELECT slug FROM event_teams WHERE id=$1 FOR UPDATE`
	organiserSQL7 = `UPDATE event_members SET team_removal_notice=$3 WHERE event_id=$1 AND user_id=$2`
	organiserSQL8 = `SELECT team_removal_notice FROM event_members WHERE event_id=$1 AND user_id=$2`
	peopleSQL1    = `SELECT looking_for FROM event_teams WHERE id = $1`
)

// teamNameTaken checks for another team with the same name, ignoring case.
func teamNameTaken(ctx context.Context, q db.Querier, eventID, name string) (bool, error) {
	var taken bool
	err := q.QueryRowContext(ctx, createSQL1, eventID, name).Scan(&taken)
	return taken, err
}

// trySavepoint permits a retry after a unique violation, which otherwise aborts
// the whole transaction.
func trySavepoint(ctx context.Context, q db.Querier, fn func() error) error {
	if _, err := q.ExecContext(ctx, "SAVEPOINT hack_try"); err != nil {
		return err
	}
	if err := fn(); err != nil {
		if _, rerr := q.ExecContext(ctx, "ROLLBACK TO SAVEPOINT hack_try"); rerr != nil {
			return rerr
		}
		return err
	}
	_, err := q.ExecContext(ctx, "RELEASE SAVEPOINT hack_try")
	return err
}

// memberTeamID locks the old membership so a concurrent move waits and the
// team actually left is the one read.
func memberTeamID(ctx context.Context, q db.Querier, eventID, userID string) string {
	var team sql.NullString
	if err := q.QueryRowContext(ctx, organiserSQL1, eventID, userID).Scan(&team); err != nil || !team.Valid {
		return ""
	}
	return team.String
}
