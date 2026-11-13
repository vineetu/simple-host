package servertest

import (
	"testing"

	"github.com/vsriram/simple-host/internal/extension"
	platformtest "github.com/vsriram/simple-host/internal/server/servertest"
	"github.com/vsriram/simple-host/internal/testdb"
	"github.com/vsriram/simple-host/products/hack"
)

func TestHackWiring(t *testing.T) {
	cfg := platformtest.Config(t, testdb.Fresh(t))
	t.Setenv("EVENTS", "hosted")
	app := platformtest.Build(t, cfg, []*extension.Extension{hack.Extension()})
	platformtest.CheckWiring(t, app, "../../..", "hack")
}
