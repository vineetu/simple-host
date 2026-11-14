package hackteams

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/lib/pq"
	"github.com/vsriram/simple-host/internal/testdb"
)

func TestMain(m *testing.M) { testdb.Main(m) }

const repoRoot = "../../../.."

func seedID(n int) string { return fmt.Sprintf("00000000-0000-4000-8000-%012d", n) }
func execSQL(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatal(err)
	}
}
func runSQLFile(dsn, path string, vars ...string) error {
	args := []string{"-X", "-d", dsn, "-v", "ON_ERROR_STOP=1", "-f", filepath.Join(repoRoot, path)}
	for _, v := range vars {
		args = append(args, "-v", v)
	}
	cmd := exec.Command("psql", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("psql %s: %w: %s", path, err, out)
	}
	return nil
}
func selectedFixture(t *testing.T) (*sql.DB, string) {
	t.Helper()
	dsn := testdb.Fresh(t)
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := runSQLFile(dsn, "scripts/split/seed/seed.sql"); err != nil {
		t.Fatal(err)
	}
	return db, dsn
}

// Rehearsals start from the recorded production schema, never the candidate's
// schema. Like testdb.Fresh, this requires the disposable integration server.
func selectedRehearsalFixture(t *testing.T, dump string) (*sql.DB, string) {
	t.Helper()
	if os.Getenv("TESTDB_ISOLATE") != "1" || os.Getenv("DB_DSN") == "" {
		t.Skip("DB_DSN unset or TESTDB_ISOLATE!=1: no disposable database server")
	}
	u, err := url.Parse(os.Getenv("DB_DSN"))
	if err != nil {
		t.Fatal("invalid DB_DSN URL")
	}
	u.Path = "/postgres"
	admin, err := sql.Open("postgres", u.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { admin.Close() })
	name := fmt.Sprintf("d2s6_%d_%d", os.Getpid(), time.Now().UnixNano())
	execSQL(t, admin, `CREATE DATABASE `+pq.QuoteIdentifier(name)+` TEMPLATE template0`)
	t.Cleanup(func() {
		if _, err := admin.Exec(`DROP DATABASE ` + pq.QuoteIdentifier(name) + ` WITH (FORCE)`); err != nil {
			t.Error(err)
		}
	})
	u.Path = "/" + name
	dsn := u.String()
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	b, err := os.ReadFile(filepath.Join(repoRoot, "scripts/split/evidence", dump))
	if err != nil {
		t.Fatal(err)
	}
	// These two pg_dump client guards are psql commands, not server SQL.
	var lines []string
	for _, line := range strings.Split(string(b), "\n") {
		if !strings.HasPrefix(line, `\restrict `) && !strings.HasPrefix(line, `\unrestrict `) {
			lines = append(lines, line)
		}
	}
	cmd := exec.Command("psql", "-X", "-d", dsn, "-v", "ON_ERROR_STOP=1")
	cmd.Stdin = strings.NewReader(strings.Join(lines, "\n"))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("load %s: %v: %s", dump, err, out)
	}
	if err := runSQLFile(dsn, "scripts/split/seed/seed.sql"); err != nil {
		t.Fatal(err)
	}
	return db, dsn
}

func migrateSelections(t *testing.T, db *sql.DB) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(repoRoot, "db/migrations/0020-hack-grant-teams.sql"))
	if err != nil {
		t.Fatal(err)
	}
	execSQL(t, db, string(b))
}
func assertSelection(t *testing.T, db *sql.DB, grant, wantColumn, wantTable int) {
	t.Helper()
	var column, table sql.NullString
	if err := db.QueryRow(`SELECT g.selected_team_id,h.team_id FROM oauth_grants g LEFT JOIN hack_grant_teams h ON h.grant_id=g.id WHERE g.id=$1`, seedID(grant)).Scan(&column, &table); err != nil {
		t.Fatal(err)
	}
	for i, v := range []sql.NullString{column, table} {
		want := []int{wantColumn, wantTable}[i]
		if v.Valid != (want != 0) || (v.Valid && v.String != seedID(want)) {
			t.Fatalf("grant %d store %d: got %v, want %d", grant, i, v, want)
		}
	}
}
func equalSelections(t *testing.T, db *sql.DB) {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM oauth_grants g FULL JOIN hack_grant_teams h ON h.grant_id=g.id WHERE g.selected_team_id IS DISTINCT FROM h.team_id`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("%d selections differ", n)
	}
}

func TestConnectorTargetMigration(t *testing.T) {
	for _, dump := range []string{"simple-host.schema.sql", "simple-hack.schema.sql"} {
		t.Run(dump, func(t *testing.T) {
			db, _ := selectedRehearsalFixture(t, dump)
			migrateSelections(t, db)
			assertSelection(t, db, 31, 11, 11)
			assertSelection(t, db, 32, 13, 13) // The deleted team's choice must survive.
			var before, after time.Time
			if err := db.QueryRow(`SELECT selected_at FROM hack_grant_teams WHERE grant_id=$1`, seedID(31)).Scan(&before); err != nil {
				t.Fatal(err)
			}
			migrateSelections(t, db)
			if err := db.QueryRow(`SELECT selected_at FROM hack_grant_teams WHERE grant_id=$1`, seedID(31)).Scan(&after); err != nil {
				t.Fatal(err)
			}
			if !before.Equal(after) {
				t.Fatal("second migration changed selected_at")
			}
			equalSelections(t, db)
		})
	}
}

func TestConnectorTargetWayBackRehearsal(t *testing.T) {
	for _, dump := range []string{"simple-host.schema.sql", "simple-hack.schema.sql"} {
		t.Run(dump, func(t *testing.T) {
			db, dsn := selectedRehearsalFixture(t, dump)
			migrateSelections(t, db)
			reconcile := func() {
				t.Helper()
				if err := runSQLFile(dsn, "scripts/split/selected-team-reconcile.sql", "column_is_newer=yes"); err != nil {
					t.Fatal(err)
				}
			}
			restore := func() {
				t.Helper()
				if err := runSQLFile(dsn, "scripts/split/selected-team-restore.sql"); err != nil {
					t.Fatal(err)
				}
			}
			t.Run("first deploy after an old writer changed the selection", func(t *testing.T) {
				execSQL(t, db, `UPDATE oauth_grants SET selected_team_id=$1 WHERE id=$2`, seedID(12), seedID(31))
				for _, vars := range [][]string{nil, {"column_is_newer=no"}, {"column_is_newer=true"}} {
					if err := runSQLFile(dsn, "scripts/split/selected-team-reconcile.sql", vars...); err == nil {
						t.Fatal("unguarded reconcile accepted")
					}
					assertSelection(t, db, 31, 12, 11)
				}
				reconcile()
				reconcile()
				assertSelection(t, db, 31, 12, 12)
				got, err := (ConnectorTarget{}).Selected(context.Background(), db, seedID(31))
				if err != nil || got.String != seedID(12) {
					t.Fatalf("new reader: %v, %v", got, err)
				}
				equalSelections(t, db)
			})
			t.Run("before retirement old program can read the complete column", func(t *testing.T) { equalSelections(t, db) })
			t.Run("forward after old program changed cleared and deleted grants", func(t *testing.T) {
				execSQL(t, db, `UPDATE oauth_grants SET selected_team_id=NULL WHERE id=$1`, seedID(31))
				execSQL(t, db, `INSERT INTO oauth_grants (id,user_id,client_id,scope,resource,selected_team_id) SELECT $1,user_id,client_id,scope,resource,$2 FROM oauth_grants WHERE id=$3`, seedID(33), seedID(11), seedID(31))
				reconcile()
				execSQL(t, db, `DELETE FROM oauth_grants WHERE id=$1`, seedID(33))
				reconcile()
				reconcile()
				assertSelection(t, db, 31, 0, 0)
				assertSelection(t, db, 32, 13, 13)
				equalSelections(t, db)
			})
			t.Run("after retirement previous table reader needs no repair", func(t *testing.T) {
				execSQL(t, db, `INSERT INTO hack_grant_teams (grant_id,team_id) VALUES ($1,$2)`, seedID(31), seedID(12))
				execSQL(t, db, `DELETE FROM hack_grant_teams WHERE grant_id=$1`, seedID(32))
				got, err := (ConnectorTarget{}).Selected(context.Background(), db, seedID(31))
				if err != nil || got.String != seedID(12) {
					t.Fatalf("table reader: %v, %v", got, err)
				}
				assertSelection(t, db, 31, 0, 12)
				assertSelection(t, db, 32, 13, 0)
			})
			t.Run("after retirement restore column including absent selections", func(t *testing.T) {
				restore()
				restore()
				assertSelection(t, db, 31, 12, 12)
				assertSelection(t, db, 32, 0, 0)
				equalSelections(t, db)
			})
			t.Run("forward again after older program ran", func(t *testing.T) {
				execSQL(t, db, `UPDATE oauth_grants SET selected_team_id=$1 WHERE id=$2`, seedID(13), seedID(31))
				reconcile()
				reconcile()
				assertSelection(t, db, 31, 13, 13)
				equalSelections(t, db)
			})
		})
	}
}

func TestConnectorTargetSelectionAuthorizationAndAtomicity(t *testing.T) {
	db, _ := selectedFixture(t)
	migrateSelections(t, db)
	target := ConnectorTarget{}
	ctx := context.Background()
	for _, tc := range []struct {
		name        string
		user, team  int
		setup, undo string
	}{
		{name: "non-member", user: 1, team: 12},
		{name: "other grant owner", user: 2, team: 12},
		{name: "deleted team", user: 1, team: 13},
		{name: "scope", user: 1, team: 11, setup: `UPDATE oauth_grants SET scope='sites'`, undo: `UPDATE oauth_grants SET scope='sites events'`},
		{name: "pending", user: 1, team: 11, setup: `UPDATE event_members SET approval_status='pending' WHERE role='participant'`, undo: `UPDATE event_members SET approval_status='approved' WHERE role='participant'`},
		{name: "role", user: 1, team: 11, setup: `UPDATE event_members SET role='judge',team_id=NULL WHERE user_id='00000000-0000-4000-8000-000000000001'`, undo: `UPDATE event_members SET role='participant',team_id='00000000-0000-4000-8000-000000000011' WHERE user_id='00000000-0000-4000-8000-000000000001'`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.setup != "" {
				execSQL(t, db, tc.setup)
				defer execSQL(t, db, tc.undo)
			}
			ok, err := target.Select(ctx, db, seedID(31), seedID(tc.user), seedID(tc.team))
			if err != nil || ok {
				t.Fatalf("Select=%v,%v", ok, err)
			}
			assertSelection(t, db, 31, 11, 11)
		})
	}
	// The UPDATE would change the old column; a missing INSERT target prevents all of it.
	execSQL(t, db, `UPDATE event_members SET team_id=$1 WHERE user_id=$2`, seedID(12), seedID(1))
	execSQL(t, db, `DROP TABLE hack_grant_teams`)
	if ok, err := target.Select(ctx, db, seedID(31), seedID(1), seedID(12)); err == nil || ok {
		t.Fatal("missing table accepted")
	}
	var old string
	if err := db.QueryRow(`SELECT selected_team_id FROM oauth_grants WHERE id=$1`, seedID(31)).Scan(&old); err != nil || old != seedID(11) {
		t.Fatalf("partial write: %q %v", old, err)
	}
}

func TestConnectorTargetConcurrentSelections(t *testing.T) {
	db, _ := selectedFixture(t)
	migrateSelections(t, db)
	// A participant may select teams in two different events.
	execSQL(t, db, `INSERT INTO users (id,username,event_account) VALUES ($1,'second-holding@example.com',true)`, seedID(5))
	execSQL(t, db, `INSERT INTO events (id,slug,account_id,title,organiser_name,contact_email,join_code,judge_code) VALUES ($1,'second-event',$2,'Second','Org','org@example.com','second-join','second-judge')`, seedID(20), seedID(5))
	execSQL(t, db, `INSERT INTO event_teams (id,event_id,slug,name,code) VALUES ($1,$2,'second-team','Second','second-team-code')`, seedID(14), seedID(20))
	execSQL(t, db, `INSERT INTO event_members (event_id,user_id,role,team_id,approval_status) VALUES ($1,$2,'participant',$3,'approved')`, seedID(20), seedID(1), seedID(14))
	for i := 0; i < 20; i++ {
		start := make(chan struct{})
		done := make(chan error, 2)
		for _, team := range []int{11, 14} {
			go func(team int) {
				<-start
				ok, err := (ConnectorTarget{}).Select(context.Background(), db, seedID(31), seedID(1), seedID(team))
				if err == nil && !ok {
					err = fmt.Errorf("selection refused")
				}
				done <- err
			}(team)
		}
		close(start)
		for j := 0; j < 2; j++ {
			if err := <-done; err != nil {
				t.Fatal(err)
			}
		}
		equalSelections(t, db)
	}
}

func TestConnectorTargetReaders(t *testing.T) {
	db, _ := selectedFixture(t)
	migrateSelections(t, db)
	ctx := context.Background()
	target := ConnectorTarget{}
	execSQL(t, db, `INSERT INTO oauth_grants (id,user_id,client_id,scope,resource) SELECT $1,user_id,client_id,scope,resource FROM oauth_grants WHERE id=$2`, seedID(33), seedID(31))
	grants, err := EventsGrants(ctx, db, []string{seedID(1)}, []string{"https://example.org/mcp"})
	if err != nil || len(grants) != 2 {
		t.Fatalf("unselected grant omitted: %v %v", grants, err)
	}
	selected := 0
	for _, g := range grants {
		if g.SelectedTeamID.Valid {
			selected++
		}
	}
	if selected != 1 {
		t.Fatalf("selected grants=%d", selected)
	}
	// The table wins over the column, and duplicate grants produce one label.
	execSQL(t, db, `INSERT INTO hack_grant_teams (grant_id,team_id) VALUES ($1,$2)`, seedID(33), seedID(11))
	labels, err := target.Labels(ctx, db, seedID(1))
	if err != nil || len(labels["seed-connected-app"]) != 1 || labels["seed-connected-app"][0].Team != "Seed Live" {
		t.Fatalf("labels: %v %v", labels, err)
	}
	execSQL(t, db, `UPDATE oauth_grants SET selected_team_id=NULL`)
	again, err := target.Labels(ctx, db, seedID(1))
	if err != nil || !reflect.DeepEqual(labels, again) {
		t.Fatalf("labels read old column: %v %v", again, err)
	}
	labels, err = target.Labels(ctx, db, seedID(2))
	if err != nil || len(labels) != 0 {
		t.Fatalf("deleted team's label: %v %v", labels, err)
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	execSQL(t, db, `DELETE FROM hack_grant_teams WHERE grant_id=$1`, seedID(33))
	if _, err := tx.Exec(`INSERT INTO hack_grant_teams (grant_id,team_id) VALUES ($1,$2)`, seedID(33), seedID(12)); err != nil {
		t.Fatal(err)
	}
	got, err := target.Selected(ctx, tx, seedID(33))
	if err != nil || got.String != seedID(12) {
		t.Fatalf("same transaction selection: %v %v", got, err)
	}
	if _, err := target.Labels(ctx, tx, seedID(1)); err != nil {
		t.Fatal(err)
	}
	var one int
	if err := tx.QueryRow(`SELECT 1`).Scan(&one); err != nil {
		t.Fatalf("Labels left rows open: %v", err)
	}
	if got, err := target.Selected(ctx, db, seedID(99)); err != nil || got.Valid {
		t.Fatalf("absent: %v %v", got, err)
	}
}
