package config

import (
	"reflect"
	"testing"
	"time"
)

func TestParseLegacyStorageCutoff(t *testing.T) {
	cases := []struct {
		in   string
		ok   bool
		zero bool
		day  string
	}{
		{"", true, true, ""},
		{"  ", true, true, ""},
		{"off", true, true, ""},
		{"OFF", true, true, ""},
		{" Off ", true, true, ""},
		{"2026-10-12", true, false, "2026-10-12"},
		{" 2026-10-12 ", true, false, "2026-10-12"},
		{"2026-01-01", true, false, "2026-01-01"},
		{"2026-13-01", false, true, ""},
		{"2026-10-32", false, true, ""},
		{"2026-10-12T00:00:00Z", false, true, ""},
		{"not-a-date", false, true, ""},
		{"2026/10/12", false, true, ""},
	}
	for _, c := range cases {
		got, ok := ParseLegacyStorageCutoff(c.in)
		if ok != c.ok {
			t.Errorf("ParseLegacyStorageCutoff(%q): ok=%v want %v", c.in, ok, c.ok)
		}
		if c.zero {
			if !got.IsZero() {
				t.Errorf("ParseLegacyStorageCutoff(%q): got %v, want zero", c.in, got)
			}
			continue
		}
		if got.Location() != time.UTC || got.Hour() != 0 || got.Minute() != 0 || got.Second() != 0 {
			t.Errorf("ParseLegacyStorageCutoff(%q): not 00:00 UTC: %v", c.in, got)
		}
		if got.Format("2006-01-02") != c.day {
			t.Errorf("ParseLegacyStorageCutoff(%q): got %s want %s", c.in, got.Format("2006-01-02"), c.day)
		}
	}
}

func TestLoadLegacyStorageCutoff(t *testing.T) {
	t.Setenv("DB_DSN", "postgres://test")
	t.Setenv("ADMIN_API_KEY", "test-key")
	t.Setenv("SITE_DOMAIN", "simple-host.app")
	t.Setenv("LEGACY_STORAGE_CUTOFF", "")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.LegacyStorageCutoff.IsZero() {
		t.Fatalf("empty: got %v", cfg.LegacyStorageCutoff)
	}
	t.Setenv("LEGACY_STORAGE_CUTOFF", "off")
	cfg, err = Load()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.LegacyStorageCutoff.IsZero() {
		t.Fatalf("off: got %v", cfg.LegacyStorageCutoff)
	}
	t.Setenv("LEGACY_STORAGE_CUTOFF", "2026-10-12")
	cfg, err = Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.LegacyStorageCutoff.Format("2006-01-02") != "2026-10-12" {
		t.Fatalf("date: got %v", cfg.LegacyStorageCutoff)
	}
	t.Setenv("LEGACY_STORAGE_CUTOFF", "nope")
	cfg, err = Load()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.LegacyStorageCutoff.IsZero() {
		t.Fatalf("unparsable: got %v", cfg.LegacyStorageCutoff)
	}
}

// SIGNUP_BLOCKED_COUNTRIES is empty by default (feature off), and its values
// are trimmed and upper-cased regardless of how the operator wrote them.
func TestSignupBlockedCountries(t *testing.T) {
	cases := []struct {
		env  string
		want []string
	}{
		{"", nil},
		{"  ", nil},
		{"iq", []string{"IQ"}},
		{"IQ,af,  Ir ,CU,kp,sy,RU,by,ve,mm", []string{"IQ", "AF", "IR", "CU", "KP", "SY", "RU", "BY", "VE", "MM"}},
		{"iq,,af", []string{"IQ", "AF"}},
		{"iq,USA,af,1x,x", []string{"IQ", "AF"}}, // not 2 letters: ignored with a warning
	}
	for _, c := range cases {
		t.Setenv("DB_DSN", "postgres://test")
		t.Setenv("ADMIN_API_KEY", "test-key")
		t.Setenv("SITE_DOMAIN", "simple-host.app")
		t.Setenv("SIGNUP_BLOCKED_COUNTRIES", c.env)
		cfg, err := Load()
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(cfg.SignupBlockedCountries, c.want) {
			t.Errorf("SIGNUP_BLOCKED_COUNTRIES=%q: got %v, want %v", c.env, cfg.SignupBlockedCountries, c.want)
		}
	}
}
