package handler

import (
	"os"
	"testing"

	db "github.com/vsriram/simple-host/internal/db"
)

// The saved-data and collections tests written before the kinds (step 2)
// describe sites that existed before them, which keep today's behaviour
// (sites.legacy_data). They run unchanged against sites created that way.
// The kinds tests (kinds_test.go) make their sites strict, as every site
// created in production now is.
func TestMain(m *testing.M) {
	db.NewSitesLegacyData = true
	os.Exit(m.Run())
}
