package servertest

import (
	"bytes"
	"log"
	"net/http"
	"os"
	"strings"
	"testing"
)

// A saved key never leaves again: not in the list, the connector's three
// tools, the site export, the account export or the program's logs; and
// request_secret answers the write-only page's link.
func TestJourneySecretIsNeverShown(t *testing.T) {
	const value = "rk_live_journey_0123456789abcdefFEDCBA"
	logs := &bytes.Buffer{}
	log.SetOutput(logs)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	a, dir := NewSiteApp(t, "canonical")
	olive := a.NewPerson(t, "secret-olive")
	key := map[string]string{"X-API-Key": olive.Key}
	token := a.Connect(t, olive, a.RegisterClient(t, TestRedirect), TestRedirect)["access_token"].(string)
	call := func(name string, args map[string]any) string {
		t.Helper()
		text, out, bad := ToolResultOf(t, a.Rpc(t, token, "tools/call", map[string]any{"name": name, "arguments": args}))
		if bad {
			t.Fatalf("%s: %s", name, text)
		}
		return text + toJSON(out)
	}
	call("create_site", map[string]any{"site": "shop", "files": map[string]any{"index.html": "<h1>shop</h1>"}})
	if r := a.At(t, "PUT", PcSiteDomain, "/v1/sites/shop/secrets/resend", map[string]string{"value": value, "destination": "api.resend.com", "who": "owner", "attach": "query", "attach_name": "key"}, key); r.Status != http.StatusOK {
		t.Fatalf("save: %d %s", r.Status, r.Body)
	}
	seen := map[string]string{
		"list_secrets":   call("list_secrets", map[string]any{"site": "shop"}),
		"request_secret": call("request_secret", map[string]any{"site": "shop", "name": "resend", "destination": "api.resend.com", "attach": "query", "attach_name": "key"}),
	}
	if !strings.Contains(seen["list_secrets"], "api.resend.com") || !strings.Contains(seen["request_secret"], "/secrets?attach=query&attach_name=key&destination=api.resend.com&name=resend&site=shop") {
		t.Fatalf("tools: %v", seen)
	}
	for name, path := range map[string]string{"site": "/v1/sites/shop/export.tar.gz", "account": "/v1/me/export.zip"} {
		r := a.At(t, "GET", PcSiteDomain, path, nil, key)
		if r.Status != http.StatusOK {
			t.Fatalf("%s export: %d", name, r.Status)
		}
		files := ZipFiles
		if name == "site" {
			files = rdTarFiles
		}
		var all strings.Builder
		for _, body := range files(t, r.Body) {
			all.WriteString(body)
		}
		seen[name+" export"] = all.String()
	}
	// The site's own pages reach the key through Access's storage caller and
	// Data's gate: an owner-only key refuses a visitor who is not signed in,
	// and a browser call without X-SH-CSRF.
	_, handle := a.UserID(t, olive)
	MarkReady(t, dir, handle)
	host := "shop." + handle + "." + PcSiteDomain
	page := map[string]string{"Origin": "https://" + host, "Sec-Fetch-Site": "same-origin"}
	if r := a.At(t, "POST", host, "/v1/sites/shop/secrets/resend/call/emails", "{}", page); r.Status != http.StatusForbidden || !strings.Contains(string(r.Body), "csrf_required") {
		t.Fatalf("call without CSRF: %d %s", r.Status, r.Body)
	}
	page["X-SH-CSRF"] = "1"
	if r := a.At(t, "POST", host, "/v1/sites/shop/secrets/resend/call/emails", "{}", page); r.Status != http.StatusUnauthorized {
		t.Fatalf("anonymous call: %d %s", r.Status, r.Body)
	}
	seen["delete_secret"] = call("delete_secret", map[string]any{"site": "shop", "name": "resend"})
	seen["logs"] = logs.String()
	for where, text := range seen {
		if strings.Contains(text, value) || strings.Contains(text, value[:16]) || strings.Contains(text, value[len(value)-16:]) {
			t.Errorf("%s shows the key", where)
		}
	}
}
