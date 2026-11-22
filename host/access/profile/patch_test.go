package profile

import (
	"strings"
	"testing"

	accounts "github.com/vsriram/simple-host/host/access"
)

func TestProfileValidation(t *testing.T) {
	for _, handle := range []string{"admin", "by-id", "UPPER", "a/b", "", strings.Repeat("a", 40)} {
		if accounts.CheckNewHandle(handle) == nil {
			t.Fatalf("accepted %q", handle)
		}
	}
	if err := accounts.CheckNewHandle("team-01"); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"   ", " Alice ", strings.Repeat("界", 100)} {
		got, err := normalizeDisplayName(name)
		if err != nil || got != strings.TrimSpace(name) {
			t.Fatal(got, err)
		}
	}
	if _, err := normalizeDisplayName(strings.Repeat("界", 101)); err == nil {
		t.Fatal("accepted long name")
	}
}
