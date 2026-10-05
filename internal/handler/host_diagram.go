package handler

import (
	"bytes"
	"net/http"
	"strings"
)

// The original diagrams are still served on Hack. Host's copies use its paper
// and ink; the shared theme script keeps standalone SVGs on the same override.
func hostDiagram(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/")
		if hackMode || !strings.HasPrefix(name, "diagrams/") || !strings.HasSuffix(name, ".svg") {
			next.ServeHTTP(w, r)
			return
		}
		raw, err := staticFiles.ReadFile("static/" + name)
		if err != nil {
			next.ServeHTTP(w, r)
			return
		}
		var theme bytes.Buffer
		_ = chromeTemplates.ExecuteTemplate(&theme, "theme.html", chromeData{})
		script := strings.ReplaceAll(strings.ReplaceAll(theme.String(), "<script>", "<script><![CDATA["), "</script>", "]]></script>")
		font := strings.SplitN(hostStatusCSS, "\n", 2)[0]
		style := `<style><![CDATA[` + font + `
svg{width:100%;height:auto;--paper:#f6f4e9;--surface:#fffdf5;--wash:#e5e4d5;--ink:#103f49;--soft:#45646a;--border:#8daaa4;--blue:#157e8a;background:var(--paper)}
svg[data-theme=dark]{--paper:#0b1222;--surface:#172332;--wash:#020617;--ink:#f6f4e9;--soft:#c4d9d0;--border:#647b87;--blue:#67e8f9}
text[font-weight="700"]{font-family:Caveat,system-ui;font-size:22px}line,path{stroke-linecap:round;stroke-linejoin:round}
]]></style>`
		palette := strings.NewReplacer("#ffffff", "var(--paper)", "#f8fafc", "var(--surface)", "#f1f5f9", "var(--wash)", "#0f172a", "var(--ink)", "#475569", "var(--soft)", "#94a3b8", "var(--border)", "#0e7490", "var(--blue)")
		svg := palette.Replace(string(raw))
		i := strings.Index(svg, ">") + 1
		svg = svg[:i] + style + script + svg[i:]
		w.Header().Set("Content-Type", "image/svg+xml")
		_, _ = w.Write(stampNonce(r, []byte(svg)))
	})
}
