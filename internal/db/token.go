package db

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
)

// tokenBytes is the raw entropy in every issued secret (session cookie,
// establish `once`, OAuth `state`). Encoded as hex on the wire.
const tokenBytes = 32

// ErrTokenTooShort is returned when an insert is given a secret with
// fewer than tokenBytes of entropy (hex-decoded).
var ErrTokenTooShort = errors.New("token too short")

// NewToken returns a hex-encoded 32-byte value from crypto/rand.
// The returned string is what we send to the client; only sha256 of it
// is ever stored.
func NewToken() (string, error) {
	b := make([]byte, tokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// HashToken is sha256 of a presented secret (cookie value, `once` query
// param, or OAuth `state`). Lookup is by this digest so Go never compares
// the secret itself.
func HashToken(presented string) []byte {
	sum := sha256.Sum256([]byte(presented))
	return sum[:]
}

// HashTokenHex is HashToken encoded as lowercase hex, for TEXT primary keys.
func HashTokenHex(presented string) string {
	return hex.EncodeToString(HashToken(presented))
}

// requirePresentedToken checks the client-facing secret has at least
// tokenBytes of entropy. Callers generate via NewToken / randomHex(32).
func requirePresentedToken(secret string) error {
	raw, err := hex.DecodeString(secret)
	if err != nil || len(raw) < tokenBytes {
		return ErrTokenTooShort
	}
	return nil
}
