package gate

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	accounts "github.com/vsriram/simple-host/host/access"
	"github.com/vsriram/simple-host/host/config"
)

// GET /v1/auth/oauth/providers: oidc carries its label only when on, and
// email_enabled needs both EMAIL_SIGNIN and a mail key.
func TestListProvidersOIDC(t *testing.T) {
	list := func(withOIDC bool, resendKey, emailSignIn string) map[string]any {
		providers := map[string]Provider{"google": fakeProvider{}}
		if withOIDC {
			providers["oidc"] = ssoOIDCProvider{}
		}
		cfg := config.Config{ResendAPIKey: resendKey, EmailSignIn: emailSignIn}
		h := &OAuthHandler{Cfg: testOAuthSettings(cfg, accounts.New(nil)), SignIn: NewProviderSignIn(accounts.New(nil), providers)}
		h.Cfg.OIDCLabel = "Acme Okta"
		rr := httptest.NewRecorder()
		h.listProviders(rr, httptest.NewRequest("GET", "/v1/auth/oauth/providers", nil))
		var body map[string]any
		if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		return body
	}
	off := list(false, "resend-key", "on")
	if _, has := off["oidc_label"]; has || off["email_enabled"] != true {
		t.Fatalf("oidc off: %v", off)
	}
	if on := list(true, "resend-key", "off"); on["oidc_label"] != "Acme Okta" || on["email_enabled"] != false {
		t.Fatalf("oidc on, EMAIL_SIGNIN=off: %v", on)
	}
	if nokey := list(false, "", "on"); nokey["email_enabled"] != false {
		t.Fatalf("no mail key: %v", nokey)
	}
}
