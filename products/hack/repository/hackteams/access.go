package hackteams

import (
	"context"
)

// EventAccess is who may act on the event's sites, as Simple Hack decides
// it: the organisers (members of the event's account), and for each team
// its approved participants (editors of the team's site).
func EventAccess(ctx context.Context, q Querier, eventID string) (accountID string, organisers []string, editors map[string][]string, err error) {
	if err = q.QueryRowContext(ctx, `SELECT account_id FROM events WHERE id = $1`, eventID).Scan(&accountID); err != nil {
		return "", nil, nil, err
	}
	rows, err := q.QueryContext(ctx, `
		SELECT m.user_id, m.role, COALESCE(t.slug, '')
		  FROM event_members m LEFT JOIN event_teams t ON t.id = m.team_id
		 WHERE m.event_id = $1 AND (m.role = 'organiser' OR (m.role = 'participant' AND m.approval_status = 'approved' AND m.team_id IS NOT NULL))`, eventID)
	if err != nil {
		return "", nil, nil, err
	}
	defer rows.Close()
	editors = map[string][]string{}
	for rows.Next() {
		var user, role, slug string
		if err := rows.Scan(&user, &role, &slug); err != nil {
			return "", nil, nil, err
		}
		if role == "organiser" {
			organisers = append(organisers, user)
		} else {
			editors[slug] = append(editors[slug], user)
		}
	}
	return accountID, organisers, editors, rows.Err()
}
