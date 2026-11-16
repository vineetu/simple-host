package config

import (
	"os"
	"strings"
	"testing"
)

// A price of 0 for ASSISTANT_PRICE_{INPUT,OUTPUT}_PER_MTOK_USD must fall
// back to the default rather than be accepted: Ask's reservation is
// amount-of-tokens * price, so a 0 price makes every reservation 0,
// which (with ASSISTANT_MONTHLY_BUDGET_USD > 0) is an uncapped assistant
// rather than a cheap one (adversarial review, SIM-394).
func TestLoadAssistantPriceZeroFallsBackToDefault(t *testing.T) {
	t.Setenv("DB_DSN", "postgres://test")
	t.Setenv("ADMIN_API_KEY", "test-key")
	t.Setenv("SITE_DOMAIN", "simple-host.app")
	t.Setenv("ASSISTANT_PRICE_INPUT_PER_MTOK_USD", "0")
	t.Setenv("ASSISTANT_PRICE_OUTPUT_PER_MTOK_USD", "0")
	cfg, _, err := Load(os.Getenv)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AssistantPriceInputPerMTokUSD != 0.5 {
		t.Fatalf("input price = %v, want the default 0.5 (0 must be rejected)", cfg.AssistantPriceInputPerMTokUSD)
	}
	if cfg.AssistantPriceOutputPerMTokUSD != 2.0 {
		t.Fatalf("output price = %v, want the default 2.0 (0 must be rejected)", cfg.AssistantPriceOutputPerMTokUSD)
	}

	t.Setenv("ASSISTANT_PRICE_INPUT_PER_MTOK_USD", "0.1")
	t.Setenv("ASSISTANT_PRICE_OUTPUT_PER_MTOK_USD", "0.4")
	cfg, _, err = Load(os.Getenv)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AssistantPriceInputPerMTokUSD != 0.1 || cfg.AssistantPriceOutputPerMTokUSD != 0.4 {
		t.Fatalf("a real positive price must still be accepted: got %v / %v", cfg.AssistantPriceInputPerMTokUSD, cfg.AssistantPriceOutputPerMTokUSD)
	}
}

func TestProductLimitsValidation(t *testing.T) {
	for _, key := range []string{"EVENT_CREATE_PER_DAY", "EVENT_MAX_ACTIVE_PER_ORGANISER", "EVENT_TEAM_SIZE_DEFAULT", "EVENT_SITES_KEEP_DAYS", "EVENT_REMOVAL_WARN_DAYS", "EVENT_APPLY_PENDING_PER_EVENT", "HACK_INSTANCE_BUDGET_GB", "HACK_EVENT_ICON_MAX_BYTES", "RATE_LIMIT_EVENT_CODES_IP", "RATE_LIMIT_EVENT_CODES_USER", "RATE_LIMIT_EVENT_NAMES_USER", "RATE_LIMIT_EVENT_APPLY_IP", "RATE_LIMIT_EVENT_APPLY_USER"} {
		_, _, err := Load(env(map[string]string{key: "bad"}))
		if err == nil || !strings.Contains(err.Error(), key) {
			t.Fatalf("%s: %v", key, err)
		}
	}
	cfg, warnings, err := Load(env(map[string]string{"ASSISTANT_DAILY_QUESTIONS": "bad", "HACK_DEMO_EVENT_SLUG": "sample", "MCP_APP_HACK_JUDGE": "on"}))
	if err != nil || len(warnings) != 1 || cfg.AssistantDailyQuestions != 30 || cfg.DemoEventSlug != "sample" || cfg.JudgeApp != "on" {
		t.Fatal("product configuration not loaded")
	}
}
