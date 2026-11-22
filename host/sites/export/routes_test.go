package export_test

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/vsriram/simple-host/host/sites"
	"github.com/vsriram/simple-host/host/sites/export"
	"github.com/vsriram/simple-host/host/sites/sitesroute/fixture"
)

// An export still completes when part of the tree cannot be read: an
// unlistable live tree still exports its redirect rules, and an unreadable
// file in a deleted site's tree is skipped.
func TestExportSurvivesUnreadableFiles(t *testing.T) {
	a := fixture.New(t)
	export.Register(a.Mux, a.H, a.Auth, a.Notice, a.Exports)
	owner := a.NewPerson(t, "export-review")
	archive := func(siteID string, files sites.FileVersion) map[string]string {
		t.Helper()
		contents := map[string]string{}
		err := a.H.SiteArchive().WriteArchive(context.Background(), func(name string, _ time.Time, _ int64, r io.Reader) error {
			b, err := io.ReadAll(r)
			contents[name] = string(b)
			return err
		}, files.SiteName, siteID, files)
		if err != nil {
			t.Fatal(err)
		}
		return contents
	}

	liveID := a.Deploy(t, owner, "live")
	dir := a.SiteDir(owner, "live")
	const rules = "/old /new 302\n"
	if err := os.WriteFile(filepath.Join(dir, "redirects"), []byte(rules), 0o644); err != nil {
		t.Fatal(err)
	}
	current := filepath.Join(dir, "current")
	if err := os.Chmod(current, 0o111); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(current, 0o755) })
	if _, err := os.ReadDir(current); err == nil {
		t.Skip("directory permissions do not restrict this user")
	}
	if got := archive(liveID, sites.FileVersion{UserID: owner.ID, SiteName: "live"}); !reflect.DeepEqual(got, map[string]string{"live/files/_redirects": rules}) {
		t.Fatalf("unlistable tree: %v", got)
	}

	trashID := a.Deploy(t, owner, "gone")
	if err := a.Disk.TrashSite(owner.ID, "gone", trashID); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(a.Disk.TrashDir(owner.ID, trashID), "current", "unreadable.txt")
	if err := os.WriteFile(path, []byte("unreadable bytes"), 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o644) })
	got := archive(trashID, sites.FileVersion{UserID: owner.ID, SiteName: "gone", TrashID: trashID})
	if len(got) != 2 || got["gone/files/index.html"] == "" || got["gone/files/sub/index.html"] != "sub" {
		t.Fatalf("unreadable file not skipped: %v", got)
	}
}
