package servertest

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	sitesrepo "github.com/vsriram/simple-host/host/sites/repo"
)

// SiteIDAny is the account's site of that name, live or Recently deleted.
func (a *PrivateApp) SiteIDAny(t *testing.T, uid, name string) string {
	t.Helper()
	ctx := context.Background()
	site, err := sitesrepo.GetSiteByUser(ctx, a.Database, uid, name)
	if errors.Is(err, sql.ErrNoRows) {
		var gone sitesrepo.DeletedSite
		gone, err = sitesrepo.GetDeletedSiteByUser(ctx, a.Database, uid, name)
		site.ID = gone.ID
	}
	if err != nil {
		t.Fatal(err)
	}
	return site.ID
}
