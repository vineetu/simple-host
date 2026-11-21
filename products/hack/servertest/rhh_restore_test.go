package servertest

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/vsriram/simple-host/host/sites"
	siterepo "github.com/vsriram/simple-host/host/sites/repo"
	"github.com/vsriram/simple-host/products/hack/app/teams"
	"github.com/vsriram/simple-host/products/hack/repository/hackteams"
)

// The disk wrapper observes the boundary after marker creation and before the
// rename that exposes the files. No production-only testing hook is needed.
type rhhObservedDisk struct {
	fixtureDisk
	beforeRestore func()
	markerError   error
	restores      int
}

func (d *rhhObservedDisk) SetTrashedSuspended(userID, siteID string, on bool) error {
	if d.markerError != nil {
		return d.markerError
	}
	return d.fixtureDisk.SetTrashedSuspended(userID, siteID, on)
}

func (d *rhhObservedDisk) RestoreSite(userID, name, siteID string) error {
	d.restores++
	if d.beforeRestore != nil {
		d.beforeRestore()
	}
	return d.fixtureDisk.RestoreSite(userID, name, siteID)
}

type rhhGate struct {
	teams.SiteGate
	suspension func(context.Context, *sql.Tx, siterepo.Site) (string, string, error)
}

func (g rhhGate) Suspension(ctx context.Context, tx *sql.Tx, site siterepo.Site) (string, string, error) {
	return g.suspension(ctx, tx, site)
}

// rhhRestoreSeed is a team site in Recently deleted, made through the front
// door, with the team's id and a person to own it when a case needs a
// personal site.
func rhhRestoreSeed(t *testing.T) (*sql.DB, *rhhObservedDisk, siterepo.DeletedSite, string, string) {
	t.Helper()
	a := newTeamSiteApp(t)
	org, member := a.newPerson(t, "org"), a.newPerson(t, "member")
	slug := a.makeEvent(t, org)
	a.join(t, slug, member, org)
	team, _ := a.startTeam(t, slug, "Trash", member)
	markReady(t, a.certDir, slug)
	wantTS(t, "deploy", a.deployTeam(t, slug, team, member.key, "trash content"), 201, "")
	ctx := context.Background()
	var accountID, teamID string
	if err := a.database.QueryRow(`SELECT e.account_id, t.id FROM events e JOIN event_teams t ON t.event_id=e.id WHERE e.slug=$1 AND t.slug=$2`, slug, team).Scan(&accountID, &teamID); err != nil {
		t.Fatal(err)
	}
	if err := a.sites.TrashTeamSite(ctx, accountID, team); err != nil {
		t.Fatal(err)
	}
	d, err := siterepo.GetDeletedSiteByUser(ctx, a.database, accountID, team)
	if err != nil {
		t.Fatal(err)
	}
	return a.database, &rhhObservedDisk{fixtureDisk: a.disk}, d, teamID, a.uid(t, member)
}

func TestRhhRestoreSuspensionSeed(t *testing.T) {
	const prefix = "Taken down by the event organiser"
	ctx := context.Background()
	for _, tc := range []struct {
		name, current, want                       string
		down, owner, personal, nilGate, noOpinion bool
	}{
		{name: "taken down in trash", down: true, want: prefix + ": spam"},
		{name: "lifted in trash", current: prefix + ": old"},
		{name: "lifted with suspended holder", current: prefix + ": old", owner: true},
		{name: "operator and organiser", current: "operator reason", down: true, want: "operator reason"},
		{name: "operator after organiser lifted", current: "operator reason", want: "operator reason"},
		{name: "personal platform only", personal: true, nilGate: true},
		{name: "personal with product", personal: true, current: "operator reason", want: "operator reason"},
		{name: "empty prefix has no opinion", current: prefix + ": old", noOpinion: true, want: prefix + ": old"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, disk, d, teamID, person := rhhRestoreSeed(t)
			if tc.personal {
				// Make the deleted site personal, with no corresponding team.
				oldUser := d.UserID
				d.UserID = person
				if _, err := db.Exec(`UPDATE sites SET user_id=$1 WHERE id=$2`, d.UserID, d.ID); err != nil {
					t.Fatal(err)
				}
				if err := os.MkdirAll(filepath.Dir(disk.TrashDir(d.UserID, d.ID)), 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.Rename(disk.TrashDir(oldUser, d.ID), disk.TrashDir(d.UserID, d.ID)); err != nil {
					t.Fatal(err)
				}
			}
			if err := siterepo.SetSiteSuspended(ctx, db, d.ID, tc.current); err != nil {
				t.Fatal(err)
			}
			if tc.owner {
				if _, err := db.Exec(`UPDATE users SET suspended_at=now(), suspended_reason='holder reason' WHERE id=$1`, d.UserID); err != nil {
					t.Fatal(err)
				}
			}
			if err := disk.SetTrashedSuspended(d.UserID, d.ID, tc.current != "" || tc.owner); err != nil {
				t.Fatal(err)
			}
			deps := sites.Deps{GroupGate: teams.SiteGate{}, Locks: &sync.Map{}}
			if tc.nilGate {
				deps.GroupGate = nil
			}
			if tc.noOpinion {
				deps.GroupGate = rhhGate{suspension: func(context.Context, *sql.Tx, siterepo.Site) (string, string, error) {
					return "", "ignored", nil
				}}
			}
			svc := sites.New(db, disk, nil, 0, deps)
			if !tc.personal && !tc.noOpinion {
				// Exercise the same site-then-team mutation as organiser moderation;
				// the deleted site is deliberately not found by LockSiteTakedown.
				if err := svc.SuspendHeldSite(ctx, sites.HeldSiteSuspension{AccountID: d.UserID, Name: d.Name, Prefix: prefix, On: tc.down, Reason: "spam", Also: func(tx *sql.Tx) error {
					return hackteams.SetTeamSiteTakedown(ctx, tx, teamID, tc.down, "spam")
				}}); err != nil {
					t.Fatal(err)
				}
			}
			wantSuspended := tc.want != "" || tc.owner
			disk.beforeRestore = func() {
				_, err := os.Stat(filepath.Join(disk.TrashDir(d.UserID, d.ID), "suspended"))
				if (err == nil) != wantSuspended || (err != nil && !os.IsNotExist(err)) {
					t.Fatalf("marker before files move: %v, want suspended=%v", err, wantSuspended)
				}
			}
			got, err := svc.RestoreTrashed(ctx, sites.RestoreTrashedInput{Site: d})
			if err != nil {
				t.Fatal(err)
			}
			if got.Deleted || got.SiteSuspendedReason != tc.want || got.Suspended() != wantSuspended || disk.IsSuspended(d.UserID, d.Name) != wantSuspended || disk.restores != 1 {
				t.Fatalf("restored state: reason=%q suspended=%v owner=%v deleted=%v moves=%d", got.SiteSuspendedReason, got.Suspended(), got.OwnerSuspended, got.Deleted, disk.restores)
			}
			if tc.owner && (got.SiteSuspended || got.OwnerSuspendedReason != "holder reason") {
				t.Fatal("lifting organiser suspension changed the holder suspension")
			}
		})
	}
}

func TestRhhRestoreSuspensionFailure(t *testing.T) {
	ctx := context.Background()
	for _, failure := range []string{"hook", "row update", "marker"} {
		t.Run(failure, func(t *testing.T) {
			db, disk, d, _, _ := rhhRestoreSeed(t)
			injected := errors.New("restore suspension failed")
			gate := rhhGate{suspension: func(ctx context.Context, tx *sql.Tx, site siterepo.Site) (string, string, error) {
				if site.Deleted || site.ID != d.ID {
					t.Fatal("hook ran before RestoreDeletedSite")
				}
				if failure == "hook" {
					return "", "", injected
				}
				return "Taken down by the event organiser", "Taken down by the event organiser: spam", nil
			}}
			if failure == "row update" {
				if _, err := db.Exec(`CREATE FUNCTION reject_restore_suspension() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected suspension update failure'; END $$;
					CREATE TRIGGER reject_restore_suspension BEFORE UPDATE OF suspended_at ON sites FOR EACH ROW WHEN (OLD.id = '` + d.ID + `') EXECUTE FUNCTION reject_restore_suspension()`); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					_, _ = db.Exec(`DROP TRIGGER reject_restore_suspension ON sites; DROP FUNCTION reject_restore_suspension()`)
				})
			}
			if failure == "marker" {
				disk.markerError = injected
			}
			svc := sites.New(db, disk, nil, 0, sites.Deps{GroupGate: gate})
			_, err := svc.RestoreTrashed(ctx, sites.RestoreTrashedInput{Site: d})
			if err == nil || (failure == "hook" && !errors.Is(err, injected)) || (failure == "marker" && !errors.Is(err, sites.ErrRestoreFiles)) {
				t.Fatalf("restore error: %v", err)
			}
			got, err := siterepo.GetSiteByID(ctx, db, d.ID)
			if err != nil || !got.Deleted || got.SiteSuspended || got.Offline || disk.restores != 0 {
				t.Fatalf("failed restore was not rolled back: %+v moves=%d err=%v", got, disk.restores, err)
			}
			if _, err := os.Stat(filepath.Join(disk.TrashDir(d.UserID, d.ID), "current", "index.html")); err != nil {
				t.Fatalf("deleted files moved: %v", err)
			}
		})
	}
}
