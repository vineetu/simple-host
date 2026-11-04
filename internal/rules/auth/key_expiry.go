package auth

import (
	"time"
)

func KeyExpired(now time.Time, expiresAt *time.Time, idleFrom time.Time, lastUsed *time.Time, keyIdle time.Duration) string {
	if expiresAt != nil && !now.Before(*expiresAt) {
		return KeyExpiredFixed
	}
	if t := IdleExpiresAt(idleFrom, lastUsed, keyIdle); t != nil && !now.Before(*t) {
		return KeyExpiredIdle
	}
	return ""
}

func IdleExpiresAt(idleFrom time.Time, lastUsed *time.Time, keyIdle time.Duration) *time.Time {
	if keyIdle <= 0 {
		return nil
	}
	from := idleFrom
	if lastUsed != nil && lastUsed.After(from) {
		from = *lastUsed
	}
	t := from.Add(keyIdle)
	return &t
}

const KeyExpiredFixed = "expired"
const KeyExpiredIdle = "idle"

const KeyScopeDeploy = "deploy"
const KeyScopeTeam = "team"
