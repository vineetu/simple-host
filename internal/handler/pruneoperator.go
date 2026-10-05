package handler

import (
	"context"
	"database/sql"
	"fmt"
	"io"

	"github.com/vsriram/simple-host/internal/config"
	"github.com/vsriram/simple-host/internal/db"
	"github.com/vsriram/simple-host/internal/storage"
)

// PruneFixedAccounts is the hosted one-time cleanup. It deliberately visits
// only non-deleted sites outside KEEP_VERSIONS_SELF_SET, and uses the same
// rows-first, folders-second path as deploys.
func PruneFixedAccounts(ctx context.Context, database *sql.DB, disk *storage.DiskStorage, apply bool, out io.Writer) error {
	l := config.Active()
	if l.KeepVersionsSelfSet == "" || keepVersions <= 0 {
		return fmt.Errorf("cleanup requires KEEP_VERSIONS_SELF_SET and a positive KEEP_VERSIONS")
	}
	sites, err := db.ListAllSites(ctx, database)
	if err != nil {
		return err
	}
	h := &SiteHandler{database: database, disk: disk}
	var freed int64
	fmt.Fprintln(out, "account\tsite\tversions removed\tMB freed")
	for _, site := range sites {
		user, err := db.GetUserByID(ctx, database, site.UserID)
		if err != nil {
			return err
		}
		if l.CanSetKeepVersions(accountHandles(ctx, database, &user)...) {
			continue
		}
		// Keep the site locked in the DB while previewing and pruning: a rollback
		// in the service cannot start copying a candidate beneath this operation.
		tx, err := database.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if err = db.LockSiteForUpdate(ctx, tx, site.ID); err != nil {
			tx.Rollback()
			if err == sql.ErrNoRows {
				continue
			}
			return err
		}
		active, err := db.SiteActiveVersion(ctx, tx, site.ID)
		if err != nil {
			tx.Rollback()
			return err
		}
		versions, err := db.ListVersionsBySite(ctx, database, site.ID)
		if err != nil {
			tx.Rollback()
			return err
		}
		floor := retainedFloor(versions, keepVersions)
		f, err := disk.SiteFootprint(site.UserID, site.Name)
		if err != nil {
			tx.Rollback()
			return err
		}
		var remove []int
		var bytes int64
		for _, v := range versions {
			if floor > 0 && v.VersionNumber < floor && v.VersionNumber != active {
				remove = append(remove, v.VersionNumber)
				bytes += f.Versions[v.VersionNumber]
			}
		}
		if apply {
			if _, err = tx.ExecContext(ctx, `UPDATE sites SET keep_versions=0 WHERE id=$1`, site.ID); err != nil {
				tx.Rollback()
				return err
			}
			removed := h.pruneVersions(ctx, site.ID, site.UserID, site.Name, active, 0)
			if len(removed) != len(remove) {
				tx.Rollback()
				return fmt.Errorf("prune %s: expected %d removed rows, got %d", site.Name, len(remove), len(removed))
			}
			after, err := disk.SiteFootprint(site.UserID, site.Name)
			if err != nil {
				tx.Rollback()
				return err
			}
			if f.Total()-after.Total() != bytes {
				tx.Rollback()
				return fmt.Errorf("prune %s: folders did not free expected bytes", site.Name)
			}
			if err = tx.Commit(); err != nil {
				return err
			}
		} else {
			tx.Rollback()
		}
		if len(remove) > 0 {
			fmt.Fprintf(out, "%s\t%s\t%v\t%.2f\n", user.Handle.String, site.Name, remove, float64(bytes)/(1<<20))
			freed += bytes
		}
	}
	fmt.Fprintf(out, "TOTAL\t%.2f MB\n", float64(freed)/(1<<20))
	return nil
}
