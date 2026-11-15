package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"

	appstorage "github.com/vsriram/simple-host/internal/app/storage"
	storagerepo "github.com/vsriram/simple-host/internal/repository/storage"
	"github.com/vsriram/simple-host/internal/storage"
)

func TestKVMoveCommandUsage(t *testing.T) {
	t.Setenv("DB_DSN", "")
	for _, args := range [][]string{{}, {"--apply", "--verify", "--log", "unused"}, {"--rollback", "--apply", "--log", "unused"}, {"--rollback", "--scratch", "unused", "--log", "unused"}, {"--dry-run", "--log", "unused"}, {"--verify", "--scratch", "unused", "--log", "unused"}, {"--verify", "--log", "unused"}} {
		if code := runKVMoveCommand(args); code != 2 {
			t.Fatalf("%v: exit %d", args, code)
		}
	}
}

func TestKVMoveCommandRefusesWrongOwnerWithoutReport(t *testing.T) {
	info, err := os.Stat("/")
	if err != nil {
		t.Fatal(err)
	}
	if int(info.Sys().(*syscall.Stat_t).Uid) == os.Geteuid() {
		t.Skip("root directory belongs to the test user")
	}
	t.Setenv("DATA_DIR", "/")
	// Ownership must be checked before any database connection is needed.
	t.Setenv("DB_DSN", "postgres://unused@127.0.0.1:1/unused?sslmode=disable")
	for _, mode := range []string{"--dry-run", "--apply", "--rollback", "--verify"} {
		t.Run(mode, func(t *testing.T) {
			report := filepath.Join(t.TempDir(), "report.jsonl")
			args := []string{mode, "--log", report}
			scratch := filepath.Join(t.TempDir(), "scratch")
			if mode == "--dry-run" {
				args = append(args, "--scratch", scratch)
			}
			if code := runKVMoveCommand(args); code != 1 {
				t.Fatalf("wrong owner: exit=%d", code)
			}
			for _, path := range []string{report, scratch} {
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					t.Fatalf("refused command created %s: %v", path, err)
				}
			}
		})
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
	log := filepath.Join(root, "report.jsonl")
	// No install identity in the data directory: every mode but dry-run
	// refuses with a plain failure, and the namespace stays where it is.
	state := func() (store string, rows int) {
		t.Helper()
		if err := db.QueryRow(`SELECT kv_store,(SELECT count(*) FROM site_storage_kv WHERE site_id=$1) FROM site_storage_resources WHERE site_id=$1 AND name='data'`, site).Scan(&store, &rows); err != nil {
			t.Fatal(err)
		}
		return store, rows
	}
	for _, mode := range []string{"--apply", "--rollback", "--verify"} {
		if code := runKVMoveCommand([]string{mode, "--site", site, "--log", log}); code != 1 {
			t.Errorf("%s without an install identity: exit=%d", mode, code)
		}
		if _, err := os.Stat(log); !os.IsNotExist(err) {
			t.Fatalf("%s identity refusal created the report: %v", mode, err)
		}
	}
	if store, rows := state(); store != "postgres" || rows != 1 {
		t.Fatalf("refused commands changed the namespace: %s %d", store, rows)
	}
	if _, err = os.Stat(filepath.Join(root, ".maintenance.lock")); !os.IsNotExist(err) {
		t.Fatalf("refused commands created the lock file: %v", err)
	}
	id, err := storagerepo.CreateInstallID(context.Background(), db, "test-install-command")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(storage.InstallIDPath(root), []byte(id+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	// A server using this database from any directory holds the install lock.
	hold, err := appstorage.HoldInstallShared(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"--apply", "--rollback"} {
		if code := runKVMoveCommand([]string{mode, "--log", log}); code != 2 {
			t.Errorf("%s install-lock refusal exit=%d", mode, code)
		}
	}
	hold.Close()
	if store, rows := state(); store != "postgres" || rows != 1 {
		t.Fatalf("refused commands changed the namespace: %s %d", store, rows)
	}
	shared, err := storage.LockMaintenance(root, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"--apply", "--rollback"} {
		if code := runKVMoveCommand([]string{mode, "--log", log}); code != 2 {
			t.Errorf("%s lock refusal exit=%d", mode, code)
		}
	}
	if code := runKVMoveCommand([]string{"--dry-run", "--scratch", t.TempDir(), "--site", site, "--site", site, "--log", log}); code != 0 {
		t.Errorf("dry-run exit=%d", code)
	}
	shared.Close()
	for _, mode := range []string{"--apply", "--apply", "--verify", "--rollback", "--rollback", "--verify", "--apply", "--rollback"} {
		if code := runKVMoveCommand([]string{mode, "--site", site, "--log", log}); code != 0 {
			t.Fatalf("%s exit=%d", mode, code)
		}
	}
	// Repeating a successful rollback reports the named namespace and leaves
	// its data unchanged, with a successful command exit.
	repeatLog := filepath.Join(t.TempDir(), "repeat.jsonl")
	namespace := storagerepo.KVNamespace{SiteID: site, Name: "data"}
	before, beforeUsed, err := storagerepo.KVMoveSnapshot(context.Background(), db, namespace)
	if err != nil {
		t.Fatal(err)
	}
	if code := runKVMoveCommand([]string{"--rollback", "--site", site, "--log", repeatLog}); code != 0 {
		t.Fatalf("repeated rollback exit=%d", code)
	}
	repeated, err := os.ReadFile(repeatLog)
	var skipped appstorage.KVMoveReport
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(repeated, &skipped); err != nil || skipped.Site != site || skipped.Resource != "data" || !strings.HasPrefix(skipped.Status, "skipped: already restored") {
		t.Fatalf("repeated rollback report: %+v, %v", skipped, err)
	}
	after, afterUsed, err := storagerepo.KVMoveSnapshot(context.Background(), db, namespace)
	if err != nil || !reflect.DeepEqual(before, after) || beforeUsed != afterUsed {
		t.Fatalf("repeated rollback changed data: before=%+v after=%+v err=%v", before, after, err)
	}
	if store, rows := state(); store != "postgres" || rows != 1 {
		t.Fatalf("repeated rollback changed the marker: %s %d", store, rows)
	}
	// Already restored: Postgres holds rows for a namespace marked sqlite.
	// Skipped and reported, exit 0.
	if code := runKVMoveCommand([]string{"--apply", "--site", site, "--log", log}); code != 0 {
		t.Fatalf("apply exit=%d", code)
	}
	if _, err = db.Exec(`INSERT INTO site_storage_kv(site_id,resource_name,key,value) VALUES($1,'data','stray','1')`, site); err != nil {
		t.Fatal(err)
	}
	if code := runKVMoveCommand([]string{"--rollback", "--site", site, "--log", log}); code != 0 {
		t.Fatalf("rollback of an already-restored namespace: exit=%d", code)
	}
	if store, rows := state(); store != "sqlite" || rows != 1 {
		t.Fatalf("skipped rollback changed the namespace: %s %d", store, rows)
	}
	report, err := os.ReadFile(log)
	if err != nil || !strings.Contains(string(report), "skipped: still on SQLite; Postgres already holds rows for it (run --verify)") {
		t.Fatalf("already-restored namespace not reported: %v", err)
	}
	// A namespace with saved rules is refused by name with a non-zero exit.
	if _, err = db.Exec(`DELETE FROM site_storage_kv WHERE site_id=$1`, site); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE site_storage_resources SET rules='{}' WHERE site_id=$1 AND name='data'`, site); err != nil {
		t.Fatal(err)
	}
	if code := runKVMoveCommand([]string{"--rollback", "--site", site, "--log", log}); code != 1 {
		t.Fatalf("rollback of a rules namespace: exit=%d", code)
	}
	if store, _ := state(); store != "sqlite" {
		t.Fatalf("rules namespace rolled back: %s", store)
	}
}
