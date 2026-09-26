package db

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"
)

// Internal credentials let in-process callers (the MCP connector, and a
// connector access token used on the REST API) act as an already
// authenticated person without holding that person's API key, which is only
// stored as a hash. One is minted per request, travels only inside the
// process as an X-API-Key value, and is revoked when the request ends; the
// TTL is a backstop.
const internalKeyPrefix = "shint_"

const internalKeyTTL = 15 * time.Minute

type internalCred struct {
	userID  string
	expires time.Time
}

var internalKeys sync.Map // key -> internalCred

// IssueInternalKey mints an in-process credential for userID. Call the
// returned revoke when the request that needed it is done.
func IssueInternalKey(userID string) (key string, revoke func(), err error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", nil, err
	}
	key = internalKeyPrefix + hex.EncodeToString(b)
	internalKeys.Store(key, internalCred{userID: userID, expires: time.Now().Add(internalKeyTTL)})
	sweepInternalKeys()
	return key, func() { internalKeys.Delete(key) }, nil
}

func lookupInternalKey(key string) (string, bool) {
	v, ok := internalKeys.Load(key)
	if !ok {
		return "", false
	}
	c := v.(internalCred)
	if time.Now().After(c.expires) {
		internalKeys.Delete(key)
		return "", false
	}
	return c.userID, true
}

// sweepInternalKeys drops expired entries; a revoke that never ran (a panic)
// must not leave a credential behind for long.
func sweepInternalKeys() {
	now := time.Now()
	internalKeys.Range(func(k, v any) bool {
		if now.After(v.(internalCred).expires) {
			internalKeys.Delete(k)
		}
		return true
	})
}
