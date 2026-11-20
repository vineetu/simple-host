package server

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"os"

	"github.com/vsriram/simple-host/host/config"
	"github.com/vsriram/simple-host/host/edition"
	"github.com/vsriram/simple-host/host/handler"
	operations "github.com/vsriram/simple-host/host/ops"
	"github.com/vsriram/simple-host/host/storage"
)

func runPruneVersions(args []string) int {
	fs := flag.NewFlagSet("prune-versions", flag.ContinueOnError)
	apply := fs.Bool("apply", false, "remove old versions and reset fixed accounts' per-site settings; without this, report only")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 {
		return 2
	}
	limits, err := config.LoadLimits(os.Getenv)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	database, err := sql.Open("postgres", os.Getenv("DB_DSN"))
	if err != nil {
		fmt.Fprintln(os.Stderr, "could not open database")
		return 1
	}
	defer database.Close()
	dataDir := os.Getenv("DATA_DIR")
	if dataDir == "" {
		fmt.Fprintln(os.Stderr, "DATA_DIR required")
		return 2
	}
	disk, err := storage.NewDiskStorage(dataDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if err = operations.PruneFixedAccounts(context.Background(), database, disk, edition.Default(limits), handler.KeepVersions(), *apply, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}
