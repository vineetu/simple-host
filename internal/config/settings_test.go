package config

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"testing"
)

// Every environment variable the server reads is in the registry, and the
// registry holds nothing it does not read.
func TestSettingsCoverEveryEnvRead(t *testing.T) {
	read := map[string]bool{}
	for _, k := range Knobs() {
		read[k.Env] = true
	}
	re := regexp.MustCompile(`(?:os\.Getenv|getEnvOrDefault)\("([A-Z0-9_]+)"`)
	files, _ := filepath.Glob("../handler/*.go")
	files = append(files, "config.go")
	for _, f := range files {
		if filepath.Ext(f) != ".go" || regexp.MustCompile(`_test\.go$`).MatchString(f) {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range re.FindAllSubmatch(b, -1) {
			read[string(m[1])] = true
		}
	}
	listed := map[string]bool{}
	for _, s := range Settings() {
		if listed[s.Name] {
			t.Errorf("%s is listed twice", s.Name)
		}
		listed[s.Name] = true
		if s.Description == "" || s.Type == "" {
			t.Errorf("%s needs a description and a type", s.Name)
		}
	}
	var missing, extra []string
	for n := range read {
		if !listed[n] {
			missing = append(missing, n)
		}
	}
	for n := range listed {
		if !read[n] {
			extra = append(extra, n)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	if len(missing) > 0 {
		t.Errorf("read by the server but not in the settings registry (internal/config/settings.go): %v", missing)
	}
	if len(extra) > 0 {
		t.Errorf("in the settings registry but never read: %v", extra)
	}
	for n := range knobDocs {
		if !read[n] {
			t.Errorf("knobDocs has %s, which is not a knob", n)
		}
	}
}

// docs/advanced/settings.json is exactly what `simple-host settings --json`
// prints. Refresh it with scripts/sync-settings.sh.
func TestSettingsJSONMatchesDocs(t *testing.T) {
	want, err := SettingsJSON()
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile("../../docs/advanced/settings.json")
	if err != nil {
		t.Fatalf("%v (run scripts/sync-settings.sh)", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatal("docs/advanced/settings.json differs from the settings in code: run scripts/sync-settings.sh")
	}
}
