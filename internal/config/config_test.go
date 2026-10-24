package config

import (
	"reflect"
	"strings"
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

func TestLoadDBPool(t *testing.T) {
	t.Setenv("DB_DSN", "postgres://test")
	t.Setenv("ADMIN_API_KEY", "test-key")
	keys := []string{"DB_MAX_OPEN_CONNS", "DB_MAX_IDLE_CONNS", "DB_CONN_MAX_LIFETIME_MINUTES", "DB_CONN_MAX_IDLE_MINUTES"}
	for _, key := range keys {
		t.Setenv(key, "")
	}
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DBMaxOpenConns != 25 || cfg.DBMaxIdleConns != 5 || cfg.DBConnMaxLifetime != 30*time.Minute || cfg.DBConnMaxIdleTime != 5*time.Minute {
		t.Fatalf("defaults: %+v", cfg)
	}
	for _, key := range keys {
		t.Run(key, func(t *testing.T) {
			for _, bad := range []string{"nope", "-1", "9999999999999999999999999"} {
				t.Setenv(key, bad)
				if _, err := Load(); err == nil || !strings.Contains(err.Error(), key) {
					t.Fatalf("%s=%s: %v", key, bad, err)
				}
			}
			t.Setenv(key, "0")
			_, err := Load()
			if (err == nil) != (key == "DB_MAX_IDLE_CONNS") {
				t.Fatalf("%s=0: %v", key, err)
			}
		})
	}
	t.Setenv("DB_MAX_OPEN_CONNS", "10")
	t.Setenv("DB_MAX_IDLE_CONNS", "2")
	t.Setenv("DB_CONN_MAX_LIFETIME_MINUTES", "12")
	t.Setenv("DB_CONN_MAX_IDLE_MINUTES", "3")
	cfg, err = Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DBMaxOpenConns != 10 || cfg.DBMaxIdleConns != 2 || cfg.DBConnMaxLifetime != 12*time.Minute || cfg.DBConnMaxIdleTime != 3*time.Minute {
		t.Fatal("overrides not applied")
	}
}
