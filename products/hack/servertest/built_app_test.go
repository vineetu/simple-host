package servertest

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	accountapp "github.com/vsriram/simple-host/host/access"
	appstorage "github.com/vsriram/simple-host/host/data"
	"github.com/vsriram/simple-host/host/extension"
	"github.com/vsriram/simple-host/host/model"
	platformtest "github.com/vsriram/simple-host/host/server/servertest"
	"github.com/vsriram/simple-host/host/sites"
	"github.com/vsriram/simple-host/products/hack"
	"github.com/vsriram/simple-host/products/hack/testdb"
)

// builtHackApp runs the real Build wiring; wire, when set, sees its Parts.
func builtHackApp(t *testing.T, wire func(extension.Deps, *extension.Parts)) *teamSiteApp {
	t.Helper()
	cfg := platformtest.Config(t, testdb.Fresh(t))
	cfg.SiteDomain = tsDomain
	cfg.ContentHost = "sites." + tsDomain
	cfg.PublicBaseURL = "https://" + tsDomain
	cfg.PersonHosts = "canonical"
	cfg.SiteHosts = "canonical"
	cfg.SiteCertDir = ""
	a := &teamSiteApp{admin: cfg.AdminAPIKey}
	ext := hack.Extension()
	inner := ext.Wire
	ext.Wire = func(d extension.Deps) (extension.Parts, error) {
		parts, err := inner(d)
		a.database, a.sites, a.data, a.conn, a.disk = d.DB, d.Site, d.Data, d.Connector, d.Disk
		if wire != nil {
			wire(d, &parts)
		}
		return parts, err
	}
	t.Cleanup(func() { accountapp.SetGroupHooks(accountapp.GroupHooks{}) })
	app := platformtest.Build(t, cfg, []*extension.Extension{ext})
	a.mux = app.Mux
	a.srv = httptest.NewServer(app.Handler)
	t.Cleanup(a.srv.Close)
	return a
}

type countedGate struct {
	sites.GroupGate
	calls *int
}

func (g countedGate) Check(ctx context.Context, tx *sql.Tx, user *model.User, name string) *sites.GateError {
	*g.calls++
	return g.GroupGate.Check(ctx, tx, user, name)
}

type countedStorageBindings struct {
	appstorage.BindingProvider
	calls int
}

func (p *countedStorageBindings) OwnerOnSite(ctx context.Context, site, user string) (bool, error) {
	p.calls++
	return p.BindingProvider.OwnerOnSite(ctx, site, user)
}

// The real program built as Simple Hack installs the product's hooks: the
// site gate on REST and MCP publishing, event and team hosts, storage
// bindings for a member signed in on the team site, account erasure, and
// the product's tools on a person's own connection. The hand-composed
// teamSiteApp the other tests use cannot prove this wiring.
func TestBuiltHackAppWiring(t *testing.T) {
	gateCalls := 0
	var bindings *countedStorageBindings
	a := builtHackApp(t, func(_ extension.Deps, parts *extension.Parts) {
		if parts.Hooks.GroupGate == nil || parts.Hooks.GroupHosts == nil || parts.Hooks.Storage == nil || parts.Hooks.Sitemap == nil {
			t.Fatal("Hack omitted a platform hook")
		}
		parts.Hooks.GroupGate = countedGate{parts.Hooks.GroupGate, &gateCalls}
		bindings = &countedStorageBindings{BindingProvider: parts.Hooks.Storage}
		parts.Hooks.Storage = bindings
	})
	org, member, outsider := a.newPerson(t, "wired-org"), a.newPerson(t, "wired-member"), a.newPerson(t, "wired-outsider")
	slug := a.makeEvent(t, org)
	a.join(t, slug, member, org)
	team, _ := a.startTeam(t, slug, "Wired", member)

	// Site gate: REST and MCP publishing both reach it.
	wantTS(t, "REST publish", a.deployTeam(t, slug, team, member.key, "wired"), 201, "")
	if gateCalls == 0 {
		t.Fatal("REST publish did not reach the product's site gate")
	}
	before := gateCalls
	mcp := func(key, name string, args map[string]any) (string, map[string]any, bool) {
		t.Helper()
		return toolResultOf(t, a.do(t, http.MethodPost, "/mcp", jsonBody(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": name, "arguments": args}}),
			map[string]string{"Content-Type": "application/json", "X-API-Key": key, "MCP-Protocol-Version": "2025-06-18"}))
	}
	if text, _, failed := mcp(member.key, "update_site", map[string]any{"site": team, "owner": slug, "files": map[string]any{"index.html": "<h1>via mcp</h1>"}}); failed || gateCalls == before {
		t.Fatalf("MCP publish: failed=%v gate calls=%d: %s", failed, gateCalls-before, text)
	}
	if text, _, failed := mcp(outsider.key, "update_site", map[string]any{"site": team, "owner": slug, "files": map[string]any{"index.html": "x"}}); !failed {
		t.Fatalf("outsider MCP publish: %s", text)
	}

	// Event and team hosts.
	if r := a.on(t, "GET", team+"."+slug+"."+tsDomain, "/", nil, ""); r.status != 200 || !strings.Contains(string(r.body), "via mcp") {
		t.Fatalf("team host: %d %s", r.status, r.body)
	}
	if r := a.on(t, "GET", slug+"."+tsDomain, "/", nil, ""); r.status != 200 {
		t.Fatalf("event host: %d", r.status)
	}

	// Storage bindings: a member signed in on the team site is the owner.
	base := "/v1/sites/" + team + "/storage"
	wantTS(t, "private resource", a.api(t, "PUT", base+"/resources/private?owner="+slug, map[string]any{"kind": "kv", "rules": json.RawMessage(privateRules)}, member.key), 201, "")
	wantTS(t, "private value", a.api(t, "PUT", base+"/kv/private/keys/k?owner="+slug, `{"value":"secret"}`, member.key), 200, "")
	sid, host := hackSiteID(t, a, slug, team), team+"."+slug+"."+tsDomain
	wantTS(t, "member on site", hackSession(t, a, a.uid(t, member), sid, host)("GET", base+"/kv/private/keys/k", nil), 200, "")
	wantTS(t, "outsider on site", hackSession(t, a, a.uid(t, outsider), sid, host)("GET", base+"/kv/private/keys/k", nil), 403, "")
	if bindings.calls == 0 {
		t.Fatal("storage never asked the product's bindings")
	}

	// Tools: the product's own on a person's connection, next to the website tools.
	names := mcpToolNames(t, a.mcpWithKey(t, member.key))
	if !hasTool(names, "hack_list_events") || !hasTool(names, "create_site") {
		t.Fatalf("personal connection tools: %v", names)
	}
	if text, out, failed := mcp(member.key, "hack_get_my_teams", map[string]any{}); failed || len(out["teams"].([]any)) != 1 {
		t.Fatalf("hack_get_my_teams: %s", text)
	}

	// Secrets: the write-only page is on Simple Hack's own apex, and the
	// connector's link points there for the team's site.
	if r := a.on(t, "GET", tsDomain, "/secrets?site="+team+"&owner="+slug, nil, ""); r.status != 200 || !strings.Contains(string(r.body), "Who may make your site use this key") || strings.Contains(string(r.body), "simple-host.app") {
		t.Fatalf("secrets page on the Hack apex: %d", r.status)
	}
	if text, out, failed := mcp(member.key, "request_secret", map[string]any{"site": team, "owner": slug, "name": "resend", "destination": "api.resend.com", "attach": "header", "attach_name": "Authorization", "attach_prefix": "Bearer "}); failed || !strings.HasPrefix(out["url"].(string), "https://"+tsDomain+"/secrets?") {
		t.Fatalf("request_secret: %s", text)
	}
	if text, _, failed := mcp(outsider.key, "list_secrets", map[string]any{"site": team, "owner": slug}); !failed {
		t.Fatalf("outsider list_secrets: %s", text)
	}
	// A team site's page uses an owner-only key with the platform's rule: a
	// member signed in on the team site is its owner, an outsider is not. The
	// destination does not resolve, so a call let through ends at dns_error.
	wantTS(t, "team key", a.api(t, "PUT", "/v1/sites/"+team+"/secrets/ai?owner="+slug, map[string]string{"value": "AIza-team-key-0123456789", "destination": "api.sec1-test-nowhere", "who": "owner", "attach": "header", "attach_name": "x-goog-api-key"}, member.key), 200, "")
	call := "/v1/sites/" + team + "/secrets/ai/call/v1beta/models"
	if r := hackSession(t, a, a.uid(t, member), sid, host)("POST", call, "{}"); r.status != http.StatusBadGateway || !strings.Contains(string(r.body), "dns_error") || strings.Contains(string(r.body), "team-key") {
		t.Fatalf("member's call: %d %s", r.status, r.body)
	}
	wantTS(t, "outsider's call", hackSession(t, a, a.uid(t, outsider), sid, host)("POST", call, "{}"), 403, "forbidden")
	wantTS(t, "anonymous call", hackSession(t, a, "", sid, host)("POST", call, "{}"), 401, "sign_in_required")

	// Account erasure: the event's holding account and an organiser of a
	// running event are refused; a participant's own erasure goes ahead.
	var holder string
	if err := a.database.QueryRow(`SELECT account_id FROM events WHERE slug=$1`, slug).Scan(&holder); err != nil {
		t.Fatal(err)
	}
	wantTS(t, "admin deletes holder", a.api(t, "DELETE", "/v1/admin/users/"+holder, nil, a.admin), 409, "event_account")
	wantTS(t, "admin suspends holder", a.api(t, "POST", "/v1/admin/users/"+holder+"/suspend", map[string]string{"reason": "x"}, a.admin), 409, "event_account")
	wantTS(t, "organiser deletes self", a.api(t, "DELETE", "/v1/me", map[string]string{"confirm": strings.Split(org.email, "@")[0]}, org.key), 409, "organises_events")
	wantTS(t, "admin deletes organiser", a.api(t, "DELETE", "/v1/admin/users/"+a.uid(t, org), nil, a.admin), 409, "organises_events")
}
