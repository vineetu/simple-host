package servertest

import (
	"net/http"
	"testing"

	"github.com/vsriram/simple-host/host/edition"
	hack "github.com/vsriram/simple-host/products/hack"
	productconfig "github.com/vsriram/simple-host/products/hack/config"
	productedition "github.com/vsriram/simple-host/products/hack/edition"
	"github.com/vsriram/simple-host/products/hack/hacktest"
)

// featApp is the whole Hack handler on the feature suites' fixture
// (hacktest), for the journeys that span features: the tests that moved here
// from a feature's suite because they drive another feature's routes.
type featApp struct {
	*hacktest.App
	hack *hack.HackHandler
}

func newFeatApp(t *testing.T, eds ...*edition.Edition) *featApp {
	t.Helper()
	a := hacktest.New(t, eds...)
	h := hack.HandlerFor(a.Hack)
	h.Register(a.Mux, a.AuthMW)
	return &featApp{a, h}
}

func newFeatAppWithLimits(t *testing.T, limits productconfig.Limits) *featApp {
	a := newFeatApp(t, productedition.ForKindWithLimits(productedition.Hack, limits.Limits))
	a.hack.SetProductLimits(limits)
	return a
}

type featJudgingWorld struct {
	*hacktest.JudgingWorld
	a *featApp
}

func newFeatJudgingWorld(t *testing.T, nTeams, nJudges int) *featJudgingWorld {
	t.Helper()
	return newFeatJudgingWorldApp(t, newFeatApp(t), nTeams, nJudges)
}

func newFeatJudgingWorldApp(t *testing.T, a *featApp, nTeams, nJudges int) *featJudgingWorld {
	t.Helper()
	return &featJudgingWorld{hacktest.NewJudgingWorld(t, a.App, nTeams, nJudges), a}
}

type featEntryWorld struct {
	*hacktest.EntryWorld
	a *featApp
}

func newFeatEntryWorld(t *testing.T) *featEntryWorld {
	t.Helper()
	a := newFeatApp(t)
	return &featEntryWorld{hacktest.NewEntryWorld(t, a.App), a}
}

type featRulesWorld struct {
	*hacktest.RulesWorld
	a *featApp
}

func newFeatRulesWorld(t *testing.T, nTeams int) *featRulesWorld {
	t.Helper()
	a := newFeatApp(t)
	return &featRulesWorld{hacktest.NewRulesWorld(t, a.App, nTeams), a}
}

// CreateEvent creates the event through the route, as a person would.
func (a *featApp) CreateEvent(t *testing.T, p hacktest.Person, slug string, extra map[string]any) hacktest.Resp {
	t.Helper()
	r := a.At(t, "POST", "/v1/hack/events", a.CreateBody(slug, extra), a.Key(p))
	if r.Status == http.StatusCreated {
		t.Cleanup(func() { a.CleanupEvent(slug) })
	}
	return r
}
