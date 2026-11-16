package server

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"os"

	appstorage "github.com/vsriram/simple-host/internal/app/storage"
	"github.com/vsriram/simple-host/internal/buildinfo"
	"github.com/vsriram/simple-host/internal/config"
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
	fs := flag.NewFlagSet("migrate-rules", flag.ContinueOnError)
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
	for _, on := range []bool{*dry, *apply, *revert} {
		if on {
			modes++
		}
	}
	one := *site != "" || *resource != ""
	ok := modes == 1 && fs.NArg() == 0
	switch {
	case !ok:
	case *dry:
		ok = *planPath != "" && !one && (*all != (len(accounts) > 0))
	case *apply:
		ok = *planPath != "" && !one && !*all && len(accounts) == 0
	case *revert:
		ok = !*all && len(accounts) == 0 && ((*planPath != "") != one) && (!one || *site != "" && *resource != "")
	}
	if !ok {
		fmt.Fprintln(os.Stderr, "usage: migrate-rules --dry-run --plan FILE (--account HANDLE... | --all)\n       migrate-rules --apply --plan FILE\n       migrate-rules --revert (--plan FILE | --site ID --resource NAME)")
		return 2
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
		var plan appstorage.ConvertPlan
		if plan, err = appstorage.ReadConvertPlan(*planPath); err == nil {
			err = converter.Apply(ctx, plan, os.Stdout)
		}
	default:
		targets := []appstorage.ConvertTarget{{SiteID: *site, Resource: *resource}}
		if *planPath != "" {
			var plan appstorage.ConvertPlan
			if plan, err = appstorage.ReadConvertPlan(*planPath); err != nil {
				break
			}
			targets = targets[:0]
			for _, entry := range plan.Resources {
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
