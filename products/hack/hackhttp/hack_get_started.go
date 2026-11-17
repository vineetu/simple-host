package hackhttp

import (
	"bytes"
	"net/http"

	"github.com/vsriram/simple-host/host/httpx"

	hackassets "github.com/vsriram/simple-host/products/hack/webassets"
)

// RegisterHackGetStarted mounts Hack onboarding and the preserved toolkit URLs.
// product is the one Assets of this install (Extension.Assets): it owns the
// skill bundle, built once and shared with /skills.zip, and the
// instance-specific archive rewriting.
func RegisterHackGetStarted(mux *http.ServeMux, pages hackPages, product *Assets) {
	assets := pages
	mux.HandleFunc("GET /hack-skills.zip", product.ServeHackSkillsZip())
	mux.Handle("GET /get-started", assets.AdminUICSP(serveHackGetStarted(assets)))
	mux.Handle("GET /skills", http.RedirectHandler("/get-started", http.StatusMovedPermanently))
	// The platform's plugin archive and installers are not offered here.
	mux.HandleFunc("GET /plugin.zip", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "this legacy plugin download was withdrawn", http.StatusGone)
	})
	installerWithdrawn := func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "this installer was withdrawn", http.StatusGone)
	}
	mux.HandleFunc("GET /install.sh", installerWithdrawn)
	mux.HandleFunc("GET /install.ps1", installerWithdrawn)
	for _, version := range []string{"0.2.0", "0.2.1", "0.2.2", "0.2.3", "0.2.4", "0.2.5"} {
		mux.HandleFunc("GET /simple-hack-skills-only-"+version+".zip", func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "this older download was withdrawn", http.StatusGone)
		})
	}
	for _, version := range []string{"0.2.6", "0.2.7", "0.2.8", "0.2.9", "0.2.10", "0.2.11", "0.2.12", "0.2.13"} {
		mux.HandleFunc("GET /simple-hack-skills-only-"+version+".zip", func(w http.ResponseWriter, r *http.Request) {
			data, err := hackassets.HackFiles.ReadFile("site/downloads/simple-hack-skills-only-" + version + ".zip")
			if err != nil {
				http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
				return
			}
			if !assets.IsCanonicalInstance() {
				data, err = product.RewriteHackPluginZip(data)
				if err != nil {
					http.Error(w, "Could not prepare the download", 500)
					return
				}
			}
			w.Header().Set("Content-Type", "application/zip")
			w.Header().Set("Content-Disposition", `attachment; filename="simple-hack-skills-only-`+version+`.zip"`)
			http.ServeContent(w, r, "simple-hack-skills-only-"+version+".zip", assets.SkillsModTime(), bytes.NewReader(data))
		})
	}
}

// serveHackGetStarted serves /get-started: the page with its chrome, and on
// an install that is not simple-hack.app itself the three places that name
// the hosted service changed for this install.
func serveHackGetStarted(pages hackPages) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := pages.ChromePage(r, "hack-get-started.html")
		if err != nil {
			http.NotFound(w, r)
			return
		}
		if !pages.IsCanonicalInstance() {
			// GitHub skills name the hosted service. Install the local ZIP instead.
			start := bytes.Index(body, []byte("<li>Install the five Simple Hack skills"))
			if start >= 0 {
				end := bytes.Index(body[start:], []byte("</li>"))
				if end >= 0 {
					step := []byte(`<li>Download the <a href="/skills.zip" download>five Simple Hack skill folders</a>, install them in your agent’s skills directory, then restart your agent.</li>`)
					body = append(append(append([]byte{}, body[:start]...), step...), body[start+end+5:]...)
				}
			}
			body = bytes.ReplaceAll(body, []byte("with Google or an email code"), []byte("with an email code or a configured sign-in provider"))
			body = bytes.ReplaceAll(body, []byte("Yes. Running an event, joining, judging and hosting team sites are free."), []byte("Simple Hack is free software. Server, domain and email costs are your provider’s bill."))
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.Write(httpx.StampNonce(r, body))
	})
}
