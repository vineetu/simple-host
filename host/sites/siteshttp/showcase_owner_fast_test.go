package siteshttp

import (
	"strings"
	"testing"

	"golang.org/x/net/html"
)

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
