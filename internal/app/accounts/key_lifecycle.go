package accounts

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/vsriram/simple-host/internal/model"
	repo "github.com/vsriram/simple-host/internal/repository/accounts"
	rules "github.com/vsriram/simple-host/internal/rules/auth"
)

var ErrKeyLimit = model.ErrKeyLimit

const maxKeyNameLen = 60

func NormalizeKeyName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if utf8.RuneCountInString(name) > maxKeyNameLen {
		return "", errors.New("name must be at most 60 characters")
	}
	// Cf covers zero-width and bidi overrides (U+200B, U+202E), which make a
	// name read differently from what it is.
	if strings.IndexFunc(name, func(r rune) bool { return unicode.IsControl(r) || unicode.Is(unicode.Cf, r) }) >= 0 {
		return "", errors.New("name must not contain control or invisible formatting characters")
	}
	if reservedKeyName(name) {
		return "", errors.New("that name is used by Simple Host for its own keys; pick another")
	}
	return name, nil
}

func reservedKeyName(name string) bool {
	n := strings.ToLower(strings.TrimSpace(name))
	for _, r := range []string{model.KeyNameDashboard, model.KeyNameAgent, model.KeyNameEvent, model.KeyNameReviewer, model.KeyNameRotated} {
		if n == r {
			return true
		}
	}
	return false
}

type KeyInfo struct {
	model.APIKey
	IdleExpiresAt *time.Time
	Expired       string
}

func keyInfo(k model.APIKey, now time.Time, idle time.Duration) KeyInfo {
	return KeyInfo{APIKey: k, IdleExpiresAt: rules.IdleExpiresAt(k.IdleFrom, k.LastUsedAt, idle), Expired: rules.KeyExpired(now, k.ExpiresAt, k.IdleFrom, k.LastUsedAt, idle)}
}
func (s *Service) ListKeys(ctx context.Context, userID string, idle time.Duration) ([]KeyInfo, error) {
	keys, err := repo.ListAPIKeys(ctx, s.database, userID)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	out := make([]KeyInfo, 0, len(keys))
	for _, k := range keys {
		out = append(out, keyInfo(k, now, idle))
	}
	return out, nil
}
func (s *Service) CreateKey(ctx context.Context, userID, callerKeyHash, plain, name, scope string, expiresAt *time.Time, maxKeys int, idle time.Duration) (KeyInfo, error) {
	tx, err := s.database.BeginTx(ctx, nil)
	if err != nil {
		return KeyInfo{}, err
	}
	defer tx.Rollback()
	if err = repo.LockKeyIssuer(ctx, tx, userID, callerKeyHash); err != nil {
		return KeyInfo{}, err
	}
	n, err := repo.CountLiveKeys(ctx, tx, userID, idle)
	if err != nil {
		return KeyInfo{}, err
	}
	if n >= maxKeys {
		return KeyInfo{}, ErrKeyLimit
	}
	if scope == "" {
		scope = model.KeyScopeFull
	}
	last4 := ""
	if len(plain) >= 4 {
		last4 = plain[len(plain)-4:]
	}
	k, err := repo.InsertNamedKey(ctx, tx, userID, model.APIKey{Hash: repo.HashAPIKey(plain), Name: name, Last4: last4, Scope: scope, ExpiresAt: expiresAt})
	if err != nil {
		return KeyInfo{}, err
	}
	if err = tx.Commit(); err != nil {
		return KeyInfo{}, err
	}
	return keyInfo(k, time.Now(), idle), nil
}
func (s *Service) DeleteKey(ctx context.Context, userID, keyID string) (bool, error) {
	return repo.DeleteAPIKey(ctx, s.database, userID, keyID)
}

// SignOut retires the browser's SSO parent first, then the request's key. These remain separate writes.
func (s *Service) SignOut(ctx context.Context, userID, keyHash, browserPass string) error {
	if browserPass != "" {
		if err := repo.DeleteSSOPassParent(ctx, s.database, repo.HashAPIKey(browserPass), userID); err != nil {
			return err
		}
	}
	_, err := repo.DeleteAPIKeyByHash(ctx, s.database, userID, keyHash)
	return err
}
