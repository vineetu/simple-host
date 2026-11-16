package config

import (
	"reflect"
	"testing"
)

func TestProductLimitsChanged(t *testing.T) {
	if got := DefaultLimits().Changed(); len(got) != 0 {
		t.Fatalf("defaults reported as changed: %v", got)
	}
	l, err := LoadLimits(env(map[string]string{
		"EVENT_TEAM_SIZE_DEFAULT": "12", "RATE_LIMIT_EVENT_CODES_USER": "10,6s",
		"EVENT_SITES_KEEP_DAYS": "60", "HACK_INSTANCE_BUDGET_GB": "0",
	}))
	if err != nil {
		t.Fatal(err)
	}
	// Platform changes are reported separately by server.Build.
	l.MaxSitesPerAccount++
	want := []string{"EVENT_TEAM_SIZE_DEFAULT=12", "EVENT_SITES_KEEP_DAYS=60", "HACK_INSTANCE_BUDGET_GB=0", "RATE_LIMIT_EVENT_CODES_USER=10,6s"}
	if got := l.Changed(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Changed = %v, want %v", got, want)
	}
}

func TestEventLimitExempt(t *testing.T) {
	for _, tc := range []struct {
		name, list, email string
		want              bool
	}{
		{"exact", "person@example.org", "person@example.org", true},
		{"case", "PERSON@EXAMPLE.ORG", "Person@Example.Org", true},
		{"domain", "@example.org", "person@example.org", true},
		{"other email", "person@example.org", "other@example.org", false},
		{"other domain", "@example.org", "person@example.com", false},
		{"subdomain", "@example.org", "person@sub.example.org", false},
		{"empty list", "", "person@example.org", false},
		{"spaces", " , other@example.com, @EXAMPLE.ORG , ", " Person@Example.Org ", true},
		{"username", "person,@example.org", "person", false},
		{"empty email", ",", "", false},
		{"missing local part", "@example.org", "@example.org", false},
		{"missing domain", "person@", "person@", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l, err := LoadLimits(env(map[string]string{"EVENT_LIMIT_EXEMPT": tc.list}))
			if err != nil {
				t.Fatal(err)
			}
			if got := l.EventLimitExempt(tc.email); got != tc.want {
				t.Fatalf("EventLimitExempt(%q) = %v, want %v", tc.email, got, tc.want)
			}
		})
	}
}

func TestEventLimitDefaultsAndZero(t *testing.T) {
	l := DefaultLimits()
	if l.EventCreatePerDay != 20 || l.EventMaxActivePerOrganiser != 0 || l.EventLimitExemptRaw != "" {
		t.Fatalf("defaults: perDay=%d maxActive=%d exempt=%q", l.EventCreatePerDay, l.EventMaxActivePerOrganiser, l.EventLimitExemptRaw)
	}
	l, err := LoadLimits(env(map[string]string{"EVENT_CREATE_PER_DAY": "0", "EVENT_MAX_ACTIVE_PER_ORGANISER": "0"}))
	if err != nil {
		t.Fatal(err)
	}
	if l.EventCreatePerDay != 0 || l.EventMaxActivePerOrganiser != 0 {
		t.Fatalf("zero limits: perDay=%d maxActive=%d", l.EventCreatePerDay, l.EventMaxActivePerOrganiser)
	}
}

func env(values map[string]string) func(string) string {
	return func(key string) string { return values[key] }
}
