package config

import (
	"strings"
	"testing"
	"time"
)

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

// Unset means today's values, down to every field.
func TestLimitsDefaults(t *testing.T) {
	l, err := LoadLimits(env(nil))
	if err != nil {
		t.Fatal(err)
	}
	if l != DefaultLimits() {
		t.Fatalf("unset env should give the defaults:\n got %+v\nwant %+v", l, DefaultLimits())
	}
	// Spot-check the values the code had built in before they were settings.
	for name, c := range map[string]struct{ got, want any }{
		"sign-in code":     {l.SigninCodeTTL, 15 * time.Minute},
		"keys":             {l.MaxKeysPerAccount, 50},
		"sites":            {l.MaxSitesPerAccount, 100},
		"files":            {l.MaxFilesPerSite, 50_000},
		"visitor session":  {l.VisitorSessionTTL, 30 * 24 * time.Hour},
		"visitor idle":     {l.VisitorSessionIdle, 14 * 24 * time.Hour},
		"refresh":          {l.OAuthRefreshTTL, 90 * 24 * time.Hour},
		"deleted":          {l.DeletedRetention, 7 * 24 * time.Hour},
		"idle":             {l.IdleAfter, 90 * 24 * time.Hour},
		"analytics":        {l.AnalyticsRetention, 400},
		"register limiter": {l.RateOAuthRegister, Rate{10, 6 * time.Minute}},
		"state limiter":    {l.RateState, Rate{60, time.Second}},
		"reply-to":         {l.IdleReplyTo, "support@simple-host.app"},
	} {
		if c.got != c.want {
			t.Errorf("%s: default %v, want %v", name, c.got, c.want)
		}
	}
	if got := l.Changed(); len(got) != 0 {
		t.Errorf("Changed() on defaults = %v, want none", got)
	}
}

// Every knob's default, printed the way its env var is spelled, parses back to
// the same value: the docs table can show Value(default) as the default.
func TestLimitsDefaultsRoundTrip(t *testing.T) {
	def := DefaultLimits()
	m := map[string]string{}
	for _, k := range Knobs() {
		m[k.Env] = k.Value(&def)
	}
	l, err := LoadLimits(env(m))
	if err != nil {
		t.Fatal(err)
	}
	if l != def {
		t.Fatalf("defaults written out and read back changed:\n got %+v\nwant %+v", l, def)
	}
}

func TestLimitsOverrides(t *testing.T) {
	l, err := LoadLimits(env(map[string]string{
		"SIGNIN_CODE_TTL_MINUTES":   "20",
		"MAX_KEYS_PER_ACCOUNT":      "7",
		"MAX_SITES_PER_ACCOUNT":     " 250 ",
		"VISITOR_SESSION_DAYS":      "60",
		"VISITOR_SESSION_IDLE_DAYS": "60",
		"DELETED_RETENTION_DAYS":    "30",
		"DOMAIN_LAPSE_HOURS":        "96",
		"RATE_LIMIT_SIGNIN_IP":      "40,2s",
		"RATE_LIMIT_OAUTH_REGISTER": "5, 1h",
		"IDLE_REPLY_TO":             "help@example.org",
		"AI_JOB_TIMEOUT_MINUTES":    "5",
	}))
	if err != nil {
		t.Fatal(err)
	}
	for name, c := range map[string]struct{ got, want any }{
		"code":                    {l.SigninCodeTTL, 20 * time.Minute},
		"keys":                    {l.MaxKeysPerAccount, 7},
		"sites":                   {l.MaxSitesPerAccount, 250},
		"session":                 {l.VisitorSessionTTL, 60 * 24 * time.Hour},
		"idle":                    {l.VisitorSessionIdle, 60 * 24 * time.Hour},
		"deleted":                 {l.DeletedRetention, 30 * 24 * time.Hour},
		"lapse":                   {l.DomainLapseAfter, 96 * time.Hour},
		"signin":                  {l.RateSigninIP, Rate{40, 2 * time.Second}},
		"register":                {l.RateOAuthRegister, Rate{5, time.Hour}},
		"reply-to":                {l.IdleReplyTo, "help@example.org"},
		"ai":                      {l.AIJobTimeout, 5 * time.Minute},
		"untouched stays default": {l.ExportLinkTTL, 10 * time.Minute},
	} {
		if c.got != c.want {
			t.Errorf("%s: got %v, want %v", name, c.got, c.want)
		}
	}
	changed := strings.Join(l.Changed(), " ")
	for _, want := range []string{"SIGNIN_CODE_TTL_MINUTES=20", "RATE_LIMIT_OAUTH_REGISTER=5,1h", "IDLE_REPLY_TO=help@example.org"} {
		if !strings.Contains(changed, want) {
			t.Errorf("Changed() = %q, missing %q", changed, want)
		}
	}
	if strings.Contains(changed, "EXPORT_LINK_TTL_MINUTES") {
		t.Errorf("Changed() lists an untouched knob: %q", changed)
	}
}

// A bad value stops startup with an error that names the variable.
func TestLimitsInvalid(t *testing.T) {
	cases := map[string]map[string]string{
		"not a number":        {"MAX_KEYS_PER_ACCOUNT": "lots"},
		"zero":                {"MAX_SITES_PER_ACCOUNT": "0"},
		"negative":            {"DELETED_RETENTION_DAYS": "-1"},
		"above max":           {"SIGNIN_CODE_TTL_MINUTES": "61"},
		"below min":           {"SIGNIN_CODE_TTL_MINUTES": "4"},
		"fraction":            {"IDLE_AFTER_DAYS": "90.5"},
		"unit in value":       {"EXPORT_LINK_TTL_MINUTES": "10m"},
		"ai past client wait": {"AI_JOB_TIMEOUT_MINUTES": "9"},
		"rate no comma":       {"RATE_LIMIT_STATE": "60"},
		"rate bad burst":      {"RATE_LIMIT_STATE": "0,1s"},
		"rate bad duration":   {"RATE_LIMIT_STATE": "60,soon"},
		"rate too slow":       {"RATE_LIMIT_STATE": "60,48h"},
		"rate too fast":       {"RATE_LIMIT_STATE": "60,1us"},
		"reply-to not email":  {"IDLE_REPLY_TO": "support"},
		"reply-to two":        {"IDLE_REPLY_TO": "a@b.com,c@d.com"},
		"idle past session":   {"VISITOR_SESSION_IDLE_DAYS": "31"},
		"lapse before warn":   {"DOMAIN_LAPSE_WARN_HOURS": "72"},
		"max age before ttl":  {"DOMAIN_UNPROVEN_HOURS": "240", "DOMAIN_UNPROVEN_MAX_DAYS": "7"},
		"per user above all":  {"AI_MAX_JOBS_PER_USER": "10", "AI_MAX_JOBS": "5"},
	}
	for name, m := range cases {
		_, err := LoadLimits(env(m))
		if err == nil {
			t.Errorf("%s: %v accepted", name, m)
			continue
		}
		named := false
		for k := range m {
			named = named || strings.Contains(err.Error(), k)
		}
		if !named {
			t.Errorf("%s: error %q does not name the variable", name, err)
		}
	}
}

// Load fails on a bad knob, so the server stops at startup instead of
// running with a value nobody chose.
func TestLoadRejectsBadLimit(t *testing.T) {
	t.Setenv("DB_DSN", "postgres://test")
	t.Setenv("ADMIN_API_KEY", "k")
	t.Setenv("MAX_KEYS_PER_ACCOUNT", "0")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "MAX_KEYS_PER_ACCOUNT") {
		t.Fatalf("Load() error = %v, want one naming MAX_KEYS_PER_ACCOUNT", err)
	}
	t.Setenv("MAX_KEYS_PER_ACCOUNT", "12")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Limits.MaxKeysPerAccount != 12 {
		t.Fatalf("Load() MaxKeysPerAccount = %d, want 12", cfg.Limits.MaxKeysPerAccount)
	}
}

func TestSpanWords(t *testing.T) {
	for d, want := range map[time.Duration]string{
		15 * time.Minute:   "15 minutes",
		time.Hour:          "one hour",
		90 * time.Minute:   "90 minutes",
		24 * time.Hour:     "one day",
		36 * time.Hour:     "36 hours",
		7 * 24 * time.Hour: "7 days",
		30 * time.Second:   "30 seconds",
	} {
		if got := Span(d); got != want {
			t.Errorf("Span(%v) = %q, want %q", d, got, want)
		}
	}
	if got := SpanAdj(7 * 24 * time.Hour); got != "7-day" {
		t.Errorf("SpanAdj(7d) = %q", got)
	}
	if got := SpanAdj(time.Hour); got != "one-hour" {
		t.Errorf("SpanAdj(1h) = %q", got)
	}
}
