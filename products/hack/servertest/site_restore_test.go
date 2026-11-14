package servertest

import (
	"context"
	"database/sql"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vsriram/simple-host/internal/app/sites"
	"github.com/vsriram/simple-host/internal/extension"
	siterepo "github.com/vsriram/simple-host/internal/repository/sites"
	platformtest "github.com/vsriram/simple-host/internal/server/servertest"
	"github.com/vsriram/simple-host/internal/storage"
	"github.com/vsriram/simple-host/internal/testdb"
	"github.com/vsriram/simple-host/products/hack/app/teams"
	"github.com/vsriram/simple-host/products/hack/repository/hackevents"
	"github.com/vsriram/simple-host/products/hack/repository/hackteams"
)

// Exercise the services built by the handler after Build installs the hook.
// A test product supplies suspension decisions for an ordinary owner's site:
// Hack's scoped team credentials cannot restore a taken-down team site today.
func TestRestoreSuspensionInstalledAfterBuild(t *testing.T) {
	const prefix = "Taken down by the event organiser"
	for _, transport := range []string{"REST", "MCP"} {
		for _, tc := range []struct {
			name, current, reason string
			fail, nilGate         bool
		}{
			{name: "taken down in trash", reason: prefix + ": spam"},
			{name: "lifted in trash", current: prefix + ": old"},
			{name: "hook error", fail: true},
			{name: "platform only", current: "operator reason", nilGate: true},
		} {
			t.Run(transport+"/"+tc.name, func(t *testing.T) {
				cfg := platformtest.Config(t, testdb.Fresh(t))
				var db *sql.DB
				var disk *storage.DiskStorage
				calls := 0
				ext := &extension.Extension{Name: "restore-test", Wire: func(d extension.Deps) (extension.Parts, error) {
					db, disk = d.DB, d.Disk
					var parts extension.Parts
					if !tc.nilGate {
						parts.Hooks.GroupGate = restoreGate{suspension: func(ctx context.Context, tx *sql.Tx, site siterepo.Site) (string, string, error) {
							calls++
							if tx == nil || site.Deleted || site.Name != "restore-wiring" {
								t.Fatal("suspension hook did not receive the restored site in its transaction")
							}
							if tc.fail {
								return "", "", errors.New("injected suspension failure")
							}
							return prefix, tc.reason, nil
						}}
					}
					return parts, nil
				}}
				app := platformtest.Build(t, cfg, []*extension.Extension{ext})
				request := func(method, path, body string) *httptest.ResponseRecorder {
					t.Helper()
					r := httptest.NewRequest(method, cfg.PublicBaseURL+path, strings.NewReader(body))
					r.Header.Set("X-API-Key", cfg.AdminAPIKey)
					r.Header.Set("Content-Type", "application/json")
					r.Header.Set("MCP-Protocol-Version", "2025-06-18")
					w := httptest.NewRecorder()
					if path == "/mcp" {
						app.Mux.ServeHTTP(w, r)
					} else {
						app.Handler.ServeHTTP(w, r)
					}
					return w
				}
				w := request("POST", "/v1/sites/restore-wiring/files", `{"files":{"index.html":"restore wiring content"}}`)
				if w.Code != 201 {
					t.Fatalf("create: %d %s", w.Code, w.Body)
				}
				w = request("DELETE", "/v1/sites/restore-wiring", "")
				if w.Code != 204 {
					t.Fatalf("delete: %d %s", w.Code, w.Body)
				}
				var siteID string
				if err := db.QueryRow(`SELECT id FROM sites WHERE name='restore-wiring'`).Scan(&siteID); err != nil {
					t.Fatal(err)
				}
				ctx := context.Background()
				if err := siterepo.SetSiteSuspended(ctx, db, siteID, tc.current); err != nil {
					t.Fatal(err)
				}
				before, err := siterepo.GetSiteByID(ctx, db, siteID)
				if err != nil {
					t.Fatal(err)
				}
				if err := disk.SetTrashedSuspended(before.UserID, siteID, before.Suspended()); err != nil {
					t.Fatal(err)
				}
				if transport == "REST" {
					w = request("POST", "/v1/sites/restore-wiring/restore", "")
				} else {
					w = request("POST", "/mcp", `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"restore_site","arguments":{"site":"restore-wiring"}}}`)
				}
				wantCalls := 1
				if tc.nilGate {
					wantCalls = 0
				}
				if calls != wantCalls {
					t.Fatalf("Suspension calls=%d, want %d: %d %s", calls, wantCalls, w.Code, w.Body)
				}
				got, err := siterepo.GetSiteByID(ctx, db, siteID)
				if err != nil {
					t.Fatal(err)
				}
				if tc.fail {
					if (transport == "REST" && w.Code != 500) || (transport == "MCP" && !strings.Contains(w.Body.String(), `"isError":true`)) || !got.Deleted {
						t.Fatalf("failed restore: deleted=%v response=%d %s", got.Deleted, w.Code, w.Body)
					}
					if _, err := os.Stat(filepath.Join(disk.TrashDir(got.UserID, siteID), "current", "index.html")); err != nil {
						t.Fatalf("failed restore moved files: %v", err)
					}
					return
				}
				wantReason := tc.reason
				if tc.nilGate {
					wantReason = tc.current
				}
				if w.Code != 200 || strings.Contains(w.Body.String(), `"isError":true`) || got.Deleted || got.SiteSuspendedReason != wantReason || disk.IsSuspended(got.UserID, got.Name) != (wantReason != "") {
					t.Fatalf("restore: deleted=%v reason=%q marker=%v response=%d %s", got.Deleted, got.SiteSuspendedReason, disk.IsSuspended(got.UserID, got.Name), w.Code, w.Body)
				}
			})
		}
	}
}

func TestRestoreTrashedTeamKeepsTakedown(t *testing.T) {
	a := newTeamSiteApp(t)
	org, member := a.newPerson(t, "org"), a.newPerson(t, "member")
	slug := a.makeEvent(t, org)
	a.join(t, slug, member, org)
	team, _ := a.startTeam(t, slug, "Restore team", member)
	key := a.teamKey(t, slug, member)
	markReady(t, a.certDir, slug)
	wantTS(t, "deploy", a.deployTeam(t, team, key, "restored team content"), 201, "")
	ctx := context.Background()
	var accountID string
	if err := a.database.QueryRow(`SELECT account_id FROM events WHERE slug=$1`, slug).Scan(&accountID); err != nil {
		t.Fatal(err)
	}
	svc := sites.New(a.database, a.disk, nil, 0, sites.Deps{GroupGate: teams.SiteGate{}, Sitemap: hackevents.FilterSitemapSites})
	checkSitemap := func(want bool) {
		t.Helper()
		list, err := svc.ListSitemapSites(ctx)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, site := range list {
			if site.UserID == accountID && site.Name == team {
				found = true
			}
		}
		if found != want {
			t.Fatalf("team in sitemap=%v, want %v", found, want)
		}
	}
	checkSitemap(true)
	if err := a.sites.TrashTeamSite(ctx, accountID, team); err != nil {
		t.Fatal(err)
	}
	base := "/v1/hack/events/" + slug + "/teams/" + team
	wantTS(t, "take down in trash", a.api(t, "POST", base+"/takedown", map[string]string{"reason": "spam"}, org.key), 200, "")
	d, err := siterepo.GetDeletedSiteByUser(ctx, a.database, accountID, team)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := svc.RestoreTrashed(ctx, sites.RestoreTrashedInput{Site: d})
	if err != nil {
		t.Fatal(err)
	}
	if !restored.Suspended() || restored.SiteSuspendedReason != "Taken down by the event organiser: spam" {
		t.Fatalf("restored team lost take-down: suspended=%v reason=%q", restored.Suspended(), restored.SiteSuspendedReason)
	}
	if !a.disk.IsSuspended(accountID, team) {
		t.Fatal("restored team has no suspension marker")
	}
	host := team + "." + slug + "." + tsDomain
	if r := a.on(t, "GET", host, "/", nil, ""); r.status == 200 || strings.Contains(string(r.body), "restored team content") {
		t.Fatalf("taken-down page served: %d %s", r.status, r.body)
	}
	checkSitemap(false)
	wantTS(t, "organiser restore", a.api(t, "POST", base+"/restore", nil, org.key), 200, "")
	if r := a.on(t, "GET", host, "/", nil, ""); r.status != 200 || !strings.Contains(string(r.body), "restored team content") {
		t.Fatalf("organiser restore did not restore page: %d %s", r.status, r.body)
	}
	checkSitemap(true)
}

// The disk wrapper observes the boundary after marker creation and before the
// rename that exposes the files. No production-only testing hook is needed.
type restoreObservedDisk struct {
	*storage.DiskStorage
	beforeRestore func()
	markerError   error
	restores      int
}

func (d *restoreObservedDisk) SetTrashedSuspended(userID, siteID string, on bool) error {
	if d.markerError != nil {
		return d.markerError
	}
	return d.DiskStorage.SetTrashedSuspended(userID, siteID, on)
}

func (d *restoreObservedDisk) RestoreSite(userID, name, siteID string) error {
	d.restores++
	if d.beforeRestore != nil {
		d.beforeRestore()
	}
	return d.DiskStorage.RestoreSite(userID, name, siteID)
}

type restoreGate struct {
	teams.SiteGate
	suspension func(context.Context, *sql.Tx, siterepo.Site) (string, string, error)
}

func (g restoreGate) Suspension(ctx context.Context, tx *sql.Tx, site siterepo.Site) (string, string, error) {
	return g.suspension(ctx, tx, site)
}

func restoreSeed(t *testing.T) (*sql.DB, *restoreObservedDisk, siterepo.DeletedSite) {
	t.Helper()
	db, err := sql.Open("postgres", testdb.Fresh(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	seed, err := os.ReadFile("../../../scripts/split/seed/seed.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(strings.TrimPrefix(string(seed), "\\set ON_ERROR_STOP on\n")); err != nil {
		t.Fatal(err)
	}
	disk, err := storage.NewDiskStorage(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	d, err := siterepo.GetDeletedSiteByUser(context.Background(), db, "00000000-0000-4000-8000-000000000003", "seed-trash")
	if err != nil {
		t.Fatal(err)
	}
	// The seed carries SQL only. Supply the deleted site's real file tree.
	dir := filepath.Join(disk.TrashDir(d.UserID, d.ID), "current")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("seed trash content"), 0644); err != nil {
		t.Fatal(err)
	}
	return db, &restoreObservedDisk{DiskStorage: disk}, d
}

func TestRestoreTrashedSuspensionSeed(t *testing.T) {
	const prefix = "Taken down by the event organiser"
	const teamID = "00000000-0000-4000-8000-000000000012"
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
			db, disk, d := restoreSeed(t)
			if tc.personal {
				// Make the deleted site personal, with no corresponding team.
				oldUser := d.UserID
				d.UserID = "00000000-0000-4000-8000-000000000001"
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
			deps := sites.Deps{GroupGate: teams.SiteGate{}, LockSite: func(string, string) func() { return func() {} }}
			if tc.nilGate {
				deps.GroupGate = nil
			}
			if tc.noOpinion {
				deps.GroupGate = restoreGate{suspension: func(context.Context, *sql.Tx, siterepo.Site) (string, string, error) {
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

func TestRestoreTrashedSuspensionFailure(t *testing.T) {
	ctx := context.Background()
	for _, failure := range []string{"hook", "row update", "marker"} {
		t.Run(failure, func(t *testing.T) {
			db, disk, d := restoreSeed(t)
			injected := errors.New("restore suspension failed")
			gate := restoreGate{suspension: func(ctx context.Context, tx *sql.Tx, site siterepo.Site) (string, string, error) {
				if site.Deleted || site.ID != d.ID {
					t.Fatal("hook ran before RestoreDeletedSite")
				}
				var alsoDone bool
				if err := tx.QueryRow(`SELECT offline_at IS NOT NULL FROM sites WHERE id=$1`, d.ID).Scan(&alsoDone); err != nil || !alsoDone {
					t.Fatalf("hook ran before also(tx): %v", err)
				}
				if failure == "hook" {
					return "", "", injected
				}
				return "Taken down by the event organiser", "Taken down by the event organiser: spam", nil
			}}
			if failure == "row update" {
				if _, err := db.Exec(`CREATE FUNCTION reject_restore_suspension() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected suspension update failure'; END $$;
					CREATE TRIGGER reject_restore_suspension BEFORE UPDATE OF suspended_at ON sites FOR EACH ROW EXECUTE FUNCTION reject_restore_suspension()`); err != nil {
					t.Fatal(err)
				}
			}
			if failure == "marker" {
				disk.markerError = injected
			}
			svc := sites.New(db, disk, nil, 0, sites.Deps{GroupGate: gate, DeletedTransaction: func(tx *sql.Tx) error {
				_, err := tx.Exec(`UPDATE sites SET offline_at=now() WHERE id=$1`, d.ID)
				return err
			}})
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
