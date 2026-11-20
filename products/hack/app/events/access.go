package events

import (
	"context"
	"database/sql"

	"github.com/vsriram/simple-host/host/app/accounts"
	"github.com/vsriram/simple-host/host/app/sites"
	teamrepo "github.com/vsriram/simple-host/products/hack/repository/hackteams"
)

// SyncAccess puts the platform in step with the event: its organisers are
// the members of the event's account, and each team's approved participants
// are the editors of the team's site. Every change to organisers, teams or
// their members calls it in its own transaction, so a person removed loses
// access on their next request (accounts.ActsAsOwner).
func SyncAccess(ctx context.Context, tx *sql.Tx, eventID string) error {
	account, organisers, editors, err := teamrepo.EventAccess(ctx, tx, eventID)
	if err != nil {
		return err
	}
	if err := accounts.SetProductTeamMembers(ctx, tx, account, organisers); err != nil {
		return err
	}
	return sites.SetProductSiteEditors(ctx, tx, account, editors)
}
