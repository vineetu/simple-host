package sites

import (
	"context"
	"database/sql"
	"errors"
	accountapp "github.com/vsriram/simple-host/internal/app/accounts"
	"github.com/vsriram/simple-host/internal/model"
	repo "github.com/vsriram/simple-host/internal/repository/accounts"
	"log"
	"time"
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
		return model.User{}, false, &accountapp.LifecycleError{Status: 500, Message: "internal server error"}
	}
	defer tx.Rollback()
	var oldHandle sql.NullString
	// First deploy takes this lock before reading the handle or creating files.
	oldHandle, err = repo.LockProfile(ctx, tx, in.UserID)
	if err != nil {
		return model.User{}, false, &accountapp.LifecycleError{Status: 500, Message: "internal server error"}
	}
	changed := in.Handle != nil && *in.Handle != oldHandle.String
	renamed := false // moved with sites: the old handle stays as an alias
	if changed {
		// Sites in Recently deleted count: they come back at this account's
		// address, so their old links need the alias too.
		var hasSites bool
		hasSites, err = repo.AccountHasSites(ctx, tx, in.UserID)
		if err != nil {
			return model.User{}, false, &accountapp.LifecycleError{Status: 500, Message: "internal server error"}
		}
		// A handle that ever served files (its handles/<h> link exists, kept
		// after a rename or a purge) is kept as an alias too, even with no
		// sites left: old shared links and hand-made vhosts keyed on the
		// folder must never follow a stranger who claims it. Only a handle
		// that never published anything is freed.
		if oldHandle.String != "" && (hasSites || s.disk.HasHandleLink(oldHandle.String)) {
			// Owner decision 2026-09-27: the handle can change after
			// publishing; old links redirect through the alias. Once per
			// handleRenameEvery, so an address cannot be churned.
			limited, last, rerr := repo.HandleRenamedSince(ctx, tx, in.UserID, time.Now().Add(-in.RenameEvery))
			if rerr != nil {
				return model.User{}, false, &accountapp.LifecycleError{Status: 500, Message: "internal server error"}
			}
			if limited {
				next := last.Add(in.RenameEvery).UTC()
				return model.User{}, false, &accountapp.LifecycleError{Status: 429, Message: "you changed your address recently; you can change it again after " + next.Format("2 Jan 2006"), Code: "rate_limited", Next: next}
			}
			_, err = repo.RenameHandleTx(ctx, tx, in.UserID, *in.Handle)
			if errors.Is(err, model.ErrDomainTaken) || repo.IsUniqueConstraintViolation(err) {
				return model.User{}, false, &accountapp.LifecycleError{Status: 409, Code: "handle_taken"}
			}
			if err != nil {
				return model.User{}, false, &accountapp.LifecycleError{Status: 500, Message: "internal server error"}
			}
			renamed = true
		} else {
			free, nsErr := repo.HandleAvailable(ctx, tx, in.UserID, *in.Handle)
			if nsErr != nil {
				return model.User{}, false, &accountapp.LifecycleError{Status: 500, Message: "internal server error"}
			}
			if !free {
				return model.User{}, false, &accountapp.LifecycleError{Status: 409, Code: "handle_taken"}
			}
			err = repo.SetUnpublishedHandle(ctx, tx, in.UserID, *in.Handle)
			if repo.IsUniqueConstraintViolation(err) {
				return model.User{}, false, &accountapp.LifecycleError{Status: 409, Code: "handle_taken"}
			}
			if err != nil {
				return model.User{}, false, &accountapp.LifecycleError{Status: 500, Message: "internal server error"}
			}
			if err = s.disk.RemoveHandleLink(oldHandle.String); err != nil {
				return model.User{}, false, &accountapp.LifecycleError{Status: 500, Message: "could not remove old handle link"}
			}
		}
	}
	if in.DisplayName != nil {
		if err = repo.SetDisplayName(ctx, tx, in.UserID, *in.DisplayName); err != nil {
			return model.User{}, false, &accountapp.LifecycleError{Status: 500, Message: "internal server error"}
		}
	}
	if in.SignInAlerts != nil {
		if err = repo.SetSignInAlerts(ctx, tx, in.UserID, *in.SignInAlerts); err != nil {
			return model.User{}, false, &accountapp.LifecycleError{Status: 500, Message: "internal server error"}
		}
	}
	updated, err := repo.GetUserByID(ctx, tx, in.UserID)
	if err != nil {
		return model.User{}, false, &accountapp.LifecycleError{Status: 500, Message: "internal server error"}
	}
	alertsOn, err := repo.SignInAlertsOn(ctx, tx, in.UserID)
	if err != nil {
		return model.User{}, false, &accountapp.LifecycleError{Status: 500, Message: "internal server error"}
	}
	if err = tx.Commit(); err != nil {
		return model.User{}, false, &accountapp.LifecycleError{Status: 500, Message: "internal server error"}
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
