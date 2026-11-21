package servertest

import (
	"testing"

	"github.com/vsriram/simple-host/host/config"
)

// withLimits loads the env settings in m into a config.Limits value. The
// caller builds a handler (or edition.ForKindWithLimits) from it directly:
// each handler holds its own snapshot now, so there is no global left to
// apply it to.
func WithLimits(t *testing.T, m map[string]string) config.Limits {
	t.Helper()
	l, err := config.LoadLimits(func(k string) string { return m[k] })
	if err != nil {
		t.Fatal(err)
	}
	return l
}

// setAppLimits overwrites a's already-built handlers' own limits snapshot in
// place, for a test scenario that changes a knob partway through and needs
// the SAME handlers (same caches, locks, deployed state) to see it rather
// than rebuilding the app. This is the direct, same-package analogue of what
// the old config.SetActive/ApplyLimits global allowed mid-test; production
// code never does this (each handler's snapshot is set once, at
// construction, from its edition).
func SetAppLimits(a *PrivateApp, l config.Limits) {
	a.Sites.Limits, a.Access.Limits, a.Data.Limits = l, l, l
	a.Users.Limits = l
	a.Conn.Limits = l
}
