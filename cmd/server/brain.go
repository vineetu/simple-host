package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/vsriram/simple-host/internal/brain"
)

// `simple-host brain-build -host DIR -enterprise DIR -out FILE` builds the
// owner's product knowledge bundle from two checkouts and swaps it into place
// (run by sh-brain-sync). It needs no config or database.
func runBrainBuild(args []string) int {
	fs := flag.NewFlagSet("brain-build", flag.ContinueOnError)
	host := fs.String("host", "", "simple-host checkout")
	ent := fs.String("enterprise", "", "simple-host-enterprise checkout")
	out := fs.String("out", "", "bundle file to write (replaced atomically)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *host == "" || *ent == "" || *out == "" {
		fmt.Fprintln(os.Stderr, "usage: simple-host brain-build -host DIR -enterprise DIR -out FILE")
		return 2
	}
	b, err := brain.Build(*host, *ent, time.Now())
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
