package migrations

import (
	"context"
	"os"
	"strings"
	"testing"
)

func TestStorageRulesMigration(t *testing.T) {
	db := openTestDB(t)
	migration, err := embedded.ReadFile("0018-storage-rules.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{"../schema.sql", "testdata/hack-v0.8.13-schema.sql"} {
		t.Run(source, func(t *testing.T) {
			schema, err := os.ReadFile(source)
			if err != nil {
				t.Fatal(err)
			}
			// The current snapshot with this slice's three additive statements removed
			// is the schema immediately before R2-S1. Never edit the frozen old snapshot.
			var before []string
			for _, line := range strings.Split(string(schema), "\n") {
				if strings.HasPrefix(line, "ALTER TABLE site_storage_") && (strings.Contains(line, "ADD COLUMN IF NOT EXISTS rules JSONB") || strings.Contains(line, "ADD COLUMN IF NOT EXISTS rules_converted_from JSONB")) {
					continue
				}
				before = append(before, line)
			}
			tx, err := db.BeginTx(context.Background(), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			// All fixture DDL and data stay in a transaction-local, throwaway schema.
			if _, err := tx.Exec(`CREATE SCHEMA r2_s1_migration_test; SET LOCAL search_path TO r2_s1_migration_test`); err != nil {
				t.Fatal(err)
			}
			if _, err := tx.Exec(strings.Join(before, "\n")); err != nil {
				t.Fatal(err)
			}
			var site string
			if err := tx.QueryRow(`WITH u AS (INSERT INTO users(username) VALUES('migration-rules@example.com') RETURNING id) INSERT INTO sites(user_id,name) SELECT id,'rules' FROM u RETURNING id`).Scan(&site); err != nil {
				t.Fatal(err)
			}
			if _, err := tx.Exec(`INSERT INTO site_storage_resources(site_id,name,kind) VALUES($1,'data','sqlite')`, site); err != nil {
				t.Fatal(err)
			}
			if _, err := tx.Exec(`INSERT INTO site_storage_tables(site_id,resource_name,table_name,acc_read,acc_add,acc_edit,acc_delete) VALUES($1,'data','posts','owner','owner','owner','owner')`, site); err != nil {
				t.Fatal(err)
			}
			for range 2 {
				if _, err := tx.Exec(string(migration)); err != nil {
					t.Fatal(err)
				}
			}
			var resourceNull, historyNull, tableNull bool
			if err := tx.QueryRow(`SELECT rules IS NULL, rules_converted_from IS NULL FROM site_storage_resources WHERE site_id=$1`, site).Scan(&resourceNull, &historyNull); err != nil {
				t.Fatal(err)
			}
			if err := tx.QueryRow(`SELECT rules IS NULL FROM site_storage_tables WHERE site_id=$1`, site).Scan(&tableNull); err != nil {
				t.Fatal(err)
			}
			if !resourceNull || !historyNull || !tableNull {
				t.Fatal("existing presets did not retain SQL NULL")
			}
			var columns int
			if err := tx.QueryRow(`SELECT count(*) FROM information_schema.columns WHERE table_schema=current_schema() AND data_type='jsonb' AND is_nullable='YES' AND column_default IS NULL AND ((table_name='site_storage_resources' AND column_name IN ('rules','rules_converted_from')) OR (table_name='site_storage_tables' AND column_name='rules'))`).Scan(&columns); err != nil {
				t.Fatal(err)
			}
			if columns != 3 {
				t.Fatalf("got %d nullable JSONB columns, want 3", columns)
			}
			const empty = `{"version":1,"policies":[]}`
			if _, err := tx.Exec(`UPDATE site_storage_resources SET rules=$1, rules_converted_from=$1`, empty); err != nil {
				t.Fatal(err)
			}
			if _, err := tx.Exec(`UPDATE site_storage_tables SET rules=$1`, empty); err != nil {
				t.Fatal(err)
			}
			if _, err := tx.Exec(string(migration)); err != nil {
				t.Fatal(err)
			}
			var kept bool
			if err := tx.QueryRow(`SELECT r.rules=$1::jsonb AND r.rules_converted_from=$1::jsonb AND t.rules=$1::jsonb FROM site_storage_resources r JOIN site_storage_tables t ON t.site_id=r.site_id AND t.resource_name=r.name`, empty).Scan(&kept); err != nil {
				t.Fatal(err)
			}
			if !kept {
				t.Fatal("reapplying migration changed saved rules")
			}
		})
	}
}
