package handler

import (
	"strings"
	"testing"

	"golang.org/x/net/html"
)

// TestShowcaseOwnerFastPath pins the owner dashboard's load shape: the public
// gallery is hidden before first paint for the handle this browser signed in
// as, the page asks /v1/me and /v1/sites once each, and the owner's own page
// never downloads every account's sites (?all=1). Browser proof:
// scripts/e2e-owner-dashboard.mjs.
func TestShowcaseOwnerFastPath(t *testing.T) {
	raw, err := staticFiles.ReadFile("static/showcase.html")
	if err != nil {
		t.Fatal(err)
	}
	page := string(raw)
	head := page[:strings.Index(page, "</head>")]
	for _, want := range []string{"localStorage.getItem('sh-owner-handle') === '__SH_HANDLE__'", "classList.add('sh-owner-pending')", ".sh-owner-pending #public-view { display: none; }"} {
		if !strings.Contains(head, want) {
			t.Errorf("head lacks %q", want)
		}
	}
	if n := strings.Count(page, "api('GET', '/v1/me')"); n != 3 {
		// bootMe, the focus re-check, and the sign-in block's later reloads.
		t.Errorf("/v1/me requested from %d places, want 3", n)
	}
	if n := strings.Count(page, "api('GET', '/v1/sites')"); n != 2 {
		// ownSites and reloadOwnerSites after a rename or delete.
		t.Errorf("/v1/sites requested from %d places, want 2", n)
	}
	if strings.Contains(page, "me.is_admin ? '?all=1'") {
		t.Error("the owner's own page still asks for every account's sites")
	}
	header, err := staticFiles.ReadFile("static/partials/header.html")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(header), "['apiKey', 'sh-email-nonce', 'sh-owner-handle']") {
		t.Error("sign-out does not forget sh-owner-handle")
	}
}

// Check parsed ancestry, not just source order: a section outside its panel
// would recreate the Analytics bug even if all three tab labels were present.
func ownerPageElements(t *testing.T, source string) map[string]*html.Node {
	t.Helper()
	root, err := html.Parse(strings.NewReader(source))
	if err != nil {
		t.Fatal(err)
	}
	nodes := map[string]*html.Node{}
	var visit func(*html.Node)
	visit = func(n *html.Node) {
		if id := ownerElementAttr(n, "id"); id != "" {
			nodes[id] = n
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			visit(c)
		}
	}
	visit(root)
	return nodes
}

func ownerElementAttr(n *html.Node, key string) string {
	if n != nil {
		for _, a := range n.Attr {
			if a.Key == key {
				return a.Val
			}
		}
	}
	return ""
}

func assertOwnerPanel(t *testing.T, nodes map[string]*html.Node, id, panel string) {
	t.Helper()
	n := nodes[id]
	if n == nil {
		t.Fatalf("missing owner section %s", id)
	}
	for p := n.Parent; p != nil; p = p.Parent {
		if ownerElementAttr(p, "role") == "tabpanel" {
			if got := ownerElementAttr(p, "id"); got != panel {
				t.Errorf("%s belongs to %s, want %s", id, got, panel)
			}
			return
		}
	}
	t.Errorf("%s is outside its tab panel", id)
}

func TestShowcaseOwnerThreeTabs(t *testing.T) {
	raw, err := staticFiles.ReadFile("static/showcase.html")
	if err != nil {
		t.Fatal(err)
	}
	page := string(raw)
	nodes := ownerPageElements(t, page)
	sections := map[string][]string{
		"sites":     {"sites-view", "deleted"},
		"analytics": {"analytics-view"},
		"settings":  {"address", "showcase-curation", "signin", "password", "connections", "keys", "notifications", "families", "data", "delete-account"},
	}
	for tab, ids := range sections {
		panel := "owner-tab-" + tab
		if ownerElementAttr(nodes[panel], "role") != "tabpanel" || ownerElementAttr(nodes[panel], "aria-labelledby") != "tab-"+tab {
			t.Errorf("%s lacks its panel role or label", panel)
		}
		button := nodes["tab-"+tab]
		selected := "false"
		if tab == "sites" {
			selected = "true"
		}
		if ownerElementAttr(button, "role") != "tab" || ownerElementAttr(button, "aria-controls") != panel || ownerElementAttr(button, "aria-selected") != selected {
			t.Errorf("%s lacks its tab role, controls, or initial selection", tab)
		}
		last := -1
		for _, id := range ids {
			assertOwnerPanel(t, nodes, "owner-"+id, panel)
			pos := strings.Index(page, `id="owner-`+id+`"`)
			if pos <= last {
				t.Errorf("%s is out of order in %s", id, tab)
			}
			last = pos
		}
	}
	for _, want := range []string{
		`<h2>Your sites</h2>`,
		`No sites yet. Visits show here once a site is live.`,
		`No apps connected yet. <a href="/install.html">Connect your AI app</a>`,
		`window.addEventListener('hashchange', routeOwnerHash)`,
		`history.replaceState(history.state, '', '#' + which)`,
		`url.searchParams.get('site') && url.searchParams.get('open')`,
		`if (passcodeSite) switchOwnerTab('sites')`,
		`section.scrollIntoView({ block: 'start' })`,
		`ownerSectionObserver.observe(panel`,
		`'keys': 'owner-keys'`, `'connected-apps': 'owner-connections'`,
		`'notifications': 'owner-notifications'`, `'recently-deleted': 'owner-deleted'`,
		`'address': 'owner-address'`, `'showcase': 'owner-showcase-curation'`,
		`'sign-in': 'owner-signin'`, `'password': 'owner-password'`,
		`'address-families': 'owner-families'`, `'your-data': 'owner-data'`,
		`'delete-account': 'owner-delete-account'`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("owner page lacks %q", want)
		}
	}
	if strings.Contains(page, `<h2>Analytics</h2>`) || strings.Contains(page, "Site Inventory") || strings.Contains(page, "box.hidden = conns.length === 0") {
		t.Error("old heading or empty-connections visibility rule remains")
	}
}

func TestOwnerSignInAlertSettingsLink(t *testing.T) {
	alerts := &SignInAlerts{publicBaseURL: "https://example.test"}
	for handle, want := range map[string]string{
		"owner": "https://example.test/owner#settings",
		"":      "https://example.test/dashboard",
	} {
		if got := alerts.ownerAppURL(handle); got != want {
			t.Errorf("ownerAppURL(%q) = %q, want %q", handle, got, want)
		}
	}
}
