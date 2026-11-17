package config

import (
	"fmt"
	"net/url"
	"path/filepath"
	"strconv"
	"time"
)

// StaticConfig requires no API keys or mail providers: Static never writes user data.
type StaticConfig struct {
	Config
	Bind, Port, APIURL, StateDir string
	RefreshInterval              time.Duration
}

func LoadStatic() (StaticConfig, error) {
	var cfg StaticConfig
	for _, load := range []func(*Config) error{loadServer, loadStoragePaths, loadDomains, loadServerProxy, loadServerPersonHosts, loadGroupAddresses, loadServerSiteAddresses, loadOperationalLimits, loadStoragePool} {
		if err := load(&cfg.Config); err != nil {
			return cfg, err
		}
	}
	cfg.Bind = getEnvOrDefault("STATIC_BIND", "127.0.0.1")
	cfg.Port = getEnvOrDefault("STATIC_PORT", "8091")
	cfg.APIURL = getEnvOrDefault("STATIC_API_URL", "http://127.0.0.1:8090")
	cfg.StateDir = getEnvOrDefault("STATIC_STATE_DIR", filepath.Join(filepath.Dir(cfg.DataDir), "static-state"))
	n, err := strconv.Atoi(cfg.Port)
	if err != nil || n < 1 || n > 65535 {
		return cfg, fmt.Errorf("STATIC_PORT must be between 1 and 65535")
	}
	seconds, err := strconv.Atoi(getEnvOrDefault("STATIC_REFRESH_SECONDS", "10"))
	if err != nil || seconds < 1 || seconds > 86400 {
		return cfg, fmt.Errorf("STATIC_REFRESH_SECONDS must be between 1 and 86400")
	}
	cfg.RefreshInterval = time.Duration(seconds) * time.Second
	target, err := url.Parse(cfg.APIURL)
	if err != nil || target.Host == "" || (target.Scheme != "http" && target.Scheme != "https") || target.User != nil || target.RawQuery != "" || target.Fragment != "" {
		return cfg, fmt.Errorf("STATIC_API_URL must be an http or https API address")
	}
	if cfg.DBDSN == "" {
		return cfg, fmt.Errorf("DB_DSN is required")
	}
	return cfg, nil
}
