package config

import (
	"errors"
	"fmt"
	"log"
	"os"
	"sort"
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

	// Which AI backend "create with AI" talks to when LLM_PROVIDER is unset.
	defaultLLMProvider = "grok"
)

// llmProvider is a named OpenAI-compatible backend. Selecting one via
// LLM_PROVIDER just fills in a base URL (and a model, where there is an obvious
// one) so an operator does not have to remember endpoint URLs. Every field
// stays overridable: LLM_BASE_URL / LLM_MODEL always win when set, which is why
// switching providers never silently changes an explicitly configured deploy.
//
// Only backends that genuinely expose POST /chat/completions in OpenAI's shape
// belong here — that is the only protocol the generate handler speaks.
type llmProvider struct {
	BaseURL string
	Model   string // "" = no sensible default, operator must set LLM_MODEL
}

var llmProviders = map[string]llmProvider{
	// The local CLIProxyAPI sidecar fronting the operator's Grok subscription.
	// This is the production default: no metered API key, no per-token cost.
	"grok": {BaseURL: "http://127.0.0.1:8102/v1", Model: "grok-4.6"},
	// Direct xAI API (metered) rather than the sidecar.
	"xai":        {BaseURL: "https://api.x.ai/v1"},
	"openai":     {BaseURL: "https://api.openai.com/v1"},
	"deepseek":   {BaseURL: "https://api.deepseek.com/v1"},
	"openrouter": {BaseURL: "https://openrouter.ai/api/v1"},
	// Anything else OpenAI-compatible: set LLM_BASE_URL and LLM_MODEL yourself.
	"custom": {},
}

// LLMProviderNames lists the selectable providers, for error messages.
func LLMProviderNames() []string {
	names := make([]string, 0, len(llmProviders))
	for n := range llmProviders {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// resolveLLM picks base URL and model for one endpoint. Explicit env always
// beats the provider's defaults; the provider only fills the gaps.
func resolveLLM(provider, envBase, envModel string) (base, model string) {
	p := llmProviders[provider]
	base, model = envBase, envModel
	if base == "" {
		base = p.BaseURL
	}
	if model == "" {
		model = p.Model
	}
	return strings.TrimRight(base, "/"), model
}

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

	// Optional "create with AI" endpoint (/v1/generate). Sign-in-gated + rate
	// limited. Exactly ONE backend at a time: an OpenAI-compatible
	// /chat/completions endpoint. Still no fallback chain — if the selected
	// backend is down, AI create reports an error rather than silently failing
	// over and spending someone else's credits.
	//
	// LLM_PROVIDER picks which backend (see llmProviders; default "grok", the
	// local CLIProxyAPI sidecar on the operator's Grok subscription).
	// LLM_BASE_URL / LLM_MODEL override the provider's defaults and win
	// whenever they are set.
	LLMProvider string
	LLMAPIKey   string
	LLMBaseURL  string // e.g. http://127.0.0.1:8102/v1 (the Grok sidecar)
	LLMModel    string // e.g. grok-4.6

	// Vision pass for image attachments, same single-provider rule. Defaults to
	// the same provider as LLMProvider; set VISION_PROVIDER to split them.
	// Optional: without a key, attachments are refused honestly rather than
	// silently ignored.
	VisionProvider string
	VisionAPIKey   string
	VisionBaseURL  string
	VisionModel    string

	// Voice input for the builder chat (/v1/transcribe). Points at a local
	// speech-to-text service; the model runs on this box, so audio never leaves
	// the host and there is no per-minute cost. Unset disables the endpoint, and
	// the chat hides its mic button.
	TranscribeURL string // e.g. http://127.0.0.1:8100/transcribe
	// Shared with the speech service to sign short-lived WebSocket tickets.
	// Empty disables live transcription; the batch endpoint still works.
	TranscribeTicketSecret string

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

	// WriteAuthMode gates anonymous state/collections writes: off | log | on.
	// Unset or any unrecognized value is "log". The source default is never "on".
	WriteAuthMode string

	// Visitor OAuth. A provider is enabled only when BOTH of its vars are set.
	GoogleOAuthClientID     string
	GoogleOAuthClientSecret string
	GitHubOAuthClientID     string
	GitHubOAuthClientSecret string
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

		LLMAPIKey:              os.Getenv("LLM_API_KEY"),
		VisionAPIKey:           os.Getenv("VISION_API_KEY"),
		TranscribeURL:          strings.TrimRight(os.Getenv("TRANSCRIBE_URL"), "/"),
		TranscribeTicketSecret: os.Getenv("TRANSCRIBE_TICKET_SECRET"),
	}
	// AI backend selection. LLM_PROVIDER names the backend; LLM_BASE_URL and
	// LLM_MODEL still override it, so an existing deploy that sets both keeps
	// exactly the behaviour it had before this switch existed.
	cfg.LLMProvider = strings.ToLower(getEnvOrDefault("LLM_PROVIDER", defaultLLMProvider))
	cfg.VisionProvider = strings.ToLower(getEnvOrDefault("VISION_PROVIDER", cfg.LLMProvider))
	if _, ok := llmProviders[cfg.LLMProvider]; !ok {
		return cfg, fmt.Errorf("LLM_PROVIDER=%q is not a known provider (choose one of: %s)",
			cfg.LLMProvider, strings.Join(LLMProviderNames(), ", "))
	}
	if _, ok := llmProviders[cfg.VisionProvider]; !ok {
		return cfg, fmt.Errorf("VISION_PROVIDER=%q is not a known provider (choose one of: %s)",
			cfg.VisionProvider, strings.Join(LLMProviderNames(), ", "))
	}
	cfg.LLMBaseURL, cfg.LLMModel = resolveLLM(cfg.LLMProvider, os.Getenv("LLM_BASE_URL"), os.Getenv("LLM_MODEL"))
	cfg.VisionBaseURL, cfg.VisionModel = resolveLLM(cfg.VisionProvider, os.Getenv("VISION_BASE_URL"), os.Getenv("VISION_MODEL"))
	// A key with nowhere to send it is a misconfiguration, not a silent no-op.
	if cfg.LLMAPIKey != "" && (cfg.LLMBaseURL == "" || cfg.LLMModel == "") {
		return cfg, fmt.Errorf("LLM_API_KEY is set but provider %q leaves base URL or model empty — set LLM_BASE_URL and LLM_MODEL",
			cfg.LLMProvider)
	}
	if cfg.VisionAPIKey != "" && (cfg.VisionBaseURL == "" || cfg.VisionModel == "") {
		return cfg, fmt.Errorf("VISION_API_KEY is set but provider %q leaves base URL or model empty — set VISION_BASE_URL and VISION_MODEL",
			cfg.VisionProvider)
	}

	// CONTENT_HOST defaults to sites.<SITE_DOMAIN> so prod/test need no extra env.
	cfg.ContentHost = getEnvOrDefault("CONTENT_HOST", "sites."+cfg.SiteDomain)
	// CNAME_TARGET defaults to cname.<SITE_DOMAIN> — the record humans add when
	// binding a custom domain.
	cfg.CNAMETarget = getEnvOrDefault("CNAME_TARGET", "cname."+cfg.SiteDomain)
	cfg.CustomDomainIP = os.Getenv("CUSTOM_DOMAIN_IP")
	cfg.AnalyticsLog = os.Getenv("ANALYTICS_LOG")

	cfg.GoogleOAuthClientID = os.Getenv("GOOGLE_OAUTH_CLIENT_ID")
	cfg.GoogleOAuthClientSecret = os.Getenv("GOOGLE_OAUTH_CLIENT_SECRET")
	cfg.GitHubOAuthClientID = os.Getenv("GITHUB_OAUTH_CLIENT_ID")
	cfg.GitHubOAuthClientSecret = os.Getenv("GITHUB_OAUTH_CLIENT_SECRET")
	if xorNonEmpty(cfg.GoogleOAuthClientID, cfg.GoogleOAuthClientSecret) {
		log.Printf("warning: Google OAuth is misconfigured (need both GOOGLE_OAUTH_CLIENT_ID and GOOGLE_OAUTH_CLIENT_SECRET); treating as off")
	}
	if xorNonEmpty(cfg.GitHubOAuthClientID, cfg.GitHubOAuthClientSecret) {
		log.Printf("warning: GitHub OAuth is misconfigured (need both GITHUB_OAUTH_CLIENT_ID and GITHUB_OAUTH_CLIENT_SECRET); treating as off")
	}

	switch mode := strings.ToLower(strings.TrimSpace(os.Getenv("WRITE_AUTH_MODE"))); mode {
	case "off", "log", "on":
		cfg.WriteAuthMode = mode
	case "":
		cfg.WriteAuthMode = "log"
	default:
		log.Printf("warning: invalid WRITE_AUTH_MODE %q; treating as log", mode)
		cfg.WriteAuthMode = "log"
	}

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

// GoogleOAuthEnabled reports whether both Google client vars are set.
func (c Config) GoogleOAuthEnabled() bool {
	return c.GoogleOAuthClientID != "" && c.GoogleOAuthClientSecret != ""
}

// GitHubOAuthEnabled reports whether both GitHub client vars are set.
func (c Config) GitHubOAuthEnabled() bool {
	return c.GitHubOAuthClientID != "" && c.GitHubOAuthClientSecret != ""
}

// EnabledVisitorProviders returns the enabled provider names in stable order
// (google, then github). Empty when neither pair is configured.
func (c Config) EnabledVisitorProviders() []string {
	var names []string
	if c.GoogleOAuthEnabled() {
		names = append(names, "google")
	}
	if c.GitHubOAuthEnabled() {
		names = append(names, "github")
	}
	return names
}

// OAuthRedirectURI is the apex callback registered with the IdP. It is derived
// from PUBLIC_BASE_URL — there is no separate redirect-base env var.
func (c Config) OAuthRedirectURI(provider string) string {
	return strings.TrimRight(c.PublicBaseURL, "/") + "/v1/auth/oauth/" + provider + "/callback"
}

func xorNonEmpty(a, b string) bool {
	return (a == "") != (b == "")
}

func getEnvOrDefault(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

