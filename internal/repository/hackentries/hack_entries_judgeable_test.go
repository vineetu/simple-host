package hackentries

import (
	"database/sql"
	_ "github.com/lib/pq"
	"github.com/vsriram/simple-host/internal/rules/hackentries"
	"os"
	"testing"
)

func TestEntryJudgeableSQL(t *testing.T) {
	dsn := os.Getenv("DB_DSN")
	if dsn == "" {
		t.Skip("DB_DSN unset")
	}
	database, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	tx, err := database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	_, err = tx.Exec(`CREATE TEMP TABLE event_teams (id integer); CREATE TEMP TABLE event_entries (team_id integer, submitted_at timestamptz, submit_settled_at timestamptz, excluded_at timestamptz)`)
	if err != nil {
		t.Fatal(err)
	}
	for i, c := range hackentries.JudgeableCases {
		t.Run(c.Name, func(t *testing.T) {
			if _, err := tx.Exec(`INSERT INTO event_teams VALUES ($1)`, i); err != nil {
				t.Fatal(err)
			}
			if c.Entry.Exists {
				_, err := tx.Exec(`INSERT INTO event_entries VALUES ($1, CASE WHEN $2 THEN now() END, CASE WHEN $3 THEN now() END, CASE WHEN $4 THEN now() END)`, i, c.Entry.SubmittedAt.Valid, c.Entry.SubmitSettledAt.Valid, c.Entry.ExcludedAt.Valid)
				if err != nil {
					t.Fatal(err)
				}
			}
			var got bool
			if err := tx.QueryRow(`SELECT `+EntryJudgeableSQL+` FROM event_teams t WHERE t.id=$1`, i).Scan(&got); err != nil {
				t.Fatal(err)
			}
			if got != c.Want || got != hackentries.Judgeable(c.Entry) {
				t.Fatalf("SQL = %v, Go = %v, want %v", got, hackentries.Judgeable(c.Entry), c.Want)
			}
		})
	}
}
