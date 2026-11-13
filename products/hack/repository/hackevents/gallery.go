package hackevents

import (
	"context"
	"database/sql"

	"github.com/lib/pq"
	"github.com/vsriram/simple-host/internal/model"
	"github.com/vsriram/simple-host/internal/repository/sqlkit"
	"github.com/vsriram/simple-host/products/hack/repository/hackentries"
)

// GalleryCard is one team on an event's public gallery. The team's site
// exists, is not deleted or suspended, the holding account is not suspended,
// and the organiser has not taken the team down. Title and Tagline are the
// entry's, or empty when the team has not written one. Tools is the entry's
// tools list (build-plan §4.6: once results are published, the gallery card
// shows tool tags too), never nil.
type GalleryCard = model.GalleryCard

// galleryLiveFrom is shared with voting.
const galleryLiveFrom = hackentries.GalleryLiveFrom

// galleryHasShot is an entry screenshot the gallery can show: bytes of one
// of the three types an entry stores (hack2-team-sites.sql).
const galleryHasShot = `COALESCE(e.screenshot_type, '') IN ('image/png', 'image/jpeg', 'image/webp') AND COALESCE(octet_length(e.screenshot), 0) > 0`

// ListGalleryCards returns the event's gallery cards. The sort key is the
// card title — the entry title, or the team name when that is empty —
// compared case-insensitively. The team slug breaks ties.
func ListGalleryCards(ctx context.Context, q sqlkit.Querier, eventID, accountID string) ([]GalleryCard, error) {
	rows, err := queryContext(ctx, q, `
		SELECT t.id, t.slug, t.name, COALESCE(e.title, ''), COALESCE(e.tagline, ''), COALESCE(e.tools, '{}'),
		       (`+galleryHasShot+`)
		`+galleryLiveFrom+`
		 ORDER BY lower(COALESCE(NULLIF(e.title, ''), t.name)), t.slug`, eventID, accountID)
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

// GalleryScreenshot returns that team's screenshot. sql.ErrNoRows when the
// team is not on the gallery, or it qualifies but has no screenshot.
func GalleryScreenshot(ctx context.Context, q sqlkit.Querier, eventID, accountID, teamSlug string) (data []byte, contentType string, err error) {
	err = q.QueryRowContext(ctx, `
		SELECT e.screenshot, e.screenshot_type
		`+galleryLiveFrom+`
		   AND t.slug = $3 AND `+galleryHasShot, eventID, accountID, teamSlug).Scan(&data, &contentType)
	if err != nil {
		return nil, "", err
	}
	if len(data) == 0 || contentType == "" {
		return nil, "", sql.ErrNoRows
	}
	return data, contentType, nil
}
