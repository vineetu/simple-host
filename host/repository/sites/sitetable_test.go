package sites

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/vsriram/simple-host/host/model"
	"github.com/vsriram/simple-host/host/testdb"
)

func TestLoadSiteTableReadOnlySnapshot(t *testing.T) {
	dsn := testdb.Fresh(t)
	db, recorded := testdb.OpenRecording(dsn)
	defer db.Close()
	var uid, sid string
	if err := db.QueryRow(`INSERT INTO users(username,handle) VALUES ('table@example.com','table-owner') RETURNING id`).Scan(&uid); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`INSERT INTO sites(user_id,name,passcode_enc,domain_token) VALUES ($1,'before',$2,'private-domain-token') RETURNING id`, uid, []byte("sealed-test-passcode")).Scan(&sid); err != nil {
		t.Fatal(err)
	}
	recorded.Reset()
	called := false
	before := time.Now()
	table, started, err := LoadSiteTable(context.Background(), db, func(ctx context.Context, tx *sql.Tx) ([]model.RouteGroup, []model.RouteGroupSite, error) {
		called = true
		var isolation, readOnly string
		if err := tx.QueryRowContext(ctx, `SHOW transaction_isolation`).Scan(&isolation); err != nil {
			return nil, nil, err
		}
		if err := tx.QueryRowContext(ctx, `SHOW transaction_read_only`).Scan(&readOnly); err != nil {
			return nil, nil, err
		}
		if isolation != "repeatable read" || readOnly != "on" {
			t.Fatalf("load transaction: %s %s", isolation, readOnly)
		}
		if _, err := db.ExecContext(ctx, `UPDATE sites SET name='after' WHERE id=$1`, sid); err != nil {
			return nil, nil, err
		}
		var name string
		if err := tx.QueryRowContext(ctx, `SELECT name FROM sites WHERE id=$1`, sid).Scan(&name); err != nil {
			return nil, nil, err
		}
		if name != "before" {
			t.Fatal("load observed a later commit")
		}
		return nil, nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !called || started.Before(before.Add(-time.Second)) || started.After(time.Now()) {
		t.Fatal("missing loader or transaction timestamp")
	}
	if len(table.Sites) != 1 || table.Sites[0].Name != "before" || !table.Sites[0].RestrictedResponse {
		t.Fatalf("wrong routing response projection: %+v", table.Sites)
	}
	encoded, err := json.Marshal(table)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "sealed-test-passcode") || strings.Contains(string(encoded), "private-domain-token") {
		t.Fatal("credential entered snapshot")
	}
	queries := recorded.Statements()
	if queries[0] != "BEGIN" || queries[len(queries)-1] != "COMMIT" {
		t.Fatal("load was not one transaction")
	}
}

func TestLoadSiteTableGroupFailure(t *testing.T) {
	db, err := sql.Open("postgres", testdb.Fresh(t))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	fail := errors.New("group lookup unavailable")
	table, _, err := LoadSiteTable(context.Background(), db, func(context.Context, *sql.Tx) ([]model.RouteGroup, []model.RouteGroupSite, error) {
		return nil, nil, fail
	})
	if !errors.Is(err, fail) || len(table.Owners) != 0 || len(table.Sites) != 0 {
		t.Fatal("failed group load returned a partial table")
	}
}

func TestLookupRoutingSiteFamilyAndDomainPrecedence(t *testing.T) {
	db, recorded := testdb.OpenRecording(testdb.Fresh(t))
	defer db.Close()
	var owner, familySite, domainSite string
	if err := db.QueryRow(`INSERT INTO users(username,handle) VALUES ('family@example.com','family-owner') RETURNING id`).Scan(&owner); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`INSERT INTO sites(user_id,name) VALUES ($1,'prefix-page') RETURNING id`, owner).Scan(&familySite); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`INSERT INTO sites(user_id,name) VALUES ($1,'domain-page') RETURNING id`, owner).Scan(&domainSite); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO address_families(user_id,suffix,site_prefix,status,verified_at,cert_mode,cert_name,token) VALUES ($1,'family.example.org','prefix-','active',now(),'wildcard','fixture','not-routing')`, owner); err != nil {
		t.Fatal(err)
	}
	host := "page.family.example.org"
	for _, tc := range []struct {
		name, set, site string
		family          bool
	}{
		{"family", "custom_domain=NULL,previous_domain=NULL", familySite, true},
		{"current-domain", "custom_domain='page.family.example.org',previous_domain=NULL", domainSite, false},
		{"previous-domain", "custom_domain='elsewhere.example.org',previous_domain='page.family.example.org'", domainSite, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := db.Exec(`UPDATE sites SET `+tc.set+` WHERE id=$1`, domainSite); err != nil {
				t.Fatal(err)
			}
			recorded.Reset()
			table, err := LookupRoutingSite(context.Background(), db, model.RoutingAddress{Domain: host}, [2]string{host, host})
			if err != nil {
				t.Fatal(err)
			}
			if len(recorded.Statements()) != 1 || len(table.Sites) != 1 || table.Sites[0].ID != tc.site || (len(table.Families) == 1) != tc.family {
				t.Fatalf("wrong bounded routing lookup: %+v", table)
			}
			if tc.family {
				f := table.Families[0]
				if f.UserID != owner || f.Suffix != "family.example.org" || f.SitePrefix != "prefix-" || !f.VerifiedAt.Valid || f.CertMode != "wildcard" || f.CertName != "fixture" || f.Token != "" {
					t.Fatalf("wrong family routing projection: %+v", f)
				}
			}
		})
	}
	for _, missing := range []string{"missing.family.example.org", "page.nested.family.example.org"} {
		if _, err := LookupRoutingSite(context.Background(), db, model.RoutingAddress{Domain: missing}, [2]string{missing, missing}); !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("guessed unknown family site %s: %v", missing, err)
		}
	}
	if _, err := db.Exec(`UPDATE sites SET deleted_at=now() WHERE id=$1`, familySite); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE sites SET custom_domain=NULL,previous_domain=NULL WHERE id=$1`, domainSite); err != nil {
		t.Fatal(err)
	}
	if _, err := LookupRoutingSite(context.Background(), db, model.RoutingAddress{Domain: host}, [2]string{host, host}); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("discovered deleted family site: %v", err)
	}
}
