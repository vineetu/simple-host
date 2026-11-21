package servertest

import (
	"context"
	"testing"
	"time"

	sitesrepo "github.com/vsriram/simple-host/host/sites/repo"
)

// deleteBehindLock does what a delete does (row marked, files moved out)
// while the test holds the site's lock, as a delete that finished first.
func (a *PrivateApp) deleteBehindLock(t *testing.T, uid, siteID, name string) {
	t.Helper()
	if err := sitesrepo.MarkSiteDeleted(context.Background(), a.Database, siteID, 7*24*time.Hour); err != nil {
		t.Fatal(err)
	}
	if err := a.Sites.Disk.TrashSite(uid, name, siteID); err != nil {
		t.Fatal(err)
	}
}

func (a *PrivateApp) SiteIDAny(t *testing.T, uid, name string) string {
	t.Helper()
	var id string
	if err := a.Database.QueryRow(`SELECT id FROM sites WHERE user_id = $1 AND name = $2`, uid, name).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}
