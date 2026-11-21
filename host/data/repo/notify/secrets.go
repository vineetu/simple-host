package notify

import (
	"context"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"strings"

	accounts "github.com/vsriram/simple-host/host/access"
	sitesrepo "github.com/vsriram/simple-host/host/sites/repo"
	"github.com/vsriram/simple-host/host/sqlkit"
)

// Secrets seals and opens a subscription's stored secrets with
// PASSCODE_ENC_KEY's cipher (nil: unset), so the stored rows only ever hold
// sealed values.
type Secrets struct{ AEAD cipher.AEAD }

// ErrNoKey is a seal without PASSCODE_ENC_KEY.
var ErrNoKey = errors.New("PASSCODE_ENC_KEY is not set")

var errSealed = errors.New("sealed value unreadable")

// Seal seals one secret of subscription id ("" stays "").
func (s Secrets) Seal(id, plaintext string) (string, error) {
	if plaintext == "" {
		return "", nil
	}
	return s.SealBound("notification:"+id, plaintext)
}

// Open opens a secret Seal stored; a legacy plaintext value passes through
// unchanged.
func (s Secrets) Open(id, stored string) (string, error) {
	if !strings.HasPrefix(stored, "enc1:") {
		return stored, nil
	}
	return s.OpenBound("notification:"+id, stored)
}

// SealBound is "enc1:" + base64(nonce | AES-GCM ciphertext) bound to ad.
func (s Secrets) SealBound(ad, plaintext string) (string, error) {
	if s.AEAD == nil {
		return "", ErrNoKey
	}
	nonce := make([]byte, s.AEAD.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	return "enc1:" + base64.StdEncoding.EncodeToString(s.AEAD.Seal(nonce, nonce, []byte(plaintext), []byte(ad))), nil
}

// OpenBound reverses SealBound; every failure is errSealed (ErrNoKey
// without a key).
func (s Secrets) OpenBound(ad, stored string) (string, error) {
	encoded, ok := strings.CutPrefix(stored, "enc1:")
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if !ok || err != nil {
		return "", errSealed
	}
	if s.AEAD == nil {
		return "", ErrNoKey
	}
	n := s.AEAD.NonceSize()
	if len(raw) < n+s.AEAD.Overhead() {
		return "", errSealed
	}
	plain, err := s.AEAD.Open(nil, raw[:n], raw[n:], []byte(ad))
	if err != nil {
		return "", errSealed
	}
	return string(plain), nil
}

// sealLegacy preserves ciphertext and empty values, including during rotation.
func (s Secrets) sealLegacy(id, value string) (string, error) {
	if value == "" || strings.HasPrefix(value, "enc1:") {
		return value, nil
	}
	return s.Seal(id, value)
}

// MayActOnSite reports whether person may still keep a subscription on the
// site: they own it, or may act as its owner (accounts.ActsAsOwner: a member
// of the team that holds it, or an editor of the site).
func MayActOnSite(ctx context.Context, q sqlkit.Querier, siteID, person string) (bool, error) {
	site, err := sitesrepo.GetSiteByID(ctx, q, siteID)
	if err != nil {
		return false, err
	}
	return accounts.ActsAsOwner(ctx, q, person, site.UserID, site.Name)
}
