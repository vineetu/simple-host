package access

import (
	"context"
	"database/sql"
	"errors"
	"github.com/vsriram/simple-host/host/access/repo"
	"github.com/vsriram/simple-host/host/model"
	"log"
	"strings"
	"time"
)

func (s *Service) ListAccountAddresses(ctx context.Context, userID string) ([]model.AccountAddress, error) {
	return repo.ListAccountAddresses(ctx, s.database, userID)
}
func (s *Service) ListRetiredNames(ctx context.Context, userID string) ([]model.RetiredAccountName, error) {
	return repo.ListRetiredNames(ctx, s.database, userID)
}
func (s *Service) ListVisitorSignIns(ctx context.Context, userID string) ([]model.VisitorSignIn, error) {
	return repo.ListVisitorSignIns(ctx, s.database, userID)
}
func (s *Service) ListChangesElsewhere(ctx context.Context, userID string) ([]model.ChangeElsewhere, error) {
	return repo.ListChangesElsewhere(ctx, s.database, userID)
}

type LifecycleError struct {
	Status        int
	Message, Code string
	Next          time.Time
}

func (e *LifecycleError) Error() string { return e.Message }
func lifecycleFail(status int, message, code string) error {
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

// DeleteAccount erases an account and everything tied to it, in one
// transaction, once every rule that holds it back is checked: the person's
// own request (Operator false) must also confirm it by name.
func (s *Service) DeleteAccount(ctx context.Context, in DeleteAccountInput) (DeletedAccount, error) {
	tx, err := s.database.BeginTx(ctx, nil)
	if err != nil {
		return DeletedAccount{}, err
	}
	defer tx.Rollback()
	acct, err := repo.LockAccountForDelete(ctx, tx, in.ID)
	if errors.Is(err, sql.ErrNoRows) {
		return DeletedAccount{}, lifecycleFail(404, "not found", "")
	}
	if err != nil {
		return DeletedAccount{}, err
	}
	if err := deletionRefusal(ctx, tx, acct, in); err != nil {
		return DeletedAccount{}, err
	}
	// Teams this person alone belongs to go with the account, sites and all.
	teams, err := eraseSoleMemberTeams(ctx, tx, acct.ID, in.Operator)
	if err != nil {
		return DeletedAccount{}, err
	}
	erased, err := repo.EraseAccount(ctx, tx, acct, erasureOperations())
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		if in.Operator {
			log.Printf("admin delete account %s: %v", acct.ID, err)
		} else {
			log.Printf("delete account %s: %v", acct.ID, err)
		}
		return DeletedAccount{}, err
	}
	return DeletedAccount{Erased: erased, Teams: teams, Username: acct.Username}, nil
}

// deletionRefusal is why the account cannot be deleted now, nil when it
// can: never an admin account, never while it holds what must be released
// first, and only as confirmed.
func deletionRefusal(ctx context.Context, tx *sql.Tx, acct model.AccountForDelete, in DeleteAccountInput) error {
	if IsOperator(acct.IsAdmin, acct.Username) {
		if in.Operator {
			return lifecycleFail(400, "cannot delete an admin account", "")
		}
		return lifecycleFail(403, "an admin account cannot delete itself here", "admin_account")
	}
	if !in.Operator {
		if err := deletionHold(acct, "this account"); err != nil {
			return err
		}
	}
	if acct.EventClaims > 0 {
		return lifecycleFail(409, "this account still holds event hostnames; release them first so their DNS records are removed", "event_hostnames")
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
		return lifecycleFail(409, HoldingText(action), "event_account")
	}
	if groupHooks.Erasure == nil {
		return nil
	}
	code, msg, err := groupHooks.Erasure.Block(ctx, tx, accountID, operator)
	if err != nil {
		return err
	}
	if code != "" {
		return lifecycleFail(409, msg, code)
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
		return lifecycleFail(400, "type "+what+" ("+want+") as confirm to delete the account", "confirm_mismatch")
	}
	return nil
}

// erasureOperations are the platform's and the product's parts of an erase.
func erasureOperations() repo.ErasureOperations {
	ops := repo.ErasureOperations{RetireSiteNames: func(ctx context.Context, tx *sql.Tx, id string) ([]string, error) {
		return repo.RetireSiteNames(ctx, tx, id, repo.PlatformDomains()...)
	}}
	if h := groupHooks.Erasure; h != nil {
		ops.Before, ops.Rows, ops.After = h.Before, h.Rows, h.After
	}
	return ops
}

// deletionHold is why an account (a person's own, or a team they alone
// belong to) may not be deleted except by the operator: it is suspended, or
// one of its sites was taken down.
func deletionHold(a model.AccountForDelete, what string) error {
	if a.Suspended {
		return lifecycleFail(403, what+" is suspended; write to support@simple-host.app about deleting it", "account_suspended")
	}
	if a.TakenDown > 0 {
		return lifecycleFail(403, "a site of "+what+" was taken down; write to support@simple-host.app about deleting it", "site_suspended")
	}
	return nil
}

func issuedAPIKey() (string, error) {
	key, err := secureHex(32)
	if err != nil {
		return "", err
	}
	return "shk_" + key, nil
}
func (s *Service) CreateAccounts(ctx context.Context, names []string, randomHandles bool, assign func(context.Context, AttributionStore, string, string, bool)) (map[string]any, error) {
	created := make([]map[string]string, 0, len(names))
	skipped := make([]map[string]string, 0)
	tx, err := s.database.BeginTx(ctx, nil)
	if err != nil {
		return nil, lifecycleFail(500, "internal server error", "")
	}
	defer tx.Rollback()
	for _, name := range names {
		key, err := issuedAPIKey()
		if err != nil {
			return nil, lifecycleFail(500, "internal server error", "")
		}
		var id string
		id, err = repo.InsertIssuedAccount(ctx, tx, name)
		if errors.Is(err, sql.ErrNoRows) {
			skipped = append(skipped, map[string]string{"username": name, "reason": "account already exists"})
			continue
		}
		if err == nil {
			err = repo.AddAPIKey(ctx, tx, id, key, model.KeyNameEvent)
		}
		if err == nil {
			err = repo.MarkEventAccount(ctx, tx, id)
		}
		if err != nil {
			return nil, lifecycleFail(500, "internal server error", "")
		}
		assign(ctx, tx, id, name, randomHandles)
		user, err := repo.GetUserByID(ctx, tx, id)
		if err != nil || !user.Handle.Valid {
			return nil, lifecycleFail(500, "could not assign account handle", "")
		}
		created = append(created, map[string]string{"username": name, "handle": user.Handle.String, "api_key": key, "display_name": user.DisplayName.String})
	}
	if err := tx.Commit(); err != nil {
		return nil, lifecycleFail(500, "internal server error", "")
	}
	return map[string]any{"created": created, "skipped": skipped}, nil
}

type ReissuedAccount struct {
	User model.User
	Key  string
}

func (s *Service) ReissueAccountKey(ctx context.Context, id string) (ReissuedAccount, error) {
	tx, err := s.database.BeginTx(ctx, nil)
	if err != nil {
		return ReissuedAccount{}, err
	}
	defer tx.Rollback()
	u, err := repo.LockReissueAccount(ctx, tx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return ReissuedAccount{}, lifecycleFail(404, "not found", "")
	}
	if err != nil {
		return ReissuedAccount{}, err
	}
	if held, err := HoldingAccount(ctx, tx, u.ID); err != nil {
		return ReissuedAccount{}, err
	} else if held {
		return ReissuedAccount{}, lifecycleFail(409, HoldingText("reissue"), "event_account")
	}
	if IsOperator(u.IsAdmin, u.Username) {
		return ReissuedAccount{}, lifecycleFail(400, "cannot reissue an admin account's key here", "")
	}
	if team, err := repo.IsTeamAccount(ctx, tx, u.ID); err != nil {
		return ReissuedAccount{}, err
	} else if team {
		return ReissuedAccount{}, lifecycleFail(409, "a team has no key: its members act with their own", "team_account")
	}
	if u.Suspended {
		return ReissuedAccount{}, lifecycleFail(409, "this account is suspended; re-enable it before giving it a new key", "account_suspended")
	}
	key, err := issuedAPIKey()
	if err == nil {
		err = repo.ReplaceAPIKeys(ctx, tx, u.ID, key, model.KeyNameEvent)
	}
	if err == nil {
		err = repo.MarkEventAccount(ctx, tx, u.ID)
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		return ReissuedAccount{}, err
	}
	return ReissuedAccount{User: u, Key: key}, nil
}
