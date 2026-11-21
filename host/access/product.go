package access

import (
	"context"
	"database/sql"

	"github.com/vsriram/simple-host/host/access/repo"
	"github.com/vsriram/simple-host/host/model"
	"github.com/vsriram/simple-host/host/sqlkit"
)

// What a product (Simple Hack) asks of accounts. A product never reads or
// writes the platform's account tables itself: it calls these.

// CreateProductTeam makes, inside tx, a team whose members the product keeps
// (SetProductTeamMembers): a Simple Hack event's account. username is a
// sign-in address that can never receive mail; handle becomes its address.
// claimed is false when the handle is taken.
func CreateProductTeam(ctx context.Context, tx *sql.Tx, username, handle string) (id string, claimed bool, err error) {
	if id, err = repo.InsertEventTeamAccount(ctx, tx, username); err != nil {
		return "", false, err
	}
	claimed, err = repo.ClaimHandle(ctx, tx, id, handle)
	return id, claimed, err
}

// SetProductTeamMembers makes the team's members exactly persons, inside the
// product's transaction. Whoever is no longer listed loses access on their
// next request (ActsAsOwner).
func SetProductTeamMembers(ctx context.Context, tx *sql.Tx, teamID string, persons []string) error {
	return repo.ReplaceTeamMembers(ctx, tx, teamID, persons)
}

// UserByID loads an account.
func UserByID(ctx context.Context, q sqlkit.Querier, id string) (model.User, error) {
	return repo.GetUserByID(ctx, q, id)
}

// LockDisplayName reads an account's name, locking its row.
func LockDisplayName(ctx context.Context, q sqlkit.Querier, id string) (string, error) {
	return repo.LockDisplayName(ctx, q, id)
}

// FillEmptyDisplayName sets an account's name when it has none.
func FillEmptyDisplayName(ctx context.Context, q sqlkit.Querier, id, name string) error {
	return repo.FillEmptyDisplayName(ctx, q, id, name)
}

// SuspendAccount takes an account and all its sites down (reason set) or
// brings it back (reason "").
func SuspendAccount(ctx context.Context, q sqlkit.Querier, id, reason string) error {
	return repo.SetUserSuspended(ctx, q, id, reason)
}

// ConnectedApps lists the apps a person has connected.
func ConnectedApps(ctx context.Context, db *sql.DB, userID string) ([]model.OAuthConnection, error) {
	return repo.ListOAuthConnections(ctx, db, userID)
}

// UsersByIDs loads these accounts in one query, keyed by id (display names,
// sign-in addresses, suspension for a list of people).
func UsersByIDs(ctx context.Context, q sqlkit.Querier, ids []string) (map[string]model.User, error) {
	return repo.UsersByIDs(ctx, q, ids)
}

// SortByDisplayName orders account ids by display name, then id.
func SortByDisplayName(ctx context.Context, q sqlkit.Querier, ids []string) ([]string, error) {
	return repo.SortByDisplayName(ctx, q, ids)
}

// LockAccount locks the account row inside tx, so the product's per-account
// limits serialise.
func LockAccount(ctx context.Context, tx *sql.Tx, id string) error {
	_, err := repo.LockProfile(ctx, tx, id)
	return err
}

// DeleteAccountRow deletes an account whose deletion the product decided.
func DeleteAccountRow(ctx context.Context, tx *sql.Tx, id string) error {
	return repo.DeleteAccountRow(ctx, tx, id)
}
