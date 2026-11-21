package ops

import (
	"context"
	"database/sql"
	"errors"
	"log"

	siterules "github.com/vsriram/simple-host/host/rules/sites"

	"github.com/vsriram/simple-host/host/access"
	accountsrepo "github.com/vsriram/simple-host/host/access/repo"
	datarepo "github.com/vsriram/simple-host/host/data/repo"
	"github.com/vsriram/simple-host/host/model"
	"github.com/vsriram/simple-host/host/sites"
	sitesrepo "github.com/vsriram/simple-host/host/sites/repo"
)

// Cutting a person off is the one workflow over all three modules, so it
// lives here, above them: ErasePerson, DeleteTeam and SuspendPerson are the
// only places it happens. Each module does its own part with its own
// functions; the account row's cascade does the rest.

// Cutoff is what the workflows act through. Sites is nil when no site
// files are kept (then no site locks or files are touched); Files trashes a
// product's orphaned team sites after an erase (nil: no sweep).
type Cutoff struct {
	DB    *sql.DB
	Sites *sites.Service
	Hosts *sites.Hosts
	Files *sites.Service
}

// ErrFilesLeft: the operator's erase committed, but some files could not be
// deleted (an operator can remove them).
var ErrFilesLeft = errors.New("account removed but its files could not be deleted")

// ErasePerson erases an account and everything tied to it, in one
// transaction, once every rule that holds it back is checked (the person's
// own request must also confirm it by name): every site's lock first, then
// the account row, so a deploy, rollback, rename or take-down in flight
// finishes before the erasure, and one that was waiting finds the site
// gone. Teams the person alone belongs to go with them. After the commit
// the files go, then the product sweeps what the erase left behind. A
// person's own erase logs a file failure and goes on; the operator's
// returns ErrFilesLeft.
func (c Cutoff) ErasePerson(ctx context.Context, in access.DeleteAccountInput) (access.DeletedAccount, error) {
	if !siterules.UUIDShape.MatchString(in.ID) {
		return access.DeletedAccount{}, &access.LifecycleError{Status: 404, Message: "not found"}
	}
	if c.Sites != nil {
		unlock, err := c.Sites.LockAccountSites(ctx, in.ID)
		if err != nil {
			return access.DeletedAccount{}, err
		}
		defer unlock()
	}
	deleted, err := c.erasePerson(ctx, in)
	if err != nil {
		if errors.As(err, new(*access.LifecycleError)) {
			return deleted, err
		}
		if in.Operator {
			log.Printf("admin delete account %s: %v", in.ID, err)
		} else {
			log.Printf("delete account %s: %v", in.ID, err)
		}
		return deleted, err
	}
	for _, e := range append([]model.ErasedAccount{deleted.Erased}, deleted.Teams...) {
		if err := c.eraseFiles(ctx, e); err != nil {
			if in.Operator {
				log.Printf("admin delete account %s: files of %s: %v", in.ID, e.UserID, err)
				return deleted, ErrFilesLeft
			}
			log.Printf("delete account %s: files of %s: %v", in.ID, e.UserID, err)
		}
	}
	if h := access.ProductErasure(); h != nil && c.Files != nil {
		h.Sweep(ctx, c.DB, deleted.Erased.AffectedEventAccounts, c.Files.TrashTeamSite)
	}
	if !in.Operator {
		log.Printf("account %s deleted by its owner", in.ID)
	}
	return deleted, nil
}

func (c Cutoff) erasePerson(ctx context.Context, in access.DeleteAccountInput) (access.DeletedAccount, error) {
	tx, err := c.DB.BeginTx(ctx, nil)
	if err != nil {
		return access.DeletedAccount{}, err
	}
	defer tx.Rollback()
	acct, err := lockForErasure(ctx, tx, in.ID)
	if errors.Is(err, sql.ErrNoRows) {
		return access.DeletedAccount{}, &access.LifecycleError{Status: 404, Message: "not found"}
	}
	if err != nil {
		return access.DeletedAccount{}, err
	}
	if err := access.ErasureRefusal(ctx, tx, acct, in); err != nil {
		return access.DeletedAccount{}, err
	}
	teamIDs, err := access.SoleMemberTeams(ctx, tx, acct.ID)
	if err != nil {
		return access.DeletedAccount{}, err
	}
	var teams []model.ErasedAccount
	for _, id := range teamIDs {
		erased, err := eraseTeam(ctx, tx, id, acct.ID, in.Operator)
		if err != nil {
			return access.DeletedAccount{}, err
		}
		teams = append(teams, erased)
	}
	erased, err := eraseAccount(ctx, tx, acct)
	if err != nil {
		return access.DeletedAccount{}, err
	}
	return access.DeletedAccount{Erased: erased, Teams: teams, Username: acct.Username}, tx.Commit()
}

// DeleteTeam deletes a team that owns no live sites, for a member, unless
// the operator suspended it or a site is taken down. Its sites in Recently
// deleted go for good with it, then their files.
func (c Cutoff) DeleteTeam(ctx context.Context, personID, handle string) error {
	if c.Sites != nil {
		id, err := access.TeamID(ctx, c.DB, personID, handle)
		if err != nil {
			return err
		}
		unlock, err := c.Sites.LockAccountSites(ctx, id)
		if err != nil {
			return err
		}
		defer unlock()
	}
	tx, err := c.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	team, err := access.LockTeamToDelete(ctx, tx, personID, handle)
	if err != nil {
		return err
	}
	live, err := sitesrepo.LiveSiteCounts(ctx, tx, []string{team.ID})
	if err != nil {
		return err
	}
	if err := access.TeamDeletionRefusal(live[team.ID], nil); err != nil {
		return err
	}
	erased, err := eraseTeam(ctx, tx, team.ID, personID, false)
	if err != nil {
		return access.TeamDeletionRefusal(0, err)
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if err := c.eraseFiles(ctx, erased); err != nil {
		log.Printf("delete team %s: files: %v", erased.UserID, err)
	}
	return nil
}

// SuspendPerson takes a person and all their sites down (on, with reason)
// or brings them back: Access decides and sets the account's flag, then
// every site of theirs is re-marked on disk.
func (c Cutoff) SuspendPerson(ctx context.Context, id, actorID, reason string, on bool) (model.User, int, error) {
	target, err := access.SetSuspension(ctx, c.DB, id, reason, on)
	if err != nil {
		return model.User{}, 0, err
	}
	n, err := c.Sites.MarkAccountSites(ctx, target.ID)
	if err != nil {
		return model.User{}, 0, err
	}
	log.Printf("admin_user_suspend on=%t user_id=%s sites=%d by=%s reason=%q", on, target.ID, n, actorID, reason)
	return target, n, nil
}

// lockForErasure locks the account row and counts what holds it back on
// the sites side.
func lockForErasure(ctx context.Context, tx *sql.Tx, id string) (model.AccountForDelete, error) {
	acct, err := accountsrepo.LockAccountForDelete(ctx, tx, id)
	if err != nil {
		return acct, err
	}
	acct.EventClaims, acct.TakenDown, err = sitesrepo.ErasureHolds(ctx, tx, acct.ID)
	return acct, err
}

// eraseTeam erases a team the caller locked and decided may go, the way any
// account is erased, and records it.
func eraseTeam(ctx context.Context, tx *sql.Tx, teamID, actorID string, operator bool) (model.ErasedAccount, error) {
	acct, err := lockForErasure(ctx, tx, teamID)
	if err != nil {
		return model.ErasedAccount{}, err
	}
	if err := access.TeamErasureRefusal(acct, operator); err != nil {
		return model.ErasedAccount{}, err
	}
	erased, err := eraseAccount(ctx, tx, acct)
	if err != nil {
		return model.ErasedAccount{}, err
	}
	return erased, accountsrepo.InsertTeamAudit(ctx, tx, teamID, acct.Handle, actorID, "delete", "", "")
}

// eraseAccount deletes the locked account and everything tied to it, inside
// tx. Locks are taken in the order a save takes them (the saved data, then
// the site rows), so a save under way finishes first and one that starts
// later waits. The handle, old handles and claimed names are retired, not
// freed. Deleting the account row cascades to its sites (with their
// versions, saved data, lists and analytics), keys, connected apps, sign-in
// identities and visitor sessions.
func eraseAccount(ctx context.Context, tx *sql.Tx, a model.AccountForDelete) (model.ErasedAccount, error) {
	out := model.ErasedAccount{UserID: a.ID, Handle: a.Handle}
	siteIDs, err := sitesrepo.AccountSiteIDs(ctx, tx, a.ID)
	if err != nil {
		return out, err
	}
	if err := datarepo.LockSavedData(ctx, tx, siteIDs); err != nil {
		return out, err
	}
	if err := sitesrepo.LockAccountSiteRows(ctx, tx, a.ID); err != nil {
		return out, err
	}
	if out.Sites, out.Families, err = sitesrepo.EraseAccountSites(ctx, tx, a.ID); err != nil {
		return out, err
	}
	product := access.ProductErasure()
	var ids []string
	if product != nil {
		if ids, err = product.Before(ctx, tx, a.ID); err != nil {
			return out, err
		}
	}
	if out.Aliases, err = accountsrepo.ReleaseAccountNames(ctx, tx, a); err != nil {
		return out, err
	}
	if err := datarepo.EraseSubmittedItems(ctx, tx, a.ID); err != nil {
		return out, err
	}
	if err := sitesrepo.ReleaseRetiredNames(ctx, tx, a.ID); err != nil {
		return out, err
	}
	if product != nil {
		if err := product.Rows(ctx, tx, a.ID); err != nil {
			return out, err
		}
	}
	if err := accountsrepo.DeleteAccountRow(ctx, tx, a.ID); err != nil {
		return out, err
	}
	if product != nil {
		if out.AffectedEventAccounts, err = product.After(ctx, tx, ids); err != nil {
			return out, err
		}
	}
	return out, nil
}

func (c Cutoff) eraseFiles(ctx context.Context, e model.ErasedAccount) error {
	if c.Sites == nil {
		return nil
	}
	return c.Sites.EraseAccountFiles(ctx, e, c.Hosts)
}
