package handler

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestServedSigninJSContainsMessageTable(t *testing.T) {
	SetInstanceHosts("simple-host.app", "", "")
	defer SetInstanceHosts("simple-host.app", "", "")
	raw, err := embeddedStatic.ReadFile("static/signin.js")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(raw, []byte(markerSigninMessages)) {
		t.Fatal("embedded signin.js is missing the message marker")
	}
	got, err := staticFiles.ReadFile("static/signin.js")
	if err != nil {
		t.Fatal(err)
	}
	js, err := json.Marshal(signInMessages)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(got, js) {
		t.Fatal("served signin.js does not contain the JSON message table")
	}
	if bytes.Contains(got, []byte(markerSigninMessages)) {
		t.Fatal("served signin.js still has the empty marker object")
	}
	if !bytes.Contains(got, []byte("window.SHSignIn")) {
		t.Fatal("served signin.js is missing SHSignIn")
	}
}

func TestServedAuthJSContainsSigninCard(t *testing.T) {
	SetInstanceHosts("simple-host.app", "", "")
	defer SetInstanceHosts("simple-host.app", "", "")
	raw, err := embeddedStatic.ReadFile("static/auth.js")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(raw, []byte(markerSigninCard)) {
		t.Fatal("embedded auth.js is missing the card marker")
	}
	if !bytes.Contains(raw, []byte(authJSOneLabel)) {
		t.Fatal("embedded auth.js is missing the person-host line")
	}
	got, err := staticFiles.ReadFile("static/auth.js")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(got, []byte(markerSigninCard)) {
		t.Fatal("served auth.js still has the card marker")
	}
	if !bytes.Contains(got, []byte("window.SHSignIn")) {
		t.Fatal("served auth.js is missing the sign-in card")
	}
	if !bytes.Contains(got, []byte(authJSOneLabel)) {
		t.Fatal("served auth.js dropped the person-host line")
	}
	js, err := json.Marshal(signInMessages)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(got, js) {
		t.Fatal("served auth.js is missing the injected message table")
	}
}

func TestSigninJSVersionHashesServedBytes(t *testing.T) {
	sum := sha256.Sum256(servedSigninJS())
	want := hex.EncodeToString(sum[:])[:10]
	if got := SigninJSVersion(); got != want {
		t.Fatalf("SigninJSVersion=%q want %q", got, want)
	}
	if len(want) != 10 {
		t.Fatalf("version length %d", len(want))
	}
	req := httptest.NewRequest("GET", "/", nil)
	req.Host = "simple-host.app"
	d := chromeDataFor(req, "")
	if d.SigninVersion != want {
		t.Fatalf("chrome SigninVersion=%q want %q", d.SigninVersion, want)
	}
}

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

func TestHackModeAuthJSStillHasCard(t *testing.T) {
	previous := hackMode
	hackMode = true
	baseTextCache.Clear()
	t.Cleanup(func() {
		hackMode = previous
		baseTextCache.Clear()
	})
	got, err := staticFiles.ReadFile("static/auth.js")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(got, []byte("window.SHSignIn")) {
		t.Fatal("hack-mode auth.js is missing the sign-in card")
	}
	if !bytes.Contains(got, []byte("Simple Hack visitor sign-in")) {
		t.Fatal("hack-mode auth.js was not cut by hackAuthJS")
	}
	if !bytes.Contains(got, []byte(`var signInBy = "Sign-in by Simple Hack";`)) || bytes.Contains(got, []byte(`var signInBy = "Sign-in by Simple Host";`)) {
		t.Fatal("hack-mode auth.js kept the Host visitor wording")
	}
	if !bytes.Contains(got, []byte(`var signInProduct = "Simple Hack";`)) {
		t.Fatal("hack-mode auth.js kept the Host product name")
	}
}
