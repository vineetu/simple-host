package mcp

import (
	"encoding/json"
	"strings"
	"testing"
)

// Telemetry drops other people's names (a product's participants, judges,
// applicants, teammates, and the raw account ids behind them) only for
// product tools (analyticsPolicy.product, set when a product tool is
// registered); an event's public organiser_name and ordinary values stay.
// Platform tools carry no participant names, so their policy leaves them.
func TestRDAnalyticsPersonNamesPolicy(t *testing.T) {
	const person = "PLANTED_PERSON_NAME"
	structured := map[string]any{
		"organiser_name": "Open Hack",
		"title":          "ordinary title",
		"display_name":   person,
		"updated_by":     person,
		"user_id":        person,
		"members":        []any{person, "other " + person},
		"judges":         []any{map[string]any{"name": person, "id": "j1"}},
		"people":         []any{map[string]any{"display_name": person, "role": "judge"}},
	}
	copyOf := func(policy toolPolicy) string {
		reply := toolResult(nil, "ok", structured, false, false)
		out, _ := json.Marshal(analyticsResponseForPolicy(policy, reply.Result))
		return string(out)
	}
	product := copyOf(toolPolicy{analytics: analyticsPolicy{product: true}})
	if strings.Contains(product, person) {
		t.Errorf("a product tool's telemetry kept a person's name: %s", product)
	}
	for _, kept := range []string{"ordinary title", "judge"} {
		if !strings.Contains(product, kept) {
			t.Errorf("a product tool's telemetry lost %q: %s", kept, product)
		}
	}
	if platform := copyOf(toolPolicy{}); !strings.Contains(platform, person) {
		t.Errorf("platform tool telemetry unexpectedly strips names: %s", platform)
	}
}
