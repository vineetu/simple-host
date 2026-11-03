package db

import (
	"time"
)

// Limits are the operational times and caps the queries in this package
// enforce, documented here in one place though each function now takes the
// few it needs as explicit parameters (from the caller's own
// edition.Edition.Limits()) rather than reading a package global. They come
// from internal/config (docs/configuration.md), which parses and checks the
// env vars. DefaultLimits is the long-standing behaviour, checked against
// config.DefaultLimits by internal/handler's tests.
type Limits struct {
	MaxAccountKeys       int           // MAX_KEYS_PER_ACCOUNT
	KeyIdleExpiry        time.Duration // KEY_IDLE_EXPIRY_DAYS (0 = never)
	EmailChangeUndoTTL   time.Duration // EMAIL_CHANGE_UNDO_DAYS
	DeletedRetention     time.Duration // DELETED_RETENTION_DAYS
	UnprovenDomainTTL    time.Duration // DOMAIN_UNPROVEN_HOURS
	UnprovenDomainMaxAge time.Duration // DOMAIN_UNPROVEN_MAX_DAYS
	DomainCertDailyCap   int           // DOMAIN_CERTS_PER_ACCOUNT_DAILY
	VisitorSessionIdle   time.Duration // VISITOR_SESSION_IDLE_DAYS
	OAuthUnusedClientAge time.Duration // OAUTH_UNUSED_CLIENT_DAYS
}

// DefaultLimits is today's behaviour. internal/handler's tests check it
// matches config.DefaultLimits.
func DefaultLimits() Limits {
	return Limits{
		MaxAccountKeys:       50,
		KeyIdleExpiry:        180 * 24 * time.Hour,
		EmailChangeUndoTTL:   7 * 24 * time.Hour,
		DeletedRetention:     7 * 24 * time.Hour,
		UnprovenDomainTTL:    24 * time.Hour,
		UnprovenDomainMaxAge: 7 * 24 * time.Hour,
		DomainCertDailyCap:   5,
		VisitorSessionIdle:   14 * 24 * time.Hour,
		OAuthUnusedClientAge: 30 * 24 * time.Hour,
	}
}
