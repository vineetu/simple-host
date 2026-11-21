package sites

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"time"

	sitesrepo "github.com/vsriram/simple-host/host/sites/repo"

	accountapp "github.com/vsriram/simple-host/host/access"
	"github.com/vsriram/simple-host/host/access/repo"
	"github.com/vsriram/simple-host/host/model"
)

type ProfileInput struct {
	UserID              string
	DisplayName, Handle *string
	SignInAlerts        *bool
	RenameEvery         time.Duration
}

func (s *Service) UpdateProfile(ctx context.Context, in ProfileInput) (model.User, bool, error) {
	tx, err := s.database.BeginTx(ctx, nil)
	if err != nil {
		return model.User{}, false, profileInternalError()
	}
	defer tx.Rollback()
	// First deploy takes this lock before reading the handle or creating files.
	oldHandle, err := repo.LockProfile(ctx, tx, in.UserID)
	if err != nil {
		return model.User{}, false, profileInternalError()
	}
	renamed := false // moved with sites: the old handle stays as an alias
	if in.Handle != nil && *in.Handle != oldHandle.String {
		if renamed, err = s.changeHandleTx(ctx, tx, in, oldHandle.String); err != nil {
			return model.User{}, false, err
		}
	}
	if in.DisplayName != nil {
		if err = repo.SetDisplayName(ctx, tx, in.UserID, *in.DisplayName); err != nil {
			return model.User{}, false, profileInternalError()
		}
	}
	if in.SignInAlerts != nil {
		if err = repo.SetSignInAlerts(ctx, tx, in.UserID, *in.SignInAlerts); err != nil {
			return model.User{}, false, profileInternalError()
		}
	}
	updated, err := repo.GetUserByID(ctx, tx, in.UserID)
	if err != nil {
		return model.User{}, false, profileInternalError()
	}
	alertsOn, err := repo.SignInAlertsOn(ctx, tx, in.UserID)
	if err != nil {
		return model.User{}, false, profileInternalError()
	}
	if err = tx.Commit(); err != nil {
		return model.User{}, false, profileInternalError()
	}
	if renamed {
		// The files answer under the new handle at once; the old handle's
		// link stays so old content-host paths keep resolving. The new
		// handle needs its own *.<handle> certificate: until it is ready,
		// every address handed out is the person-path one (siteHostLive).
		if err := s.disk.EnsureHandleLink(updated.Handle.String, updated.ID); err != nil {
			log.Printf("handle rename: ensure handle link %s: %v", updated.Handle.String, err)
		}
		s.deps.RequestCert(updated.Handle.String)
	}
	return updated, alertsOn, nil
}

// changeHandleTx moves the account to in.Handle. It reports renamed when the
// old handle is kept as an alias, and false when the old handle was freed.
func (s *Service) changeHandleTx(ctx context.Context, tx *sql.Tx, in ProfileInput, oldHandle string) (renamed bool, err error) {
	// Sites in Recently deleted count: they come back at this account's
	// address, so their old links need the alias too.
	hasSites, err := sitesrepo.AccountHasSites(ctx, tx, in.UserID)
	if err != nil {
		return false, profileInternalError()
	}
	// A handle that ever served files (its handles/<h> link exists, kept
	// after a rename or a purge) is kept as an alias too, even with no
	// sites left: old shared links and hand-made vhosts keyed on the
	// folder must never follow a stranger who claims it. Only a handle
	// that never published anything is freed.
	if oldHandle != "" && (hasSites || s.disk.HasHandleLink(oldHandle)) {
		// The handle can change after publishing; old links redirect
		// through the alias. Once per
		// handleRenameEvery, so an address cannot be churned.
		limited, last, err := repo.HandleRenamedSince(ctx, tx, in.UserID, time.Now().Add(-in.RenameEvery))
		if err != nil {
			return false, profileInternalError()
		}
		if limited {
			next := last.Add(in.RenameEvery).UTC()
			return false, &accountapp.LifecycleError{Status: 429, Message: "you changed your address recently; you can change it again after " + next.Format("2 Jan 2006"), Code: "rate_limited", Next: next}
		}
		_, err = repo.RenameHandleTx(ctx, tx, in.UserID, *in.Handle)
		if errors.Is(err, model.ErrDomainTaken) || repo.IsUniqueConstraintViolation(err) {
			return false, &accountapp.LifecycleError{Status: 409, Code: "handle_taken"}
		}
		if err != nil {
			return false, profileInternalError()
		}
		return true, nil
	}
	free, err := repo.HandleAvailable(ctx, tx, in.UserID, *in.Handle)
	if err != nil {
		return false, profileInternalError()
	}
	if !free {
		return false, &accountapp.LifecycleError{Status: 409, Code: "handle_taken"}
	}
	err = repo.SetUnpublishedHandle(ctx, tx, in.UserID, *in.Handle)
	if repo.IsUniqueConstraintViolation(err) {
		return false, &accountapp.LifecycleError{Status: 409, Code: "handle_taken"}
	}
	if err != nil {
		return false, profileInternalError()
	}
	if err = s.disk.RemoveHandleLink(oldHandle); err != nil {
		return false, &accountapp.LifecycleError{Status: 500, Message: "could not remove old handle link"}
	}
	return false, nil
}

func profileInternalError() error {
	return &accountapp.LifecycleError{Status: 500, Message: "internal server error"}
}
