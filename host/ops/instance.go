package ops

import (
	"context"
	"database/sql"
	"errors"

	opsrepo "github.com/vsriram/simple-host/host/ops/repo"
)

// InstanceConfigured reads the hostnames the setup page saved; empty means
// setup is unfinished. Apply host/db/migrations/instance-config.sql before
// calling this on older databases.
func InstanceConfigured(ctx context.Context, db *sql.DB) (siteDomain, contentHost string, err error) {
	return opsrepo.InstanceHosts(ctx, db)
}

// ErrSetupDone: setup had already finished, so nothing was saved.
var ErrSetupDone = errors.New("setup is already complete")

// ErrSetupUnconfirmed: saving may or may not have landed.
var ErrSetupUnconfirmed = opsrepo.ErrUnconfirmed

// FinishSetup saves the instance's two hostnames once, in one locked
// transaction: ErrSetupDone when another request finished setup first.
func FinishSetup(ctx context.Context, db *sql.DB, siteDomain, contentHost string) error {
	saved, err := opsrepo.SaveInstanceHosts(ctx, db, siteDomain, contentHost)
	if err == nil && !saved {
		return ErrSetupDone
	}
	return err
}
