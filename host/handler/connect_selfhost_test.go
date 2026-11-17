package handler

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// TestConnectDataNamesSelfHostedInstall is SIM connect-mcp rule 4: a
// self-hosted install's OAuth consent page (connect.html) and its sign-in
// step must name this install's own host. newConnectorApp's fixture runs
// with SiteDomain "simple-host.test" (not simple-host.app), which is
// exactly a self-hosted instance from hostedOwner()'s point of view, so the
// authorize() handler should mark the connect-data payload self_hosted and
// give its own request Host -- connect.html's script turns those into the
// "Sign in to connect — <host>" title and the "on <host>" line under Allow.
// Needs DB_DSN (newConnectorApp); run via scripts/test-integration.sh.
func TestConnectDataNamesSelfHostedInstall(t *testing.T) {
	// instanceIsHostedService is a package-level global several other tests
	// in this package toggle (chrome_test.go, owner_line_test.go,
	// password_test.go), each with its own save/restore. Pin it here too so
	// this test's hostedOwner()-derived assertion does not depend on test
	// execution order.
	prevHosted := instanceIsHostedService
	instanceIsHostedService = false
	t.Cleanup(func() { instanceIsHostedService = prevHosted })

	app := newConnectorApp(t)
	_, challenge := newVerifier()
	clientID := app.registerClient(t, "https://app.example.com/callback")
	q := authorizeQuery(clientID, "https://app.example.com/callback", challenge)
	page := app.do(t, http.MethodGet, "/oauth/authorize?"+q.Encode(), nil, nil)
	if page.status != http.StatusOK {
		t.Fatalf("authorize page: %d %s", page.status, page.body)
	}
	m := connectDataRe.FindSubmatch(page.body)
	if m == nil {
		t.Fatal("consent page carries no request data")
	}
	var data map[string]any
	if err := json.Unmarshal(m[1], &data); err != nil {
		t.Fatalf("connect-data not JSON: %v", err)
	}
	if data["self_hosted"] != true {
		t.Errorf("self_hosted = %v, want true (SiteDomain is simple-host.test, not simple-host.app)", data["self_hosted"])
	}
	host, _ := data["host"].(string)
	if host == "" || strings.Contains(host, "simple-host.app") || strings.Contains(host, "simple-hack.app") {
		t.Errorf("host = %q, want this test server's own host", host)
	}
	srvHost, err := url.Parse(app.srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if host != srvHost.Host {
		t.Errorf("host = %q, want %q (the request's own Host header)", host, srvHost.Host)
	}
	// The page itself (not just the JSON) never names simple-host.app or
	// simple-hack.app either: connect.html is otherwise brand-only
	// (productName()), so nothing here should leak the hosted domains.
	for _, bad := range []string{"simple-host.app", "simple-hack.app"} {
		if strings.Contains(string(page.body), bad) {
			t.Errorf("self-hosted /oauth/authorize page mentions %q", bad)
		}
	}
}
