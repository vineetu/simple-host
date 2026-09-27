package db

import (
	"sync/atomic"
	"time"
)

// Limits are the operational times and caps the queries in this package
// enforce. They come from internal/config (docs/configuration.md), which
// parses and checks the env vars; handler.ApplyLimits installs them at
// startup. The zero state is today's values, so a caller that never sets them
// (a test, a tool) sees the long-standing behaviour.
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

var limits atomic.Pointer[Limits]

func init() { SetLimits(DefaultLimits()) }

// SetLimits installs l. Call at startup, before serving.
func SetLimits(l Limits) { limits.Store(&l) }

func lim() *Limits { return limits.Load() }
