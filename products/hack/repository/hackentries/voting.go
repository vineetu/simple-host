package hackentries

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/lib/pq"

	"github.com/vsriram/simple-host/host/sqlkit"
	"github.com/vsriram/simple-host/products/hack/model"
	"github.com/vsriram/simple-host/products/hack/repository/hackmembers"
	"github.com/vsriram/simple-host/products/hack/repository/hackteams"
)

type VoteOption = model.VoteOption

// VoteOptions uses the same live-site gate as the event gallery.
func VoteOptions(ctx context.Context, q sqlkit.Querier, eventID string, live []string) ([]VoteOption, error) {
	args, err := liveArgs(ctx, q, eventID, live)
	if err != nil {
		return nil, err
	}
	rows, err := queryContext(ctx, q, `SELECT t.id, t.slug, t.name,
		COALESCE(e.title, ''), COALESCE(e.tagline, ''), 0
		`+liveFrom+` ORDER BY lower(COALESCE(NULLIF(e.title, ''), t.name)), t.slug`, args...)
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
	args, err := liveArgs(ctx, q, eventID, shown)
	if err != nil {
		return nil, err
	}
	rows, err := queryContext(ctx, q, `SELECT t.id, t.slug, t.name, '', '', COUNT(v.team_id)
		FROM `+teamRows(2)+`
		LEFT JOIN event_votes v ON v.event_id = $1 AND v.team_id = t.id
		WHERE t.event_id = $1
		GROUP BY t.id, t.slug, t.name
		ORDER BY COUNT(v.team_id) DESC, t.slug`, args...)
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

func OwnVote(ctx context.Context, q sqlkit.Querier, eventID, voterID string) (string, error) {
	var teamID string
	err := q.QueryRowContext(ctx, `SELECT team_id FROM event_votes
		WHERE event_id = $1 AND voter_id = $2`, eventID, voterID).Scan(&teamID)
	if err != nil {
		return "", err
	}
	t, err := hackteams.GetEventTeamByID(ctx, q, teamID)
	return t.Slug, err
}

// VoterEligible reports whether the account may vote under eligibility
// ("all_signed_in", "event_members" or "participants"), the same approved-
// membership rule CastVote itself enforces.
func VoterEligible(ctx context.Context, q sqlkit.Querier, eventID, eligibility, voterID string) (bool, error) {
	if eligibility == "all_signed_in" {
		return true, nil
	}
	f := voterFilter(eventID, eligibility)
	f.UserID = voterID
	n, err := hackmembers.Count(ctx, q, f)
	return n > 0, err
}

func VoteClock(ctx context.Context, q sqlkit.Querier) (time.Time, error) {
	var now time.Time
	err := q.QueryRowContext(ctx, `SELECT clock_timestamp()`).Scan(&now)
	return now, err
}

func VoteTeamID(ctx context.Context, q sqlkit.Querier, eventID string, live []string, teamSlug string) (string, error) {
	teams, err := hackteams.LiveTeams(ctx, q, eventID, live)
	if err != nil {
		return "", err
	}
	for _, t := range teams {
		if t.Slug == teamSlug {
			return t.ID, nil
		}
	}
	return "", sql.ErrNoRows
}

func VoteOwnTeam(ctx context.Context, q sqlkit.Querier, eventID, teamID, voterID string) (bool, error) {
	n, err := hackmembers.Count(ctx, q, hackmembers.Filter{EventID: eventID, TeamID: teamID, Role: "participant", UserID: voterID})
	return n > 0, err
}

// VoterTeamSlug is the slug of the voter's own team in the event, or "" when they are on none.
func VoterTeamSlug(ctx context.Context, q sqlkit.Querier, eventID, voterID string) (string, error) {
	m, err := hackmembers.Member(ctx, q, eventID, voterID, hackmembers.NoLock)
	if errors.Is(err, sql.ErrNoRows) || err == nil && (!m.TeamID.Valid || m.Role != "participant") {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	t, err := hackteams.GetEventTeamByID(ctx, q, m.TeamID.String)
	return t.Slug, err
}

func SaveVote(ctx context.Context, q sqlkit.Querier, eventID, voterID, teamID string) (bool, error) {
	var created bool
	err := q.QueryRowContext(ctx, `INSERT INTO event_votes (event_id, voter_id, team_id)
		VALUES ($1, $2, $3)
		ON CONFLICT (event_id, voter_id) DO UPDATE SET team_id = EXCLUDED.team_id, updated_at = now()
		WHERE event_votes.team_id <> EXCLUDED.team_id
		RETURNING created_at = updated_at`, eventID, voterID, teamID).Scan(&created)
	return created, err
}

// IneligibleVotes counts or removes the votes of accounts that are not
// voters under eligibility, the rule CastVote enforces. The caller locks the
// event for removal.
func IneligibleVotes(ctx context.Context, q sqlkit.Querier, eventID, eligibility string, remove bool) (int64, error) {
	if eligibility == "all_signed_in" {
		return 0, nil
	}
	eligible, err := hackmembers.IDs(ctx, q, voterFilter(eventID, eligibility))
	if err != nil {
		return 0, err
	}
	where := ` FROM event_votes v WHERE v.event_id = $1 AND NOT v.voter_id = ANY($2::uuid[])`
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

// voterFilter is who may vote under eligibility ("event_members" or
// "participants"): approved members, or approved participants only.
func voterFilter(eventID, eligibility string) hackmembers.Filter {
	f := hackmembers.Filter{EventID: eventID, Status: "approved"}
	if eligibility == "participants" {
		f.Role = "participant"
	}
	return f
}

func nullTime(t sql.NullTime) any {
	if !t.Valid {
		return nil
	}
	return t.Time
}
