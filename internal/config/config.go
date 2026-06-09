package config

import (
	"errors"
	"os"
)

const (
	defaultDataDir       = "/root/workspace/general/sites"
	defaultSiteDomain    = "ideaflow.page"
	defaultPort          = "8090"
	defaultDeployScript  = "/root/workspace/general/scripts/deploy-site"
	defaultPublicBaseURL = "https://simple-host.ideaflow.page"
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
