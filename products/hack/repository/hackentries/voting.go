package hackentries

import (
	"context"
	"database/sql"
	"time"

	"github.com/lib/pq"

	accounts "github.com/vsriram/simple-host/host/access"
	hostmodel "github.com/vsriram/simple-host/host/model"
	"github.com/vsriram/simple-host/host/sqlkit"
	"github.com/vsriram/simple-host/products/hack/model"
)

type VotingSettings = model.VotingSettings

func GetVotingSettings(ctx context.Context, q sqlkit.Querier, eventID string) (VotingSettings, error) {
	var s VotingSettings
	err := q.QueryRowContext(ctx, `SELECT voting_enabled, voting_opens_at, voting_closes_at,
		voting_eligibility, directory_listed FROM events WHERE id = $1`, eventID).
		Scan(&s.Enabled, &s.OpensAt, &s.ClosesAt, &s.Eligibility, &s.DirectoryListed)
	return s, err
}

func SaveVotingSettings(ctx context.Context, q sqlkit.Querier, eventID string, s VotingSettings) error {
	_, err := q.ExecContext(ctx, `UPDATE events SET voting_enabled = $2, voting_opens_at = $3,
		voting_closes_at = $4, voting_eligibility = $5, directory_listed = $6,
		updated_at = now() WHERE id = $1`, eventID, s.Enabled, nullTime(s.OpensAt),
		nullTime(s.ClosesAt), s.Eligibility, s.DirectoryListed)
	return err
}

func SetDirectoryListed(ctx context.Context, q sqlkit.Querier, eventID string, listed bool) error {
	_, err := q.ExecContext(ctx, `UPDATE events SET directory_listed = $2, updated_at = now() WHERE id = $1`, eventID, listed)
	return err
}

type VoteOption = model.VoteOption

// VoteOptions uses the same live-site gate as the event gallery.
func VoteOptions(ctx context.Context, q sqlkit.Querier, eventID string, live []string) ([]VoteOption, error) {
	rows, err := queryContext(ctx, q, `SELECT t.id, t.slug, t.name,
		COALESCE(e.title, ''), COALESCE(e.tagline, ''), 0
		`+GalleryLiveFrom+` ORDER BY lower(COALESCE(NULLIF(e.title, ''), t.name)), t.slug`, eventID, pq.Array(live))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []VoteOption{}
	for rows.Next() {
		var v VoteOption
		if err := rows.Scan(&v.TeamID, &v.Slug, &v.Name, &v.Title, &v.Tagline, &v.Count); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// VoteFinalRanking retains the final tally after archived sites are removed
// from the live gallery.
func VoteFinalRanking(ctx context.Context, q sqlkit.Querier, eventID string, shown []string) ([]VoteOption, error) {
	rows, err := queryContext(ctx, q, `SELECT t.id, t.slug, t.name, '', '', COUNT(v.team_id)
		FROM event_teams t
		LEFT JOIN event_votes v ON v.event_id = $1 AND v.team_id = t.id
		WHERE t.event_id = $1 AND t.site_taken_down_at IS NULL AND t.slug = ANY($2::text[])
		GROUP BY t.id, t.slug, t.name
		ORDER BY COUNT(v.team_id) DESC, t.slug`, eventID, pq.Array(shown))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []VoteOption{}
	for rows.Next() {
		var v VoteOption
		if err := rows.Scan(&v.TeamID, &v.Slug, &v.Name, &v.Title, &v.Tagline, &v.Count); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func OwnVote(ctx context.Context, q sqlkit.Querier, eventID, canonicalEmail string) (string, error) {
	var slug string
	err := q.QueryRowContext(ctx, `SELECT t.slug FROM event_votes v
		JOIN event_teams t ON t.id = v.team_id
		WHERE v.event_id = $1 AND v.voter_email = $2`, eventID, canonicalEmail).Scan(&slug)
	return slug, err
}

// VoterEligible reports whether canonicalEmail may vote under eligibility
// ("all_signed_in", "event_members" or "participants"), the same approved-
// membership rule CastVote itself enforces.
func VoterEligible(ctx context.Context, q sqlkit.Querier, eventID, eligibility, canonicalEmail string) (bool, error) {
	if eligibility == "all_signed_in" {
		return true, nil
	}
	roleClause := ""
	if eligibility == "participants" {
		roleClause = " AND m.role = 'participant'"
	}
	emails, err := memberEmails(ctx, q, `SELECT m.user_id FROM event_members m WHERE m.event_id = $1
		AND m.approval_status = 'approved'`+roleClause, eventID)
	return emails[canonicalEmail], err
}

type VoteWindow struct {
	Enabled, GalleryOpen bool
	Opens, Closes        sql.NullTime
	Eligibility, Stage   string
	TakenDown            sql.NullTime
}

// LockVoteWindow locks and reads the event's voting state.
func LockVoteWindow(ctx context.Context, tx *sql.Tx, eventID string) (VoteWindow, error) {
	var v VoteWindow
	err := tx.QueryRowContext(ctx, `SELECT voting_enabled, gallery_open, voting_opens_at, voting_closes_at,
		voting_eligibility, stage, taken_down_at FROM events WHERE id = $1 FOR SHARE`, eventID).
		Scan(&v.Enabled, &v.GalleryOpen, &v.Opens, &v.Closes, &v.Eligibility, &v.Stage, &v.TakenDown)
	return v, err
}

func VoteClock(ctx context.Context, q sqlkit.Querier) (time.Time, error) {
	var now time.Time
	err := q.QueryRowContext(ctx, `SELECT clock_timestamp()`).Scan(&now)
	return now, err
}

func VoteTeamID(ctx context.Context, q sqlkit.Querier, eventID string, live []string, teamSlug string) (string, error) {
	var teamID string
	err := q.QueryRowContext(ctx, `SELECT t.id `+GalleryLiveFrom+` AND t.slug = $3`, eventID, pq.Array(live), teamSlug).Scan(&teamID)
	return teamID, err
}

func VoteOwnTeam(ctx context.Context, q sqlkit.Querier, eventID, teamID, canonicalEmail string) (bool, error) {
	emails, err := memberEmails(ctx, q, `SELECT m.user_id FROM event_members m
		WHERE m.event_id = $1 AND m.team_id = $2 AND m.role = 'participant'`, eventID, teamID)
	return emails[canonicalEmail], err
}

func SaveVote(ctx context.Context, q sqlkit.Querier, eventID, canonicalEmail, teamID string) (bool, error) {
	var created bool
	err := q.QueryRowContext(ctx, `INSERT INTO event_votes (event_id, voter_email, team_id)
		VALUES ($1, $2, $3)
		ON CONFLICT (event_id, voter_email) DO UPDATE SET team_id = EXCLUDED.team_id, updated_at = now()
		WHERE event_votes.team_id <> EXCLUDED.team_id
		RETURNING created_at = updated_at`, eventID, canonicalEmail, teamID).Scan(&created)
	return created, err
}

// IneligibleVotes counts or removes votes using the same canonical email and
// approved membership rule as CastVote. The caller locks the event for removal.
func IneligibleVotes(ctx context.Context, q sqlkit.Querier, eventID, eligibility string, remove bool) (int64, error) {
	if eligibility == "all_signed_in" {
		return 0, nil
	}
	role := ""
	if eligibility == "participants" {
		role = " AND m.role = 'participant'"
	}
	emails, err := memberEmails(ctx, q, `SELECT m.user_id FROM event_members m
		WHERE m.event_id = $1 AND m.approval_status = 'approved'`+role, eventID)
	if err != nil {
		return 0, err
	}
	eligible := []string{}
	for e := range emails {
		eligible = append(eligible, e)
	}
	where := ` FROM event_votes v WHERE v.event_id = $1 AND NOT v.voter_email = ANY($2::text[])`
	if remove {
		result, err := q.ExecContext(ctx, `DELETE`+where, eventID, pq.Array(eligible))
		if err != nil {
			return 0, err
		}
		return result.RowsAffected()
	}
	var count int64
	err = q.QueryRowContext(ctx, `SELECT COUNT(*)`+where, eventID, pq.Array(eligible)).Scan(&count)
	return count, err
}

// memberEmails is the base sign-in address (model.BaseEmail, what a vote is
// cast under) of each member query selects by user_id.
func memberEmails(ctx context.Context, q sqlkit.Querier, query string, args ...any) (map[string]bool, error) {
	rows, err := queryContext(ctx, q, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	users, err := accounts.UsersByIDs(ctx, q, ids)
	if err != nil {
		return nil, err
	}
	out := make(map[string]bool, len(users))
	for _, u := range users {
		out[hostmodel.BaseEmail(u.Username)] = true
	}
	return out, nil
}

// GalleryLiveFrom selects the live projects shared by the gallery and voting:
// teams the organiser has not taken down whose site is one of $2, the names of
// the event account's live sites (sites.LiveSiteNames, the platform's answer).
const GalleryLiveFrom = `
  FROM event_teams t
  LEFT JOIN event_entries e ON e.team_id = t.id
 WHERE t.event_id = $1 AND t.site_taken_down_at IS NULL AND t.slug = ANY($2::text[])`

func nullTime(t sql.NullTime) any {
	if !t.Valid {
		return nil
	}
	return t.Time
}

// OrganiserRoleForShare locks and reads the caller's event role.
func OrganiserRoleForShare(ctx context.Context, tx *sql.Tx, eventID, userID string) (string, error) {
	var role string
	err := tx.QueryRowContext(ctx, `SELECT role FROM event_members WHERE event_id = $1 AND user_id = $2 FOR SHARE`, eventID, userID).Scan(&role)
	return role, err
}
