package config

import (
	"strings"
	"testing"
)

// askTestEnv sets the two variables Load requires.
func askTestEnv(t *testing.T) {
	t.Setenv("DB_DSN", "postgres://test")
	t.Setenv("ADMIN_API_KEY", "test-admin-key")
}

func TestAskEnabledValues(t *testing.T) {
	askTestEnv(t)
	for v, want := range map[string]bool{
		"": true, "on": true, "yes": true, "1": true,
		"off": false, "OFF": false, "false": false, "0": false, "no": false, " No ": false,
	} {
		t.Setenv("ASK_ENABLED", v)
		cfg, err := Load()
		if err != nil {
			t.Fatalf("ASK_ENABLED=%q: %v", v, err)
		}
		if cfg.Limits.Ask.Enabled != want {
			t.Errorf("ASK_ENABLED=%q: enabled=%v, want %v", v, cfg.Limits.Ask.Enabled, want)
		}
	}
}

func TestAskKnobRanges(t *testing.T) {
	askTestEnv(t)
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if a := cfg.Limits.Ask; a.Burst != 5 || a.EverySeconds != 20 || a.DailyMax != 500 || a.MaxInFlight != 4 {
		t.Fatalf("defaults: %+v", a)
	}
	cases := []struct {
		key, ok, bad string
	}{
		{"ASK_BURST", "50", "51"},
		{"ASK_BURST", "1", "0"},
		{"ASK_EVERY_SECONDS", "3600", "3601"},
		{"ASK_EVERY_SECONDS", "1", "0"},
		{"ASK_DAILY_MAX", "0", "-1"},
		{"ASK_DAILY_MAX", "100000", "100001"},
		{"ASK_MAX_IN_FLIGHT", "32", "33"},
		{"ASK_MAX_IN_FLIGHT", "1", "0"},
		{"ASK_BURST", "7", "seven"},
		{"ASK_ENABLED", "off", "maybe"},
	}
	for _, c := range cases {
		t.Run(c.key+"="+c.bad, func(t *testing.T) {
			t.Setenv(c.key, c.ok)
			if _, err := Load(); err != nil {
				t.Fatalf("%s=%s: %v", c.key, c.ok, err)
			}
			t.Setenv(c.key, c.bad)
			_, err := Load()
			if err == nil || !strings.Contains(err.Error(), c.key) {
				t.Fatalf("%s=%s: got %v, want a startup error naming it", c.key, c.bad, err)
			}
		})
	}
}
