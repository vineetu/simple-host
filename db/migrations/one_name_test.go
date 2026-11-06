package migrations

import (
	"context"
	"database/sql"
	"os"
	"testing"
)

func TestHackOneNameBackfill(t *testing.T) {
	var db *sql.DB
	if dsn := os.Getenv("DB_DSN"); dsn != "" {
		var err error
		db, err = sql.Open("postgres", dsn)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { db.Close() })
	} else {
		db = openTestDB(t)
	}
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	// Temporary tables isolate the data-only migration from other fixtures.
	_, err = tx.Exec(`CREATE TEMP TABLE users (id int PRIMARY KEY, display_name text);
 CREATE TEMP TABLE event_members (user_id int,event_id int,display_name text,joined_at timestamptz);
 CREATE TEMP TABLE event_former_judges (user_id int,event_id int,display_name text,left_at timestamptz);
 INSERT INTO users VALUES (1,NULL),(2,'Account Name'),(3,' '),(4,''),(5,''),(6,'');
 INSERT INTO event_members VALUES (1,1,' Old Name ','2026-01-01'),(1,2,' Recent Name ','2026-02-01'),
 (2,1,'Wrong Name','2026-02-01'),(4,1,'   ','2026-02-01'),
 (5,1,'Member Name','2026-01-01'),(6,1,'Tie One','2026-01-01');
 INSERT INTO event_former_judges VALUES (3,1,' Former Judge ','2026-02-01'),(4,1,'  ','2026-02-01'),
 (5,2,' Later Former ','2026-02-01'),(6,2,'Tie Two','2026-01-01');`)
	if err != nil {
		t.Fatal(err)
	}
	migration, err := embedded.ReadFile("0006-hack-one-name.sql")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err = tx.Exec(string(migration)); err != nil {
			t.Fatal(err)
		}
		for id, want := range map[int]string{1: "Recent Name", 2: "Account Name", 3: "Former Judge", 4: "", 5: "Later Former", 6: "Tie Two"} {
			var got string
			if err = tx.QueryRow(`SELECT COALESCE(display_name,'') FROM users WHERE id=$1`, id).Scan(&got); err != nil {
				t.Fatal(err)
			}
			if got != want {
				t.Fatalf("run %d user %d = %q, want %q", i, id, got, want)
			}
		}
	}
	if _, err = tx.Exec(`TRUNCATE users,event_members,event_former_judges`); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(string(migration)); err != nil {
		t.Fatalf("fresh schema: %v", err)
	}
}
