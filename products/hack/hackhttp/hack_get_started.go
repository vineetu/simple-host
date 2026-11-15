package hackhttp

import (
	"bytes"
	"net/http"

	"github.com/vsriram/simple-host/internal/webassets"
)

// RegisterHackGetStarted mounts Hack onboarding and the preserved toolkit URLs.
// product is the one Assets of this install (Extension.Assets): it owns the
// skill bundle, built once and shared with /skills.zip, and the
// instance-specific archive rewriting.
func RegisterHackGetStarted(mux *http.ServeMux, pages hackPages, product *Assets) {
	assets := pages
	mux.HandleFunc("GET /hack-skills.zip", product.ServeHackSkillsZip())
	mux.Handle("GET /get-started", assets.AdminUICSP(assets.ServeStaticPage("hack-get-started.html")))
	mux.Handle("GET /skills", http.RedirectHandler("/get-started", http.StatusMovedPermanently))
	for _, version := range []string{"0.2.0", "0.2.1", "0.2.2", "0.2.3", "0.2.4", "0.2.5"} {
		mux.HandleFunc("GET /simple-hack-skills-only-"+version+".zip", func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "this older download was withdrawn", http.StatusGone)
		})
	}
	for _, version := range []string{"0.2.6", "0.2.7", "0.2.8", "0.2.9", "0.2.10", "0.2.11", "0.2.12", "0.2.13"} {
		mux.HandleFunc("GET /simple-hack-skills-only-"+version+".zip", func(w http.ResponseWriter, r *http.Request) {
			data, err := webassets.HackFiles.ReadFile("site/downloads/simple-hack-skills-only-" + version + ".zip")
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
