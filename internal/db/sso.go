package db

import (
	"context"
	"database/sql"
	"github.com/vsriram/simple-host/internal/model"
	"github.com/vsriram/simple-host/internal/repository/accounts"
	"time"
)

type SSOPass = model.SSOPass
type SSOHop = model.SSOHop

// ReplaceSSOPass serializes issuance on the parent key, retaining at most one
// pass per key. Replacing a browser's previous pass retires that key's
// family. keyIdle (KEY_IDLE_EXPIRY_DAYS) decides whether the parent key is
// still live; ttl and idle are the pass's own visitor-session-like limits.
func ReplaceSSOPass(ctx context.Context, database *sql.DB, hash, keyHash, oldHash string, ttl, idle, keyIdle time.Duration) error {
	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	err = accounts.ReplaceSSOPass(ctx, tx, hash, keyHash, oldHash, ttl, idle, keyIdle)
	if err != nil {
		return err
	}
	return tx.Commit()
}

// GetSSOPass loads a pass, live only while its parent key is (keyIdle,
// KEY_IDLE_EXPIRY_DAYS).
func GetSSOPass(ctx context.Context, q Querier, hash string, keyIdle time.Duration) (SSOPass, error) {
	return accounts.GetSSOPass(ctx, q, hash, keyIdle)
}

func RememberSSOConsent(ctx context.Context, q Querier, userID, siteID string) error {
	return accounts.RememberSSOConsent(ctx, q, userID, siteID)
}

type SSOSignout = model.SSOSignout

func SweepSSO(ctx context.Context, q Querier) error {
	_, err := accounts.SweepSSOCount(ctx, q)
	return err
}
