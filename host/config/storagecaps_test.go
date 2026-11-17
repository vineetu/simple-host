package config

import "testing"

func TestFileAllowanceKnobs(t *testing.T) {
	d := DefaultLimits()
	if d.MaxSiteTotalMB != 0 || d.MaxAccountMB != 0 || !d.CanSetKeepVersions("anyone") {
		t.Fatal("self-hosted defaults changed")
	}
	env := map[string]string{"KEEP_VERSIONS_SELF_SET": " ChhotaBreak, vineetu, jot-transcribe ", "KEEP_VERSIONS_OVERRIDES": "chhotabreak:10", "MAX_SITE_TOTAL_MB": "200", "SITE_TOTAL_CAP_FROM": "2026-10-05T15:00:00Z", "MAX_SITE_TOTAL_OVERRIDES": "chhotabreak:2000", "MAX_ACCOUNT_MB": "1024", "MAX_ACCOUNT_MB_OVERRIDES": "chhotabreak:10000"}
	l, err := LoadLimits(func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	if l.CanSetKeepVersions("other") || !l.CanSetKeepVersions("new-name", "chhotabreak") || l.KeepVersionsFor(4, "new-name", "chhotabreak") != 10 || l.SiteTotalMBFor("new-name", "chhotabreak") != 2000 || l.AccountMBFor("new-name", "chhotabreak") != 10000 {
		t.Fatal("old handle overrides failed")
	}
	for k, v := range map[string]string{"KEEP_VERSIONS_SELF_SET": "not a handle", "KEEP_VERSIONS_OVERRIDES": "vineetu:0", "MAX_SITE_TOTAL_MB": "-1", "SITE_TOTAL_CAP_FROM": "2026-10-05", "MAX_SITE_TOTAL_OVERRIDES": "vineetu:no", "MAX_ACCOUNT_MB": "-1", "MAX_ACCOUNT_MB_OVERRIDES": "vineetu:0"} {
		_, err := LoadLimits(func(name string) string {
			if name == k {
				return v
			}
			return ""
		})
		if err == nil {
			t.Errorf("accepted %s=%s", k, v)
		}
	}
}

func TestFileAllowanceSettingsTypes(t *testing.T) {
	for _, s := range Settings() {
		switch s.Name {
		case "KEEP_VERSIONS_SELF_SET", "KEEP_VERSIONS_OVERRIDES", "SITE_TOTAL_CAP_FROM", "MAX_SITE_TOTAL_OVERRIDES", "MAX_ACCOUNT_MB_OVERRIDES":
			if s.Type != "string" || s.Min != nil || s.Max != nil {
				t.Errorf("%s should accept text: %+v", s.Name, s)
			}
		}
	}
}
