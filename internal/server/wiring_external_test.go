package server_test

import (
	"testing"

	"github.com/vsriram/simple-host/internal/server/servertest"
	"github.com/vsriram/simple-host/internal/testdb"
)

func TestHostWiring(t *testing.T) {
	cfg := servertest.Config(t, testdb.Fresh(t))
	app := servertest.Build(t, cfg, nil)
	servertest.CheckWiring(t, app, "../..", "host")
}
