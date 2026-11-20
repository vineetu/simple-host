package handler

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestServedSigninAndAuthJSParse(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not installed")
	}
	SetInstanceHosts("simple-host.app", "", "")
	defer SetInstanceHosts("simple-host.app", "", "")
	dir := t.TempDir()
	check := func(name string, body []byte) {
		t.Helper()
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, body, 0o644); err != nil {
			t.Fatal(err)
		}
		out, err := exec.Command("node", "--check", p).CombinedOutput()
		if err != nil {
			t.Fatalf("%s: %v\n%s", name, err, out)
		}
	}
	signin, _ := staticFiles.ReadFile("static/signin.js")
	auth, _ := staticFiles.ReadFile("static/auth.js")
	rawAuth, _ := embeddedStatic.ReadFile("static/auth.js")
	check("signin.js", signin)
	check("auth.js", auth)
	check("auth-raw.js", rawAuth)
}
