package handler

import (
	"bytes"
	"net/http"

	hacktoolkit "github.com/vsriram/simple-host/hack-toolkit"
)

// RegisterHackGetStarted is mounted only when EVENTS=hosted. Takes the
// hackPages interface, not *SiteHandler (cleanup 3.2 slice 2a): this only
// ever called sh.pipeline(). hostedHackSkill, BuildHackSkillsZip,
// ServeHackSkillsZip and RewriteHackPluginZip moved to hack_assets.go
// (staying): ui.go (staying) also calls them, and BuildHackSkillsZip and
// RewriteHackPluginZip are *assetPipeline methods, which must live in
// handler regardless of who calls them.
func RegisterHackGetStarted(mux *http.ServeMux, pages hackPages) {
	assets := pages
	mux.Handle("GET /get-started", assets.AdminUICSP(assets.ServeStaticPage("hack-get-started.html")))
	mux.Handle("GET /skills", http.RedirectHandler("/get-started", http.StatusMovedPermanently))
	mux.HandleFunc("GET /hack-skills.zip", assets.ServeHackSkillsZip())
	for _, version := range []string{"0.2.0", "0.2.1", "0.2.2", "0.2.3", "0.2.4", "0.2.5"} {
		mux.HandleFunc("GET /simple-hack-skills-only-"+version+".zip", func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "this older download was withdrawn", http.StatusGone)
		})
	}
	for _, version := range []string{"0.2.6", "0.2.7", "0.2.8", "0.2.9", "0.2.10", "0.2.11", "0.2.12", "0.2.13"} {
		mux.HandleFunc("GET /simple-hack-skills-only-"+version+".zip", func(w http.ResponseWriter, r *http.Request) {
			data, err := hacktoolkit.Files.ReadFile("site/downloads/simple-hack-skills-only-" + version + ".zip")
			if err != nil {
				http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
				return
			}
			if !assets.IsCanonicalInstance() {
				data, err = assets.RewriteHackPluginZip(data)
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
