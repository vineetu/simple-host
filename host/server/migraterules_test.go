package server

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	appstorage "github.com/vsriram/simple-host/host/app/storage"
	"github.com/vsriram/simple-host/host/buildinfo"
	repo "github.com/vsriram/simple-host/host/repository/storage"
	"github.com/vsriram/simple-host/host/storage"
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
		{"--list-recipients"},
		{"--list-recipients", "--out", "p", "--all"},
		{"--list-recipients", "--out", "p", "--apply"},
		{"--dry-run", "--plan", "p", "--all", "--legacy-log", "p"},
		{"--dry-run", "--all"},                                  // no plan
		{"--dry-run", "--plan", "p"},                            // no scope
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
	if read.Commit == "" || read.Commit != buildinfo.Commit {
		t.Fatalf("dry run commit = %q, want %q", read.Commit, buildinfo.Commit)
	}
	// Every plan refusal must leave all resource columns and table overrides
	// unchanged, both before conversion and when reverting converted resources.
	snapshot := func() string {
		t.Helper()
		var result string
		if err := db.QueryRow(`SELECT jsonb_build_object(
			'resources', (SELECT jsonb_agg(to_jsonb(r) ORDER BY r.name) FROM site_storage_resources r WHERE site_id=$1),
			'tables', (SELECT jsonb_agg(to_jsonb(t) ORDER BY t.resource_name,t.table_name) FROM site_storage_tables t WHERE site_id=$1))::text`, site).Scan(&result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	refusals := func(mode string) {
		t.Helper()
		for _, kind := range []string{"wrong owner", "group writable", "world writable", "world writable directory", "other commit", "missing commit", "not regular"} {
			t.Run(mode+"/"+kind, func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "plan.json")
				candidate := read
				uid := os.Geteuid()
				switch kind {
				case "other commit":
					candidate.Commit = buildinfo.Commit + "-different"
				case "missing commit":
					candidate.Commit = ""
				case "wrong owner":
					if os.Geteuid() == 0 {
						t.Skip("root-owned plans are allowed for every command user")
					}
					// Exercise the actual UID check without requiring chown/root.
					uid++
				}
				if err := appstorage.WriteConvertPlan(path, candidate); err != nil {
					t.Fatal(err)
				}
				switch kind {
				case "group writable":
					if err := os.Chmod(path, 0620); err != nil {
						t.Fatal(err)
					}
				case "world writable":
					if err := os.Chmod(path, 0602); err != nil {
						t.Fatal(err)
					}
				case "world writable directory":
					if err := os.Chmod(filepath.Dir(path), 0777); err != nil {
						t.Fatal(err)
					}
				case "missing commit":
					raw, err := os.ReadFile(path)
					if err != nil {
						t.Fatal(err)
					}
					var fields map[string]json.RawMessage
					if err := json.Unmarshal(raw, &fields); err != nil {
						t.Fatal(err)
					}
					delete(fields, "commit")
					raw, err = json.Marshal(fields)
					if err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(path, raw, 0600); err != nil {
						t.Fatal(err)
					}
				case "not regular":
					if err := os.Remove(path); err != nil {
						t.Fatal(err)
					}
					if err := os.Mkdir(path, 0700); err != nil {
						t.Fatal(err)
					}
				}
				before := snapshot()
				if code := runMigrateRulesCommandWithPlanUser([]string{mode, "--plan", path}, nil, uid); code != 1 {
					t.Fatalf("refused plan exit = %d, want 1", code)
				}
				if after := snapshot(); after != before {
					t.Fatal("a refused plan changed resources or table overrides")
				}
			})
		}
	}
	refusals("--apply")
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
	refusals("--revert")
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

func TestMigrateRulesPlanChecksBeforeDecode(t *testing.T) {
	for _, kind := range []string{"group", "others", "directory", "owner", "regular"} {
		t.Run(kind, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "plan.json")
			if err := os.WriteFile(path, []byte("not JSON"), 0600); err != nil {
				t.Fatal(err)
			}
			uid := os.Geteuid()
			want := "writable by group or others"
			switch kind {
			case "group":
				if err := os.Chmod(path, 0620); err != nil {
					t.Fatal(err)
				}
			case "others":
				if err := os.Chmod(path, 0602); err != nil {
					t.Fatal(err)
				}
			case "directory":
				if err := os.Chmod(filepath.Dir(path), 0777); err != nil {
					t.Fatal(err)
				}
				want = "without the sticky bit"
			case "owner":
				if os.Geteuid() == 0 {
					t.Skip("root-owned plans are allowed for every command user")
				}
				uid++
				want = "owned by the command user or root"
			case "regular":
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(path, 0700); err != nil {
					t.Fatal(err)
				}
				want = "regular file"
			}
			if _, err := readMigrateRulesPlan(path, uid); err == nil || !strings.Contains(err.Error(), want) || strings.Contains(err.Error(), "\n") {
				t.Fatalf("refusal = %v, want one sentence containing %q", err, want)
			}
		})
	}
	// /tmp's sticky bit permits a private plan there, and commit refusals have
	// their own plain reasons even when the JSON is otherwise valid.
	for _, commit := range []string{"", buildinfo.Commit + "-different", buildinfo.Commit} {
		path := filepath.Join(t.TempDir(), "plan.json")
		raw, err := json.Marshal(appstorage.ConvertPlan{Version: 1, Commit: commit})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, raw, 0600); err != nil {
			t.Fatal(err)
		}
		_, err = readMigrateRulesPlan(path, os.Geteuid())
		if commit == buildinfo.Commit {
			if err != nil {
				t.Fatal(err)
			}
		} else {
			want := "does not record a commit"
			if commit != "" {
				want = "differs from the running program"
			}
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("refusal = %v, want %q", err, want)
			}
		}
	}
}

func TestMigrateRulesRecipientsRequireRoot(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("requires a non-root process")
	}
	t.Setenv("DB_DSN", "") // Root refusal must precede any database work.
	path := filepath.Join(t.TempDir(), "recipients.json")
	if code := runMigrateRulesCommand([]string{"--list-recipients", "--out", path}); code != 1 {
		t.Fatalf("non-root recipient command exit %d, want 1", code)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("non-root recipient command created a report: %v", err)
	}
}
