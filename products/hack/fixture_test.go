package hack

import (
	"testing"

	"github.com/vsriram/simple-host/host/edition"
	"github.com/vsriram/simple-host/products/hack/hacktest"
	"github.com/vsriram/simple-host/products/hack/testdb"
)

func TestMain(m *testing.M) { testdb.Main(m) }

// hackApp is the whole Hack handler on a test server, for the workers the
// composition owns.
type hackApp struct {
	*hacktest.App
	hack *HackHandler
}

func newHackApp(t *testing.T, eds ...*edition.Edition) *hackApp {
	t.Helper()
	a := hacktest.New(t, eds...)
	h := newHandler(a.Hack)
	h.Register(a.Mux, a.AuthMW)
	return &hackApp{a, h}
}
