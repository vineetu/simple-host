package servertest

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/vsriram/simple-host/host/edition"
)

// newSiteApp is the router with person hosts canonical and site hosts in
// mode, with a certificate hand-off directory in which only the handles in
// ready have a certificate.
func NewSiteApp(t *testing.T, mode string, eds ...*edition.Edition) (*PrivateApp, string) {
	t.Helper()
	a := NewPersonApp(t, "canonical", eds...)
	dir := t.TempDir()
	for _, d := range []string{"requests", "ready"} {
		if err := os.MkdirAll(filepath.Join(dir, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	a.Sites.Hosts.SetSiteHosts(mode, dir)
	return a, dir
}

func MarkReady(t *testing.T, dir, handle string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "ready", handle), nil, 0o644); err != nil {
		t.Fatal(err)
	}
}
