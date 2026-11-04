package accounts

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
		g := NewSignupGeoBlock(geo, nil)
		if _, blocked := g.BlockedCountry("198.51.100.7"); blocked {
			t.Fatal("an empty blocklist must never block")
		}
	})

	t.Run("listed country is blocked", func(t *testing.T) {
		g := NewSignupGeoBlock(geo, []string{"iq"}) // lower-case input, case-insensitive
		cc, blocked := g.BlockedCountry("198.51.100.7")
		if !blocked || cc != "IQ" {
			t.Fatalf("got country=%q blocked=%v, want IQ true", cc, blocked)
		}
	})

	t.Run("resolved but unlisted country is allowed", func(t *testing.T) {
		g := NewSignupGeoBlock(geo, []string{"IQ"})
		if _, blocked := g.BlockedCountry("203.0.113.5"); blocked {
			t.Fatal("US is not on the list")
		}
	})

	t.Run("unresolved country is allowed", func(t *testing.T) {
		g := NewSignupGeoBlock(geo, []string{"IQ"})
		if cc, blocked := g.BlockedCountry("8.8.8.8"); blocked || cc != "" {
			t.Fatalf("an address with no entry must be allowed, got country=%q blocked=%v", cc, blocked)
		}
	})

	t.Run("nil geo database is allowed", func(t *testing.T) {
		g := NewSignupGeoBlock(nil, []string{"IQ"})
		if _, blocked := g.BlockedCountry("198.51.100.7"); blocked {
			t.Fatal("no geo database must never block")
		}
	})

	t.Run("typed nil geo database is allowed", func(t *testing.T) {
		var geo *geoip.DB
		g := NewSignupGeoBlock(geo, []string{"IQ"})
		if cc, blocked := g.BlockedCountry("198.51.100.7"); blocked || cc != "" {
			t.Fatalf("no geo database must allow, got country=%q blocked=%v", cc, blocked)
		}
	})

	t.Run("empty ip is allowed", func(t *testing.T) {
		g := NewSignupGeoBlock(geo, []string{"IQ"})
		if _, blocked := g.BlockedCountry(""); blocked {
			t.Fatal("an empty ip must never block")
		}
	})
}
