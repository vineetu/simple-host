package upgrade

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/vsriram/simple-host/host/testdb"
)

func TestMain(m *testing.M) { testdb.Main(m) }

func TestFloor(t *testing.T) {
	for product, f := range floors {
		t.Run(product, func(t *testing.T) {
			db, err := sql.Open("postgres", testdb.Fresh(t))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			ctx := context.Background()
			exec := func(query string, args ...any) {
				t.Helper()
				if _, err := db.ExecContext(ctx, query, args...); err != nil {
					t.Fatal(err)
				}
			}
			exec(`DELETE FROM install_product; CREATE TABLE IF NOT EXISTS schema_migrations (name text PRIMARY KEY, applied_at timestamptz DEFAULT now()); TRUNCATE schema_migrations`)
			for _, name := range f.migrations {
				exec(`INSERT INTO schema_migrations (name) VALUES ($1)`, name)
			}
			check := func(want string, supported ...string) {
				t.Helper()
				// A successful read-only transaction proves the compatibility check
				// cannot create a ledger or record on an unsupported old install.
				tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				err = Check(ctx, tx, product, supported...)
				if want == "" && err != nil || want != "" && (err == nil || !strings.Contains(err.Error(), want)) {
					t.Fatalf("Check = %v, want %q", err, want)
				}
			}
			check("") // supported ledger, no product record
			exec(`INSERT INTO schema_migrations (name) VALUES ('9999-newer.sql')`)
			check("") // newer ledger retains every required entry
			exec(`DELETE FROM schema_migrations WHERE name=$1`, f.migrations[0])
			check("upgrade to " + f.release + " first")
			exec(`INSERT INTO schema_migrations (name) VALUES ($1)`, f.migrations[0])
			exec(`INSERT INTO install_product (product, release) VALUES ($1, '')`, product)
			check("") // empty release still uses the ledger
			exec(`ALTER TABLE oauth_grants RENAME TO missing_baseline_grants`)
			check("upgrade to " + f.release + " first")
			exec(`ALTER TABLE missing_baseline_grants RENAME TO oauth_grants`)
			exec(`UPDATE install_product SET release=$1`, f.release)
			check("")
			exec(`UPDATE install_product SET release='0.9.0'`)
			check("unsupported starting release")
			check("", "0.9.0")
			exec(`DROP TABLE install_product; DROP TABLE schema_migrations`)
			check("upgrade to " + f.release + " first")
		})
	}
}
