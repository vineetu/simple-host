package events

import (
	"context"
	"database/sql"

	accounts "github.com/vsriram/simple-host/host/access"
	teamrepo "github.com/vsriram/simple-host/products/hack/repository/hackteams"
)

// SyncAccess puts the platform in step with the event.
func SyncAccess(ctx context.Context, tx *sql.Tx, eventID string) error {
	account, organisers, editors, err := teamrepo.EventAccess(ctx, tx, eventID)
	if err != nil {
		return err
	}
	if err := accounts.SetProductTeamMembers(ctx, tx, account, organisers); err != nil {
		return err
	}
	return accounts.SetProductSiteEditors(ctx, tx, account, editors)
}
