package storage

import (
	"context"
	"database/sql"
	"os"
	"testing"
)

func TestPruneVisitorAIWritesKeepsThirtyDays(t *testing.T) {
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
	if err := db.QueryRow(`INSERT INTO sites(user_id,name) VALUES($1,'prune-test') RETURNING id`, owner).Scan(&site); err != nil {
		t.Fatal(err)
	}
	for _, op := range []string{"old", "recent"} {
		if err := RecordVisitorAIWrite(ctx, db, site, "notes", op, owner, "Claude"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`UPDATE data_history SET created_at = now() - interval '31 days' WHERE site_id=$1 AND op='old'`, site); err != nil {
		t.Fatal(err)
	}
	if _, err := PruneVisitorAIWrites(ctx, db); err != nil {
		t.Fatal(err)
	}
	var ops []string
	rows, err := db.Query(`SELECT op FROM data_history WHERE site_id=$1`, site)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var op string
		if err := rows.Scan(&op); err != nil {
			t.Fatal(err)
		}
		ops = append(ops, op)
	}
	if len(ops) != 1 || ops[0] != "recent" {
		t.Fatalf("after prune: %v, want [recent]", ops)
	}
}
