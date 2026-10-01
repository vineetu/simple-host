package handler

import (
	"path/filepath"
	"testing"

	"github.com/vsriram/simple-host/internal/geoip"
	"github.com/vsriram/simple-host/internal/geoip/geoiptest"
)

func TestSignupGeoBlock(t *testing.T) {
	dir := t.TempDir()
	if err := geoiptest.Write(filepath.Join(dir, geoip.CityFile), "DBIP-City-Lite", map[string]geoiptest.Record{
		"198.51.100.0/24": {"country": geoiptest.Record{"iso_code": "IQ", "names": geoiptest.Record{"en": "Iraq"}}},
		"203.0.113.0/24":  {"country": geoiptest.Record{"iso_code": "US", "names": geoiptest.Record{"en": "United States"}}},
	}); err != nil {
		t.Fatal(err)
	}
	geo := geoip.Open(dir)
	defer geo.Close()

	t.Run("empty list is always off", func(t *testing.T) {
		g := newSignupGeoBlock(geo, nil)
		if _, blocked := g.blockedCountry("198.51.100.7"); blocked {
			t.Fatal("an empty blocklist must never block")
		}
	})

	t.Run("listed country is blocked", func(t *testing.T) {
		g := newSignupGeoBlock(geo, []string{"iq"}) // lower-case input, case-insensitive
		cc, blocked := g.blockedCountry("198.51.100.7")
		if !blocked || cc != "IQ" {
			t.Fatalf("got country=%q blocked=%v, want IQ true", cc, blocked)
		}
	})

	t.Run("resolved but unlisted country is allowed", func(t *testing.T) {
		g := newSignupGeoBlock(geo, []string{"IQ"})
		if _, blocked := g.blockedCountry("203.0.113.5"); blocked {
			t.Fatal("US is not on the list")
		}
	})

	t.Run("unresolved country is allowed", func(t *testing.T) {
		g := newSignupGeoBlock(geo, []string{"IQ"})
		if cc, blocked := g.blockedCountry("8.8.8.8"); blocked || cc != "" {
			t.Fatalf("an address with no entry must be allowed, got country=%q blocked=%v", cc, blocked)
		}
	})

	t.Run("nil geo database is allowed", func(t *testing.T) {
		g := newSignupGeoBlock(nil, []string{"IQ"})
		if _, blocked := g.blockedCountry("198.51.100.7"); blocked {
			t.Fatal("no geo database must never block")
		}
	})

	t.Run("empty ip is allowed", func(t *testing.T) {
		g := newSignupGeoBlock(geo, []string{"IQ"})
		if _, blocked := g.blockedCountry(""); blocked {
			t.Fatal("an empty ip must never block")
		}
	})
}

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
	return newSignupGeoBlock(geo, blocked)
}
