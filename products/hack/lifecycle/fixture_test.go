package lifecycle

import (
	"net/http"
	"testing"

	"github.com/vsriram/simple-host/host/edition"
	"github.com/vsriram/simple-host/products/hack/core"
	"github.com/vsriram/simple-host/products/hack/hacktest"
	"github.com/vsriram/simple-host/products/hack/testdb"
)

func TestMain(m *testing.M) { testdb.Main(m) }

// hackApp is this package's test server: the lifecycle routes alone, on hacktest.
type hackApp struct {
	*hacktest.App
	hack *core.Hack
	lc   *Handler
}

func newHackApp(t *testing.T, eds ...*edition.Edition) *hackApp {
	t.Helper()
	a := hacktest.New(t, eds...)
	lc := &Handler{Hack: a.Hack}
	lc.Register(a.Mux, a.AuthMW)
	return &hackApp{a, a.Hack, lc}
}

// CreateEvent creates the event through this package's own route.
func (a *hackApp) CreateEvent(t *testing.T, p hacktest.Person, slug string, extra map[string]any) hacktest.Resp {
	t.Helper()
	r := a.At(t, "POST", "/v1/hack/events", a.CreateBody(slug, extra), a.Key(p))
	if r.Status == http.StatusCreated {
		t.Cleanup(func() { a.CleanupEvent(slug) })
	}
	return r
}

// OpenEvent opens sign-up through this package's own route.
func (a *hackApp) OpenEvent(t *testing.T, p hacktest.Person, slug string) {
	t.Helper()
	if r := a.At(t, "POST", "/v1/hack/events/"+slug+"/stage", map[string]string{"stage": "open"}, a.Key(p)); r.Status != 200 {
		t.Fatalf("open: %d %s", r.Status, r.Body)
	}
}
