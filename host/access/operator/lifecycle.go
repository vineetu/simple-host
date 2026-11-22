package operator

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/vsriram/simple-host/host/access"
	"github.com/vsriram/simple-host/host/access/repo"
	"github.com/vsriram/simple-host/host/model"
	rules "github.com/vsriram/simple-host/host/rules/sites"
	"github.com/vsriram/simple-host/host/sqlkit"
)

func issuedAPIKey() (string, error) {
	key, err := access.SecureHex(32)
	if err != nil {
		return "", err
	}
	return "shk_" + key, nil
}

func CreateAccounts(ctx context.Context, db *sql.DB, names []string, randomHandles bool, assign func(context.Context, sqlkit.Querier, string, string, bool)) (map[string]any, error) {
	created := make([]map[string]string, 0, len(names))
	skipped := make([]map[string]string, 0)
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, access.LifecycleFail(500, "internal server error", "")
	}
	defer tx.Rollback()
	for _, name := range names {
		key, err := issuedAPIKey()
		if err != nil {
			return nil, access.LifecycleFail(500, "internal server error", "")
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
			return nil, access.LifecycleFail(500, "internal server error", "")
		}
		assign(ctx, tx, id, name, randomHandles)
		user, err := repo.GetUserByID(ctx, tx, id)
		if err != nil || !user.Handle.Valid {
			return nil, access.LifecycleFail(500, "could not assign account handle", "")
		}
		created = append(created, map[string]string{"username": name, "handle": user.Handle.String, "api_key": key, "display_name": user.DisplayName.String})
	}
	if err := tx.Commit(); err != nil {
		return nil, access.LifecycleFail(500, "internal server error", "")
	}
	return map[string]any{"created": created, "skipped": skipped}, nil
}

type ReissuedAccount struct {
	User model.User
	Key  string
}

func ReissueAccountKey(ctx context.Context, db *sql.DB, id string) (ReissuedAccount, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return ReissuedAccount{}, err
	}
	defer tx.Rollback()
	u, err := repo.LockReissueAccount(ctx, tx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return ReissuedAccount{}, access.LifecycleFail(404, "not found", "")
	}
	if err != nil {
		return ReissuedAccount{}, err
	}
	if held, err := access.HoldingAccount(ctx, tx, u.ID); err != nil {
		return ReissuedAccount{}, err
	} else if held {
		return ReissuedAccount{}, access.LifecycleFail(409, access.HoldingText("reissue"), "event_account")
	}
	if access.IsOperator(u.IsAdmin, u.Username) {
		return ReissuedAccount{}, access.LifecycleFail(400, "cannot reissue an admin account's key here", "")
	}
	if team, err := repo.IsTeamAccount(ctx, tx, u.ID); err != nil {
		return ReissuedAccount{}, err
	} else if team {
		return ReissuedAccount{}, access.LifecycleFail(409, "a team has no key: its members act with their own", "team_account")
	}
	if u.Suspended {
		return ReissuedAccount{}, access.LifecycleFail(409, "this account is suspended; re-enable it before giving it a new key", "account_suspended")
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

// BulkAccountNames checks the prefix before producing the requested account names.
func BulkAccountNames(prefix string, count int) ([]string, error) {
	if !rules.VisitorHandle.MatchString(prefix) {
		return nil, errors.New("prefix must contain 1 to 39 lowercase letters, digits or hyphens")
	}
	names := make([]string, count)
	for i := range names {
		names[i] = fmt.Sprintf("%s-%02d", prefix, i+1)
	}
	return names, nil
}
