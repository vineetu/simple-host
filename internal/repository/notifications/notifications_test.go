package notifications

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"testing"

	_ "github.com/lib/pq"
	"github.com/vsriram/simple-host/internal/model"
	"github.com/vsriram/simple-host/internal/testdb"
)

func TestMain(m *testing.M) { testdb.Main(m) }

func TestPushRuleLookupFailureDoesNotWidenRecipients(t *testing.T) {
	dsn := os.Getenv("DB_DSN")
	if dsn == "" {
		t.Skip("DB_DSN unset")
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var owner, site string
	if err := db.QueryRow(`INSERT INTO users(username) VALUES(gen_random_uuid()::text) RETURNING id`).Scan(&owner); err != nil {
		t.Fatal(err)
	}
	defer db.Exec(`DELETE FROM users WHERE id=$1`, owner)
	if err := db.QueryRow(`INSERT INTO sites(user_id,name) VALUES($1,'push-rules-test') RETURNING id`, owner).Scan(&site); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	// Both a resource-specific restriction and a site-wide fallback must
	// survive a failed lookup: callers need an error, never default "both".
	for _, resource := range []string{"orders", ""} {
		t.Run("resource="+resource, func(t *testing.T) {
			if _, err := db.Exec(`DELETE FROM site_push_rules WHERE site_id=$1`, site); err != nil {
				t.Fatal(err)
			}
			want := model.PushRule{SiteID: site, Resource: resource, Recipients: "none", BodyColumn: "item"}
			if err := SetPushRule(ctx, db, want); err != nil {
				t.Fatal(err)
			}
			if got, err := GetPushRule(ctx, db, site, "orders"); err != nil || got != want {
				t.Fatalf("configured rule = %+v, %v; want %+v", got, err, want)
			}
			failed, cancel := context.WithCancel(ctx)
			cancel()
			if got, err := GetPushRule(failed, db, site, "orders"); !errors.Is(err, context.Canceled) || got != (model.PushRule{}) {
				t.Fatalf("failed lookup = %+v, %v; want empty rule and cancellation", got, err)
			}
			if got, err := GetPushRule(ctx, db, site, "orders"); err != nil || got != want {
				t.Fatalf("retry = %+v, %v; want %+v", got, err, want)
			}
		})
	}
}
