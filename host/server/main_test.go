package server

import (
	"testing"

	"github.com/vsriram/simple-host/host/testdb"
)

// TestMain runs the platform server tests in their own disposable database.
func TestMain(m *testing.M) { testdb.Main(m) }
