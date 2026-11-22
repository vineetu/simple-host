package assistant

import (
	"testing"

	"github.com/vsriram/simple-host/host/edition"
	"github.com/vsriram/simple-host/products/hack/core"
	"github.com/vsriram/simple-host/products/hack/hacktest"
	"github.com/vsriram/simple-host/products/hack/testdb"
)

func TestMain(m *testing.M) { testdb.Main(m) }

// hackApp is this package's test server: the assistant routes alone, on hacktest.
type hackApp struct {
	*hacktest.App
	hack *core.Hack
	as   *Handler
}

func newHackApp(t *testing.T, eds ...*edition.Edition) *hackApp {
	t.Helper()
	a := hacktest.New(t, eds...)
	as := &Handler{Hack: a.Hack}
	as.Register(a.Mux, a.AuthMW)
	return &hackApp{a, a.Hack, as}
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

func (h *Handler) setService(s *AssistantService) { h.Service = s }
