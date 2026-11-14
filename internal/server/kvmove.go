package server

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"os"

	appstorage "github.com/vsriram/simple-host/internal/app/storage"
	"github.com/vsriram/simple-host/internal/storage"
)

type kvMoveSites []string

func (s *kvMoveSites) String() string     { return fmt.Sprint([]string(*s)) }
func (s *kvMoveSites) Set(v string) error { *s = append(*s, v); return nil }

func runKVMoveCommand(args []string) int {
	fs := flag.NewFlagSet("kv-move", flag.ContinueOnError)
	dry := fs.Bool("dry-run", false, "build and compare scratch copies; server may be running")
	apply := fs.Bool("apply", false, "move Postgres namespaces; server must be stopped")
	verify := fs.Bool("verify", false, "check namespace health; server may be running")
	scratch := fs.String("scratch", "", "scratch directory required for dry-run")
	logPath := fs.String("log", "", "JSON-lines report file (required)")
	var sites kvMoveSites
	fs.Var(&sites, "site", "site ID to include; repeat for several sites")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	count, mode := 0, ""
	for name, on := range map[string]bool{"dry-run": *dry, "apply": *apply, "verify": *verify} {
		if on {
			count++
			mode = name
		}
	}
	if count != 1 || fs.NArg() != 0 || *logPath == "" || (*dry && *scratch == "") || (!*dry && *scratch != "") {
		fmt.Fprintln(os.Stderr, "usage: kv-move (--dry-run --scratch DIR | --apply | --verify) [--site ID]... --log FILE")
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
	log, err := os.OpenFile(*logPath, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0600)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer log.Close()
	mover := appstorage.KVMover{DB: db, DataDir: dataDir}
	err = mover.Run(context.Background(), appstorage.KVMoveOptions{Mode: mode, Scratch: *scratch, Sites: sites, Log: log})
	if err == nil {
		return 0
	}
	fmt.Fprintln(os.Stderr, err)
	if errors.Is(err, storage.ErrDataDirInUse) {
		return 2
	}
	if *dry && errors.Is(err, appstorage.ErrKVCopyDifference) {
		return 3
	}
	return 1
}
