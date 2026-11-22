package judging

import (
	"testing"

	"github.com/vsriram/simple-host/host/edition"
	"github.com/vsriram/simple-host/products/hack/core"
	"github.com/vsriram/simple-host/products/hack/hacktest"
	"github.com/vsriram/simple-host/products/hack/testdb"
)

func TestMain(m *testing.M) { testdb.Main(m) }

// hackApp is this package's test server: the judging routes alone, on hacktest.
type hackApp struct {
	*hacktest.App
	hack *core.Hack
}

func newHackApp(t *testing.T, eds ...*edition.Edition) *hackApp {
	t.Helper()
	a := hacktest.New(t, eds...)
	Register(a.Mux, a.Hack, a.AuthMW)
	return &hackApp{a, a.Hack}
}

type judgingWorld struct {
	*hacktest.JudgingWorld
	a *hackApp
}

func newJudgingWorld(t *testing.T, nTeams, nJudges int) *judgingWorld {
	t.Helper()
	return newJudgingWorldApp(t, newHackApp(t), nTeams, nJudges)
}

func newJudgingWorldApp(t *testing.T, a *hackApp, nTeams, nJudges int) *judgingWorld {
	t.Helper()
	return &judgingWorld{hacktest.NewJudgingWorld(t, a.App, nTeams, nJudges), a}
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
