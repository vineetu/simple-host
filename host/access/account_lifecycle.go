package access

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"github.com/vsriram/simple-host/host/access/repo"
	"github.com/vsriram/simple-host/host/model"
)

func (s *Service) ListVisitorSignIns(ctx context.Context, userID string) ([]model.VisitorSignIn, error) {
	return repo.ListVisitorSignIns(ctx, s.database, userID)
}

type LifecycleError struct {
	Status        int
	Message, Code string
	Next          time.Time
}

func (e *LifecycleError) Error() string { return e.Message }
func LifecycleFail(status int, message, code string) error {
	return &LifecycleError{Status: status, Message: message, Code: code}
}

// IsOperator: the install's protected operator account (the "admin" row, or
// any account flagged admin). It cannot be deleted, suspended, reissued or
// have its password or sign-in email changed through the ordinary routes.
func IsOperator(isAdmin bool, username string) bool { return isAdmin || username == "admin" }

type DeleteAccountInput struct {
	ID, Confirm string
	Operator    bool
}
type DeletedAccount struct {
	Erased model.ErasedAccount
	// Teams are the teams the account alone belonged to, deleted with it;
	// their files are removed after the commit like the account's own.
	Teams    []model.ErasedAccount
	Username string
}

// ErasureRefusal is why the account (locked by repo.LockAccountForDelete,
// its EventClaims and TakenDown counted by Sites) cannot be erased now, nil
// when it can: never an admin account, never while it holds what must be
// released first, and only as confirmed. host/ops ErasePerson asks it.
func ErasureRefusal(ctx context.Context, tx *sql.Tx, acct model.AccountForDelete, in DeleteAccountInput) error {
	if IsOperator(acct.IsAdmin, acct.Username) {
		if in.Operator {
			return LifecycleFail(400, "cannot delete an admin account", "")
		}
		return LifecycleFail(403, "an admin account cannot delete itself here", "admin_account")
	}
	if !in.Operator {
		if err := deletionHold(acct, "this account"); err != nil {
			return err
		}
	}
	if acct.EventClaims > 0 {
		return LifecycleFail(409, "this account still holds event hostnames; release them first so their DNS records are removed", "event_hostnames")
	}
	if err := holdingRefusal(ctx, tx, acct.ID, in.Operator); err != nil {
		return err
	}
	if !in.Operator {
		return confirmRefusal(acct, in.Confirm)
	}
	return nil
}

// holdingRefusal: an account that holds an event, or that the product's
// erasure rules hold back, is not deleted.
func holdingRefusal(ctx context.Context, tx *sql.Tx, accountID string, operator bool) error {
	if held, err := HoldingAccount(ctx, tx, accountID); err != nil {
		return err
	} else if held {
		action := "delete"
		if operator {
			action = "delete_operator"
		}
		return LifecycleFail(409, HoldingText(action), "event_account")
	}
	if groupHooks.Erasure == nil {
		return nil
	}
	code, msg, err := groupHooks.Erasure.Block(ctx, tx, accountID, operator)
	if err != nil {
		return err
	}
	if code != "" {
		return LifecycleFail(409, msg, code)
	}
	return nil
}

// confirmRefusal: the person typed their handle (or, without one, their
// email) as confirm.
func confirmRefusal(acct model.AccountForDelete, confirm string) error {
	want, what := acct.Handle, "your handle"
	if want == "" {
		want, what = acct.Username, "your email"
	}
	if !strings.EqualFold(strings.TrimSpace(confirm), want) {
		return LifecycleFail(400, "type "+what+" ("+want+") as confirm to delete the account", "confirm_mismatch")
	}
	return nil
}

// deletionHold is why an account (a person's own, or a team they alone
// belong to) may not be deleted except by the operator: it is suspended, or
// one of its sites was taken down.
func deletionHold(a model.AccountForDelete, what string) error {
	if a.Suspended {
		return LifecycleFail(403, what+" is suspended; write to support@simple-host.app about deleting it", "account_suspended")
	}
	if a.TakenDown > 0 {
		return LifecycleFail(403, "a site of "+what+" was taken down; write to support@simple-host.app about deleting it", "site_suspended")
	}
	return nil
}
