package server

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	appstorage "github.com/vsriram/simple-host/internal/app/storage"
	repo "github.com/vsriram/simple-host/internal/repository/storage"
	"github.com/vsriram/simple-host/internal/storage"
)

func TestMigrateRulesCommandIsWired(t *testing.T) {
	for _, c := range commands(nil) {
		if c.name == "migrate-rules" {
			return
		}
	}
	t.Fatal("migrate-rules is not a subcommand")
}

func TestMigrateRulesCommandUsage(t *testing.T) {
	t.Setenv("DB_DSN", "")
	for _, args := range [][]string{
		{},
		{"--dry-run", "--all"},       // no plan
		{"--dry-run", "--plan", "p"}, // no scope
		{"--dry-run", "--plan", "p", "--all", "--account", "a"}, // both scopes
		{"--dry-run", "--apply", "--plan", "p", "--all"},        // two modes
		{"--apply"},                         // no plan
		{"--apply", "--plan", "p", "--all"}, // scope belongs to the dry run
		{"--apply", "--plan", "p", "--site", "s", "--resource", "r"}, // one resource belongs to revert
		{"--revert"}, // neither a plan nor a resource
		{"--revert", "--plan", "p", "--site", "s", "--resource", "r"}, // both
		{"--revert", "--site", "s"},                                   // half a resource
		{"--revert", "--resource", "r"},
		{"--dry-run", "--plan", "p", "--all", "extra"},
		{"--dry-run", "--plan", "p", "--all"}, // well formed, but DB_DSN is not set
	} {
		if code := runMigrateRulesCommand(args); code != 2 {
			t.Fatalf("%v: exit %d", args, code)
		}
	}
}

func TestMigrateRulesCommandModesAndExitCodes(t *testing.T) {
	dsn := os.Getenv("DB_DSN")
	if dsn == "" {
		t.Skip("DB_DSN unset")
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var b [8]byte
	if _, err = rand.Read(b[:]); err != nil {
		t.Fatal(err)
	}
	handle := "mc" + hex.EncodeToString(b[:])
	var owner, site string
	if err = db.QueryRow(`INSERT INTO users(username,handle) VALUES(gen_random_uuid()::text,$1) RETURNING id`, handle).Scan(&owner); err != nil {
		t.Fatal(err)
	}
	defer db.Exec(`DELETE FROM users WHERE id=$1`, owner)
	if err = db.QueryRow(`INSERT INTO sites(user_id,name) VALUES($1,'rules-command') RETURNING id`, owner).Scan(&site); err != nil {
		t.Fatal(err)
	}
	insert := `INSERT INTO site_storage_resources(site_id,name,kind,read_policy,write_policy,write_mode,acc_read,acc_add,acc_edit,acc_delete,preset_base,kv_store) VALUES($1,$2,'kv','anyone','owner','add','anyone','owner','owner','owner','public','sqlite')`
	if _, err = db.Exec(insert, site, "menu"); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	id, err := repo.CreateInstallID(context.Background(), db, "migrate-rules-command-test")
	if err != nil {
		t.Fatal(err)
	}
	if err := storage.WriteInstallID(root, id); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DATA_DIR", root)
	if err = os.MkdirAll(filepath.Join(root, "by-id", owner, "rules-command"), 0700); err != nil {
		t.Fatal(err)
	}
	plan := filepath.Join(t.TempDir(), "plan.json")
	state := func() (rules, record bool) {
		t.Helper()
		if err := db.QueryRow(`SELECT rules IS NOT NULL, rules_converted_from IS NOT NULL FROM site_storage_resources WHERE site_id=$1 AND name='menu'`, site).Scan(&rules, &record); err != nil {
			t.Fatal(err)
		}
		return
	}
	run := func(want int, args ...string) {
		t.Helper()
		if code := runMigrateRulesCommand(args); code != want {
			t.Fatalf("%v: exit %d, want %d", args, code, want)
		}
	}
	// The dry run may run beside a server; apply and revert may not.
	server, err := storage.LockMaintenance(root, false)
	if err != nil {
		t.Fatal(err)
	}
	run(0, "--dry-run", "--plan", plan, "--account", handle)
	if info, err := os.Stat(plan); err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("plan file: %v", err)
	}
	read, err := appstorage.ReadConvertPlan(plan)
	if err != nil || len(read.Resources) != 1 || read.Resources[0].Name != "menu" || read.Resources[0].Account != handle {
		t.Fatalf("plan: %+v %v", read, err)
	}
	run(2, "--apply", "--plan", plan)
	run(2, "--revert", "--plan", plan)
	run(2, "--revert", "--site", site, "--resource", "menu")
	if rules, record := state(); rules || record {
		t.Fatal("a refused run changed the resource")
	}
	server.Close()
	// A copy of the directory must not bypass a live server's database lock.
	hold, err := appstorage.HoldInstallShared(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	run(2, "--apply", "--plan", plan)
	run(2, "--revert", "--plan", plan)
	hold.Close()
	run(0, "--apply", "--plan", plan)
	if rules, record := state(); !rules || !record {
		t.Fatal("apply did not convert")
	}
	run(0, "--apply", "--plan", plan) // already converted
	run(0, "--revert", "--site", site, "--resource", "menu")
	if rules, record := state(); rules || record {
		t.Fatal("revert did not restore")
	}
	run(1, "--revert", "--plan", plan) // nothing left to put back: reported, not silent
	run(0, "--apply", "--plan", plan)
	run(0, "--revert", "--plan", plan)
	// A resource that needs a person: the plan is written and the exit is 3.
	if _, err = db.Exec(`INSERT INTO site_storage_resources(site_id,name,kind,read_policy,write_policy,write_mode,kv_store) VALUES($1,'old','kv','signed-in','signed-in','full','sqlite')`, site); err != nil {
		t.Fatal(err)
	}
	run(3, "--dry-run", "--plan", plan, "--account", handle)
	read, err = appstorage.ReadConvertPlan(plan)
	if err != nil || len(read.Resources) != 1 || len(read.NeedsPerson) == 0 || read.NeedsPerson[0].Resource != "old" {
		t.Fatalf("plan: %+v %v", read, err)
	}
	run(1, "--dry-run", "--plan", plan, "--account", "no-such-"+handle)
	if again, err := appstorage.ReadConvertPlan(plan); err != nil || len(again.Resources) != 1 {
		t.Fatalf("a failed dry run replaced the plan: %v", err)
	}
	run(1, "--apply", "--plan", filepath.Join(t.TempDir(), "missing.json"))
}
