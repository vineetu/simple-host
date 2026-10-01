package config

import (
	"reflect"
	"testing"
)

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
