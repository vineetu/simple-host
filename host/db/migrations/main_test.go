package migrations

import (
	"testing"

	"github.com/vsriram/simple-host/host/testdb"
)

// TestMain keeps DB_DSN tests off the shared template when packages run in parallel.
func TestMain(m *testing.M) { testdb.Main(m) }
