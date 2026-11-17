package servertest

import (
	"context"
	"testing"
	"time"

	appstorage "github.com/vsriram/simple-host/host/app/storage"
	"github.com/vsriram/simple-host/products/hack"
)

func TestNoticeHoldingAccountUsesEventLabel(t *testing.T) {
	a := newHackApp(t)
	org := a.newPerson(t, "notice-organiser")
	const slug = "notice-fixture"
	a.createEvent(t, org, slug, nil)
	var owner, site string
	if err := a.database.QueryRow(`SELECT account_id FROM events WHERE slug=$1`, slug).Scan(&owner); err != nil {
		t.Fatal(err)
	}
	if err := a.database.QueryRow(`INSERT INTO sites(user_id,name) VALUES($1,'notice-team') RETURNING id`, owner).Scan(&site); err != nil {
		t.Fatal(err)
	}
	if _, err := a.database.Exec(`INSERT INTO site_storage_resources(site_id,name,kind) VALUES($1,'not-converted','kv')`, site); err != nil {
		t.Fatal(err)
	}
	templates, err := appstorage.LoadNoticeTemplates(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	report, err := appstorage.ListNoticeRecipients(context.Background(), a.database, false, nil, hack.Extension().StorageNoticeGroup, templates, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range report.Presets {
		if r.AccountID == owner {
			t.Fatal("holding account on email list")
		}
	}
	found := false
	for _, r := range report.Held {
		if r.AccountID == owner && r.Event == slug && r.Site.ID == site {
			found = true
		}
	}
	if !found {
		t.Fatalf("event resource missing: %+v", report.Held)
	}
}
