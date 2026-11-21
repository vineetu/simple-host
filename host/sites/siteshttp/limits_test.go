package siteshttp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vsriram/simple-host/host/config"
	"github.com/vsriram/simple-host/host/edition"
	"github.com/vsriram/simple-host/host/sites"
	"github.com/vsriram/simple-host/host/web"
)

// withLimits loads the env settings in m into a config.Limits value. The
// caller builds a handler (or edition.ForKindWithLimits) from it directly:
// each handler holds its own snapshot now, so there is no global left to
// apply it to.
func withLimits(t *testing.T, m map[string]string) config.Limits {
	t.Helper()
	l, err := config.LoadLimits(func(k string) string { return m[k] })
	if err != nil {
		t.Fatal(err)
	}
	return l
}

// otherValue is a valid, non-default setting for env.
func otherValue(t *testing.T, env string) string {
	t.Helper()
	def := config.DefaultLimits()
	for _, k := range config.Knobs() {
		if k.Env != env {
			continue
		}
		switch {
		case k.Sensitive:
			// A sign-in limit may be at most 4 times looser: keep its
			// interval and change the burst.
			_, every, _ := strings.Cut(k.Value(&def), ",")
			return "7," + every
		case strings.HasPrefix(env, "RATE_LIMIT_"):
			return "7,7s"
		case env == "IDLE_REPLY_TO":
			return "help@example.org"
		case env == "SUPPORT_EMAIL":
			return "help@example.org"
		case env == "PRIVACY_URL":
			return "https://example.org/privacy"
		case env == "MAX_SITES_OVERRIDES":
			return "someone:7"
		case env == "SITE_TOTAL_CAP_FROM":
			return "2026-10-05T00:00:00Z"
		case env == "KEEP_VERSIONS_SELF_SET":
			return "someone"
		case env == "KEEP_VERSIONS_OVERRIDES", env == "MAX_SITE_TOTAL_OVERRIDES", env == "MAX_ACCOUNT_MB_OVERRIDES":
			return "someone:7"
		case env == "MAX_ARCHIVE_MB_OVERRIDES":
			return "someone:7"
		}
		// Somewhere inside the range, away from the default.
		for _, v := range []int64{k.Min + 1, k.Max - 1, k.Min, k.Max} {
			if s := itoa(v); s != k.Value(&def) {
				return s
			}
		}
	}
	t.Fatalf("no knob %s", env)
	return ""
}

// changedLimits sets every knob to a non-default value that still passes the
// cross-checks (idle within session, lapse after warning, ...).
func changedLimits(t *testing.T) map[string]string {
	t.Helper()
	m := map[string]string{}
	for _, k := range config.Knobs() {
		m[k.Env] = otherValue(t, k.Env)
	}
	// Hand-picked where the generic pick would break a cross-check or say
	// nothing readable.
	for k, v := range map[string]string{
		"SIGNIN_CODE_TTL_MINUTES":        "20",
		"MAX_KEYS_PER_ACCOUNT":           "12",
		"HANDLE_RENAME_EVERY_DAYS":       "45",
		"EMAIL_CHANGE_UNDO_DAYS":         "9",
		"MAX_SITES_PER_ACCOUNT":          "250",
		"MAX_FILES_PER_SITE":             "30000",
		"PREVIEW_LINK_TTL_MINUTES":       "120",
		"EXPORT_LINK_TTL_MINUTES":        "25",
		"VISITOR_SESSION_DAYS":           "60",
		"VISITOR_SESSION_IDLE_DAYS":      "21",
		"OAUTH_ACCESS_TTL_MINUTES":       "45",
		"OAUTH_REFRESH_TTL_DAYS":         "120",
		"DOMAIN_UNPROVEN_HOURS":          "36",
		"DOMAIN_UNPROVEN_MAX_DAYS":       "10",
		"DOMAIN_LAPSE_WARN_HOURS":        "48",
		"DOMAIN_LAPSE_HOURS":             "120",
		"DOMAIN_CHECK_INTERVAL_MINUTES":  "5",
		"DOMAIN_CERTS_PER_ACCOUNT_DAILY": "8",
		"EVENT_TTL_DAYS":                 "30",
		"DELETED_RETENTION_DAYS":         "30",
		"IDLE_AFTER_DAYS":                "180",
		"IDLE_GRACE_DAYS":                "14",
		"ANALYTICS_RETENTION_DAYS":       "200",
		"API_METRICS_RETENTION_DAYS":     "60",
	} {
		m[k] = v
	}
	return m
}

// The served pages, documents and skills carry the configured values, and not
// the defaults they were written with.
func TestServedTextFollowsLimits(t *testing.T) {
	l := withLimits(t, changedLimits(t))
	mux := http.NewServeMux()
	RegisterUIRoutes(mux, "https://simple-host.app", chromeTestHandlerFor(edition.Default(l)))

	for _, c := range []struct {
		path      string
		want, not []string
	}{
		{"/llms.txt",
			[]string{"Recently deleted for 30 days", "After 30 days it is removed for good", "no saved data for 194 days moves to Recently deleted",
				"9-day undo link", "once every 45 days", "link that works for 25 minutes", "has it, for 2 hours", "at most 8 new domain certificates a day"},
			[]string{"for 7 days", "for 90 days", "7-day undo link", "once every 30 days", "works for 10 minutes", "for one hour", "at most 5 new domain"}},
		{"/openapi.json",
			[]string{"expires in 20 minutes", "valid for 20 minutes", "at most 12 keys", "holds 12 keys", "Session has 60-day absolute and 21-day idle lifetimes",
				"expires 36 hours after binding", "for 2 days the owner is emailed, and after 5 days", "restorable for 30 days", "a 25-minute download link"},
			[]string{"15 minutes", "50 keys", "30-day absolute", "expires 24 hours after binding", "after 72 hours", "restorable for 7 days"}},
		{"/openapi.yaml", []string{"no new version for 180 days"}, []string{"90 days"}},
		{"/features", []string{"after <b>20 minutes</b>", "Same 20-minute expiry", "<b>250 sites</b>", "Up to 250 sites", "yours for 2 hours", "keeps it for 30 days",
			"about every 5 minutes", "lapses after 36 hours"},
			[]string{"a hundred sites", "<b>100 sites</b>", "for one hour", "for 7 days", "two minutes"}},
		{"/privacy.html", []string{"lasts up to 60 days and covers", "access token that lasts 45 minutes", "expires after 120 days without use", "deleted after 200 days",
			"shortened IP for 60 days", "Analytics records:</strong> 200 days",
			// Paper activity has a fixed lifetime, independent of AnalyticsRetention.
			"one-minute totals are kept for 400 days"},
			[]string{"up to 30 days", "Analytics records:</strong> 400 days", "shortened IP for 30 days"}},
		{"/architecture.html", []string{"30,000 entries", "every 5 minutes (releasing unproven bindings after 36 hours)"},
			[]string{"50,000 entries", "8-minute", "64 in total", "burst 30"}},
		{"/dashboard", []string{"For 30 days you can restore it"}, []string{"For 7 days"}},
		{"/skills/connect-domain/SKILL.md", []string{"expires after 36 hours", "If it fails every check for 2 days", "After 5 days the domain is disconnected", "after 5 days, be disconnected"},
			[]string{"after 24 hours", "for a day", "three days"}},
		// The enterprise pages describe the other product's limits.
		{"/enterprise/architecture", []string{"at most 50,000 entries"}, []string{"30,000 entries"}},
	} {
		rec := get(t, mux, "simple-host.app", c.path)
		if rec.Code != http.StatusOK {
			t.Errorf("%s: %d", c.path, rec.Code)
			continue
		}
		body := rec.Body.String()
		if c.path == "/openapi.json" {
			// Upload links have a fixed lifetime, independent of sign-in codes.
			var spec map[string]any
			if err := json.Unmarshal(rec.Body.Bytes(), &spec); err != nil {
				t.Fatal(err)
			}
			paths := spec["paths"].(map[string]any)
			delete(paths, "/v1/uploads/{token}")
			encoded, _ := json.Marshal(spec)
			body = string(encoded)
		}
		for _, w := range c.want {
			if !strings.Contains(body, w) {
				t.Errorf("%s: missing %q", c.path, w)
			}
		}
		for _, n := range c.not {
			if strings.Contains(body, n) {
				t.Errorf("%s: still says %q", c.path, n)
			}
		}
	}
}

// The owner app's own page (a showcase template) and the message pages carry
// the settings too.
func TestShowcaseAndMessagePagesFollowLimits(t *testing.T) {
	l := withLimits(t, changedLimits(t))
	ed := edition.Default(l)
	page, err := chromeTestHandlerFor(ed).Pipeline().BuildChromePage("showcase.html", web.ChromeData{})
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range []string{"kept for 30 days with their saved data", "Recently deleted for 30 days", "Recently deleted within 30 days", "change it again after 45 days"} {
		if !strings.Contains(string(page), w) {
			t.Errorf("showcase: missing %q", w)
		}
	}
	h := chromeTestHandlerFor(ed)
	rec := httptest.NewRecorder()
	h.renderPreviewExpired(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if !strings.Contains(rec.Body.String(), "Preview links work for 2 hours.") {
		t.Errorf("preview-expired page: %s", rec.Body.String())
	}
}

// Every route that serves a skill file states this instance's upload cap, not
// the 100 MB the files are written with.
func TestSkillRoutesFollowArchiveLimit(t *testing.T) {
	old := sites.Upload.Body
	sites.Upload.Configure(300 << 20)
	t.Cleanup(func() { sites.Upload.Configure(old) })
	mux := http.NewServeMux()
	RegisterUIRoutes(mux, "https://simple-host.app", chromeTestHandler(platformTestEdition()))
	testAssetPipeline(edition.Default(config.DefaultLimits()), "").RegisterSkillsHub(mux, "https://simple-host.app")
	for _, c := range []struct{ path, want string }{
		{"/v1/skills/website-deploy", "**Archive limit** is 300 MB."},
		{"/v1/skills/website-deploy/SKILL.md", "**Archive limit** is 300 MB."},
		{"/.well-known/skills/website-deploy/SKILL.md", "**Archive limit** is 300 MB."},
		{"/skills/website-deploy", "**Archive limit** is 300 MB."},
		{"/skills/website-deploy/SKILL.md", "**Archive limit** is 300 MB."},
		{"/v1/skills/website-deploy/references/rest-api.md", "The API rejects archives over 300 MB."},
		{"/.well-known/skills/website-deploy/references/rest-api.md", "The API rejects archives over 300 MB."},
		{"/skills/website-deploy/references/rest-api.md", "The API rejects archives over 300 MB."},
	} {
		rec := get(t, mux, "simple-host.app", c.path)
		if rec.Code != http.StatusOK {
			t.Errorf("%s: %d", c.path, rec.Code)
			continue
		}
		if body := rec.Body.String(); !strings.Contains(body, c.want) || strings.Contains(body, "100 MB") {
			t.Errorf("%s: want %q and no 100 MB", c.path, c.want)
		}
	}
}
