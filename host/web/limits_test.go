package web

import (
	"encoding/json"
	"io/fs"
	"strings"
	"testing"
	"time"

	"github.com/vsriram/simple-host/host/config"
	"github.com/vsriram/simple-host/host/mcp"
	"github.com/vsriram/simple-host/host/sites"
	"github.com/vsriram/simple-host/host/webassets"
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

// MCP must use the same operational defaults and span formatting as config.
// The independent DB defaults comparison lives in cmd/server.
func TestLeafLimitDefaultsMatchConfig(t *testing.T) {
	def := config.DefaultLimits()
	if got, want := mcp.DefaultLimits(), (mcp.Limits{
		DeletedRetention:     def.DeletedRetention,
		PreviewLinkTTL:       def.PreviewLinkTTL,
		ExportLinkTTL:        def.ExportLinkTTL,
		IdleAfter:            def.IdleAfter,
		IdleGrace:            def.IdleGrace,
		DomainUnprovenTTL:    def.DomainUnprovenTTL,
		DomainLapseWarnAfter: def.DomainLapseWarnAfter,
		DomainLapseAfter:     def.DomainLapseAfter,
		SiteViewersMax:       def.SiteViewersMax,
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
			case strings.Contains(p, "enterprise"):
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
	add(StaticFiles, "static", "")
	add(webassets.Host, "skills", "")
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

// Tool descriptions and output schemas state the settings.
func TestMCPTextFollowsLimits(t *testing.T) {
	def, _ := json.Marshal(mcp.Tools())
	if !strings.Contains(string(def), "Recently deleted for 7 days") {
		t.Fatal("default MCP text lost its wording")
	}
	l := withLimits(t, changedLimits(t))
	b, _ := json.Marshal(mcp.ToolsWithLimits(mcp.LimitsOf(l)))
	tools := string(b)
	for _, w := range []string{
		"Recently deleted for 30 days", "deleted within the last 30 days", // delete_site, list_deleted_sites
		"updated for 194 days to Recently deleted unless it is marked Keep", // keep_site
		"The link works for 25 minutes",                                     // export_site
		"can open it, for 2 hours", "for anyone who has it, for 2 hours",    // deploy preview, preview_version
		"The owner is emailed after 2 days; after 5 days", // domain output schema
		"after 194 days without visits",                   // keep output schema
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
	old := sites.Upload.Body
	sites.Upload.Configure(300 << 20)
	defer sites.Upload.Configure(old)
	rw := newLimitsRewriter(config.DefaultLimits())
	if got := string(rw.apply([]byte("Up to <b>100 MB</b> per upload."))); got != "Up to <b>300 MB</b> per upload." {
		t.Fatalf("rewritten: %q", got)
	}
}
