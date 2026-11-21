package hackteams

import (
	"context"

	"github.com/lib/pq"
	"github.com/vsriram/simple-host/products/hack/repository/hackmembers"
)

// The teams the entries owner (hackentries) asks about. It joins its own
// rows to these team rows by id; it never reads event_teams itself.

// JudgedTeams is every team of the event that still counts for judging and
// results, in id order: someone is on it, or it was kept after its last
// member left (leaving never deletes a team it empties past its deadline, so
// its entry keeps being judged and ranked).
func JudgedTeams(ctx context.Context, q Querier, eventID string) ([]EventTeam, error) {
	withMembers, err := hackmembers.TeamIDsWithMembers(ctx, q, eventID)
	if err != nil {
		return nil, err
	}
	return listTeams(ctx, q, `
		SELECT `+teamColumns+` FROM event_teams
		 WHERE event_id = $1 AND (id = ANY($2::uuid[]) OR kept_after_leave)
		 ORDER BY id`, eventID, pq.Array(withMembers))
}

// LiveTeams is the event's teams the organiser has not taken down whose site
// is one of live (the event account's live site names, the platform's
// answer): the projects the gallery and voting show.
func LiveTeams(ctx context.Context, q Querier, eventID string, live []string) ([]EventTeam, error) {
	return listTeams(ctx, q, `
		SELECT `+teamColumns+` FROM event_teams
		 WHERE event_id = $1 AND site_taken_down_at IS NULL AND slug = ANY($2::text[])
		 ORDER BY id`, eventID, pq.Array(live))
}

// TeamsByIDs loads these teams by id; an id with no team is absent.
func TeamsByIDs(ctx context.Context, q Querier, ids []string) (map[string]EventTeam, error) {
	list, err := listTeams(ctx, q, `SELECT `+teamColumns+` FROM event_teams WHERE id = ANY($1::uuid[])`, pq.Array(ids))
	if err != nil {
		return nil, err
	}
	out := make(map[string]EventTeam, len(list))
	for _, t := range list {
		out[t.ID] = t
	}
	return out, nil
}

func listTeams(ctx context.Context, q Querier, query string, args ...any) ([]EventTeam, error) {
	rows, err := queryContext(ctx, q, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []EventTeam{}
	for rows.Next() {
		t, err := scanTeamRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
