package handler

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/html"
)

func TestShowcaseStorageSwitch(t *testing.T) {
	h := chromeTestHandler(platformTestEdition())
	data := showcaseData{Handle: "owner", MainURL: "https://simple-host.app"}
	for _, at := range []time.Time{{}, time.Date(2026, 12, 17, 17, 0, 0, 0, time.UTC)} {
		page, err := h.pipeline().showcasePage(ChromeData{}, data, at)
		if err != nil {
			t.Fatal(err)
		}
		want := `"storageSwitchSet":false`
		if !at.IsZero() {
			want = `"storageSwitchSet":true`
			if !strings.Contains(string(page), `"storageSwitchAt":"2026-12-17T17:00:00Z"`) {
				t.Fatal("dashboard missing the configured switch instant")
			}
		} else if strings.Contains(string(page), `"storageSwitchAt":`) {
			t.Fatal("dashboard has a switch instant when no date is set")
		}
		if !strings.Contains(string(page), want) {
			t.Fatalf("dashboard missing %s", want)
		}
	}
	feed, err := json.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(feed), "storageSwitch") {
		t.Fatal("dashboard notice setting leaked into the public showcase feed")
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
	for _, id := range []string{"skill-install", "build-with-ai", "connect-ai"} {
		assertOwnerPanel(t, nodes, id, "owner-tab-sites")
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
