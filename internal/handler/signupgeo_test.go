package handler

import (
	"path/filepath"
	"testing"

	"github.com/vsriram/simple-host/internal/app/accounts"
	"github.com/vsriram/simple-host/internal/geoip"
	"github.com/vsriram/simple-host/internal/geoip/geoiptest"
)

// geoBlockFor builds a signupGeoBlock whose local database maps ip to iso.
// Shared by emailcode_test.go and any other test needing a real lookup.
func geoBlockFor(t *testing.T, ip, iso string, blocked []string) signupGeoBlock {
	t.Helper()
	dir := t.TempDir()
	if err := geoiptest.Write(filepath.Join(dir, geoip.CityFile), "DBIP-City-Lite", map[string]geoiptest.Record{
		ip + "/32": {"country": geoiptest.Record{"iso_code": iso, "names": geoiptest.Record{"en": iso}}},
	}); err != nil {
		t.Fatal(err)
	}
	geo := geoip.Open(dir)
	t.Cleanup(geo.Close)
	return accounts.NewSignupGeoBlock(geo, blocked)
}
