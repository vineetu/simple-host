package access

import (
	"context"
	"database/sql"

	"github.com/vsriram/simple-host/host/access/repo"
)

// Site editors (TM-S2): a person who may publish, update and roll back one
// site of an account (a person's or a team's) without belonging to it, and
// who is that site's owner for its saved data. The account's owner or a
// member of the team manages them; whether someone may act is decided by
// accounts.ActsAsOwner alone.

// SetProductSiteEditors makes the editors of the account's sites exactly
// editors (site name -> people), inside the product's transaction: a
// product (Simple Hack) keeping its teams and the platform in step.
func SetProductSiteEditors(ctx context.Context, tx *sql.Tx, accountID string, editors map[string][]string) error {
	return repo.ReplaceSiteEditors(ctx, tx, accountID, editors)
}
