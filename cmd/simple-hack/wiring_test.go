package main

import (
	"testing"

	"github.com/vsriram/simple-host/internal/server"
	"github.com/vsriram/simple-host/internal/server/servertest"
	"github.com/vsriram/simple-host/products/hack/testdb"
)

func TestMain(m *testing.M) { testdb.Main(m) }

func TestHackWiring(t *testing.T) {
	for _, events := range []string{"", "hosted"} {
		t.Run(events, func(t *testing.T) {
			cfg := servertest.Config(t, testdb.Fresh(t))
			t.Setenv("EVENTS", events)
			exts := programExtensions()
			servertest.RecordProduct(t, cfg, exts)
			app, err := server.Build(cfg, exts, programOptions())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(app.Close)
			servertest.CheckWiring(t, app, "../..", "products/hack/servertest/testdata", "hack")
		})
	}
}
