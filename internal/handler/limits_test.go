package handler

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/vsriram/simple-host/internal/config"
	db "github.com/vsriram/simple-host/internal/db"
	"github.com/vsriram/simple-host/internal/mcp"
	plugin "github.com/vsriram/simple-host/simple-host-website"
)

// withLimits applies the env settings in m for the rest of the test and puts
// the defaults back afterwards.
func withLimits(t *testing.T, m map[string]string) config.Limits {
	t.Helper()
	l, err := config.LoadLimits(func(k string) string { return m[k] })
	if err != nil {
		t.Fatal(err)
	}
	ApplyLimits(l)
	t.Cleanup(func() { ApplyLimits(config.DefaultLimits()) })
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
		case env == "ASK_ENABLED":
			return "off"
		case env == "ASK_MODEL":
			return "grok-test"
		case env == "ASK_REASONING_EFFORT":
			return "low"
		case env == "SAVED_DATA_DEFAULT_KIND":
			return "declare_first"
		case env == "MAX_SITES_OVERRIDES":
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
		"AI_MAX_JOBS_PER_USER":           "4",
		"AI_MAX_JOBS":                    "40",
		"AI_JOB_TIMEOUT_MINUTES":         "6",
	} {
		m[k] = v
	}
	return m
}

// The leaf packages keep their own copy of today's values (they may not import
// config); those must be the same defaults, word for word.
func TestLeafLimitDefaultsMatchConfig(t *testing.T) {
	def := config.DefaultLimits()
	if got, want := db.DefaultLimits(), (db.Limits{
		MaxAccountKeys:       def.MaxKeysPerAccount,
		KeyIdleExpiry:        def.KeyIdleExpiry,
		EmailChangeUndoTTL:   def.EmailChangeUndoTTL,
		DeletedRetention:     def.DeletedRetention,
		UnprovenDomainTTL:    def.DomainUnprovenTTL,
		UnprovenDomainMaxAge: def.DomainUnprovenMaxAge,
		DomainCertDailyCap:   def.DomainCertsDaily,
		VisitorSessionIdle:   def.VisitorSessionIdle,
		OAuthUnusedClientAge: def.OAuthUnusedClientAge,
	}); got != want {
		t.Errorf("db defaults %+v, config says %+v", got, want)
	}
	if got, want := mcp.DefaultLimits(), (mcp.Limits{
		DeletedRetention:     def.DeletedRetention,
		PreviewLinkTTL:       def.PreviewLinkTTL,
		ExportLinkTTL:        def.ExportLinkTTL,
		IdleAfter:            def.IdleAfter,
		IdleGrace:            def.IdleGrace,
		DomainUnprovenTTL:    def.DomainUnprovenTTL,
		DomainLapseWarnAfter: def.DomainLapseWarnAfter,
		DomainLapseAfter:     def.DomainLapseAfter,
		UndoDays:             time.Duration(def.SavedData.UndoDays) * 24 * time.Hour,
		FamilyUnprovenTTL:    def.FamilyUnprovenTTL,
	}); got != want {
		t.Errorf("mcp defaults %+v, config says %+v", got, want)
	}
	for _, d := range []time.Duration{30 * time.Second, time.Minute, 15 * time.Minute, time.Hour, 90 * time.Minute, 24 * time.Hour, 36 * time.Hour, 7 * 24 * time.Hour} {
		if mcp.Span(d) != config.Span(d) {
			t.Errorf("mcp says %q for %v, config says %q", mcp.Span(d), d, config.Span(d))
		}
	}
}

// servedTexts is every text file served with the limits rewrite: the static
// pages and documents (not the enterprise pages, which describe another
// product) and the skills.
func servedTexts(t *testing.T) map[string]string {
	t.Helper()
	out := map[string]string{}
	add := func(fsys fs.FS, root, prefix string) {
		err := fs.WalkDir(fsys, root, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			switch {
			case strings.Contains(p, "enterprise"), strings.Contains(p, "swagger-ui"):
				return nil
			case strings.HasSuffix(p, ".html"), strings.HasSuffix(p, ".txt"), strings.HasSuffix(p, ".yaml"),
				strings.HasSuffix(p, ".json"), strings.HasSuffix(p, ".md"), strings.HasSuffix(p, ".js"):
				b, err := fs.ReadFile(fsys, p)
				if err != nil {
					return err
				}
				out[prefix+p] = string(b)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	add(staticFiles, "static", "")
	add(plugin.FS, "skills", "")
	return out
}

// Every phrase in the table still occurs in some served file. A copy edit that
// rewords one makes this fail, instead of the sentence silently keeping its
// old number on an install that changed the knob.
func TestLimitPhrasesAppearInServedText(t *testing.T) {
	texts := servedTexts(t)
	for _, ph := range limitPhrases {
		found := false
		for _, body := range texts {
			if strings.Contains(body, ph.text) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("phrase %q (%v) is in no served file; update limitstext.go with the new wording", ph.text, ph.env)
		}
	}
}

// Each phrase follows each knob it names: changing the knob changes the
// words, and the rewrite puts the new words where the old ones were.
func TestEveryLimitPhraseFollowsItsKnob(t *testing.T) {
	envs := map[string]bool{}
	for _, k := range config.Knobs() {
		envs[k.Env] = true
	}
	def := config.DefaultLimits()
	for _, ph := range limitPhrases {
		if len(ph.env) == 0 {
			t.Errorf("phrase %q names no knob", ph.text)
		}
		for _, env := range ph.env {
			if !envs[env] {
				t.Errorf("phrase %q names unknown knob %s", ph.text, env)
				continue
			}
			v := otherValue(t, env)
			l, err := config.LoadLimits(func(k string) string {
				if k == env {
					return v
				}
				return ""
			})
			if err != nil {
				// The generic pick broke a cross-check; use the full set.
				m := changedLimits(t)
				if l, err = config.LoadLimits(func(k string) string { return m[k] }); err != nil {
					t.Fatal(err)
				}
			}
			now := ph.say(&l)
			if now == ph.say(&def) {
				t.Errorf("phrase %q does not change with %s=%s", ph.text, env, v)
				continue
			}
			got := string(newLimitsRewriter(l).apply([]byte("<<" + ph.text + ">>")))
			if got != "<<"+now+">>" {
				t.Errorf("%s=%s: %q rewritten to %q, want %q", env, v, ph.text, got, "<<"+now+">>")
			}
		}
	}
}

// With every knob at its default nothing is rewritten, so simple-host.app
// serves its files byte for byte.
func TestDefaultLimitsRewriteNothing(t *testing.T) {
	if rw := newLimitsRewriter(config.DefaultLimits()); rw != nil {
		t.Fatal("the rewriter should be nil at the defaults")
	}
	ApplyLimits(config.DefaultLimits())
	if instanceLimits != nil || assetsRewritten() {
		t.Fatal("defaults switched serve-time rewriting on")
	}
}

// The served pages, documents and skills carry the configured values, and not
// the defaults they were written with.
func TestServedTextFollowsLimits(t *testing.T) {
	withLimits(t, changedLimits(t))
	mux := http.NewServeMux()
	RegisterUIRoutes(mux, "https://simple-host.app", chromeTestHandler())

	for _, c := range []struct {
		path      string
		want, not []string
	}{
		{"/llms.txt",
			[]string{"Recently deleted for 30 days", "After 30 days it is removed for good", "for 180 days gets its owner", "14 days later with nothing done",
				"9-day undo link", "once every 45 days", "link that works for 25 minutes", "has it, for 2 hours", "at most 8 new domain certificates a day"},
			[]string{"for 7 days", "for 90 days", "7-day undo link", "once every 30 days", "works for 10 minutes", "for one hour", "at most 5 new domain"}},
		{"/openapi.json",
			[]string{"expires in 20 minutes", "valid for 20 minutes", "at most 12 keys", "holds 12 keys", "Session has 60-day absolute and 21-day idle lifetimes",
				"expires 36 hours after binding", "for 2 days the owner is emailed, and after 5 days", "restorable for 30 days", "warned 14 days ago", "a 25-minute download link"},
			[]string{"15 minutes", "50 keys", "30-day absolute", "expires 24 hours after binding", "after 72 hours", "restorable for 7 days"}},
		{"/openapi.yaml", []string{"under 180 days of visit records", "no new version for 180 days"}, []string{"90 days"}},
		{"/features", []string{"after <b>20 minutes</b>", "Same 20-minute expiry", "<b>250 sites</b>", "Up to 250 sites", "yours for 2 hours", "keeps it for 30 days",
			"about every 5 minutes", "lapses after 36 hours"},
			[]string{"a hundred sites", "<b>100 sites</b>", "for one hour", "for 7 days", "two minutes"}},
		{"/privacy.html", []string{"lasts up to 60 days and covers", "access token that lasts 45 minutes", "expires after 120 days without use", "deleted after 200 days",
			"shortened IP for 60 days", "Analytics records:</strong> 200 days"},
			[]string{"up to 30 days", "400 days", "shortened IP for 30 days"}},
		{"/architecture.html", []string{"30,000 entries", "6-minute run ceiling", "4 builds in flight per user, 40 in total", "per user (burst 7, +1 per 7 s)", "every 5 minutes (releasing unproven bindings after 36 hours)"},
			[]string{"50,000 entries", "8-minute", "64 in total", "burst 30"}},
		{"/dashboard", []string{"Expires in 20 minutes", "For 30 days you can restore it"}, []string{"15 minutes", "For 7 days"}},
		{"/skills/connect-domain/SKILL.md", []string{"expires after 36 hours", "If it fails every check for 2 days", "After 5 days the domain is disconnected", "after 5 days, be disconnected"},
			[]string{"after 24 hours", "for a day", "three days"}},
		// The hackathon skill's claims live on the public instance, under its
		// limits, so this install's EVENT_TTL_DAYS is not written into it.
		{"/skills/run-hackathon/SKILL.md", []string{"A claim lasts three weeks", "A claim expires after three weeks"}, []string{"30 days"}},
		// The enterprise pages describe the other product's limits.
		{"/enterprise/architecture", []string{"at most 50,000 entries"}, []string{"30,000 entries"}},
	} {
		rec := get(t, mux, "simple-host.app", c.path)
		if rec.Code != http.StatusOK {
			t.Errorf("%s: %d", c.path, rec.Code)
			continue
		}
		body := rec.Body.String()
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
	withLimits(t, changedLimits(t))
	page, err := chromePage("showcase.html", chromeData{})
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range []string{"kept for 30 days with their saved data", "For 30 days you can restore it", "Recently deleted within 30 days", "updates for 180 days", "change it again after 45 days"} {
		if !strings.Contains(string(page), w) {
			t.Errorf("showcase: missing %q", w)
		}
	}
	h := chromeTestHandler()
	rec := httptest.NewRecorder()
	h.renderPreviewExpired(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if !strings.Contains(rec.Body.String(), "Preview links work for 2 hours.") {
		t.Errorf("preview-expired page: %s", rec.Body.String())
	}
}

// What the connector tells an AI app — the instructions, the tool
// descriptions and output schemas — states the settings.
func TestMCPTextFollowsLimits(t *testing.T) {
	def, _ := json.Marshal(mcp.Tools())
	if !strings.Contains(mcp.Instructions(), "Recently deleted for 7 days") || !strings.Contains(string(def), "Recently deleted for 7 days") {
		t.Fatal("default MCP text lost its wording")
	}
	withLimits(t, changedLimits(t))
	if s := mcp.Instructions(); !strings.Contains(s, "Recently deleted for 30 days") || strings.Contains(s, "for 7 days") {
		t.Errorf("instructions do not follow DELETED_RETENTION_DAYS")
	}
	b, _ := json.Marshal(mcp.Tools())
	tools := string(b)
	for _, w := range []string{
		"Recently deleted for 30 days", "deleted within the last 30 days", // delete_site, list_deleted_sites
		"updated for 180 days and moves it to Recently deleted 14 days later", // keep_site
		"The link works for 25 minutes",                                       // export_site
		"can open it, for 2 hours", "for anyone who has it, for 2 hours",      // deploy preview, preview_version
		"The owner is emailed after 2 days; after 5 days", // domain output schema
		"after 180 days without visits",                   // keep output schema
		"25 minutes after it was made",                    // export output schema
	} {
		if !strings.Contains(tools, w) {
			t.Errorf("MCP tools: missing %q", w)
		}
	}
	for _, n := range []string{"for 7 days", "for 90 days", "10 minutes", "for one hour", "after a day"} {
		if strings.Contains(tools, n) {
			t.Errorf("MCP tools still say %q", n)
		}
	}
}

// The limiters are built from the RATE_LIMIT_* settings.
func TestRateLimitsFollowSettings(t *testing.T) {
	withLimits(t, map[string]string{"RATE_LIMIT_SIGNIN_IP": "2,1h", "RATE_LIMIT_SIGNIN_EMAIL": "9,20s"})
	h := NewUserHandler(nil, nil, "https://simple-host.app")
	if h.ipLimiter.capacity != 2 || h.ipLimiter.rate != 1.0/3600 {
		t.Errorf("sign-in IP limiter: burst %v, %v/s", h.ipLimiter.capacity, h.ipLimiter.rate)
	}
	if h.emailLimiter.capacity != 9 || h.emailLimiter.rate != 1.0/20 {
		t.Errorf("sign-in email limiter: burst %v, %v/s", h.emailLimiter.capacity, h.emailLimiter.rate)
	}
	if !h.ipLimiter.allow("a") || !h.ipLimiter.allow("a") || h.ipLimiter.allow("a") {
		t.Error("a burst of 2 should allow two and refuse the third")
	}
}

// MAX_ARCHIVE_MB: every sentence stating the upload cap is in a served file
// and follows the cap in force.
func TestArchivePhrasesFollowTheUploadCap(t *testing.T) {
	texts := servedTexts(t)
	for _, ph := range archivePhrases {
		found := false
		for _, body := range texts {
			if strings.Contains(body, ph.text) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("archive phrase %q is in no served file", ph.text)
		}
	}
	if newLimitsRewriter(config.DefaultLimits()) != nil {
		t.Fatal("default cap rewrote text")
	}
	old := SiteLimit()
	SetSiteLimit(300 << 20)
	defer SetSiteLimit(old)
	rw := newLimitsRewriter(config.DefaultLimits())
	if got := string(rw.apply([]byte("Up to <b>100 MB</b> per upload."))); got != "Up to <b>300 MB</b> per upload." {
		t.Fatalf("rewritten: %q", got)
	}
}

// Every route that serves a skill file states this instance's upload cap, not
// the 100 MB the files are written with.
func TestSkillRoutesFollowArchiveLimit(t *testing.T) {
	old := SiteLimit()
	SetSiteLimit(300 << 20)
	t.Cleanup(func() { SetSiteLimit(old); ApplyLimits(config.DefaultLimits()) })
	ApplyLimits(config.DefaultLimits())
	mux := http.NewServeMux()
	RegisterUIRoutes(mux, "https://simple-host.app", chromeTestHandler())
	RegisterSkillsHub(mux, "https://simple-host.app")
	for _, c := range []struct{ path, want string }{
		{"/v1/skills/website-deploy", "**Archive limit** is 300 MB."},
		{"/v1/skills/website-deploy/SKILL.md", "**Archive limit** is 300 MB."},
		{"/.well-known/skills/website-deploy/SKILL.md", "**Archive limit** is 300 MB."},
		{"/skills/website-deploy", "**Archive limit** is 300 MB."},
		{"/skills/website-deploy/SKILL.md", "**Archive limit** is 300 MB."},
		{"/v1/skills/website-deploy/references/packaging-and-validation.md", "The API rejects archives over 300 MB."},
		{"/.well-known/skills/website-deploy/references/packaging-and-validation.md", "The API rejects archives over 300 MB."},
		{"/skills/website-deploy/references/packaging-and-validation.md", "The API rejects archives over 300 MB."},
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
