package mcp

// fixtureProduct is a small product for the tests of how the tool server
// composes a contribution: one family with one tool, the platform's
// notification tools as the product's own, and instructions for a product connection. It stands in for a real product; a real
// product's own package tests its tools against this server.
func fixtureProduct() *Contribution {
	read := Tool{
		Name: "fixture_read", Title: "Read the fixture", Description: "Read the fixture product's one value.",
		InputSchema: noArgs(), OutputSchema: outObject(map[string]any{"ok": outBool("Succeeded.")}, "ok"), Annotations: readOnly(),
		run: func(*call, map[string]any) (output, error) {
			return output{Text: "Read the fixture.", Structured: map[string]any{"ok": true}}, nil
		},
	}
	return &Contribution{
		Families:      []FamilyTools{{Family: "fixture", Tools: []Tool{read}}},
		Notifications: notificationTools(),
		Instructions: func(mode string) string {
			switch mode {
			case CallerModeEvents:
				return "Fixture product: a personal connection."
			}
			return ""
		},
		MutationNames: map[string]RetiredMutation{
			"fixture_retired": {Message: "Fixture: this tool was retired. [fixture_retired]", Code: "fixture_retired"},
		},
	}
}

// testConfig is cfg with the fixture product unless the test chose its own
// contribution.
func testConfig(cfg Config) Config {
	if cfg.Contribution == nil {
		cfg.Contribution = fixtureProduct()
	}
	return cfg
}

// newRegistry is the registry of a server built with testConfig.
func newRegistry(cfg Config) *toolRegistry { return buildRegistry(testConfig(cfg), nil) }
