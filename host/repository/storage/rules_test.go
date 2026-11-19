package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"reflect"
	"testing"

	_ "github.com/lib/pq"
	"github.com/vsriram/simple-host/host/model"
	"github.com/vsriram/simple-host/host/testdb"
)

func TestMain(m *testing.M) { testdb.Main(m) }

func TestResourceRulesRoundTrip(t *testing.T) {
	dsn := os.Getenv("DB_DSN")
	if dsn == "" {
		t.Skip("DB_DSN unset")
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	var owner, site string
	if err := db.QueryRow(`INSERT INTO users(username) VALUES(gen_random_uuid()::text) RETURNING id`).Scan(&owner); err != nil {
		t.Fatal(err)
	}
	defer db.Exec(`DELETE FROM users WHERE id=$1`, owner)
	if err := db.QueryRow(`INSERT INTO sites(user_id,name) VALUES($1,'rules-test') RETURNING id`, owner).Scan(&site); err != nil {
		t.Fatal(err)
	}
	// A resource saved before rules: no stored rules, older columns only.
	if _, err := db.Exec(`INSERT INTO site_storage_resources(site_id,name,kind,read_policy,write_policy,write_mode) VALUES($1,'data','sqlite','anyone','anyone','full')`, site); err != nil {
		t.Fatal(err)
	}
	before, err := LoadResource(ctx, db, site, "data")
	if err != nil || before.Rules != nil || !before.BeforePresets || before.Read != "anyone" || before.WriteMode != "full" {
		t.Fatalf("legacy load: %+v %v", before, err)
	}
	r := model.Resource{Name: "data", Kind: "sqlite", SitePasscode: "inherit"}
	values := []struct {
		name string
		raw  json.RawMessage
	}{
		{"empty", json.RawMessage(`{"version":1,"policies":[]}`)},
		{"set", json.RawMessage(`{"version":1,"policies":[{"name":"public_read","command":"read","using":"true"}]}`)},
	}
	fingerprints := map[string]string{}
	for _, test := range values {
		t.Run(test.name, func(t *testing.T) {
			r.Rules = test.raw
			tx, err := db.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			if _, err := UpsertResource(ctx, tx, site, r); err != nil {
				t.Fatal(err)
			}
			if _, err := DeleteResourceTables(ctx, tx, site, r.Name); err != nil {
				t.Fatal(err)
			}
			table := model.TableRule{Rules: test.raw}
			if _, err := InsertResourceTable(ctx, tx, site, r.Name, "posts", table); err != nil {
				t.Fatal(err)
			}
			if err := tx.Commit(); err != nil {
				t.Fatal(err)
			}
			got, err := LoadResource(ctx, db, site, r.Name)
			if err != nil {
				t.Fatal(err)
			}
			assertJSON := func(got, want json.RawMessage) {
				t.Helper()
				if (got == nil) != (want == nil) {
					t.Fatalf("NULL mismatch: got %s want %s", got, want)
				}
				if want == nil {
					return
				}
				var a, b any
				if err := json.Unmarshal(got, &a); err != nil {
					t.Fatal(err)
				}
				if err := json.Unmarshal(want, &b); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(a, b) {
					t.Fatalf("JSON mismatch: got %s want %s", got, want)
				}
			}
			if got.BeforePresets || got.Read != "owner" || got.Write != "owner" || got.WriteMode != "add" {
				t.Fatalf("a rules save writes the older columns' fill: %+v", got)
			}
			assertJSON(got.Rules, test.raw)
			assertJSON(got.Tables["posts"].Rules, test.raw)
			listed, err := ListResources(ctx, db, site)
			if err != nil {
				t.Fatal(err)
			}
			if len(listed) != 1 || !reflect.DeepEqual(listed[0], got) {
				t.Fatal("list and load differ")
			}
			fingerprints[test.name] = got.Fingerprint()
		})
	}
	if before.Fingerprint() == fingerprints["empty"] || fingerprints["empty"] == fingerprints["set"] {
		t.Fatal("fingerprint did not tell NULL, empty and nonempty sets apart")
	}
}
