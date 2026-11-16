// Package testdb supplies the composed platform and product schema to tests.
package testdb

import (
	"os"
	"testing"

	platform "github.com/vsriram/simple-host/internal/testdb"
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
