package keys

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"time"

	"github.com/vsriram/simple-host/host/access"
	"github.com/vsriram/simple-host/host/access/repo"
	"github.com/vsriram/simple-host/host/model"
)

func ListKeys(ctx context.Context, db *sql.DB, userID string, idle time.Duration) ([]access.KeyInfo, error) {
	keys, err := repo.ListAPIKeys(ctx, db, userID)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	out := make([]access.KeyInfo, 0, len(keys))
	for _, k := range keys {
		out = append(out, access.KeyInfoOf(k, now, idle))
	}
	return out, nil
}

func DeleteKey(ctx context.Context, db *sql.DB, userID, keyID string) (bool, error) {
	return repo.DeleteAPIKey(ctx, db, userID, keyID)
}

// SignOut retires the browser's SSO parent first, then the request's key. These remain separate writes.
func SignOut(ctx context.Context, db *sql.DB, userID, keyHash, browserPass string) error {
	if browserPass != "" {
		if err := repo.DeleteSSOPassParent(ctx, db, repo.HashAPIKey(browserPass), userID); err != nil {
			return err
		}
	}
	_, err := repo.DeleteAPIKeyByHash(ctx, db, userID, keyHash)
	return err
}

// IssueKey is POST /v1/me/keys: it checks the request (CheckNewKey, a 400
// *Refusal) and records plain as the account's new named key (CreateKey).
// ErrCallerKeyGone when the calling key went meanwhile, ErrKeyLimit when the
// account holds maxKeys already.
func IssueKey(ctx context.Context, db *sql.DB, user *model.User, req access.NewKeyRequest, plain string, maxKeys int, idle time.Duration) (access.KeyInfo, error) {
	spec, err := access.CheckNewKey(req, time.Now())
	if err != nil {
		return access.KeyInfo{}, err
	}
	k, err := access.New(db).CreateKey(ctx, user.ID, user.KeyHash, plain, spec.Name, spec.Scope, spec.ExpiresAt, maxKeys, idle)
	if errors.Is(err, sql.ErrNoRows) {
		return access.KeyInfo{}, access.ErrCallerKeyGone
	}
	return k, err
}

var ErrDisconnectAfterRotation = errors.New("key rotated, but connected apps could not be disconnected; try again")

// RotateKey commits replacement and visitor-session revocation before disconnecting apps.
func RotateKey(ctx context.Context, db *sql.DB, userID, currentKeyHash, newKey string) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := repo.LockKeyIssuer(ctx, tx, userID, currentKeyHash); err != nil {
		return err
	}
	if err := repo.DeleteAllKeys(ctx, tx, userID); err != nil {
		return err
	}
	if err := repo.EndVisitorSessions(ctx, tx, userID); err != nil {
		return err
	}
	if err := repo.AddAPIKey(ctx, tx, userID, newKey, model.KeyNameRotated); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if err := repo.DeleteOAuthGrantsForUser(ctx, db, userID); err != nil {
		log.Printf("rotate key: disconnect apps user_id=%s: %v", userID, err)
		return ErrDisconnectAfterRotation
	}
	return nil
}
