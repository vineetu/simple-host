package servertest

import (
	"strings"
	"testing"
)

// A private site created through the connector is gated from its very first
// served request: the anonymous visitor gets the sign-in answer, never the page.
func TestRsCreateSpecificMCP(t *testing.T) {
	p := NewPasscodeApp(t)
	token := p.Connect(t, p.Olive, p.RegisterClient(t, TestRedirect), TestRedirect)["access_token"].(string)
	r := p.Rpc(t, token, "tools/call", map[string]any{"name": "create_site", "arguments": map[string]any{
		"site": "mcpprivate", "files": map[string]string{"index.html": PasscodeCanary}, "access": "specific"}})
	if _, _, bad := ToolResultOf(t, r); bad {
		t.Fatalf("create_site: %s", r.Body)
	}
	if !p.Sites.Disk.HasPasscodeMarker(rsUID(t, p), "mcpprivate") {
		t.Fatal("private site created without its marker")
	}
	for _, req := range []struct{ host, path string }{{"mcpprivate." + p.Person, "/"}, {p.Person, "/mcpprivate/"}} {
		if r := p.At(t, "GET", req.host, req.path, nil, nil); r.Status != 401 && r.Status/100 != 3 || strings.Contains(string(r.Body), PasscodeCanary) {
			t.Fatalf("first anonymous request %s%s: %d %s", req.host, req.path, r.Status, r.Body)
		}
	}
}
