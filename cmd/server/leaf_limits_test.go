package main

import (
	"testing"

	"github.com/vsriram/simple-host/internal/config"
	"github.com/vsriram/simple-host/internal/db"
)

// The independent query defaults must match the configured operational defaults.
func TestLeafLimitDefaultsMatchConfig(t *testing.T) {
	def := config.DefaultLimits()
	if got, want := db.DefaultLimits(), (db.Limits{
		MaxAccountKeys:       def.MaxKeysPerAccount,
		KeyIdleExpiry:        def.KeyIdleExpiry,
		EmailChangeUndoTTL:   def.EmailChangeUndoTTL,
		DeletedRetention:     def.DeletedRetention,
		UnprovenDomainTTL:    def.DomainUnprovenTTL,
		UnprovenDomainMaxAge: def.DomainUnprovenMaxAge,
		DomainCertDailyCap:   def.DomainCertsDaily,
		VisitorSessionIdle:   def.VisitorSessionIdle,
		OAuthUnusedClientAge: def.OAuthUnusedClientAge,
	}); got != want {
		t.Errorf("db defaults %+v, config says %+v", got, want)
	}
}
