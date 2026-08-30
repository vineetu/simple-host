package config

import "testing"

func TestProviderResolution(t *testing.T) {
	cases := []struct{ name, prov, envBase, envModel, wantBase, wantModel string }{
		{"prod: explicit env wins", "grok", "http://127.0.0.1:8102/v1", "grok-4.6", "http://127.0.0.1:8102/v1", "grok-4.6"},
		{"grok defaults", "grok", "", "", "http://127.0.0.1:8102/v1", "grok-4.6"},
		{"openai needs model", "openai", "", "", "https://api.openai.com/v1", ""},
		{"model override only", "grok", "", "grok-9", "http://127.0.0.1:8102/v1", "grok-9"},
		{"base override only", "grok", "https://api.x.ai/v1", "", "https://api.x.ai/v1", "grok-4.6"},
		{"trailing slash trimmed", "custom", "https://x.example/v1/", "m", "https://x.example/v1", "m"},
	}
	for _, c := range cases {
		b, m := resolveLLM(c.prov, c.envBase, c.envModel)
		if b != c.wantBase || m != c.wantModel {
			t.Errorf("%s: got (%q,%q) want (%q,%q)", c.name, b, m, c.wantBase, c.wantModel)
		}
	}
}
