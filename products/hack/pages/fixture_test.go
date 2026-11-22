package pages

import (
	"testing"

	"github.com/vsriram/simple-host/host/edition"
	"github.com/vsriram/simple-host/products/hack/core"
	"github.com/vsriram/simple-host/products/hack/hacktest"
	"github.com/vsriram/simple-host/products/hack/testdb"
)

func TestMain(m *testing.M) { testdb.Main(m) }

// hackApp is this package's test server: the pages routes alone, on hacktest.
type hackApp struct {
	*hacktest.App
	hack *core.Hack
}

func newHackApp(t *testing.T, eds ...*edition.Edition) *hackApp {
	t.Helper()
	a := hacktest.New(t, eds...)
	return &hackApp{a, a.Hack}
}

type rulesWorld struct {
	*hacktest.RulesWorld
	a *hackApp
}

func newRulesWorld(t *testing.T, nTeams int) *rulesWorld {
	t.Helper()
	a := newHackApp(t)
	return &rulesWorld{hacktest.NewRulesWorld(t, a.App, nTeams), a}
}
