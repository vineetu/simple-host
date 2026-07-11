package config

import (
	"errors"
	"os"
	"strconv"
	"strings"
	"time"
)

// Defaults are generic. A real deployment sets SITE_DOMAIN, PUBLIC_BASE_URL,
// and DATA_DIR via env to match its own domain and disk layout. DEPLOY_SCRIPT
// is an optional post-activation hook (used to re-register shares on hosts that
// have one); empty means no hook.
const (
	defaultDataDir       = "./data/sites"
	defaultSiteDomain    = "simple-host.app"
	defaultPort          = "8090"
	defaultDeployScript  = ""
	defaultPublicBaseURL = "https://simple-host.app"
	defaultMailFrom      = "Simple Host <noreply@simple-host.app>"
)

type Config struct {
	DBDSN      string
	DataDir    string
	SiteDomain string
	// ContentHost is the shared v3 content-serving host (e.g. sites.simple-host.app).
	// Pages served there call state/collections with this Origin for every site.
	// Defaults to "sites."+SiteDomain; override with CONTENT_HOST if needed.
	ContentHost string
	// CNAMETarget is the hostname users CNAME their custom domains to (e.g.
	// cname.simple-host.app). Defaults to "cname."+SiteDomain; override with
	// CNAME_TARGET if needed.
	CNAMETarget string
	// CustomDomainIP is the box's public IPv4, returned as the A-record value when
	// a user connects an APEX custom domain (which can't use a CNAME). Set via
	// CUSTOM_DOMAIN_IP; empty means apex records fall back to the CNAME target.
	CustomDomainIP string
	AdminAPIKey    string
	Port           string
	DeployScript   string
	PublicBaseURL  string
	MailFrom       string
	ResendAPIKey   string

	// Optional "create with AI" endpoint (/v1/generate). Disabled when neither
	// AnthropicAPIKey nor AgentServerURL is set. Sign-in-gated + rate limited.
	//
	// Two backends:
	//   - AgentServerURL set: proxy each turn to the Claude Agent SDK server
	//     (real agent w/ tools, runs on the operator's box subscription). The
	//     shared secret authenticates that call; both must match. Preferred.
	//   - else AnthropicAPIKey set: call the Messages API directly (metered).
	AnthropicAPIKey   string
	GenerateModel     string
	AgentServerURL    string // e.g. https://simple-host-agent.ideaflow.page (no trailing slash)
	AgentSharedSecret string

	// Ephemeral "preview" sites. Sites created by an account in PreviewAccounts
	// get an expires_at = now + PreviewTTL, and a background sweep deletes them
	// once expired. Everyone else's sites are permanent (expires_at NULL).
	// PREVIEW_ACCOUNTS is a comma-separated list of usernames/emails; PreviewTTL
	// comes from PREVIEW_TTL_HOURS (default 48h). Empty list = feature off.
	PreviewAccounts map[string]bool
	PreviewTTL      time.Duration

	// AnalyticsLog is the path to the nginx analytics access log. Empty (the
	// default) disables the log ingester entirely — safe for local dev and
	// hosts that have not configured the analytics log yet.
	// Set ANALYTICS_LOG=/var/log/simple-host/analytics.log in production.
	AnalyticsLog string
}

func Load() (Config, error) {
	cfg := Config{
		DBDSN:         os.Getenv("DB_DSN"),
		DataDir:       getEnvOrDefault("DATA_DIR", defaultDataDir),
		SiteDomain:    getEnvOrDefault("SITE_DOMAIN", defaultSiteDomain),
		AdminAPIKey:   os.Getenv("ADMIN_API_KEY"),
		Port:          getEnvOrDefault("PORT", defaultPort),
		DeployScript:  getEnvOrDefault("DEPLOY_SCRIPT", defaultDeployScript),
		PublicBaseURL: getEnvOrDefault("PUBLIC_BASE_URL", defaultPublicBaseURL),
		MailFrom:      getEnvOrDefault("MAIL_FROM", defaultMailFrom),
		ResendAPIKey:  os.Getenv("RESEND_API_KEY"),

		AnthropicAPIKey:   os.Getenv("ANTHROPIC_API_KEY"),
		GenerateModel:     getEnvOrDefault("GENERATE_MODEL", "claude-haiku-4-5-20251001"),
		AgentServerURL:    strings.TrimRight(os.Getenv("AGENT_SERVER_URL"), "/"),
		AgentSharedSecret: os.Getenv("AGENT_SHARED_SECRET"),
	}
	// CONTENT_HOST defaults to sites.<SITE_DOMAIN> so prod/test need no extra env.
	cfg.ContentHost = getEnvOrDefault("CONTENT_HOST", "sites."+cfg.SiteDomain)
	// CNAME_TARGET defaults to cname.<SITE_DOMAIN> — the record humans add when
	// binding a custom domain.
	cfg.CNAMETarget = getEnvOrDefault("CNAME_TARGET", "cname."+cfg.SiteDomain)
	cfg.CustomDomainIP = os.Getenv("CUSTOM_DOMAIN_IP")
	cfg.AnalyticsLog = os.Getenv("ANALYTICS_LOG")

	cfg.PreviewAccounts = map[string]bool{}
	for _, a := range strings.Split(os.Getenv("PREVIEW_ACCOUNTS"), ",") {
		if a = strings.TrimSpace(strings.ToLower(a)); a != "" {
			cfg.PreviewAccounts[a] = true
		}
	}
	cfg.PreviewTTL = 48 * time.Hour
	if v := os.Getenv("PREVIEW_TTL_HOURS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.PreviewTTL = time.Duration(n) * time.Hour
		}
	}

	if cfg.DBDSN == "" {
		return Config{}, errors.New("DB_DSN is required")
	}
	if cfg.AdminAPIKey == "" {
		return Config{}, errors.New("ADMIN_API_KEY is required (no default — set it explicitly so the public source doesn't ship a known key)")
	}

	return cfg, nil
}

func getEnvOrDefault(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
