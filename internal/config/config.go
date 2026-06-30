package config

import (
	"errors"
	"os"
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
	DBDSN         string
	DataDir       string
	SiteDomain    string
	AdminAPIKey   string
	Port          string
	DeployScript  string
	PublicBaseURL string
	MailFrom      string
	ResendAPIKey  string

	// Optional "create with AI" endpoint (/v1/generate). Disabled when the key
	// is empty. Spends real Anthropic credits, so it is sign-in-gated + rate
	// limited; the model defaults to Haiku (cheap).
	AnthropicAPIKey string
	GenerateModel   string
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

		AnthropicAPIKey: os.Getenv("ANTHROPIC_API_KEY"),
		GenerateModel:   getEnvOrDefault("GENERATE_MODEL", "claude-haiku-4-5-20251001"),
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
