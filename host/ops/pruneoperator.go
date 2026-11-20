package ops

import (
	"context"
	"database/sql"
	"fmt"
	"io"

	accountapp "github.com/vsriram/simple-host/host/access"
	accounts "github.com/vsriram/simple-host/host/access/repo"
	"github.com/vsriram/simple-host/host/edition"
	"github.com/vsriram/simple-host/host/model"
	"github.com/vsriram/simple-host/host/ops/repo"
	appsites "github.com/vsriram/simple-host/host/sites"
	db "github.com/vsriram/simple-host/host/sites/repo"
)

// PruneFixedAccounts is the hosted one-time cleanup. It deliberately visits
// only non-deleted sites outside KEEP_VERSIONS_SELF_SET, and uses the same
// rows-first, folders-second path as deploys. l is the instance's limits
// snapshot (config.LoadLimits), since this runs outside any server edition.
func PruneFixedAccounts(ctx context.Context, database *sql.DB, disk appsites.Disk, ed *edition.Edition, keepVersions int, apply bool, out io.Writer) error {
	l := ed.Limits()
	if l.KeepVersionsSelfSet == "" || keepVersions <= 0 {
		return fmt.Errorf("cleanup requires KEEP_VERSIONS_SELF_SET and a positive KEEP_VERSIONS")
	}
	sites, err := db.ListAllSites(ctx, database)
	if err != nil {
		return err
	}

	acct := accountapp.New(database)
	pruner := appsites.New(database, disk, nil, 0, appsites.Deps{
		AccountRetention: func(ctx context.Context, id string, siteKeep int) (int, error) {
			keep, _, err := acct.SiteRetention(ctx, id, siteKeep, keepVersions, l)
			return keep, err
		},
		RetainedFloor: func(versions []model.Version, keep, active, siteKeep int) int {
			return appsites.RetainedFloor(versions, keep)
		},
		PinnedFloor: func(_ context.Context, _, _ string, floor int) int { return floor },
	})

	var freed int64
	fmt.Fprintln(out, "account\tsite\tversions removed\tMB freed")
	for _, site := range sites {
		user, err := accounts.GetUserByID(ctx, database, site.UserID)
		if err != nil {
			return err
		}
		if l.CanSetKeepVersions(acct.SiteAccountHandles(ctx, &user)...) {
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
		floor := appsites.RetainedFloor(versions, keepVersions)
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
			if _, err = repo.ResetFixedRetention(ctx, tx, site.ID); err != nil {
				tx.Rollback()
				return err
			}
			removed := pruner.PruneVersions(ctx, appsites.PruneInput{SiteID: site.ID, UserID: site.UserID, SiteName: site.Name, ActiveVersion: active})
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
