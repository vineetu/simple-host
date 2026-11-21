package config

import (
	"fmt"
	"strconv"
	"strings"
)

type Config struct {
	// SIM-394 hack organiser assistant (Hack edition only).
	AssistantBaseURL         string
	AssistantModel           string
	AssistantAPIKey          string
	AssistantReasoningEffort string
	// AssistantMonthlyBudgetUSD is the instance-wide monthly spend ceiling
	// (default 5); AssistantDailyQuestions is the per-organiser daily question
	// ceiling (default 30).
	AssistantMonthlyBudgetUSD      float64
	AssistantDailyQuestions        int
	AssistantPriceInputPerMTokUSD  float64
	AssistantPriceOutputPerMTokUSD float64

	Limits        Limits
	DemoEventSlug string
	JudgeApp      string
	OrganiserApp  string
}

func Load(getenv func(string) string) (Config, []string, error) {
	limits, err := LoadLimits(getenv)
	if err != nil {
		return Config{}, nil, err
	}
	cfg := Config{Limits: limits, DemoEventSlug: getenv("HACK_DEMO_EVENT_SLUG"), JudgeApp: getenv("MCP_APP_HACK_JUDGE"), OrganiserApp: getenv("MCP_APP_HACK_ORGANISER")}
	warnings := loadAssistant(&cfg, getenv)
	warnings = append(warnings, limits.Warnings()...)
	return cfg, warnings, nil
}

// loadAssistant reads the SIM-394 hack organiser assistant settings.
func loadAssistant(cfg *Config, getenv func(string) string) (warnings []string) {
	cfg.AssistantBaseURL = strings.TrimSpace(getenv("ASSISTANT_BASE_URL"))
	cfg.AssistantModel = strings.TrimSpace(getenv("ASSISTANT_MODEL"))
	cfg.AssistantAPIKey = getenv("ASSISTANT_API_KEY")
	cfg.AssistantReasoningEffort = strings.TrimSpace(getenv("ASSISTANT_REASONING_EFFORT"))

	cfg.AssistantMonthlyBudgetUSD = 5
	if v := strings.TrimSpace(getenv("ASSISTANT_MONTHLY_BUDGET_USD")); v != "" {
		if n, err := strconv.ParseFloat(v, 64); err == nil && n >= 0 {
			cfg.AssistantMonthlyBudgetUSD = n
		} else {
			warnings = append(warnings, fmt.Sprintf("warning: invalid ASSISTANT_MONTHLY_BUDGET_USD %q; using the default", v))
		}
	}
	cfg.AssistantDailyQuestions = 30
	if v := strings.TrimSpace(getenv("ASSISTANT_DAILY_QUESTIONS")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.AssistantDailyQuestions = n
		} else {
			warnings = append(warnings, fmt.Sprintf("warning: invalid ASSISTANT_DAILY_QUESTIONS %q; using the default", v))
		}
	}
	// Strictly > 0, not >= 0: a price of 0 makes every reservation 0 --
	// with MonthlyBudgetUSD > 0 that is an uncapped assistant, not a
	// cheap one, since AssistantReserve's "spent + reserveMicro >
	// budgetMicro" check can then never trip (adversarial review).
	cfg.AssistantPriceInputPerMTokUSD = 0.5
	if v := strings.TrimSpace(getenv("ASSISTANT_PRICE_INPUT_PER_MTOK_USD")); v != "" {
		if n, err := strconv.ParseFloat(v, 64); err == nil && n > 0 {
			cfg.AssistantPriceInputPerMTokUSD = n
		} else {
			warnings = append(warnings, fmt.Sprintf("warning: invalid ASSISTANT_PRICE_INPUT_PER_MTOK_USD %q; using the default", v))
		}
	}
	cfg.AssistantPriceOutputPerMTokUSD = 2.0
	if v := strings.TrimSpace(getenv("ASSISTANT_PRICE_OUTPUT_PER_MTOK_USD")); v != "" {
		if n, err := strconv.ParseFloat(v, 64); err == nil && n > 0 {
			cfg.AssistantPriceOutputPerMTokUSD = n
		} else {
			warnings = append(warnings, fmt.Sprintf("warning: invalid ASSISTANT_PRICE_OUTPUT_PER_MTOK_USD %q; using the default", v))
		}
	}
	return warnings
}
