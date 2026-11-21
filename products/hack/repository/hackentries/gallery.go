package hackentries

import (
	"context"
	"database/sql"

	"github.com/lib/pq"
	"github.com/vsriram/simple-host/host/sqlkit"
	"github.com/vsriram/simple-host/products/hack/model"
	"github.com/vsriram/simple-host/products/hack/repository/hackteams"
)

type GalleryCard = model.GalleryCard

// liveFrom selects the live projects shared by the gallery and voting: the
// teams hackteams.LiveTeams gives (as teamRows from $2) with their entries.
var liveFrom = `
  FROM ` + teamRows(2) + `
  LEFT JOIN event_entries e ON e.team_id = t.id
 WHERE t.event_id = $1`

// liveArgs is eventID and the event's live teams, the parameters liveFrom reads.
func liveArgs(ctx context.Context, q sqlkit.Querier, eventID string, live []string) ([]any, error) {
	teams, err := hackteams.LiveTeams(ctx, q, eventID, live)
	if err != nil {
		return nil, err
	}
	return append([]any{eventID}, teamArgs(teams)...), nil
}

// galleryHasShot is an entry screenshot the gallery can show: bytes of one of the three types an entry stores.
const galleryHasShot = `COALESCE(e.screenshot_type, '') IN ('image/png', 'image/jpeg', 'image/webp') AND COALESCE(octet_length(e.screenshot), 0) > 0`

func ListGalleryCards(ctx context.Context, q sqlkit.Querier, eventID string, live []string) ([]GalleryCard, error) {
	args, err := liveArgs(ctx, q, eventID, live)
	if err != nil {
		return nil, err
	}
	rows, err := queryContext(ctx, q, `
		SELECT t.id, t.slug, t.name, COALESCE(e.title, ''), COALESCE(e.tagline, ''), COALESCE(e.tools, '{}'),
		       (`+galleryHasShot+`)
		`+liveFrom+`
		 ORDER BY lower(COALESCE(NULLIF(e.title, ''), t.name)), t.slug`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []GalleryCard
	for rows.Next() {
		var c GalleryCard
		if err := rows.Scan(&c.ID, &c.Slug, &c.Name, &c.Title, &c.Tagline, pq.Array(&c.Tools), &c.HasScreenshot); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func GalleryScreenshot(ctx context.Context, q sqlkit.Querier, eventID string, live []string, teamSlug string) (data []byte, contentType string, err error) {
	args, err := liveArgs(ctx, q, eventID, live)
	if err != nil {
		return nil, "", err
	}
	err = q.QueryRowContext(ctx, `
		SELECT e.screenshot, e.screenshot_type
		`+liveFrom+`
		   AND t.slug = $9 AND `+galleryHasShot, append(args, teamSlug)...).Scan(&data, &contentType)
	if err != nil {
		return nil, "", err
	}
	if len(data) == 0 || contentType == "" {
		return nil, "", sql.ErrNoRows
	}
	return data, contentType, nil
}
