package handler

import (
	"net/http"
	"strings"
	"testing"
)

// The architecture diagrams under static/diagrams are what the GitHub guides
// show at the top of each "run it on X" page (docs/platforms/*.md, and the
// enterprise repo's docs/cloud/aws.md), so simple-host.app is the one copy.
// Each is served as SVG off the file server, and carries a title and a
// description for screen readers.
func TestDiagramsServedAsSVG(t *testing.T) {
	mux := chromeTestMux(t)
	for _, name := range []string{"fly", "render", "upcloud", "enterprise-aws"} {
		path := "/diagrams/" + name + ".svg"
		rec := get(t, mux, "simple-host.app", path)
		if rec.Code != http.StatusOK {
			t.Errorf("%s: status %d, want 200", path, rec.Code)
			continue
		}
		if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "image/svg+xml") {
			t.Errorf("%s: Content-Type %q, want image/svg+xml", path, ct)
		}
		body := rec.Body.String()
		if !strings.HasPrefix(body, "<svg ") || !strings.Contains(body, `viewBox="0 0 800 `) {
			t.Errorf("%s: not an SVG with the 800-wide viewBox", path)
		}
		if !strings.Contains(body, "<title id=\"t\">") || !strings.Contains(body, "<desc id=\"d\">") {
			t.Errorf("%s: missing the accessible <title> or <desc>", path)
		}
	}
}
