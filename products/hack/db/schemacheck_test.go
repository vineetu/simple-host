package db

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	_ "github.com/lib/pq"
	operationrepo "github.com/vsriram/simple-host/internal/repository/operations"
	"github.com/vsriram/simple-host/internal/testdb"
)

func TestMain(m *testing.M) { testdb.Main(m) }

func TestVerifyHackSchemaCatchesMissingGrantTeams(t *testing.T) {
	db, err := sql.Open("postgres", testdb.Fresh(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	ctx := context.Background()
	if err := VerifyHackSchema(ctx, db); err != nil {
		t.Fatalf("VerifyHackSchema on an up-to-date schema: %v", err)
	}
	if _, err := db.ExecContext(ctx, `DROP TABLE hack_grant_teams`); err != nil {
		t.Fatal(err)
	}
	err = VerifyHackSchema(ctx, db)
	if err == nil || !strings.Contains(err.Error(), "hack_grant_teams (whole table)") {
		t.Fatalf("VerifyHackSchema with missing grant teams: %v; want startup refusal naming the table", err)
	}
	if err := operationrepo.VerifySchema(ctx, db); err != nil {
		t.Fatalf("platform schema must not require Hack's grant teams: %v", err)
	}
}

func TestVerifyHackSchemaMissingColumnAndIndex(t *testing.T) {
	for _, tc := range []struct {
		name, change, restore, want string
	}{
		{
			name:    "column",
			change:  `ALTER TABLE events DROP COLUMN theme`,
			restore: `ALTER TABLE events ADD COLUMN theme text`,
			want: "database is behind this build; missing: events.theme\n" +
				"Run `simple-host migrate` against this database (on a small box, re-running the install\n" +
				"command does it), or apply the files in db/migrations/ by hand and record each with\n" +
				"`simple-host migrate -mark FILE`. A new database takes db/schema.sql",
		},
		{
			name:    "index",
			change:  `DROP INDEX event_teams_name_idx`,
			restore: `CREATE UNIQUE INDEX event_teams_name_idx ON event_teams(event_id, lower(name))`,
			want:    "database is behind this build; missing: index event_teams_name_idx\nRun `simple-host migrate` against this database",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, err := sql.Open("postgres", testdb.Fresh(t))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			ctx := context.Background()
			if err := VerifyHackSchema(ctx, db); err != nil {
				t.Fatal(err)
			}
			if _, err := db.ExecContext(ctx, tc.change); err != nil {
				t.Fatal(err)
			}
			if err := VerifyHackSchema(ctx, db); err == nil || err.Error() != tc.want {
				t.Fatalf("schema failure = %v; want %s", err, tc.want)
			}
			if err := operationrepo.VerifySchema(ctx, db); err != nil {
				t.Fatalf("platform requires product schema: %v", err)
			}
			if _, err := db.ExecContext(ctx, tc.restore); err != nil {
				t.Fatal(err)
			}
			if err := VerifyHackSchema(ctx, db); err != nil {
				t.Fatalf("restored schema: %v", err)
			}
		})
	}
}
