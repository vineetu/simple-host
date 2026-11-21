package sitestest

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lib/pq"
	"github.com/vsriram/simple-host/host/server/servertest"
	"github.com/vsriram/simple-host/host/sites"
	siteapp "github.com/vsriram/simple-host/host/sites"
)

var errMarkerInjected = errors.New("injected access failure")

// Wrap the real Postgres driver: after means the server completed the
// autocommit, but the caller receives an error instead of its acknowledgement.
type accessConnector struct {
	driver.Connector
	before, after func(string) error
}

func (c accessConnector) Connect(ctx context.Context) (driver.Conn, error) {
	conn, err := c.Connector.Connect(ctx)
	if err != nil {
		return nil, err
	}
	return &accessConn{Conn: conn, hooks: c}, nil
}

type accessConn struct {
	driver.Conn
	hooks accessConnector
}

func (c *accessConn) ExecContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Result, error) {
	if err := c.hooks.before(q); err != nil {
		return nil, err
	}
	result, err := c.Conn.(driver.ExecerContext).ExecContext(ctx, q, args)
	if err == nil {
		err = c.hooks.after(q)
	}
	return result, err
}
func (c *accessConn) QueryContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Rows, error) {
	if err := c.hooks.before(q); err != nil {
		return nil, err
	}
	rows, err := c.Conn.(driver.QueryerContext).QueryContext(ctx, q, args)
	if err != nil {
		return rows, err
	}
	if strings.Contains(q, "UPDATE sites SET") {
		// Drain RETURNING before injecting the lost acknowledgement.
		values := make([]driver.Value, len(rows.Columns()))
		var saved [][]driver.Value
		for {
			err := rows.Next(values)
			if err == io.EOF {
				break
			}
			if err != nil {
				rows.Close()
				return nil, err
			}
			saved = append(saved, append([]driver.Value(nil), values...))
		}
		columns := rows.Columns()
		if err := rows.Close(); err != nil {
			return nil, err
		}
		if err := c.hooks.after(q); err != nil {
			return nil, err
		}
		return &accessRows{columns: columns, values: saved}, nil
	}
	return rows, nil
}

type accessRows struct {
	columns []string
	values  [][]driver.Value
}

func (r *accessRows) Columns() []string { return r.columns }
func (r *accessRows) Close() error      { return nil }
func (r *accessRows) Next(dst []driver.Value) error {
	if len(r.values) == 0 {
		return io.EOF
	}
	copy(dst, r.values[0])
	r.values = r.values[1:]
	return nil
}
func installAccessDB(t *testing.T, p *servertest.PasscodeApp, before, after func(string) error) {
	t.Helper()
	connector, err := pq.NewConnector(os.Getenv("DB_DSN"))
	if err != nil {
		t.Fatal(err)
	}
	database := sql.OpenDB(accessConnector{connector, before, after})
	original := p.Sites.Database
	p.Sites.Database = database
	p.Access.Database, p.Data.Database = p.Sites.Database, p.Sites.Database
	t.Cleanup(func() { p.Sites.Database = original; database.Close() })
}

type accessFaultDisk struct {
	sites.Disk
	before, after func(bool) error
	missing       bool
}

func (d *accessFaultDisk) SetPasscodeMarker(uid, name string, on bool) error {
	if d.before != nil {
		if err := d.before(on); err != nil {
			return err
		}
	}
	if d.missing && on {
		return nil
	}
	if err := d.Disk.SetPasscodeMarker(uid, name, on); err != nil {
		return err
	}
	if d.after != nil {
		return d.after(on)
	}
	return nil
}

// Every site owned by this fixture is checked, including Recently deleted.
// Other fixtures own different disk roots and are not part of this install.
func assertAccessInvariant(t *testing.T, p *servertest.PasscodeApp) {
	t.Helper()
	uid, _ := p.UserID(t, p.Olive)
	rows, err := p.Database.Query(`SELECT id, name, deleted_at IS NOT NULL FROM sites WHERE user_id=$1 AND (passcode_enc IS NOT NULL OR access='specific')`, uid)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, name string
		var deleted bool
		if err := rows.Scan(&id, &name, &deleted); err != nil {
			t.Fatal(err)
		}
		dir := p.Sites.Disk.SiteDir(uid, name)
		if deleted {
			dir = p.Sites.Disk.TrashDir(uid, id)
		}
		if _, err := os.Lstat(filepath.Join(dir, "passcode")); err != nil {
			t.Fatalf("protected %s has no marker: %v", name, err)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
}
func accessRequest(t *testing.T, p *servertest.PasscodeApp, method, path string, body any) servertest.Resp {
	t.Helper()
	result := p.At(t, method, servertest.PcSiteDomain, path, body, p.Okey)
	assertAccessInvariant(t, p)
	return result
}

func TestAccessMarkerFailures(t *testing.T) {
	for _, operation := range []string{"passcode", "specific", "clear", "anyone", "generation"} {
		for _, failure := range []string{"none", "marker-before", "marker-missing", "marker-after", "database-before", "database-after", "database-and-read"} {
			protecting := operation == "passcode" || operation == "specific"
			if (!protecting && failure == "marker-missing") || (operation == "generation" && strings.HasPrefix(failure, "marker")) {
				continue
			}
			t.Run(operation+"/"+failure, func(t *testing.T) {
				p := servertest.NewPasscodeApp(t)
				if operation == "clear" || operation == "generation" {
					if r := p.Lock(t, "test-passcode"); r.Status != 200 {
						t.Fatalf("setup: %d", r.Status)
					}
					assertAccessInvariant(t, p)
				}
				if operation == "anyone" {
					if r := accessRequest(t, p, "PUT", "/v1/sites/trip/access", map[string]any{"access": "specific"}); r.Status != 200 {
						t.Fatalf("setup: %d %s", r.Status, r.Body)
					}
				}
				uid, _ := p.UserID(t, p.Olive)
				disk := &accessFaultDisk{Disk: p.Sites.Disk, missing: failure == "marker-missing"}
				disk.before = func(bool) error {
					if failure == "marker-before" {
						return errMarkerInjected
					}
					return nil
				}
				disk.after = func(bool) error {
					if failure == "marker-after" {
						return errMarkerInjected
					}
					return nil
				}
				p.Sites.Disk = disk
				p.Access.Disk, p.Data.Disk = p.Sites.Disk, p.Sites.Disk
				attempted := false
				isWrite := func(q string) bool {
					return strings.Contains(q, "UPDATE sites SET passcode_") || strings.Contains(q, "UPDATE sites SET access =")
				}
				installAccessDB(t, p, func(q string) error {
					if isWrite(q) {
						attempted = true
						if protecting && !disk.HasPasscodeMarker(uid, "trip") {
							t.Fatal("protecting statement sent before marker")
						}
						if failure == "database-before" {
							return errMarkerInjected
						}
					}
					if attempted && failure == "database-and-read" && strings.Contains(q, "SELECT id, user_id, name, passcode_enc") {
						return errMarkerInjected
					}
					return nil
				}, func(q string) error {
					if isWrite(q) && (failure == "database-after" || failure == "database-and-read") {
						return errMarkerInjected
					}
					return nil
				})
				method, path, body := "PUT", "/v1/sites/trip/lock", any(map[string]any{"passcode": "new-passcode"})
				switch operation {
				case "specific", "anyone":
					path = "/v1/sites/trip/access"
					body = map[string]any{"access": operation}
				case "clear":
					method = "DELETE"
					body = nil
				case "generation":
					method = "POST"
					path += "/sign-out-everyone"
					body = nil
				}
				r := accessRequest(t, p, method, path, body)
				if failure == "none" {
					if r.Status != 200 {
						t.Fatalf("success: %d %s", r.Status, r.Body)
					}
				} else if r.Status != 500 {
					t.Fatalf("failure: %d %s", r.Status, r.Body)
				}
				if protecting && strings.HasPrefix(failure, "marker") && attempted {
					t.Fatal("marker failure wrote database")
				}
				// A completed protecting write with a lost answer must remain gated.
				if protecting && (failure == "database-after" || failure == "database-and-read" || failure == "none") {
					if r := p.At(t, "GET", p.Host, "/", nil, nil); r.Status == 200 || strings.Contains(string(r.Body), servertest.PasscodeCanary) {
						t.Fatalf("leaked protected content: %d", r.Status)
					}
				}
				if protecting && failure == "database-before" && disk.HasPasscodeMarker(uid, "trip") {
					t.Fatal("fresh open row did not clear failed write's marker")
				}
				if !protecting && operation != "generation" && failure != "database-before" && failure != "database-and-read" {
					if result := p.At(t, "GET", p.Host, "/", nil, nil); result.Status != 200 {
						t.Fatalf("committed opening still gated: %d", result.Status)
					}
				}
				// Repair a leftover removal failure with the ordinary startup/sweep path.
				p.Sites.Disk = disk.Disk
				p.Access.Disk, p.Data.Disk = p.Sites.Disk, p.Sites.Disk
				p.Access.SyncSuspendMarkers(context.Background())
				assertAccessInvariant(t, p)
			})
		}
	}
}

func TestAccessMarkerCrashBoundaries(t *testing.T) {
	p := servertest.NewPasscodeApp(t)
	uid, _ := p.UserID(t, p.Olive)
	disk := p.Sites.Disk
	if err := disk.SetPasscodeMarker(uid, "trip", true); err != nil {
		t.Fatal(err)
	}
	// Crash before sending the statement: a durable extra marker is harmless.
	if r := p.At(t, "GET", p.Host, "/", nil, nil); r.Status != 200 || !strings.Contains(string(r.Body), servertest.PasscodeCanary) {
		t.Fatalf("open row: %d", r.Status)
	}
	if r := accessRequest(t, p, "PUT", "/v1/sites/trip/access", map[string]any{"access": "specific"}); r.Status != 200 {
		t.Fatal(r.Status)
	}
	// Crash after commit: reopen the marker independently, no repair worker.
	f, err := os.Open(filepath.Join(p.PageDir, "passcode"))
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	if r := p.At(t, "GET", p.Host, "/", nil, nil); r.Status == 200 {
		t.Fatal("post-commit site opened")
	}
}

func TestAccessMarkerStaleSync(t *testing.T) {
	p := servertest.NewPasscodeApp(t)
	uid, _ := p.UserID(t, p.Olive)
	// A leftover marker makes this row a removal candidate. Additions no
	// longer acquire the lock, so pause the actual removal path below.
	if err := p.Sites.Disk.SetPasscodeMarker(uid, "trip", true); err != nil {
		t.Fatal(err)
	}
	stale, err := p.Sites.SitesSvc().GetSiteByUser(context.Background(), uid, "trip")
	if err != nil {
		t.Fatal(err)
	}
	entered, resume := make(chan struct{}), make(chan struct{})
	var once sync.Once
	svc := siteapp.New(p.Database, p.Sites.Disk, nil, 0, siteapp.Deps{LockSite: func(uid, name string) func() {
		once.Do(func() { close(entered); <-resume })
		return p.Sites.LockSite(uid, name)
	}})
	done := make(chan struct{})
	var release sync.Once
	unblock := func() { release.Do(func() { close(resume) }) }
	go func() { defer close(done); svc.SyncSuspendMarkers(context.Background()) }()
	t.Cleanup(func() { unblock(); <-done })
	select {
	case <-entered: // ListAllSites finished; no name lock has been acquired.
	case <-done:
		t.Fatal("marker sync did not take a name lock")
	}
	r := accessRequest(t, p, "PUT", "/v1/sites/trip/access", map[string]any{"access": "specific"})
	unblock()
	<-done
	if r.Status != 200 {
		t.Fatal(r.Status)
	}
	assertAccessInvariant(t, p)
	p.Access.SyncSuspendMarkers(context.Background())
	assertAccessInvariant(t, p)
	// Account take-down repair must likewise ignore an old unsuspended row.
	if _, err := p.Database.Exec(`UPDATE users SET suspended_at=now() WHERE id=$1`, uid); err != nil {
		t.Fatal(err)
	}
	if err := p.Sites.Disk.SetSuspended(uid, "trip", true); err != nil {
		t.Fatal(err)
	}
	if err := p.Sites.SitesSvc().SyncSiteSuspensionMarker(stale); err != nil {
		t.Fatal(err)
	}
	if !p.Sites.Disk.IsSuspended(uid, "trip") {
		t.Fatal("stale account repair removed take-down")
	}
}

func TestAccessMarkerAccountSuspensionDoesNotWait(t *testing.T) {
	for _, failRead := range []bool{false, true} {
		name := "held-lock"
		if failRead {
			name = "failed-reread"
		}
		t.Run(name, func(t *testing.T) {
			p := servertest.NewPasscodeApp(t)
			uid, _ := p.UserID(t, p.Olive)
			if r := accessRequest(t, p, "POST", "/v1/sites/later/files", map[string]any{"files": map[string]string{"index.html": "later"}}); r.Status != http.StatusCreated {
				t.Fatalf("create later site: %d %s", r.Status, r.Body)
			}
			if failRead {
				installAccessDB(t, p, func(q string) error {
					if strings.Contains(q, "WHERE user_id = $1 AND name = $2 AND deleted_at IS NULL") {
						return errMarkerInjected
					}
					return nil
				}, func(string) error { return nil })
				if _, err := p.Sites.SitesSvc().GetSiteByUser(context.Background(), uid, "trip"); !errors.Is(err, errMarkerInjected) {
					t.Fatalf("read failure was not injected: %v", err)
				}
			}
			unlock := func() {}
			if !failRead {
				unlock = p.Sites.LockSite(uid, "trip")
			}
			done := make(chan servertest.Resp, 1)
			finished := make(chan struct{})
			go func() {
				defer close(finished)
				done <- p.At(t, "POST", servertest.PcSiteDomain, "/v1/admin/users/"+uid+"/suspend", map[string]string{"reason": "test"}, map[string]string{"X-API-Key": p.Admin})
			}()
			t.Cleanup(func() { unlock(); <-finished })
			select {
			case r := <-done:
				if r.Status != http.StatusOK {
					t.Fatalf("account suspension: %d %s", r.Status, r.Body)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("account suspension waited for the site's lock")
			}
			for _, site := range []string{"trip", "later"} {
				if !p.Sites.Disk.IsSuspended(uid, site) {
					t.Fatalf("%s lacks take-down marker before lock release", site)
				}
			}
		})
	}
}

func TestAccessMarkerSyncAddsBeforeWaiting(t *testing.T) {
	for _, removal := range []bool{false, true} {
		name := "additions-only"
		if removal {
			name = "blocked-removal"
		}
		t.Run(name, func(t *testing.T) {
			p := servertest.NewPasscodeApp(t)
			uid, _ := p.UserID(t, p.Olive)
			if r := accessRequest(t, p, "POST", "/v1/sites/later/files", map[string]any{"files": map[string]string{"index.html": "later"}}); r.Status != http.StatusCreated {
				t.Fatalf("create later site: %d %s", r.Status, r.Body)
			}
			// trip is first in ListAllSites, and needs all three additions.
			// With removal=true it instead has a stale offline marker; even
			// that removal must not delay any later site's additions.
			if _, err := p.Database.Exec(`UPDATE sites SET suspended_at=now(), offline_at=now(), access='specific', updated_at=CASE WHEN name='trip' THEN now()+interval '1 hour' ELSE now() END WHERE user_id=$1`, uid); err != nil {
				t.Fatal(err)
			}
			if removal {
				if _, err := p.Database.Exec(`UPDATE sites SET offline_at=NULL WHERE id=$1`, p.SiteID); err != nil {
					t.Fatal(err)
				}
				if err := p.Sites.Disk.SetOffline(uid, "trip", true); err != nil {
					t.Fatal(err)
				}
			}
			unlock := p.Sites.LockSite(uid, "trip")
			entered := make(chan struct{}, 1)
			svc := siteapp.New(p.Database, p.Sites.Disk, nil, 0, siteapp.Deps{LockSite: func(ownerID, site string) func() {
				if ownerID == uid && site == "trip" {
					entered <- struct{}{}
				}
				return p.Sites.LockSite(ownerID, site)
			}})
			done := make(chan struct{})
			go func() { defer close(done); svc.SyncSuspendMarkers(context.Background()) }()
			t.Cleanup(func() { unlock(); <-done })
			if removal {
				select {
				case <-entered:
				case <-done:
					t.Fatal("sync did not lock before removing a marker")
				case <-time.After(3 * time.Second):
					t.Fatal("sync did not reach removal")
				}
			} else {
				select {
				case <-done:
				case <-time.After(3 * time.Second):
					t.Fatal("marker additions waited for the site's lock")
				}
			}
			for _, site := range []string{"trip", "later"} {
				if !p.Sites.Disk.IsSuspended(uid, site) || !p.Sites.Disk.IsOffline(uid, site) || !p.Sites.Disk.HasPasscodeMarker(uid, site) {
					t.Fatalf("%s lacks markers before lock release", site)
				}
			}
		})
	}
}

func TestAccessMarkerProtectionLockWaitEnds(t *testing.T) {
	for _, cancelRequest := range []bool{false, true} {
		name := "timeout"
		if cancelRequest {
			name = "cancelled"
		}
		t.Run(name, func(t *testing.T) {
			p := servertest.NewPasscodeApp(t)
			uid, _ := p.UserID(t, p.Olive)
			if r := accessRequest(t, p, "PUT", "/v1/sites/trip/access", map[string]any{"access": "specific"}); r.Status != http.StatusOK {
				t.Fatal(r.Status)
			}
			unlock := p.Sites.LockSite(uid, "trip")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			req := httptest.NewRequest("GET", "https://"+p.Host+"/", nil).WithContext(ctx)
			started := make(chan struct{})
			w := httptest.NewRecorder()
			done := make(chan struct{})
			go func() {
				defer close(done)
				close(started)
				p.Sites.ServeSiteFileRel(w, req, uid, "trip", "/")
			}()
			t.Cleanup(func() { unlock(); <-done })
			<-started
			wait := 3 * time.Second
			if cancelRequest {
				cancel()
				wait = 500 * time.Millisecond
			}
			select {
			case <-done:
			case <-time.After(wait):
				t.Fatal("visitor waited for the site's lock after cancellation or timeout")
			}
			if w.Code != http.StatusServiceUnavailable || strings.Contains(w.Body.String(), servertest.PasscodeCanary) || w.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("expected uncached service error, got %d %s", w.Code, w.Body.String())
			}
		})
	}
}

// Pause at ownerFrameGuard, after the marker checks and before opening current.
type accessPauseWriter struct {
	*httptest.ResponseRecorder
	pause func()
}

func (w *accessPauseWriter) Header() http.Header {
	if w.pause != nil {
		fn := w.pause
		w.pause = nil
		fn()
	}
	return w.ResponseRecorder.Header()
}
func TestAccessMarkerDirectoryReuse(t *testing.T) {
	for _, change := range []string{"delete", "rename"} {
		for _, rel := range []string{"/", "/gone"} {
			t.Run(change+rel, func(t *testing.T) {
				p := servertest.NewPasscodeApp(t)
				uid, _ := p.UserID(t, p.Olive)
				if r := accessRequest(t, p, "PUT", "/v1/sites/trip/files", map[string]any{"files": map[string]string{"index.html": "old page", "404.html": "old missing", "_redirects": "/gone /404.html 410!"}}); r.Status != 200 {
					t.Fatal(r.Status)
				}
				serve := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					p.Sites.ServeSiteFileRel(w, r, uid, "trip", rel)
				})
				w := &accessPauseWriter{ResponseRecorder: httptest.NewRecorder()}
				w.pause = func() {
					if change == "rename" {
						if r := accessRequest(t, p, "PATCH", "/v1/sites/trip", map[string]any{"name": "voyage"}); r.Status != 200 {
							t.Fatal(r.Status)
						}
					} else {
						if r := accessRequest(t, p, "DELETE", "/v1/sites/trip", nil); r.Status != 200 && r.Status != 204 {
							t.Fatal(r.Status)
						}
						// Expire only this fixture's trash, freeing the reserved name.
						if _, err := p.Database.Exec(`DELETE FROM sites WHERE id=$1 AND deleted_at IS NOT NULL`, p.SiteID); err != nil {
							t.Fatal(err)
						}
						if err := p.Sites.Disk.PurgeTrashedSite(uid, p.SiteID); err != nil {
							t.Fatal(err)
						}
					}
					if r := accessRequest(t, p, "POST", "/v1/sites/trip/files", map[string]any{"access": "specific", "files": map[string]string{"index.html": servertest.PasscodeCanary, "404.html": servertest.PasscodeCanary, "_redirects": "/gone /404.html 410!"}}); r.Status != 201 {
						t.Fatalf("replacement: %d %s", r.Status, r.Body)
					}
				}
				req := httptest.NewRequest("GET", "https://"+p.Host+rel, nil)
				serve(w, req)
				if w.pause != nil {
					t.Fatal("request did not reach pause")
				}
				if strings.Contains(w.Body.String(), servertest.PasscodeCanary) {
					t.Fatal("request read replacement's protected files")
				}
				if change == "rename" && !strings.Contains(w.Body.String(), "old") {
					t.Fatalf("did not read original directory: %d %s", w.Code, w.Body)
				}
			})
		}
	}
}

// Check the source directory at the exact boundary at which it becomes
// reachable under the destination name.
type accessMoveDisk struct {
	sites.Disk
	t *testing.T
}

func (d *accessMoveDisk) RenameSite(uid, oldName, newName string, domains ...string) error {
	if !d.HasPasscodeMarker(uid, oldName) {
		d.t.Fatal("rename moved an unmarked protected folder")
	}
	return d.Disk.RenameSite(uid, oldName, newName, domains...)
}
func (d *accessMoveDisk) RestoreSite(uid, name, id string) error {
	if _, err := os.Lstat(filepath.Join(d.TrashDir(uid, id), "passcode")); err != nil {
		d.t.Fatal("restore moved an unmarked protected folder")
	}
	return d.Disk.RestoreSite(uid, name, id)
}

func TestAccessMarkerRestoreAndRenameRepair(t *testing.T) {
	for _, access := range []string{"passcode", "specific"} {
		t.Run(access, func(t *testing.T) {
			p := servertest.NewPasscodeApp(t)
			p.Sites.Disk = &accessMoveDisk{p.Sites.Disk, t}
			p.Access.Disk, p.Data.Disk = p.Sites.Disk, p.Sites.Disk
			uid, _ := p.UserID(t, p.Olive)
			if access == "passcode" {
				if r := p.Lock(t, "test-passcode"); r.Status != 200 {
					t.Fatal(r.Status)
				}
				assertAccessInvariant(t, p)
			} else {
				if r := accessRequest(t, p, "PUT", "/v1/sites/trip/access", map[string]any{"access": "specific"}); r.Status != 200 {
					t.Fatal(r.Status)
				}
			}
			if err := os.Remove(filepath.Join(p.PageDir, "passcode")); err != nil {
				t.Fatal(err)
			}
			if r := accessRequest(t, p, "PATCH", "/v1/sites/trip", map[string]any{"name": "voyage"}); r.Status != 200 {
				t.Fatalf("rename %d %s", r.Status, r.Body)
			}
			if r := accessRequest(t, p, "DELETE", "/v1/sites/voyage", nil); r.Status != 200 && r.Status != 204 {
				t.Fatal(r.Status)
			}
			if err := os.Remove(filepath.Join(p.Sites.Disk.TrashDir(uid, p.SiteID), "passcode")); err != nil {
				t.Fatal(err)
			}
			if r := accessRequest(t, p, "POST", "/v1/sites/voyage/restore", nil); r.Status != 200 {
				t.Fatalf("restore %d %s", r.Status, r.Body)
			}
			if r := p.At(t, "GET", "voyage."+p.Person, "/", nil, nil); r.Status == 200 || strings.Contains(string(r.Body), servertest.PasscodeCanary) {
				t.Fatal("restored site leaked")
			}
		})
	}
}

func TestAccessMarkerRepublishMissingDirectory(t *testing.T) {
	p := servertest.NewPasscodeApp(t)
	if r := accessRequest(t, p, "PUT", "/v1/sites/trip/access", map[string]any{"access": "specific"}); r.Status != 200 {
		t.Fatal(r.Status)
	}
	uid, _ := p.UserID(t, p.Olive)
	if err := p.Sites.Disk.DeleteSite(uid, "trip"); err != nil {
		t.Fatal(err)
	}
	// The archive import and JSON update converge on Service.Update.
	disk := &createAccessDisk{Disk: p.Sites.Disk, t: t}
	p.Sites.Disk = disk
	p.Access.Disk, p.Data.Disk = p.Sites.Disk, p.Sites.Disk
	r := accessRequest(t, p, "PUT", "/v1/sites/trip/files", map[string]any{"files": map[string]string{"index.html": servertest.PasscodeCanary}})
	if r.Status != 200 || !disk.marked {
		t.Fatalf("republish: %d %s", r.Status, r.Body)
	}
	if r := p.At(t, "GET", p.Host, "/", nil, nil); r.Status == 200 {
		t.Fatal("republished site opened")
	}
}

// The directory handle alone does not bind a protection row to that folder:
// after rename, a fresh by-name lookup can describe a different, public site.
func TestAccessMarkerProtectionRowMustMatchDirectory(t *testing.T) {
	for _, access := range []string{"specific", "passcode"} {
		for _, change := range []string{"rename", "rename-and-reuse", "delete", "missing-row"} {
			t.Run(access+"/"+change, func(t *testing.T) {
				p := servertest.NewPasscodeApp(t)
				if access == "passcode" {
					if r := p.Lock(t, "test-passcode"); r.Status != 200 {
						t.Fatal(r.Status)
					}
					assertAccessInvariant(t, p)
				} else if r := accessRequest(t, p, "PUT", "/v1/sites/trip/access", map[string]any{"access": access}); r.Status != 200 {
					t.Fatal(r.Status)
				}
				uid, _ := p.UserID(t, p.Olive)
				// Pause with the original directory open, as serveSiteFile does,
				// before the gate takes the name lock for its protection lookup.
				root, err := os.OpenRoot(p.PageDir)
				if err != nil {
					t.Fatal(err)
				}
				defer root.Close()
				if _, err := root.Lstat("passcode"); err != nil {
					t.Fatal(err)
				}
				switch change {
				case "rename", "rename-and-reuse":
					if r := accessRequest(t, p, "PATCH", "/v1/sites/trip", map[string]any{"name": "voyage"}); r.Status != 200 {
						t.Fatalf("rename: %d %s", r.Status, r.Body)
					}
					if change == "rename-and-reuse" {
						if r := accessRequest(t, p, "POST", "/v1/sites/trip/files", map[string]any{"files": map[string]string{"index.html": "replacement public page"}}); r.Status != 201 {
							t.Fatalf("reuse: %d %s", r.Status, r.Body)
						}
					}
				case "delete":
					if r := accessRequest(t, p, "DELETE", "/v1/sites/trip", nil); r.Status != 200 && r.Status != 204 {
						t.Fatalf("delete: %d %s", r.Status, r.Body)
					}
				case "missing-row":
					// An orphaned marked directory must not authorize its own files.
					if _, err := p.Database.Exec(`DELETE FROM sites WHERE id=$1`, p.SiteID); err != nil {
						t.Fatal(err)
					}
				}
				w := httptest.NewRecorder()
				r := httptest.NewRequest("GET", "https://"+p.Host+"/", nil)
				if !p.Access.GateRefuses(w, r, sites.Serving{UserID: uid, Name: "trip", Rel: "/", Root: root}) {
					content, err := root.OpenRoot("current")
					if err != nil {
						t.Fatal(err)
					}
					defer content.Close()
					p.Sites.ServeRootFile(w, r, content, "/", "/")
				}
				if strings.Contains(w.Body.String(), servertest.PasscodeCanary) {
					t.Fatalf("replacement or missing row authorized the original protected directory: status %d", w.Code)
				}
				if w.Code != http.StatusNotFound {
					t.Fatalf("stale directory: status %d, want 404", w.Code)
				}
			})
		}
	}
}

func TestAccessMarkerProtectionLookupHoldsNameLock(t *testing.T) {
	p := servertest.NewPasscodeApp(t)
	uid, _ := p.UserID(t, p.Olive)
	// A leftover marker on an open site still needs a directory-bound lookup.
	if err := p.Sites.Disk.SetPasscodeMarker(uid, "trip", true); err != nil {
		t.Fatal(err)
	}
	checked := false
	installAccessDB(t, p, func(q string) error {
		if strings.Contains(q, "SELECT id, user_id, name, passcode_enc") {
			checked = true
			mu, ok := p.Sites.UploadLocks.Load(uid + "/trip")
			if !ok {
				t.Fatal("protection lookup has no name lock")
			}
			if mu.(*sync.Mutex).TryLock() {
				mu.(*sync.Mutex).Unlock()
				t.Fatal("protection lookup did not hold the name lock")
			}
		}
		return nil
	}, func(string) error { return nil })
	w := httptest.NewRecorder()
	p.Sites.ServeSiteFileRel(w, httptest.NewRequest("GET", "https://"+p.Host+"/", nil), uid, "trip", "/")
	if !checked || w.Code != http.StatusOK || !strings.Contains(w.Body.String(), servertest.PasscodeCanary) {
		t.Fatalf("open row with leftover marker: checked=%t status=%d", checked, w.Code)
	}
	mu, _ := p.Sites.UploadLocks.Load(uid + "/trip")
	if !mu.(*sync.Mutex).TryLock() {
		t.Fatal("protection lookup did not release the name lock")
	}
	mu.(*sync.Mutex).Unlock()
}
