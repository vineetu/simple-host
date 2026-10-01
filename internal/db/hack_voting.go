package db

import (
	"context"
	"database/sql"
	"errors"
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

func GetVotingSettings(ctx context.Context, q Querier, eventID string) (VotingSettings, error) {
	var s VotingSettings
	err := q.QueryRowContext(ctx, `SELECT voting_enabled, voting_opens_at, voting_closes_at,
		voting_eligibility, directory_listed FROM events WHERE id = $1`, eventID).
		Scan(&s.Enabled, &s.OpensAt, &s.ClosesAt, &s.Eligibility, &s.DirectoryListed)
	return s, err
}

func SaveVotingSettings(ctx context.Context, q Querier, eventID string, s VotingSettings) error {
	_, err := q.ExecContext(ctx, `UPDATE events SET voting_enabled = $2, voting_opens_at = $3,
		voting_closes_at = $4, voting_eligibility = $5, directory_listed = $6,
		updated_at = now() WHERE id = $1`, eventID, s.Enabled, nullTime(s.OpensAt),
		nullTime(s.ClosesAt), s.Eligibility, s.DirectoryListed)
	return err
}

func SetDirectoryListed(ctx context.Context, q Querier, eventID string, listed bool) error {
	_, err := q.ExecContext(ctx, `UPDATE events SET directory_listed = $2, updated_at = now() WHERE id = $1`, eventID, listed)
	return err
}

type VoteOption struct {
	TeamID, Slug, Name, Title, Tagline string
	Count                              int
}

// VoteOptions uses the same live-site gate as the event gallery.
func VoteOptions(ctx context.Context, q Querier, eventID, accountID string) ([]VoteOption, error) {
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
func VoteFinalRanking(ctx context.Context, q Querier, eventID, accountID string) ([]VoteOption, error) {
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

func OwnVote(ctx context.Context, q Querier, eventID, canonicalEmail string) (string, error) {
	var slug string
	err := q.QueryRowContext(ctx, `SELECT t.slug FROM event_votes v
		JOIN event_teams t ON t.id = v.team_id
		WHERE v.event_id = $1 AND v.voter_email = $2`, eventID, canonicalEmail).Scan(&slug)
	return slug, err
}

// CastVote serializes a vote against stage/window changes by locking the event
// row. The primary key serializes aliases of the same verified email.
func CastVote(ctx context.Context, database *sql.DB, eventID, accountID, canonicalEmail, teamSlug string) (bool, error) {
	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var enabled, galleryOpen bool
	var opens, closes sql.NullTime
	var eligibility, stage string
	var takenDown sql.NullTime
	err = tx.QueryRowContext(ctx, `SELECT voting_enabled, gallery_open, voting_opens_at, voting_closes_at,
		voting_eligibility, stage, taken_down_at FROM events WHERE id = $1 FOR SHARE`, eventID).
		Scan(&enabled, &galleryOpen, &opens, &closes, &eligibility, &stage, &takenDown)
	if err != nil {
		return false, err
	}
	var now time.Time
	if err = tx.QueryRowContext(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
		return false, err
	}
	if !enabled || !galleryOpen || !opens.Valid || !closes.Valid || now.Before(opens.Time) || !now.Before(closes.Time) || stage == "draft" || stage == "archived" || takenDown.Valid {
		return false, ErrVoteClosed
	}
	if eligibility != "all_signed_in" {
		var allowed bool
		roleClause := ""
		if eligibility == "participants" {
			roleClause = " AND m.role = 'participant'"
		}
		err = tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM event_members m
			JOIN users u ON u.id = m.user_id WHERE m.event_id = $1
			AND `+baseEmailSQL("u.username")+` = $2
			AND m.approval_status = 'approved'`+roleClause+`)`, eventID, canonicalEmail).Scan(&allowed)
		if err != nil {
			return false, err
		}
		if !allowed {
			return false, ErrVoteIneligible
		}
	}
	var teamID string
	err = tx.QueryRowContext(ctx, `SELECT t.id `+galleryLiveFrom+` AND t.slug = $3`, eventID, accountID, teamSlug).Scan(&teamID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, ErrVoteTeamNotFound
	}
	if err != nil {
		return false, err
	}
	var own bool
	err = tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM event_members m JOIN users u ON u.id = m.user_id
		WHERE m.event_id = $1 AND m.team_id = $2 AND m.role = 'participant'
		AND `+baseEmailSQL("u.username")+` = $3)`, eventID, teamID, canonicalEmail).Scan(&own)
	if err != nil {
		return false, err
	}
	if own {
		return false, ErrVoteOwnTeam
	}
	var created bool
	err = tx.QueryRowContext(ctx, `INSERT INTO event_votes (event_id, voter_email, team_id)
		VALUES ($1, $2, $3)
		ON CONFLICT (event_id, voter_email) DO UPDATE SET team_id = EXCLUDED.team_id, updated_at = now()
		WHERE event_votes.team_id <> EXCLUDED.team_id
		RETURNING created_at = updated_at`, eventID, canonicalEmail, teamID).Scan(&created)
	if errors.Is(err, sql.ErrNoRows) {
		return false, ErrVoteDuplicate
	}
	if err != nil {
		return false, err
	}
	if err = tx.Commit(); err != nil {
		return false, err
	}
	return created, nil
}

type DirectoryEvent struct {
	Slug, Title, Tagline, Stage, TimeZone string
	StartsAt, EndsAt                      sql.NullTime
}

func ListDirectoryEvents(ctx context.Context, q Querier) ([]DirectoryEvent, error) {
	rows, err := queryContext(ctx, q, `SELECT slug, title, tagline, stage, time_zone, starts_at, ends_at
		FROM events WHERE directory_listed AND stage <> 'draft' AND taken_down_at IS NULL
		ORDER BY starts_at NULLS LAST, slug`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DirectoryEvent{}
	for rows.Next() {
		var e DirectoryEvent
		if err := rows.Scan(&e.Slug, &e.Title, &e.Tagline, &e.Stage, &e.TimeZone, &e.StartsAt, &e.EndsAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
