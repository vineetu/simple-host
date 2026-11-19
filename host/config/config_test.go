package config

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

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
