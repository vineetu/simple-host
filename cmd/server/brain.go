package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/vsriram/simple-host/internal/brain"
)

// `simple-host brain-build -host DIR -enterprise DIR -out FILE` builds the
// owner's product knowledge bundle from two checkouts and optional local archives and swaps it into place
// (run by sh-brain-sync). It needs no config or database.
func runBrainBuild(args []string) int {
	fs := flag.NewFlagSet("brain-build", flag.ContinueOnError)
	host := fs.String("host", "", "simple-host checkout")
	ent := fs.String("enterprise", "", "simple-host-enterprise checkout")
	console := fs.String("console", "", "extracted chhota-console archive")
	consoleRepo := fs.String("console-repo", "", "chhota-console checkout for master metadata")
	travel := fs.String("travel", "", "extracted simple-host-travel archive")
	travelRepo := fs.String("travel-repo", "", "simple-host-travel checkout for master metadata")
	out := fs.String("out", "", "bundle file to write (replaced atomically)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *host == "" || *ent == "" || *out == "" {
		fmt.Fprintln(os.Stderr, "usage: simple-host brain-build -host DIR -enterprise DIR -out FILE [-console DIR -console-repo DIR] [-travel DIR -travel-repo DIR]")
		return 2
	}
	var locals []brain.LocalSource
	if *console != "" && *consoleRepo != "" {
		locals = append(locals, brain.LocalSource{Repo: brain.RepoConsole, RepoDir: *consoleRepo, Branch: "master", FilesDir: *console})
	}
	if *travel != "" && *travelRepo != "" {
		locals = append(locals, brain.LocalSource{Repo: brain.RepoTravel, RepoDir: *travelRepo, Branch: "master", FilesDir: *travel})
	}
	b, err := brain.Build(*host, *ent, time.Now(), locals...)
	if err != nil {
		fmt.Fprintln(os.Stderr, "brain-build:", err)
		return 1
	}
	if err := brain.WriteFile(b, *out); err != nil {
		fmt.Fprintln(os.Stderr, "brain-build: write:", err)
		return 1
	}
	fmt.Printf("brain-build: %d docs, %d parity rows, %d decisions, %d changes -> %s\n", len(b.Docs), len(b.Parity), len(b.Decisions), len(b.Changes), *out)
	return 0
}
