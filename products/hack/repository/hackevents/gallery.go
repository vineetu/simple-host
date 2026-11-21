package hackevents

import (
	"context"
	"database/sql"

	"github.com/lib/pq"
	"github.com/vsriram/simple-host/host/sqlkit"
	"github.com/vsriram/simple-host/products/hack/model"
	"github.com/vsriram/simple-host/products/hack/repository/hackentries"
)

type GalleryCard = model.GalleryCard

const galleryLiveFrom = hackentries.GalleryLiveFrom

// galleryHasShot is an entry screenshot the gallery can show: bytes of one of the three types an entry stores.
const galleryHasShot = `COALESCE(e.screenshot_type, '') IN ('image/png', 'image/jpeg', 'image/webp') AND COALESCE(octet_length(e.screenshot), 0) > 0`

func ListGalleryCards(ctx context.Context, q sqlkit.Querier, eventID string, live []string) ([]GalleryCard, error) {
	rows, err := queryContext(ctx, q, `
		SELECT t.id, t.slug, t.name, COALESCE(e.title, ''), COALESCE(e.tagline, ''), COALESCE(e.tools, '{}'),
		       (`+galleryHasShot+`)
		`+galleryLiveFrom+`
		 ORDER BY lower(COALESCE(NULLIF(e.title, ''), t.name)), t.slug`, eventID, pq.Array(live))
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
	err = q.QueryRowContext(ctx, `
		SELECT e.screenshot, e.screenshot_type
		`+galleryLiveFrom+`
		   AND t.slug = $3 AND `+galleryHasShot, eventID, pq.Array(live), teamSlug).Scan(&data, &contentType)
	if err != nil {
		return nil, "", err
	}
	if len(data) == 0 || contentType == "" {
		return nil, "", sql.ErrNoRows
	}
	return data, contentType, nil
}
