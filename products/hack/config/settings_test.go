package config

import (
	"bytes"
	"os"
	"testing"

	platform "github.com/vsriram/simple-host/host/config"
)

func TestSettingsJSONMatchesDocs(t *testing.T) {
	want, err := platform.SettingsJSON(Settings()...)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"../docs/advanced/settings.json", "../webassets/static/setup/small-box-settings.json"} {
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(want, got) {
			t.Fatalf("%s differs: run scripts/sync-settings.sh", path)
		}
	}
}
