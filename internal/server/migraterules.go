package server

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	appstorage "github.com/vsriram/simple-host/internal/app/storage"
	"github.com/vsriram/simple-host/internal/buildinfo"
	"github.com/vsriram/simple-host/internal/config"
	"github.com/vsriram/simple-host/internal/extension"
	repo "github.com/vsriram/simple-host/internal/repository/storage"
	"github.com/vsriram/simple-host/internal/storage"
)

type migrateRulesAccounts []string

func (a *migrateRulesAccounts) String() string     { return fmt.Sprint([]string(*a)) }
func (a *migrateRulesAccounts) Set(v string) error { *a = append(*a, v); return nil }

// runMigrateRulesCommand is the operator's preset-to-rules conversion. It is
// not a route, a tool or a recipe: nothing offers it to site owners.
//
// Exit codes: 0 done; 1 a resource failed or was skipped; 2 wrong usage, or
// the data directory or database is in use; 3 the dry run found a difference or a resource
// that needs a person (the plan is still written).
func runMigrateRulesCommand(args []string) int {
	return runMigrateRulesCommandWithExtensions(args, nil)
}

func runMigrateRulesCommandWithExtensions(args []string, exts []*extension.Extension) int {
	return runMigrateRulesCommandWithPlanUser(args, exts, os.Geteuid())
}

// planUser is explicit so tests can exercise a foreign-owned file without root.
func runMigrateRulesCommandWithPlanUser(args []string, exts []*extension.Extension, planUser int) int {
	fs := flag.NewFlagSet("migrate-rules", flag.ContinueOnError)
	list := fs.Bool("list-recipients", false, "write both notice recipient lists; sends nothing")
	out := fs.String("out", "", "with --list-recipients: private JSON output file")
	legacyLog := fs.String("legacy-log", "", "with --list-recipients: log lines collected since the notice machinery deployed")
	dry := fs.Bool("dry-run", false, "compare and write a plan; the server may be running; changes nothing")
	apply := fs.Bool("apply", false, "convert the plan's resources; the server must be stopped")
	revert := fs.Bool("revert", false, "put converted resources back; the server must be stopped")
	planPath := fs.String("plan", "", "plan file: written by --dry-run, read by --apply and --revert")
	all := fs.Bool("all", false, "with --dry-run: every account")
	site := fs.String("site", "", "with --revert: the site ID of one resource")
	resource := fs.String("resource", "", "with --revert: the name of one resource")
	var accounts migrateRulesAccounts
	fs.Var(&accounts, "account", "with --dry-run: an account handle; repeat for several")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	modes := 0
	for _, on := range []bool{*dry, *apply, *revert, *list} {
		if on {
			modes++
		}
	}
	one := *site != "" || *resource != ""
	ok := modes == 1 && fs.NArg() == 0 && (*list || *out == "" && *legacyLog == "")
	switch {
	case !ok:
	case *list:
		ok = *out != "" && *planPath == "" && !one && !*all && len(accounts) == 0
	case *dry:
		ok = *planPath != "" && !one && (*all != (len(accounts) > 0))
	case *apply:
		ok = *planPath != "" && !one && !*all && len(accounts) == 0
	case *revert:
		ok = !*all && len(accounts) == 0 && ((*planPath != "") != one) && (!one || *site != "" && *resource != "")
	}
	if !ok {
		fmt.Fprintln(os.Stderr, "usage: migrate-rules --list-recipients --out FILE [--legacy-log FILE]\n       migrate-rules --dry-run --plan FILE (--account HANDLE... | --all)\n       migrate-rules --apply --plan FILE\n       migrate-rules --revert (--plan FILE | --site ID --resource NAME)")
		return 2
	}
	if *list && os.Geteuid() != 0 {
		fmt.Fprintln(os.Stderr, "recipient reports must be generated as root")
		return 1
	}
	var checkedPlan appstorage.ConvertPlan
	if (*apply || *revert) && *planPath != "" {
		var err error
		checkedPlan, err = readMigrateRulesPlan(*planPath, planUser)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
	}

	dsn := os.Getenv("DB_DSN")
	if dsn == "" {
		fmt.Fprintln(os.Stderr, "DB_DSN is not set")
		return 2
	}
	dataDir := os.Getenv("DATA_DIR")
	if dataDir == "" {
		dataDir = "./data/sites"
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		fmt.Fprintln(os.Stderr, "could not open Postgres")
		return 1
	}
	defer db.Close()
	if *list {
		var calls io.Reader
		if *legacyLog != "" {
			f, e := os.Open(*legacyLog)
			if e != nil {
				fmt.Fprintln(os.Stderr, "could not open legacy log")
				return 1
			}
			defer f.Close()
			calls = f
		}
		templates, e := appstorage.LoadNoticeTemplates(dataDir)
		if e != nil {
			fmt.Fprintln(os.Stderr, "could not load notice templates")
			return 1
		}
		at, e := config.ParseStorageSwitchAt(os.Getenv("STORAGE_SWITCH_AT"), os.Getenv("STORAGE_RULES") == "on")
		if e != nil {
			fmt.Fprintln(os.Stderr, e)
			return 2
		}
		legacyRoutes := true
		var group appstorage.NoticeGroup
		for _, ext := range exts {
			if ext != nil {
				if ext.Edition != nil {
					legacyRoutes = ext.Edition(config.Limits{}).Storage().LegacyRoutes
				}
				group = ext.StorageNoticeGroup
			}
		}
		report, e := appstorage.ListNoticeRecipients(context.Background(), db, legacyRoutes, calls, group, templates, at)
		if e == nil {
			e = appstorage.WriteNoticeReport(*out, report)
		}
		if e != nil {
			fmt.Fprintln(os.Stderr, "could not write notice recipients")
			return 1
		}
		fmt.Fprintf(os.Stdout, "preset accounts=%d resources=%d; older saved-data accounts=%d; held by an event resources=%d\n", len(report.Presets), report.PresetResources, len(report.Legacy), len(report.Held))
		return 0
	}
	converter := appstorage.RulesConverter{DB: db, DataDir: dataDir, Commit: buildinfo.Commit, SpamCheckAvailable: config.SpamCheckConfigured()}
	ctx := context.Background()
	switch {
	case *dry:
		var plan appstorage.ConvertPlan
		plan, err = converter.DryRun(ctx, accounts, os.Stdout)
		// A plan is written when every resource in scope was examined; a run
		// that could not finish leaves any earlier plan file as it was.
		if err == nil || errors.Is(err, appstorage.ErrConvertAttention) {
			if writeErr := appstorage.WriteConvertPlan(*planPath, plan); writeErr != nil {
				fmt.Fprintln(os.Stderr, writeErr)
				return 1
			}
		}
	case *apply:
		err = converter.Apply(ctx, checkedPlan, os.Stdout)
	default:
		targets := []appstorage.ConvertTarget{{SiteID: *site, Resource: *resource}}
		if *planPath != "" {
			targets = targets[:0]
			for _, entry := range checkedPlan.Resources {
				targets = append(targets, appstorage.ConvertTarget{SiteID: entry.SiteID, Resource: entry.Name})
			}
		}
		err = converter.Revert(ctx, targets, os.Stdout)
	}
	if err == nil {
		return 0
	}
	fmt.Fprintln(os.Stderr, err)
	if errors.Is(err, storage.ErrDataDirInUse) || errors.Is(err, repo.ErrDatabaseInUse) {
		return 2
	}
	if *dry && errors.Is(err, appstorage.ErrConvertAttention) {
		return 3
	}
	return 1
}

// Keep directory handles pinned while walking the resolved parent path and
// decode the same file descriptor whose metadata was checked. No plan bytes
// are read until the file and its containing directories have passed.
func readMigrateRulesPlan(path string, uid int) (appstorage.ConvertPlan, error) {
	var empty appstorage.ConvertPlan
	dir, err := filepath.EvalSymlinks(filepath.Dir(path))
	if err == nil {
		dir, err = filepath.Abs(dir)
	}
	if err != nil {
		return empty, errors.New("could not inspect the plan directory")
	}
	root, err := os.OpenRoot(string(filepath.Separator))
	if err != nil {
		return empty, errors.New("could not inspect the plan directory")
	}
	defer func() { root.Close() }()
	parts := strings.Split(strings.TrimPrefix(dir, string(filepath.Separator)), string(filepath.Separator))
	for i := 0; ; i++ {
		info, err := root.Stat(".")
		if err != nil {
			return empty, errors.New("could not inspect the plan directory")
		}
		if info.Mode().Perm()&0002 != 0 && info.Mode()&os.ModeSticky == 0 {
			return empty, errors.New("the plan directory is writable by others without the sticky bit")
		}
		if i == len(parts) || parts[i] == "" {
			break
		}
		next, err := root.OpenRoot(parts[i])
		if err != nil {
			return empty, errors.New("could not inspect the plan directory")
		}
		root.Close()
		root = next
	}
	name := filepath.Base(path)
	info, err := root.Lstat(name)
	if err != nil {
		return empty, errors.New("could not inspect the plan file")
	}
	if !info.Mode().IsRegular() {
		return empty, errors.New("the plan must be a regular file")
	}
	f, err := root.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return empty, errors.New("could not open the plan file")
	}
	defer f.Close()
	info, err = f.Stat()
	if err != nil {
		return empty, errors.New("could not inspect the plan file")
	}
	if !info.Mode().IsRegular() {
		return empty, errors.New("the plan must be a regular file")
	}
	owner, ok := info.Sys().(*syscall.Stat_t)
	if !ok || (owner.Uid != 0 && int(owner.Uid) != uid) {
		return empty, errors.New("the plan must be owned by the command user or root")
	}
	if info.Mode().Perm()&0022 != 0 {
		return empty, errors.New("the plan must not be writable by group or others")
	}
	plan, err := appstorage.DecodeConvertPlan(f)
	if err != nil {
		return empty, err
	}
	if plan.Commit == "" {
		return empty, errors.New("the plan does not record a commit")
	}
	if plan.Commit != buildinfo.Commit {
		return empty, errors.New("the plan commit differs from the running program")
	}
	return plan, nil
}
