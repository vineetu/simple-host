package registration

import (
	"testing"

	productconfig "github.com/vsriram/simple-host/products/hack/config"
	productedition "github.com/vsriram/simple-host/products/hack/edition"

	"github.com/vsriram/simple-host/host/edition"
	"github.com/vsriram/simple-host/products/hack/core"
	"github.com/vsriram/simple-host/products/hack/hacktest"
	"github.com/vsriram/simple-host/products/hack/testdb"
)

func TestMain(m *testing.M) { testdb.Main(m) }

// hackApp is this package's test server: the registration routes alone, on hacktest.
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

func newHackAppWithLimits(t *testing.T, limits productconfig.Limits) *hackApp {
	a := newHackApp(t, productedition.ForKindWithLimits(productedition.Hack, limits.Limits))
	a.hack.SetProductLimits(limits)
	return a
}
