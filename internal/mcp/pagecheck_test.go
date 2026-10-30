package mcp

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The page checks name the mistakes small models make that leave a page
// looking fine but saving nothing, and stay quiet on a page that is right.
func TestPageWarnings(t *testing.T) {
	up := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/v1/sites/shop/storage/resources":
			_, _ = w.Write([]byte(`{"resources":[{"name":"bookings","kind":"sqlite"},{"name":"menu","kind":"kv"}]}`))
		case r.URL.Path == "/v1/sites/shop/storage/sqlite/bookings/query":
			_, _ = w.Write([]byte(`{"columns":["name"],"rows":[["bookings"]]}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
	c := &call{server: newTestServer(up), orig: httptest.NewRequest("POST", "/mcp", nil), caller: Caller{APIKey: "k"}}
	head := `<script src="https://simple-host.app/auth.js"></script>`
	cases := []struct {
		name  string
		files map[string]string
		want  []string
	}{
		{"right", map[string]string{"index.html": head + `<script>var b = SH.storage.sqlite("bookings").table("bookings"); SH.storage.kv("menu").get("today");</script>`}, nil},
		{"no auth.js", map[string]string{"index.html": `<script>SH.storage.sqlite("bookings").table("bookings").add({})</script>`}, []string{"never loads auth.js"}},
		{"local script without auth.js", map[string]string{"index.html": `<script src="app.js"></script>`, "app.js": `SH.mount("#x")`}, []string{"never loads auth.js"}},
		{"repeated local script", map[string]string{"index.html": head + strings.Repeat(`<script src="a.js"></script>`, 2000), "a.js": strings.Repeat("x", 400<<10)}, nil},
		{"defer without waiting", map[string]string{"index.html": `<script src="https://simple-host.app/auth.js" defer></script><script>SH.mount("#a")</script>`}, []string{"defer"}},
		{"defer with DOMContentLoaded", map[string]string{"index.html": `<script src="https://simple-host.app/auth.js" defer></script><script>document.addEventListener("DOMContentLoaded", function () { SH.mount("#a") })</script>`}, nil},
		{"double escaped", map[string]string{"index.html": head + `<script>var a = 1;\n    var b = 2;</script>`}, []string{"double-escaped"}},
		{"newline in a string", map[string]string{"index.html": head + "<script>\nvar a = \"x;\\n  y\";\nvar b = 1;\nvar c = 2;\n</script>"}, nil},
		{"auth.js with a query", map[string]string{"index.html": `<script src="https://simple-host.app/auth.js?v=2"></script><script>SH.mount("#a")</script>`}, nil},
		{"json-ld", map[string]string{"index.html": head + `<script type="application/ld+json">{"a":"b;\n  c"}</script>`}, nil},
		{"missing resource", map[string]string{"index.html": head + `<script>SH.storage.sqlite("quotes").table("quotes").add({})</script>`}, []string{`resource "quotes"`}},
		{"missing table", map[string]string{"index.html": head + `<script>var db = SH.storage.sqlite("bookings"); db.table("requests").add({})</script>`}, []string{`table "requests"`, "tables: bookings"}},
		{"page SQL", map[string]string{"index.html": head + `<script>var db = SH.storage.sqlite("bookings"); db.execute("INSERT INTO bookings (name) VALUES (?)", ["a"])</script>`}, []string{"sends SQL from the page"}},
		{"hidden and display none", map[string]string{"index.html": head + `<style>#editor { display: none; }</style><div id="editor" hidden></div><script>document.getElementById("editor").hidden = false;</script>`}, []string{"#editor has the hidden attribute"}},
		{"hidden without css", map[string]string{"index.html": head + `<div id="editor" hidden></div><script>document.getElementById("editor").hidden = false;</script>`}, nil},
		{"file pasted into itself", map[string]string{"index.html": head + "<script>\nvar p = make('div','price','\n});\n</script>\n</body></html>+(n/100));\nlist.append(card);\n});\n</script>\n</body></html>"}, []string{"text after </html>"}},
		{"comment after html", map[string]string{"index.html": head + "<p>hi</p></body></HTML>\n<!-- built 2026-10-30 -->\n"}, nil},
		{"wrong kind", map[string]string{"index.html": head + `<script>SH.storage.sqlite("menu").table("x")</script>`}, []string{"it is a kv resource"}},
	}
	for _, tc := range cases {
		warns := strings.Join(pageWarnings(c, "shop", tc.files), "\n")
		if len(tc.want) == 0 && warns != "" {
			t.Errorf("%s: unexpected warnings: %s", tc.name, warns)
		}
		for _, w := range tc.want {
			if !strings.Contains(warns, w) {
				t.Errorf("%s: want %q in %q", tc.name, w, warns)
			}
		}
	}
}
