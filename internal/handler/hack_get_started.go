package handler

import (
	"archive/zip"
	"bytes"
	"io/fs"
	"net/http"
	"sync"

	hacktoolkit "github.com/vsriram/simple-host/hack-toolkit"
	plugin "github.com/vsriram/simple-host/simple-host-website"
)

var hackSkillNames = []string{"run-hackathon", "join-hackathon", "judge-hackathon", "website-deploy", "website-deploy-builder"}

func hostedHackSkill(name string) bool {
	for _, skill := range hackSkillNames {
		if name == skill {
			return true
		}
	}
	return false
}

var (
	hackSkillsZipOnce sync.Once
	hackSkillsZipData []byte
	hackSkillsZipErr  error
)

// The hosted bundle has the five Simple Hack skills from the canonical embed.
func buildHackSkillsZip() ([]byte, error) {
	hackSkillsZipOnce.Do(func() {
		var buf bytes.Buffer
		zw := zip.NewWriter(&buf)
		for _, name := range hackSkillNames {
			root, err := fs.Sub(plugin.FS, "skills/"+name)
			if err != nil {
				hackSkillsZipErr = err
				break
			}
			err = fs.WalkDir(root, ".", func(path string, entry fs.DirEntry, walkErr error) error {
				if walkErr != nil || path == "." || entry.IsDir() {
					return walkErr
				}
				data, err := fs.ReadFile(root, path)
				if err != nil {
					return err
				}
				dst, err := zw.Create(name + "/" + path)
				if err != nil {
					return err
				}
				_, err = dst.Write(skillServedText(name+"/"+path, data))
				return err
			})
			if err != nil {
				hackSkillsZipErr = err
				break
			}
		}
		if err := zw.Close(); hackSkillsZipErr == nil {
			hackSkillsZipErr = err
		}
		if hackSkillsZipErr == nil {
			hackSkillsZipData = buf.Bytes()
		}
	})
	return hackSkillsZipData, hackSkillsZipErr
}

func serveHackSkillsZip(w http.ResponseWriter, r *http.Request) {
	data, err := buildHackSkillsZip()
	if err != nil {
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="simple-hack-skills.zip"`)
	http.ServeContent(w, r, "simple-hack-skills.zip", skillsModTime, bytes.NewReader(data))
}

// RegisterHackGetStarted is mounted only when EVENTS=hosted.
func RegisterHackGetStarted(mux *http.ServeMux) {
	mux.Handle("GET /get-started", adminUICSP(serveStaticPage("hack-get-started.html")))
	mux.Handle("GET /skills", http.RedirectHandler("/get-started", http.StatusMovedPermanently))
	mux.HandleFunc("GET /hack-skills.zip", serveHackSkillsZip)
	mux.HandleFunc("GET /simple-hack-skills-only-0.2.0.zip", func(w http.ResponseWriter, r *http.Request) {
		data, err := hacktoolkit.Files.ReadFile("site/downloads/simple-hack-skills-only-0.2.0.zip")
		if err != nil {
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Disposition", `attachment; filename="simple-hack-skills-only-0.2.0.zip"`)
		http.ServeContent(w, r, "simple-hack-skills-only-0.2.0.zip", skillsModTime, bytes.NewReader(data))
	})
	mux.HandleFunc("GET /simple-hack-skills-only-0.2.1.zip", func(w http.ResponseWriter, r *http.Request) {
		data, err := hacktoolkit.Files.ReadFile("site/downloads/simple-hack-skills-only-0.2.1.zip")
		if err != nil {
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Disposition", `attachment; filename="simple-hack-skills-only-0.2.1.zip"`)
		http.ServeContent(w, r, "simple-hack-skills-only-0.2.1.zip", skillsModTime, bytes.NewReader(data))
	})
	mux.HandleFunc("GET /simple-hack-skills-only-0.2.2.zip", func(w http.ResponseWriter, r *http.Request) {
		data, err := hacktoolkit.Files.ReadFile("site/downloads/simple-hack-skills-only-0.2.2.zip")
		if err != nil {
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Disposition", `attachment; filename="simple-hack-skills-only-0.2.2.zip"`)
		http.ServeContent(w, r, "simple-hack-skills-only-0.2.2.zip", skillsModTime, bytes.NewReader(data))
	})
	mux.HandleFunc("GET /simple-hack-skills-only-0.2.3.zip", func(w http.ResponseWriter, r *http.Request) {
		data, err := hacktoolkit.Files.ReadFile("site/downloads/simple-hack-skills-only-0.2.3.zip")
		if err != nil {
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Disposition", `attachment; filename="simple-hack-skills-only-0.2.3.zip"`)
		http.ServeContent(w, r, "simple-hack-skills-only-0.2.3.zip", skillsModTime, bytes.NewReader(data))
	})
	mux.HandleFunc("GET /simple-hack-skills-only-0.2.4.zip", func(w http.ResponseWriter, r *http.Request) {
		data, err := hacktoolkit.Files.ReadFile("site/downloads/simple-hack-skills-only-0.2.4.zip")
		if err != nil {
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Disposition", `attachment; filename="simple-hack-skills-only-0.2.4.zip"`)
		http.ServeContent(w, r, "simple-hack-skills-only-0.2.4.zip", skillsModTime, bytes.NewReader(data))
	})

}
