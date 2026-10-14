package config

import (
	"os"
	"path/filepath"
	"testing"
)

// The Turnstile secret comes from TURNSTILE_SECRET or the file
// TURNSTILE_SECRET_FILE names; anything missing turns the spam check off.
func TestLoadTurnstile(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "secret")
	if err := os.WriteFile(file, []byte("file-secret\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		key, secret, path   string
		wantKey, wantSecret string
	}{
		{"site", "", file, "site", "file-secret"},
		{"site", "env-secret", file, "site", "env-secret"},
		{"site", "", filepath.Join(dir, "missing"), "", ""},
		{"site", "", "", "", ""},
		{"", "env-secret", "", "", ""},
		{"", "", file, "", ""},
	}
	for i, c := range cases {
		t.Setenv("TURNSTILE_SITE_KEY", c.key)
		t.Setenv("TURNSTILE_SECRET", c.secret)
		t.Setenv("TURNSTILE_SECRET_FILE", c.path)
		k, s := loadTurnstile()
		if k != c.wantKey || s != c.wantSecret {
			t.Fatalf("case %d: got key %q, secret set %v", i, k, s != "")
		}
	}
}
