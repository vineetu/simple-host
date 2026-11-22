package access

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/vsriram/simple-host/host/access/repo"
	"github.com/vsriram/simple-host/host/access/rules"
	"github.com/vsriram/simple-host/host/model"
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

// KeyInfoOf is k as a person sees it at now, with idle expiry idle.
func KeyInfoOf(k model.APIKey, now time.Time, idle time.Duration) KeyInfo {
	return KeyInfo{APIKey: k, IdleExpiresAt: rules.IdleExpiresAt(k.IdleFrom, k.LastUsedAt, idle), Expired: rules.KeyExpired(now, k.ExpiresAt, k.IdleFrom, k.LastUsedAt, idle)}
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
	return KeyInfoOf(k, time.Now(), idle), nil
}

// MaxKeyExpiryDays bounds expires_in_days at mint (about ten years).
const MaxKeyExpiryDays = 3650

// NewKeyRequest is what a person asks a new key to be: where it lives, full
// or deploy scope, and optionally a lifetime in days.
type NewKeyRequest struct {
	Name          string `json:"name"`
	Scope         string `json:"scope"`
	ExpiresInDays *int   `json:"expires_in_days"`
}

// NewKeySpec is a checked NewKeyRequest.
type NewKeySpec struct {
	Name, Scope string
	ExpiresAt   *time.Time
}

// CheckNewKey checks a key request: a name is required, the scope is full
// (the default) or deploy, and a lifetime is 1..MaxKeyExpiryDays days from
// now. A refusal is a 400 Refusal with no code.
func CheckNewKey(in NewKeyRequest, now time.Time) (NewKeySpec, error) {
	name, err := NormalizeKeyName(in.Name)
	if err == nil && name == "" {
		err = errors.New("name is required: say where the key will live, e.g. \"GitHub Actions\"")
	}
	if err != nil {
		return NewKeySpec{}, &Refusal{Status: http.StatusBadRequest, Message: err.Error()}
	}
	spec := NewKeySpec{Name: name, Scope: strings.TrimSpace(in.Scope)}
	switch spec.Scope {
	case "":
		spec.Scope = model.KeyScopeFull
	case model.KeyScopeFull, model.KeyScopeDeploy:
	default:
		return NewKeySpec{}, &Refusal{Status: http.StatusBadRequest, Message: `scope must be "full" (the default) or "deploy" (create, update, roll back and list sites, and preview links only)`}
	}
	if in.ExpiresInDays != nil {
		n := *in.ExpiresInDays
		if n < 1 || n > MaxKeyExpiryDays {
			return NewKeySpec{}, &Refusal{Status: http.StatusBadRequest, Message: fmt.Sprintf("expires_in_days must be 1..%d, or left out for a key that expires only when unused", MaxKeyExpiryDays)}
		}
		t := now.Add(time.Duration(n) * 24 * time.Hour).UTC()
		spec.ExpiresAt = &t
	}
	return spec, nil
}
