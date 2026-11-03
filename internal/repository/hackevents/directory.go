package hackevents

import (
	"context"

	"github.com/vsriram/simple-host/internal/model"
	"github.com/vsriram/simple-host/internal/repository/sqlkit"
)

type DirectoryEvent = model.DirectoryEvent

func ListDirectoryEvents(ctx context.Context, q sqlkit.Querier) ([]DirectoryEvent, error) {
	rows, err := queryContext(ctx, q, `SELECT slug, title, tagline, stage, time_zone, accent_color, starts_at, ends_at, applications_open, taken_down_at
		FROM events WHERE directory_listed AND stage <> 'draft' AND taken_down_at IS NULL
		ORDER BY starts_at NULLS LAST, slug`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DirectoryEvent{}
	for rows.Next() {
		var e DirectoryEvent
		if err := rows.Scan(&e.Slug, &e.Title, &e.Tagline, &e.Stage, &e.TimeZone, &e.AccentColor, &e.StartsAt, &e.EndsAt, &e.ApplicationsOpen, &e.TakenDownAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
