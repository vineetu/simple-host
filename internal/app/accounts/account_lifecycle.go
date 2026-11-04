package accounts

import (
	"context"
	"database/sql"
	"errors"
	"github.com/vsriram/simple-host/internal/model"
	repo "github.com/vsriram/simple-host/internal/repository/accounts"
	sites "github.com/vsriram/simple-host/internal/repository/sites"
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
func (s *Service) ListSubmissionsElsewhere(ctx context.Context, userID string) ([]model.Submission, error) {
	return repo.ListSubmissionsElsewhere(ctx, s.database, userID)
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

type MembershipErasure interface {
	AdminTeamIDs(context.Context, *sql.Tx, string) ([]string, error)
	FinishTeams(context.Context, *sql.Tx, []string) ([]string, error)
}
type DeleteAccountInput struct {
	ID, AdminID, Confirm      string
	Operator, HoldingAccounts bool
	Teams                     MembershipErasure
}
type DeletedAccount struct {
	Erased   model.ErasedAccount
	Username string
}

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
	if acct.IsAdmin || acct.ID == in.AdminID {
		if in.Operator {
			return DeletedAccount{}, lifecycleFail(400, "cannot delete an admin account", "")
		}
		return DeletedAccount{}, lifecycleFail(403, "an admin account cannot delete itself here", "admin_account")
	}
	if !in.Operator {
		if acct.Suspended {
			return DeletedAccount{}, lifecycleFail(403, "this account is suspended; write to support@simple-host.app about deleting it", "account_suspended")
		}
		if acct.TakenDown > 0 {
			return DeletedAccount{}, lifecycleFail(403, "a site of this account was taken down; write to support@simple-host.app about deleting the account", "site_suspended")
		}
	}
	if acct.EventClaims > 0 {
		return DeletedAccount{}, lifecycleFail(409, "this account still holds event hostnames; release them first so their DNS records are removed", "event_hostnames")
	}
	if code, msg, err := CheckHackAccountDeletion(ctx, tx, acct.ID, in.HoldingAccounts); err != nil {
		return DeletedAccount{}, err
	} else if code != "" {
		if in.Operator {
			msg = "this account organises an event that has not ended; end it or delete it from the Events tab first"
			if code == "event_account" {
				msg = "this account holds an event; delete the event from the Events tab"
			}
		}
		return DeletedAccount{}, lifecycleFail(409, msg, code)
	}
	if !in.Operator {
		want, what := acct.Handle, "your handle"
		if want == "" {
			want, what = acct.Username, "your email"
		}
		if !strings.EqualFold(strings.TrimSpace(in.Confirm), want) {
			return DeletedAccount{}, lifecycleFail(400, "type "+what+" ("+want+") as confirm to delete the account", "confirm_mismatch")
		}
	}
	erased, err := repo.EraseAccount(ctx, tx, acct, repo.ErasureOperations{RetireSiteNames: func(ctx context.Context, tx *sql.Tx, id string) ([]string, error) {
		return sites.RetireSiteNames(ctx, tx, id, repo.PlatformDomains()...)
	}, AdminTeamIDs: in.Teams.AdminTeamIDs, FinishTeams: in.Teams.FinishTeams})
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
	return DeletedAccount{Erased: erased, Username: acct.Username}, nil
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

func (s *Service) ReissueAccountKey(ctx context.Context, id, adminID string, holdingAccounts bool) (ReissuedAccount, error) {
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
	if held, err := HoldingAccount(ctx, tx, u.ID, holdingAccounts); err != nil {
		return ReissuedAccount{}, err
	} else if held {
		return ReissuedAccount{}, lifecycleFail(409, "this account holds an event and never signs in; manage the event from the admin Events tab", "event_account")
	}
	if u.IsAdmin || u.ID == adminID {
		return ReissuedAccount{}, lifecycleFail(400, "cannot reissue an admin account's key here", "")
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

type SuspensionInput struct {
	ID, AdminID, ActorID, Reason string
	On, HoldingAccounts          bool
	Marker                       func(model.Site) error
}
type SuspendedAccount struct {
	User      model.User
	SiteCount int
}

func (s *Service) SuspendAccount(ctx context.Context, in SuspensionInput) (SuspendedAccount, error) {
	if held, err := HoldingAccount(ctx, s.database, in.ID, in.HoldingAccounts); err != nil {
		return SuspendedAccount{}, err
	} else if held {
		return SuspendedAccount{}, lifecycleFail(409, "this account holds an event; take the event down or restore it from the admin Events tab", "event_account")
	}
	target, err := repo.GetUserByID(ctx, s.database, in.ID)
	if errors.Is(err, sql.ErrNoRows) {
		return SuspendedAccount{}, lifecycleFail(404, "not found", "")
	}
	if err != nil {
		return SuspendedAccount{}, err
	}
	if in.On && (target.IsAdmin || target.ID == in.AdminID) {
		return SuspendedAccount{}, lifecycleFail(400, "cannot suspend an admin account", "admin_account")
	}
	if err := repo.SetUserSuspended(ctx, s.database, target.ID, in.Reason); err != nil {
		return SuspendedAccount{}, err
	}
	list, err := sites.ListSitesByUser(ctx, s.database, target.ID)
	if err != nil {
		return SuspendedAccount{}, lifecycleFail(500, "saved, but the account's sites could not be listed; retry", "")
	}
	failed := 0
	for _, site := range list {
		if err := in.Marker(site); err != nil {
			log.Printf("suspend: marker for %s/%s: %v", site.UserID, site.Name, err)
			failed++
		}
	}
	if failed > 0 {
		return SuspendedAccount{}, lifecycleFail(500, "saved, but some of the account's sites could not be marked; retry", "")
	}
	log.Printf("admin_user_suspend on=%t user_id=%s sites=%d by=%s reason=%q", in.On, target.ID, len(list), in.ActorID, in.Reason)
	return SuspendedAccount{User: target, SiteCount: len(list)}, nil
}
