package db

import (
	"context"
	"database/sql"
	"github.com/vsriram/simple-host/internal/model"
	"sort"

	"github.com/vsriram/simple-host/internal/repository/accounts"
	"github.com/vsriram/simple-host/internal/repository/hackevents"
	"github.com/vsriram/simple-host/internal/repository/hackteams"
	"github.com/vsriram/simple-host/internal/repository/sites"
)

// Account deletion and export (GDPR self-service, 2026-09-27).
//
// Deleting an account is one function for both the person (DELETE /v1/me)
// and the operator (DELETE /v1/admin/users/{id}). It is immediate and final:
// nothing goes to Recently deleted. Names the account held on the platform
// domain outlive it as retired, owned by nobody, so no stranger inherits
// links that used to point at this person: claimed site names stay in
// legacy_hostnames ("this site was removed"), and the handle and every old
// handle stay in handle_aliases with no account behind them.

// AccountForDelete is the account row, locked, with what the delete policy
// needs to decide.
type AccountForDelete = model.AccountForDelete

// LockAccountForDelete locks the account row inside tx. sql.ErrNoRows when
// there is no such account.
func LockAccountForDelete(ctx context.Context, tx *sql.Tx, id string) (AccountForDelete, error) {
	return accounts.LockAccountForDelete(ctx, tx, id)
}

// ErasedSite is one site removed with its account, for the disk clean-up.
type ErasedSite = model.ErasedSite

// ErasedAccount is what EraseAccount removed from the database and leaves
// the caller to remove on disk after the commit.
type ErasedAccount = model.ErasedAccount

// EraseAccount deletes the account locked by LockAccountForDelete and
// everything tied to it, inside tx: every site (Recently deleted ones too)
// with its versions, saved data, lists and analytics; keys, connected apps,
// sign-in identities and visitor sessions; the items this person submitted
// to other people's lists; pending sign-in codes sent to its email. The
// handle, old handles and claimed names are retired, not freed.
func EraseAccount(ctx context.Context, tx *sql.Tx, a AccountForDelete) (ErasedAccount, error) {
	return accounts.EraseAccount(ctx, tx, a, accounts.ErasureOperations{
		RetireSiteNames: func(ctx context.Context, tx *sql.Tx, id string) ([]string, error) {
			return RetireSiteNames(ctx, tx, id)
		},
		AdminTeamIDs: func(ctx context.Context, tx *sql.Tx, id string) ([]string, error) {
			return hackteams.AdminTeamIDs(ctx, tx, id)
		},
		FinishTeams: finishErasedAccountTeams,
	})
}

// ---- export ----------------------------------------------------------------

// AccountAddress is one own address of one of the account's live sites.
type AccountAddress = model.AccountAddress

// RetiredAccountName is a name the account's sites used to have.
type RetiredAccountName = model.RetiredAccountName

// HandleAliasesByUser returns every account's earlier handles, by user id.
func HandleAliasesByUser(ctx context.Context, database *sql.DB) (map[string][]string, error) {
	return accounts.HandleAliasesByUser(ctx, database)
}

// SignInIdentity is a linked Google or GitHub sign-in.
type SignInIdentity = model.SignInIdentity

// UnlinkSignInIdentity removes one of the account's linked Google/GitHub
// sign-ins: that Google or GitHub account no longer signs in here.
// sql.ErrNoRows when the account has no such link.
// Every site sign-in of the account ends with it (EndVisitorSessions): a
// session cannot say which sign-in made it, and removing one is how a person
// shuts out whoever used it.
func UnlinkSignInIdentity(ctx context.Context, database *sql.DB, userID, id string) error {
	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := accounts.UnlinkSignInIdentityInTx(ctx, tx, userID, id); err != nil {
		return err
	}
	return tx.Commit()
}

// VisitorSignIn is a site this person is signed in to as a visitor.
type VisitorSignIn = model.VisitorSignIn

// Submission is one list item this person submitted to someone else's site.
type Submission = model.Submission

// ListSubmissionsElsewhere returns every list item this person submitted to
// a site that is not theirs, oldest first.
func ListSubmissionsElsewhere(ctx context.Context, database *sql.DB, userID string) ([]Submission, error) {
	return accounts.ListSubmissionsElsewhere(ctx, database, userID)
}

// ExportItem is one list item as the export writes it.
type ExportItem = sites.ExportItem

// ListExportItems returns every item of every list of a site (other people's
// Personal records left out), with its id,
// time and who submitted it (items sent while signed in). That is the address
// the visitor signed in with on the site, kept with the item,
// never a join to their account: the account's email may be one they did
// not give the site owner, and it changes later.
func ListExportItems(ctx context.Context, database *sql.DB, siteID string) ([]ExportItem, error) {
	return sites.ListExportItems(ctx, database, siteID)
}

func finishErasedAccountTeams(ctx context.Context, tx *sql.Tx, adminTeamIDs []string) ([]string, error) {
	var affected []string
	sort.Strings(adminTeamIDs)
	for _, teamID := range adminTeamIDs {
		var eventAccountID string
		if err := hackteams.ErasureEventAccount(ctx, tx, teamID, &eventAccountID); err != nil {
			if err == sql.ErrNoRows {
				continue // the team is already gone by some other path
			}
			return affected, err
		}
		if err := hackteams.DeleteTeamIfEmpty(ctx, tx, teamID); err != nil {
			return affected, err
		}
		var stillExists bool
		if err := hackteams.ErasureTeamExists(ctx, tx, teamID, &stillExists); err != nil {
			return affected, err
		}
		if !stillExists {
			affected = append(affected, eventAccountID)
			continue
		}
		var members int
		if err := hackteams.ErasureMemberCount(ctx, tx, teamID, &members); err != nil {
			return affected, err
		}
		if members == 0 {
			// Not keepable (frozen or pinned): kept for good and locked, the
			// same as a voluntary last-member leave past the deadline, and
			// its code rotated so the dead link that used to work for this
			// person stops working for anyone else too.
			if err := hackevents.MarkTeamKeptAfterLeave(ctx, tx, teamID); err != nil {
				return affected, err
			}
			if err := hackteams.RotateTeamCodeTx(ctx, tx, teamID); err != nil {
				return affected, err
			}
			continue
		}
		// Members remain: give them a successor. hackteams.EnsureTeamAdmin locks
		// that team's remaining members (sorted), then the team row, so
		// running this sequentially here (never concurrently with itself)
		// carries no extra deadlock risk.
		if err := hackteams.EnsureTeamAdmin(ctx, tx, teamID); err != nil {
			return affected, err
		}
	}
	return affected, nil
}
