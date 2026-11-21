package hackentries

import (
	"fmt"
	"time"

	"github.com/lib/pq"
	"github.com/vsriram/simple-host/products/hack/model"
)

// Teams are hackteams' rows. A query here that needs team columns gets the
// teams from hackteams first (JudgedTeams, LiveTeams, ListEventTeams,
// TeamsByIDs) and joins them as the row set teamRows builds, so the SQL
// below reads t.id, t.event_id, t.slug, t.name, t.track_id,
// t.deadline_override and t.site_taken_down as before.

// teamRows is the SQL of teams as the row set t, from the seven parameters
// starting at $p that teamArgs gives.
func teamRows(p int) string {
	return fmt.Sprintf(`(SELECT u.id, u.event_id, u.slug, u.name, NULLIF(u.track, '')::uuid AS track_id,
		NULLIF(u.dl, '')::timestamptz AS deadline_override, u.down AS site_taken_down
	  FROM unnest($%d::uuid[], $%d::uuid[], $%d::text[], $%d::text[], $%d::text[], $%d::text[], $%d::bool[])
	    AS u(id, event_id, slug, name, track, dl, down)) t`, p, p+1, p+2, p+3, p+4, p+5, p+6)
}

// teamArgs is the seven array parameters teamRows reads.
func teamArgs(teams []model.EventTeam) []any {
	n := len(teams)
	ids, events, slugs, names, tracks, deadlines := make([]string, n), make([]string, n), make([]string, n), make([]string, n), make([]string, n), make([]string, n)
	down := make([]bool, n)
	for i, t := range teams {
		ids[i], events[i], slugs[i], names[i], tracks[i] = t.ID, t.EventID, t.Slug, t.Name, t.TrackID.String
		if t.DeadlineOverride.Valid {
			deadlines[i] = t.DeadlineOverride.Time.UTC().Format(time.RFC3339Nano)
		}
		down[i] = t.SiteTakenDownAt.Valid
	}
	return []any{pq.Array(ids), pq.Array(events), pq.Array(slugs), pq.Array(names), pq.Array(tracks), pq.Array(deadlines), pq.Array(down)}
}
