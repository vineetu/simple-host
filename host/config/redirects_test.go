package config

import "testing"

func TestRedirectsRuleLimit(t *testing.T) {
	if DefaultLimits().RedirectsMaxRules != 1000 {
		t.Fatal("default")
	}
	l, e := LoadLimits(env(map[string]string{"REDIRECTS_MAX_RULES": "2"}))
	if e != nil || l.RedirectsMaxRules != 2 {
		t.Fatalf("%+v %v", l.RedirectsMaxRules, e)
	}
	if _, e = LoadLimits(env(map[string]string{"REDIRECTS_MAX_RULES": "0"})); e == nil {
		t.Fatal("zero accepted")
	}
}
