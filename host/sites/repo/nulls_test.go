package repo

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/vsriram/simple-host/host/testdb"
)

// Legacy rows the schema allows: an account whose is_admin and created_at
// are NULL, a site of it whose created_at and updated_at are NULL, and a
// site with no account. The lists the owner join served
// before read them as it did: the account's sites are listed, the ownerless
// site is left out, and nothing fails.
func TestOwnerReadsTolerateLegacyNulls(t *testing.T) {
	db := testdb.DB(t)
	ctx := context.Background()
	stamp := time.Now().Format("150405.000000000")
	var userID string
	if err := db.QueryRow(`INSERT INTO users (username, handle, is_admin, created_at) VALUES ($1, $2, NULL, NULL) RETURNING id`,
		"nulls-"+stamp+"@example.com", "nulls-"+stamp[7:]).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Exec(`DELETE FROM users WHERE id = $1`, userID) })
	name := "legacy" + stamp[7:]
	var ownerless, owned string
	if err := db.QueryRow(`INSERT INTO sites (user_id, name, created_at) VALUES (NULL, $1, now() - interval '1 day') RETURNING id`, name).Scan(&ownerless); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Exec(`DELETE FROM sites WHERE id = $1`, ownerless) })
	if err := db.QueryRow(`INSERT INTO sites (user_id, name, created_at, updated_at) VALUES ($1, $2, NULL, NULL) RETURNING id`, userID, name).Scan(&owned); err != nil {
		t.Fatal(err)
	}

	all, err := ListAllSites(ctx, db)
	if err != nil {
		t.Fatalf("ListAllSites: %v", err)
	}
	seen := map[string]bool{}
	for _, s := range all {
		seen[s.ID] = true
	}
	if !seen[owned] || seen[ownerless] {
		t.Fatalf("ListAllSites: owned %t, ownerless %t", seen[owned], seen[ownerless])
	}
	if _, err := ListSitemapSites(ctx, db); err != nil {
		t.Fatalf("ListSitemapSites: %v", err)
	}
	if h, err := GetHandleBySiteName(ctx, db, name); err != nil || h != "nulls-"+stamp[7:] {
		t.Fatalf("GetHandleBySiteName: %q %v", h, err)
	}
	if _, err := GetSiteByID(ctx, db, ownerless); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("GetSiteByID(ownerless): %v", err)
	}
	if s, err := GetSiteByID(ctx, db, owned); err != nil || s.OwnerHandle == "" {
		t.Fatalf("GetSiteByID(owned): %+v %v", s, err)
	}
	if down, _, err := SiteSuspension(ctx, db, owned); err != nil || down {
		t.Fatalf("SiteSuspension: %t %v", down, err)
	}
	if _, id, _, err := GetSiteOwner(ctx, db, owned); err != nil || id != userID {
		t.Fatalf("GetSiteOwner: %s %v", id, err)
	}
	if email, err := GetSiteOwnerEmail(ctx, db, owned); err != nil || email == "" {
		t.Fatalf("GetSiteOwnerEmail: %q %v", email, err)
	}
	if _, err := ListHandlesWithSites(ctx, db); err != nil {
		t.Fatalf("ListHandlesWithSites: %v", err)
	}
}
