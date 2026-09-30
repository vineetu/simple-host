package db

import (
	"context"
	"database/sql"
)

// GalleryCard is one team on an event's public gallery. The team's site
// exists, is not deleted or suspended, the holding account is not suspended,
// and the organiser has not taken the team down. Title and Tagline are the
// entry's, or empty when the team has not written one.
type GalleryCard struct {
	Slug, Name, Title, Tagline string
	HasScreenshot              bool
}

// galleryLiveFrom is the teams of event $1 on holding account $2 that belong
// on the public gallery. A caller adds `AND t.slug = $3` for one team.
const galleryLiveFrom = `
  FROM event_teams t
  JOIN sites s ON s.user_id = $2 AND s.name = t.slug
              AND s.deleted_at IS NULL AND s.suspended_at IS NULL
  JOIN users u ON u.id = s.user_id AND u.suspended_at IS NULL
  LEFT JOIN event_entries e ON e.team_id = t.id
 WHERE t.event_id = $1 AND t.site_taken_down_at IS NULL`

// galleryHasShot is an entry screenshot the gallery can show: bytes of one
// of the three types an entry stores (hack2-team-sites.sql).
const galleryHasShot = `COALESCE(e.screenshot_type, '') IN ('image/png', 'image/jpeg', 'image/webp') AND COALESCE(octet_length(e.screenshot), 0) > 0`

// ListGalleryCards returns the event's gallery cards. The sort key is the
// card title — the entry title, or the team name when that is empty —
// compared case-insensitively. The team slug breaks ties.
func ListGalleryCards(ctx context.Context, q Querier, eventID, accountID string) ([]GalleryCard, error) {
	rows, err := queryContext(ctx, q, `
		SELECT t.slug, t.name, COALESCE(e.title, ''), COALESCE(e.tagline, ''),
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
		if err := rows.Scan(&c.Slug, &c.Name, &c.Title, &c.Tagline, &c.HasScreenshot); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// GalleryScreenshot returns that team's screenshot. sql.ErrNoRows when the
// team is not on the gallery, or it qualifies but has no screenshot.
func GalleryScreenshot(ctx context.Context, q Querier, eventID, accountID, teamSlug string) (data []byte, contentType string, err error) {
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
