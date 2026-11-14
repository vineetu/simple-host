package server

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/vsriram/simple-host/internal/storage"
)

func TestKVMoveCommandUsage(t *testing.T) {
	t.Setenv("DB_DSN", "")
	for _, args := range [][]string{{}, {"--apply", "--verify", "--log", "unused"}, {"--dry-run", "--log", "unused"}, {"--verify", "--scratch", "unused", "--log", "unused"}, {"--verify", "--log", "unused"}} {
		if code := runKVMoveCommand(args); code != 2 {
			t.Fatalf("%v: exit %d", args, code)
		}
	}
}

func TestKVMoveCommandModesAndLockExit(t *testing.T) {
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
	if err = db.QueryRow(`INSERT INTO users(username) VALUES(gen_random_uuid()::text) RETURNING id`).Scan(&owner); err != nil {
		t.Fatal(err)
	}
	defer db.Exec(`DELETE FROM users WHERE id=$1`, owner)
	if err = db.QueryRow(`INSERT INTO sites(user_id,name) VALUES($1,'move-command') RETURNING id`, owner).Scan(&site); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO site_storage_resources(site_id,name,kind) VALUES($1,'data','kv')`, site); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO site_storage_kv(site_id,resource_name,key,value) VALUES($1,'data','key','{"a":1}')`, site); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	t.Setenv("DATA_DIR", root)
	if err = os.MkdirAll(filepath.Join(root, "by-id", owner, "move-command"), 0700); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(t.TempDir(), "report.jsonl")
	shared, err := storage.LockMaintenance(root, false)
	if err != nil {
		t.Fatal(err)
	}
	if code := runKVMoveCommand([]string{"--apply", "--log", log}); code != 2 {
		t.Errorf("lock refusal exit=%d", code)
	}
	if code := runKVMoveCommand([]string{"--dry-run", "--scratch", t.TempDir(), "--site", site, "--site", site, "--log", log}); code != 0 {
		t.Errorf("dry-run exit=%d", code)
	}
	shared.Close()
	for _, mode := range []string{"--apply", "--apply", "--verify"} {
		if code := runKVMoveCommand([]string{mode, "--site", site, "--log", log}); code != 0 {
			t.Fatalf("%s exit=%d", mode, code)
		}
	}
}
