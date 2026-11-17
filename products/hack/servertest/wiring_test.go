package servertest

import (
	"testing"

	"github.com/vsriram/simple-host/host/extension"
	platformtest "github.com/vsriram/simple-host/host/server/servertest"
	"github.com/vsriram/simple-host/products/hack"
	"github.com/vsriram/simple-host/products/hack/testdb"
)

func TestHackWiring(t *testing.T) {
	cfg := platformtest.Config(t, testdb.Fresh(t))
	t.Setenv("EVENTS", "hosted")
	app := platformtest.Build(t, cfg, []*extension.Extension{hack.Extension()})
	platformtest.CheckWiring(t, app, "../../..", "products/hack/servertest/testdata", "hack")
}
