package accounts

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

type InternalCred struct {
	UserID  string
	Visitor *InternalVisitorPass
	TeamID  string // set: the person acts on their team's site only (hack_sites.go)
	Expires time.Time
}

var internalKeys sync.Map // key -> InternalCred

// IssueInternalKey mints an in-process credential for userID. Call the
// returned revoke when the request that needed it is done.
func IssueInternalKey(userID string) (key string, revoke func(), err error) {
	return issueInternal(userID, "", nil)
}

// IssueInternalTeamKey mints an in-process credential that acts like a team
// key of userID for teamID (a connector connection bound to a team site).
func IssueInternalTeamKey(userID, teamID string) (key string, revoke func(), err error) {
	return issueInternal(userID, teamID, nil)
}

func issueInternal(userID, teamID string, visitor *InternalVisitorPass) (key string, revoke func(), err error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", nil, err
	}
	key = internalKeyPrefix + hex.EncodeToString(b)
	internalKeys.Store(key, InternalCred{UserID: userID, TeamID: teamID, Visitor: visitor, Expires: time.Now().Add(internalKeyTTL)})
	sweepInternalKeys()
	return key, func() { internalKeys.Delete(key) }, nil
}

func LookupInternalKey(key string) (string, bool) {
	c, ok := LookupInternalCred(key)
	return c.UserID, ok && c.Visitor == nil
}

func LookupInternalCred(key string) (InternalCred, bool) {
	v, ok := internalKeys.Load(key)
	if !ok {
		return InternalCred{}, false
	}
	c := v.(InternalCred)
	if time.Now().After(c.Expires) {
		internalKeys.Delete(key)
		return InternalCred{}, false
	}
	return c, true
}

// LookupInternalTeamKey: key is a live internal credential bound to a team.
func LookupInternalTeamKey(key string) (memberID, teamID string, ok bool) {
	c, ok := LookupInternalCred(key)
	if !ok || c.TeamID == "" {
		return "", "", false
	}
	return c.UserID, c.TeamID, true
}

// sweepInternalKeys drops expired entries; a revoke that never ran (a panic)
// must not leave a credential behind for long.
func sweepInternalKeys() {
	now := time.Now()
	internalKeys.Range(func(k, v any) bool {
		if now.After(v.(InternalCred).Expires) {
			internalKeys.Delete(k)
		}
		return true
	})
}

// InternalVisitorPass has no owner or admin authority. It is only accepted by
// Visitor handlers for this site and host, for the lifetime of one tool call.
type InternalVisitorPass struct {
	UserID, SiteID, Host, AppName string
}

func IssueInternalVisitorKey(pass InternalVisitorPass) (string, func(), error) {
	return issueInternal(pass.UserID, "", &pass)
}

func LookupInternalVisitorKey(key string) (InternalVisitorPass, bool) {
	c, ok := LookupInternalCred(key)
	if !ok || c.Visitor == nil {
		return InternalVisitorPass{}, false
	}
	return *c.Visitor, true
}
