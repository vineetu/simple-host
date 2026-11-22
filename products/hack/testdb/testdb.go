// Package testdb supplies the composed platform and product schema to tests.
package testdb

import (
	"os"
	"testing"

	platform "github.com/vsriram/simple-host/host/testdb"
)

// Main runs product tests in their own clone of the composed schema template.
func Main(m *testing.M) {
	platform.MainWithSchema(m, os.Getenv("TESTDB_PRODUCT_DSN"))
}

// Fresh returns a new composed-schema clone, dropped when the test ends.
func Fresh(t *testing.T) string {
	t.Helper()
	return platform.FreshWithSchema(t, os.Getenv("TESTDB_PRODUCT_DSN"))
}

// IsolateQueueTest gives a test that consumes a shared queue its own
// database under the integration runner (TESTDB_ISOLATE=1).
func IsolateQueueTest(t *testing.T) {
	t.Helper()
	if os.Getenv("TESTDB_ISOLATE") == "1" && os.Getenv("DB_DSN") != "" {
		t.Setenv("DB_DSN", Fresh(t))
	}
}
