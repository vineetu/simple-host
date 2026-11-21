package accesshttp

import (
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vsriram/simple-host/host/sites"
)

// The visitor sign-in pages escape the typed email, offer no form on the
// shared host, and the gate pages keep a CSP with no inline script but the
// one hashed helper.
func TestViewerAndPasscodeGateHTML(t *testing.T) {
	gate := &AccessHandler{ViewerGoogle: true, Hosts: &sites.Hosts{}}
	req := httptest.NewRequest(http.MethodGet, "https://scale.sam.example/days/", nil)
	req.Host = "scale.sam.example"
	req.Header.Set("X-Forwarded-Proto", "https")
	code := gate.viewerPageHTML(req, viewerPageCode, "/days/", `a<b>@gmail.com`, "")
	if strings.Contains(code, `a<b>@gmail.com`) || !strings.Contains(code, "a&lt;b&gt;@gmail.com") {
		t.Error("code page leaked the raw email")
	}
	if closed := gate.viewerPageHTML(req, viewerPageSignIn, "", "", ""); strings.Contains(closed, "<form") {
		t.Error("shared host still offers a form")
	}
	sum := sha256.Sum256([]byte(gateAssistJS))
	hash := "'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'"
	for name, csp := range map[string]string{"hack": passcodeGateCSP, "host": hostPasscodeGateCSP} {
		if strings.Contains(csp, "unsafe-inline") || !strings.Contains(csp, hash) || !strings.Contains(csp, "frame-ancestors 'none'") || !strings.Contains(csp, "form-action 'self'") {
			t.Fatalf("%s csp: %s", name, csp)
		}
	}
}
