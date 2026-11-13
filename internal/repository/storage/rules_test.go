package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"reflect"
	"testing"

	_ "github.com/lib/pq"
	"github.com/vsriram/simple-host/internal/model"
	"github.com/vsriram/simple-host/internal/testdb"
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
	deny := model.AccessMatrix{Read: "nobody", Add: "nobody", Edit: "nobody", Delete: "nobody"}
	r := model.Resource{Name: "data", Kind: "sqlite", Read: "owner", Write: "owner", WriteMode: "add", SitePasscode: "inherit", Access: &deny, PresetBase: "rules"}
	values := []struct {
		name string
		raw  json.RawMessage
	}{
		{"NULL", nil},
		{"empty", json.RawMessage(`{"version":1,"policies":[]}`)},
		{"set", json.RawMessage(`{"version":1,"policies":[{"name":"public_read","command":"read","using":"true"}]}`)},
		{"back to NULL", nil},
	}
	fingerprints := map[string]string{}
	for _, test := range values {
		t.Run(test.name, func(t *testing.T) {
			r.Rules = test.raw
			r.ConvertedFrom = test.raw
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
			table := model.TableRule{Matrix: deny, PresetBase: "rules", Rules: test.raw}
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
			assertJSON(got.Rules, test.raw)
			assertJSON(got.ConvertedFrom, test.raw)
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
	if fingerprints["NULL"] == fingerprints["empty"] || fingerprints["empty"] == fingerprints["set"] || fingerprints["NULL"] != fingerprints["back to NULL"] {
		t.Fatal("fingerprint did not preserve NULL, empty and nonempty sets")
	}
}
