package accesshttp

import (
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vsriram/simple-host/host/sites"
	"github.com/vsriram/simple-host/host/web"
)

func TestViewerAndPasscodeGateHTML(t *testing.T) {
	gate := &AccessHandler{ViewerGoogle: true, Hosts: &sites.Hosts{}}
	req := httptest.NewRequest(http.MethodGet, "https://scale.sam.example/days/", nil)
	req.Host = "Scale.Sam.Example"
	req.Header.Set("X-Forwarded-Proto", "https")
	start := gate.viewerPageHTML(req, viewerPageSignIn, "/days/", "", web.SignInMessages["rate_limited"])
	for _, want := range []string{
		">Continue</button>",
		"This site is private",
		"Only people the owner named can open it. Sign in with the email they added.",
		"We email you a 6-digit code. No password, no account to make.",
		`name="email"`,
		web.SignInMessages["rate_limited"],
		`class="bad"`,
		`href="/v1/visitor/oauth/google?return_to=https%3A%2F%2Fscale.sam.example%2Fdays%2F"`,
		"<body><main class=\"gate\">",
	} {
		if !strings.Contains(start, want) {
			t.Errorf("start page missing %q", want)
		}
	}
	if strings.Contains(start, gateAssistJS) || strings.Contains(start, "<body ") {
		t.Error("start page has the code script or a body attribute")
	}
	android := httptest.NewRequest(http.MethodGet, "https://scale.sam.example/days/", nil)
	android.Host = "scale.sam.example"
	android.Header.Set("User-Agent", "Mozilla/5.0 (Linux; Android 14; Pixel)")
	code := gate.viewerPageHTML(android, viewerPageCode, "/days/", `a<b>@gmail.com`, "")
	for _, want := range []string{
		"Check your email",
		"We sent a 6-digit code to <b>a&lt;b&gt;@gmail.com</b>",
		">Change</button>",
		`name="code"`,
		`autocomplete="one-time-code"`,
		`inputmode="numeric"`,
		">Sign in</button>",
		`data-sh-resend`,
		">Send a new code</button>",
		">Open Gmail</a>",
		"intent://#Intent;scheme=googlegmail",
		`data-sh-sent="1"`,
		`target="_blank"`,
		`rel="noopener noreferrer"`,
		gateAssistJS,
	} {
		if !strings.Contains(code, want) {
			t.Errorf("code page missing %q", want)
		}
	}
	if strings.Contains(code, `a<b>@gmail.com`) || strings.Contains(code, ">Use another email</button>") {
		t.Error("code page leaked the raw email or kept Use another email")
	}
	page := gate.viewerPageHTML(req, viewerPageCode, "/days/", "Sam@GoogleMail.com", "That code is not right")
	if !strings.Contains(page, `href="https://mail.google.com/mail/"`) || strings.Contains(page, "intent://") || !strings.Contains(page, `data-sh-sent="0"`) || !strings.Contains(page, "That code is not right") {
		t.Fatal("gmail web link or error state")
	}
	if other := gate.viewerPageHTML(req, viewerPageCode, "/days/", "sam@example.com", ""); strings.Contains(other, "Open Gmail") {
		t.Error("Open Gmail offered for a non-gmail address")
	}
	denied := gate.viewerPageHTML(req, viewerPagePrivate, "/days/", "sam@example.com", "")
	for _, want := range []string{
		"You do not have access",
		"Signed in as <b>sam@example.com</b>",
		`class="av">S</span>`,
		">Use another email</button>",
		`value="out"`,
		">Sign out</button>",
		"This address is not on the owner's list. Ask them to add it, or sign in with another email.",
	} {
		if !strings.Contains(denied, want) {
			t.Errorf("denied page missing %q", want)
		}
	}
	closed := gate.viewerPageHTML(req, viewerPageSignIn, "", "", "")
	if !strings.Contains(closed, "Open it at its own address to sign in.") || strings.Contains(closed, "<form") {
		t.Fatal("shared host still offers a form")
	}
	pass := gate.passcodeGateHTML("menu.sam.example", "/days/", "")
	if !strings.Contains(pass, "Enter the passcode") || !strings.Contains(pass, ">Open site</button>") || !strings.Contains(pass, "menu.sam.example") || strings.Contains(pass, "<body ") {
		t.Fatal("passcode gate wording")
	}
	wrong := gate.passcodeGateHTML("menu.sam.example", "/", "That passcode is not right")
	if !strings.Contains(wrong, "That passcode is not right") || !strings.Contains(wrong, `class="bad"`) {
		t.Fatal("passcode error")
	}
	sum := sha256.Sum256([]byte(gateAssistJS))
	hash := "'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'"
	for name, csp := range map[string]string{"hack": passcodeGateCSP, "host": hostPasscodeGateCSP} {
		if strings.Contains(csp, "unsafe-inline") || !strings.Contains(csp, hash) || !strings.Contains(csp, "frame-ancestors 'none'") || !strings.Contains(csp, "form-action 'self'") || !strings.Contains(csp, "default-src 'none'") || !strings.Contains(csp, "base-uri 'none'") {
			t.Fatalf("%s csp: %s", name, csp)
		}
	}
	if !strings.Contains(hostPasscodeGateCSP, "font-src data:") {
		t.Fatal("host gate CSP lost the status font")
	}
	if dir := os.Getenv("SH_SHOTS_HTML"); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		shot := httptest.NewRequest(http.MethodGet, "https://scale-plan.vineetu.simple-host.app/", nil)
		shot.Host = "scale-plan.vineetu.simple-host.app"
		shot.Header.Set("X-Forwarded-Proto", "https")
		files := map[string]string{
			"private-gate.html":   gate.viewerPageHTML(shot, viewerPageSignIn, "/", "", ""),
			"private-code.html":   gate.viewerPageHTML(shot, viewerPageCode, "/", "sam@gmail.com", ""),
			"private-denied.html": gate.viewerPageHTML(shot, viewerPagePrivate, "/", "sam@example.com", ""),
			"passcode-gate.html":  gate.passcodeGateHTML("menu.sam.simple-host.app", "/", ""),
		}
		auth, err := web.StaticFiles.ReadFile("static/auth.js")
		if err != nil {
			t.Fatal(err)
		}
		files["auth.js"] = string(auth)
		for name, body := range files {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
}
