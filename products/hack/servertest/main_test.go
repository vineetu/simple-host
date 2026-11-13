package servertest

import (
	"os"
	"testing"

	"github.com/vsriram/simple-host/internal/testdb"
	"github.com/vsriram/simple-host/internal/webassets"
	"github.com/vsriram/simple-host/products/hack/hackhttp"
)

// testHackPages is the shared event template cache built from embedded assets.
var testHackPages = hackhttp.NewHackEventPages(webassets.Static)

// TestMain keeps the moved tests in their own disposable package database.
func TestMain(m *testing.M) { testdb.Main(m) }

func isolateQueueTest(t *testing.T) {
	t.Helper()
	if os.Getenv("TESTDB_ISOLATE") == "1" && os.Getenv("DB_DSN") != "" {
		t.Setenv("DB_DSN", testdb.Fresh(t))
	}
}
