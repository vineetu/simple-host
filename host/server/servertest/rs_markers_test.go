package servertest

// Access markers fail closed (restored from the pre-refactor suite). The
// disk marker is what the serving layer reads first, so a protected site must
// get it back whenever the site moves, is republished or is synced, and a
// request holding an old directory must never be authorised by a different
// site that reuses the name. Fault-injection variants (failing disk/DB
// drivers, crash boundaries, lock-timing races) are deliberately not here.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	appsites "github.com/vsriram/simple-host/host/sites"
)

// rsProtect makes olive's "trip" protected by how ("passcode" or "specific").
func rsProtect(t *testing.T, p *PasscodeApp, how string) {
	t.Helper()
	var r Resp
	if how == "passcode" {
		r = p.Lock(t, "test-passcode")
	} else {
		r = p.At(t, "PUT", PcSiteDomain, "/v1/sites/trip/access", map[string]any{"access": "specific"}, p.Okey)
	}
	if r.Status != http.StatusOK {
		t.Fatalf("protect %s: %d %s", how, r.Status, r.Body)
	}
	if !p.Sites.Disk.HasPasscodeMarker(rsUID(t, p), "trip") {
		t.Fatal("protecting did not write the marker")
	}
}

func rsUID(t *testing.T, p *PasscodeApp) string {
	t.Helper()
	uid, _ := p.UserID(t, p.Olive)
	return uid
}

func rsLeaks(r Resp) bool {
	return r.Status == http.StatusOK || strings.Contains(string(r.Body), PasscodeCanary)
}

// A protected site whose marker file was lost is rebuilt by a rename, a
// restore from Recently deleted and a republish after the folder vanished; the
// site is never served openly afterwards.
func TestRsMarkerLostIsRepaired(t *testing.T) {
	for _, how := range []string{"passcode", "specific"} {
		t.Run(how+"/rename-delete-restore", func(t *testing.T) {
			p := NewPasscodeApp(t)
			uid := rsUID(t, p)
			rsProtect(t, p, how)
			if err := os.Remove(filepath.Join(p.PageDir, "passcode")); err != nil {
				t.Fatal(err)
			}
			if r := p.At(t, "PATCH", PcSiteDomain, "/v1/sites/trip", map[string]any{"name": "voyage"}, p.Okey); r.Status != 200 {
				t.Fatalf("rename %d %s", r.Status, r.Body)
			}
			if !p.Sites.Disk.HasPasscodeMarker(uid, "voyage") {
				t.Fatal("rename left the protected site without its marker")
			}
			if r := p.At(t, "GET", "voyage."+p.Person, "/", nil, nil); rsLeaks(r) {
				t.Fatalf("renamed site leaked: %d", r.Status)
			}
			if r := p.At(t, "DELETE", PcSiteDomain, "/v1/sites/voyage", nil, p.Okey); r.Status != 200 && r.Status != 204 {
				t.Fatalf("delete %d %s", r.Status, r.Body)
			}
			if err := os.Remove(filepath.Join(p.Sites.Disk.TrashDir(uid, p.SiteID), "passcode")); err != nil {
				t.Fatal(err)
			}
			if r := p.At(t, "POST", PcSiteDomain, "/v1/sites/voyage/restore", nil, p.Okey); r.Status != 200 {
				t.Fatalf("restore %d %s", r.Status, r.Body)
			}
			if !p.Sites.Disk.HasPasscodeMarker(uid, "voyage") {
				t.Fatal("restore left the protected site without its marker")
			}
			if r := p.At(t, "GET", "voyage."+p.Person, "/", nil, nil); rsLeaks(r) {
				t.Fatalf("restored site leaked: %d", r.Status)
			}
		})
	}
	t.Run("republish-missing-directory", func(t *testing.T) {
		p := NewPasscodeApp(t)
		rsProtect(t, p, "specific")
		uid := rsUID(t, p)
		if err := p.Sites.Disk.DeleteSite(uid, "trip"); err != nil {
			t.Fatal(err)
		}
		r := p.At(t, "PUT", PcSiteDomain, "/v1/sites/trip/files", map[string]any{"files": map[string]string{"index.html": PasscodeCanary}}, p.Okey)
		if r.Status != 200 || !p.Sites.Disk.HasPasscodeMarker(uid, "trip") {
			t.Fatalf("republish: %d %s", r.Status, r.Body)
		}
		if r := p.At(t, "GET", p.Host, "/", nil, nil); rsLeaks(r) {
			t.Fatalf("republished protected site opened: %d", r.Status)
		}
	})
	t.Run("sync-restores-marker", func(t *testing.T) {
		p := NewPasscodeApp(t)
		rsProtect(t, p, "passcode")
		uid := rsUID(t, p)
		if err := os.Remove(filepath.Join(p.PageDir, "passcode")); err != nil {
			t.Fatal(err)
		}
		p.Access.SyncSuspendMarkers(context.Background())
		if !p.Sites.Disk.HasPasscodeMarker(uid, "trip") {
			t.Fatal("sync did not rebuild the lost marker")
		}
		if r := p.At(t, "GET", p.Host, "/", nil, nil); rsLeaks(r) {
			t.Fatalf("leaked after sync: %d", r.Status)
		}
	})
}

// A sync working from an old row (read before a hold was set) cannot lift the
// operator's take-down or offline marker, and a leftover marker on an open
// site is removed by the ordinary sync.
func TestRsMarkerStaleSyncKeepsOperatorHold(t *testing.T) {
	p := NewPasscodeApp(t)
	uid := rsUID(t, p)
	disk := p.Sites.Disk
	stale, err := p.Sites.SitesSvc().GetSiteByUser(context.Background(), uid, "trip")
	if err != nil {
		t.Fatal(err)
	}
	// Take-down after the stale row was read: the account is suspended.
	if r := p.At(t, "POST", PcSiteDomain, "/v1/admin/users/"+uid+"/suspend", map[string]string{"reason": "test"}, map[string]string{"X-API-Key": p.Admin}); r.Status != 200 {
		t.Fatalf("suspend: %d %s", r.Status, r.Body)
	}
	if !disk.IsSuspended(uid, "trip") {
		t.Fatal("setup: no take-down marker")
	}
	if err := p.Sites.SitesSvc().SyncSiteSuspensionMarker(stale); err != nil {
		t.Fatal(err)
	}
	if !disk.IsSuspended(uid, "trip") {
		t.Fatal("a stale sync lifted the take-down")
	}
	if r := p.At(t, "GET", p.Host, "/", nil, nil); r.Status != http.StatusGone || strings.Contains(string(r.Body), PasscodeCanary) {
		t.Fatalf("held site: %d", r.Status)
	}
	// The ordinary sync keeps a hold the database still records.
	p.Access.SyncSuspendMarkers(context.Background())
	if !disk.IsSuspended(uid, "trip") {
		t.Fatal("sync lifted a recorded take-down")
	}
	// ... and drops a leftover protection marker on an open site.
	if r := p.At(t, "POST", PcSiteDomain, "/v1/admin/users/"+uid+"/enable", nil, map[string]string{"X-API-Key": p.Admin}); r.Status != 200 {
		t.Fatalf("restore account: %d %s", r.Status, r.Body)
	}
	if err := disk.SetPasscodeMarker(uid, "trip", true); err != nil {
		t.Fatal(err)
	}
	p.Access.SyncSuspendMarkers(context.Background())
	if disk.HasPasscodeMarker(uid, "trip") || disk.IsSuspended(uid, "trip") {
		t.Fatal("sync left a marker the database does not record")
	}
}

// rsPauseWriter runs pause once, when the response is first written, which is
// after the serving layer opened the site's folder and before it reads it.
type rsPauseWriter struct {
	*httptest.ResponseRecorder
	pause func()
}

func (w *rsPauseWriter) Header() http.Header {
	if w.pause != nil {
		fn := w.pause
		w.pause = nil
		fn()
	}
	return w.ResponseRecorder.Header()
}

// A request that opened an old site folder is never answered from a
// replacement that reuses the name (after a rename or a delete) and is protected.
func TestRsMarkerDirectoryReuse(t *testing.T) {
	for _, change := range []string{"delete", "rename"} {
		for _, rel := range []string{"/", "/gone"} {
			t.Run(change+rel, func(t *testing.T) {
				p := NewPasscodeApp(t)
				uid := rsUID(t, p)
				if r := p.At(t, "PUT", PcSiteDomain, "/v1/sites/trip/files", map[string]any{"files": map[string]string{"index.html": "old page", "404.html": "old missing", "_redirects": "/gone /404.html 410!"}}, p.Okey); r.Status != 200 {
					t.Fatal(r.Status)
				}
				w := &rsPauseWriter{ResponseRecorder: httptest.NewRecorder()}
				w.pause = func() {
					if change == "rename" {
						if r := p.At(t, "PATCH", PcSiteDomain, "/v1/sites/trip", map[string]any{"name": "voyage"}, p.Okey); r.Status != 200 {
							t.Fatal(r.Status)
						}
					} else {
						if r := p.At(t, "DELETE", PcSiteDomain, "/v1/sites/trip", nil, p.Okey); r.Status != 200 && r.Status != 204 {
							t.Fatal(r.Status)
						}
						// Free the reserved name by expiring only this site's trash.
						if _, err := p.Database.Exec(`DELETE FROM sites WHERE id=$1 AND deleted_at IS NOT NULL`, p.SiteID); err != nil {
							t.Fatal(err)
						}
						if err := p.Sites.Disk.PurgeTrashedSite(uid, p.SiteID); err != nil {
							t.Fatal(err)
						}
					}
					files := map[string]string{"index.html": PasscodeCanary, "404.html": PasscodeCanary, "_redirects": "/gone /404.html 410!"}
					if r := p.At(t, "POST", PcSiteDomain, "/v1/sites/trip/files", map[string]any{"access": "specific", "files": files}, p.Okey); r.Status != 201 {
						t.Fatalf("replacement: %d %s", r.Status, r.Body)
					}
				}
				p.Sites.ServeSiteFileRel(w, httptest.NewRequest("GET", "https://"+p.Host+rel, nil), uid, "trip", rel)
				if w.pause != nil {
					t.Fatal("request did not reach the pause")
				}
				if strings.Contains(w.Body.String(), PasscodeCanary) {
					t.Fatal("request read the replacement's protected files")
				}
			})
		}
	}
}

// A protection row found by name must belong to the folder that was opened:
// after a rename, a rename plus reuse, a delete, or with the row gone, the
// old protected folder is neither served nor authorised by what the name now means.
func TestRsMarkerProtectionRowMustMatchDirectory(t *testing.T) {
	for _, access := range []string{"specific", "passcode"} {
		for _, change := range []string{"rename", "rename-and-reuse", "delete", "missing-row"} {
			t.Run(access+"/"+change, func(t *testing.T) {
				p := NewPasscodeApp(t)
				uid := rsUID(t, p)
				rsProtect(t, p, access)
				// The original directory stays open, as a request in flight holds it.
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
					if r := p.At(t, "PATCH", PcSiteDomain, "/v1/sites/trip", map[string]any{"name": "voyage"}, p.Okey); r.Status != 200 {
						t.Fatalf("rename: %d %s", r.Status, r.Body)
					}
					if change == "rename-and-reuse" {
						if r := p.At(t, "POST", PcSiteDomain, "/v1/sites/trip/files", map[string]any{"files": map[string]string{"index.html": "replacement public page"}}, p.Okey); r.Status != 201 {
							t.Fatalf("reuse: %d %s", r.Status, r.Body)
						}
					}
				case "delete":
					if r := p.At(t, "DELETE", PcSiteDomain, "/v1/sites/trip", nil, p.Okey); r.Status != 200 && r.Status != 204 {
						t.Fatalf("delete: %d %s", r.Status, r.Body)
					}
				case "missing-row":
					if _, err := p.Database.Exec(`DELETE FROM sites WHERE id=$1`, p.SiteID); err != nil {
						t.Fatal(err)
					}
				}
				w := httptest.NewRecorder()
				r := httptest.NewRequest("GET", "https://"+p.Host+"/", nil)
				if !p.Access.GateRefuses(w, r, appsites.Serving{UserID: uid, Name: "trip", Rel: "/", Root: root}) {
					content, err := root.OpenRoot("current")
					if err != nil {
						t.Fatal(err)
					}
					defer content.Close()
					p.Sites.ServeRootFile(w, r, content, "/", "/")
				}
				if strings.Contains(w.Body.String(), PasscodeCanary) {
					t.Fatalf("old protected directory was served: status %d", w.Code)
				}
				if w.Code != http.StatusNotFound {
					t.Fatalf("stale directory: status %d, want 404", w.Code)
				}
			})
		}
	}
}
