package hackentries

import (
	"context"
	"database/sql"
	"errors"
	"github.com/vsriram/simple-host/internal/repository/sqlkit"
	"time"
)

var (
	ErrVoteClosed       = errors.New("voting is not open")
	ErrVoteIneligible   = errors.New("voter is not eligible")
	ErrVoteOwnTeam      = errors.New("cannot vote for own team")
	ErrVoteTeamNotFound = errors.New("team is not eligible")
	ErrVoteDuplicate    = errors.New("vote already exists for this team")
)

type VotingSettings struct {
	Enabled           bool
	OpensAt, ClosesAt sql.NullTime
	Eligibility       string
	DirectoryListed   bool
}

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

type VoteOption struct {
	TeamID, Slug, Name, Title, Tagline string
	Count                              int
}

// VoteOptions uses the same live-site gate as the event gallery.
func VoteOptions(ctx context.Context, q sqlkit.Querier, eventID, accountID string) ([]VoteOption, error) {
	rows, err := queryContext(ctx, q, `SELECT t.id, t.slug, t.name,
		COALESCE(e.title, ''), COALESCE(e.tagline, ''), 0
		`+galleryLiveFrom+` ORDER BY lower(COALESCE(NULLIF(e.title, ''), t.name)), t.slug`, eventID, accountID)
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
// from the live gallery. Moderated teams and suspended sites stay hidden.
func VoteFinalRanking(ctx context.Context, q sqlkit.Querier, eventID, accountID string) ([]VoteOption, error) {
	rows, err := queryContext(ctx, q, `SELECT t.id, t.slug, t.name, '', '', COUNT(v.team_id)
		FROM event_teams t
		JOIN sites s ON s.user_id = $2 AND s.name = t.slug AND s.suspended_at IS NULL
		JOIN users u ON u.id = s.user_id AND u.suspended_at IS NULL
		LEFT JOIN event_votes v ON v.event_id = $1 AND v.team_id = t.id
		WHERE t.event_id = $1 AND t.site_taken_down_at IS NULL
		GROUP BY t.id, t.slug, t.name
		ORDER BY COUNT(v.team_id) DESC, t.slug`, eventID, accountID)
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
// membership rule CastVote itself enforces. Shared so the public vote page
// can tell a signed-in visitor up front that they can't vote (Grok retest
// 3, Confusing #4), instead of only after they press a Vote button that
// looked active.
func VoterEligible(ctx context.Context, q sqlkit.Querier, eventID, eligibility, canonicalEmail string) (bool, error) {
	if eligibility == "all_signed_in" {
		return true, nil
	}
	roleClause := ""
	if eligibility == "participants" {
		roleClause = " AND m.role = 'participant'"
	}
	var allowed bool
	err := q.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM event_members m
		JOIN users u ON u.id = m.user_id WHERE m.event_id = $1
		AND `+baseEmailSQL("u.username")+` = $2
		AND m.approval_status = 'approved'`+roleClause+`)`, eventID, canonicalEmail).Scan(&allowed)
	return allowed, err
}

// VoteWindow is the locked voting configuration and event state.
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

// VoteClock reads the database clock used for the voting window.
func VoteClock(ctx context.Context, q sqlkit.Querier) (time.Time, error) {
	var now time.Time
	err := q.QueryRowContext(ctx, `SELECT clock_timestamp()`).Scan(&now)
	return now, err
}

// VoteTeamID finds a live project by slug.
func VoteTeamID(ctx context.Context, q sqlkit.Querier, eventID, accountID, teamSlug string) (string, error) {
	var teamID string
	err := q.QueryRowContext(ctx, `SELECT t.id `+galleryLiveFrom+` AND t.slug = $3`, eventID, accountID, teamSlug).Scan(&teamID)
	return teamID, err
}

// VoteOwnTeam reports whether the voter belongs to the selected team.
func VoteOwnTeam(ctx context.Context, q sqlkit.Querier, eventID, teamID, canonicalEmail string) (bool, error) {
	var own bool
	err := q.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM event_members m JOIN users u ON u.id = m.user_id
		WHERE m.event_id = $1 AND m.team_id = $2 AND m.role = 'participant'
		AND `+baseEmailSQL("u.username")+` = $3)`, eventID, teamID, canonicalEmail).Scan(&own)
	return own, err
}

// SaveVote inserts or changes a vote; no row means the selection was unchanged.
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
	where := ` FROM event_votes v WHERE v.event_id = $1 AND NOT EXISTS (
		SELECT 1 FROM event_members m JOIN users u ON u.id = m.user_id
		WHERE m.event_id = v.event_id AND m.approval_status = 'approved'
		AND ` + baseEmailSQL("u.username") + ` = v.voter_email` + role + `)`
	if remove {
		result, err := q.ExecContext(ctx, `DELETE`+where, eventID)
		if err != nil {
			return 0, err
		}
		return result.RowsAffected()
	}
	var count int64
	err := q.QueryRowContext(ctx, `SELECT COUNT(*)`+where, eventID).Scan(&count)
	return count, err
}

const galleryLiveFrom = `
  FROM event_teams t
  JOIN sites s ON s.user_id = $2 AND s.name = t.slug
              AND s.deleted_at IS NULL AND s.suspended_at IS NULL
  JOIN users u ON u.id = s.user_id AND u.suspended_at IS NULL
  LEFT JOIN event_entries e ON e.team_id = t.id
 WHERE t.event_id = $1 AND t.site_taken_down_at IS NULL`

func baseEmailSQL(col string) string {
	return `regexp_replace(lower(` + col + `), '^([^+@]+)\+[^@]*@', '\1@')`
}
func nullTime(t sql.NullTime) any {
	if !t.Valid {
		return nil
	}
	return t.Time
}

// OrganiserRoleForShare locks and reads the caller's event role.
func OrganiserRoleForShare(ctx context.Context, q sqlkit.Querier, eventID, userID string) (string, error) {
	var role string
	err := q.QueryRowContext(ctx, `SELECT role FROM event_members WHERE event_id = $1 AND user_id = $2 FOR SHARE`, eventID, userID).Scan(&role)
	return role, err
}
