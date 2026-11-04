package sites

import (
	"context"

	"github.com/vsriram/simple-host/internal/model"
)

type SitePasscodeRow = model.SitePasscodeRow

// GetSitePasscode loads the passcode state of a live (not deleted) site.
// sql.ErrNoRows for an unknown or deleted site.
func GetSitePasscode(ctx context.Context, q Querier, siteID string) (SitePasscodeRow, error) {
	var r SitePasscodeRow
	err := q.QueryRowContext(ctx, `
		SELECT id, user_id, name, passcode_enc, passcode_set_at, passcode_generation, offline_at IS NOT NULL, access = 'specific'
		  FROM sites WHERE id::text = $1 AND deleted_at IS NULL`, siteID).Scan(
		&r.SiteID, &r.UserID, &r.Name, &r.Enc, &r.SetAt, &r.Generation, &r.Offline, &r.NamedViewers)
	return r, err
}
