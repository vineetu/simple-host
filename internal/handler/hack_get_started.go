package handler

import (
	"archive/zip"
	"bytes"
	"io"
	"io/fs"
	"net/http"
	"path/filepath"
	"sync"

	hacktoolkit "github.com/vsriram/simple-host/hack-toolkit"
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
			root, err := fs.Sub(hacktoolkit.Skills, "skills/"+name)
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
	for _, version := range []string{"0.2.0", "0.2.1", "0.2.2", "0.2.3", "0.2.4", "0.2.5"} {
		mux.HandleFunc("GET /simple-hack-skills-only-"+version+".zip", func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "this older download was withdrawn", http.StatusGone)
		})
	}
	for _, version := range []string{"0.2.6", "0.2.7", "0.2.8", "0.2.9"} {
		mux.HandleFunc("GET /simple-hack-skills-only-"+version+".zip", func(w http.ResponseWriter, r *http.Request) {
			data, err := hacktoolkit.Files.ReadFile("site/downloads/simple-hack-skills-only-" + version + ".zip")
			if err != nil {
				http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
				return
			}
			if hackInkBaseURL != "https://simple-hack.app" {
				data, err = rewriteHackPluginZip(data)
				if err != nil {
					http.Error(w, "Could not prepare the download", 500)
					return
				}
			}
			w.Header().Set("Content-Type", "application/zip")
			w.Header().Set("Content-Disposition", `attachment; filename="simple-hack-skills-only-`+version+`.zip"`)
			http.ServeContent(w, r, "simple-hack-skills-only-"+version+".zip", skillsModTime, bytes.NewReader(data))
		})
	}
}

// rewriteHackPluginZip keeps binary assets intact and rewrites only our text.
func rewriteHackPluginZip(data []byte) ([]byte, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	zw := zip.NewWriter(&out)
	for _, f := range zr.File {
		src, err := f.Open()
		if err != nil {
			return nil, err
		}
		body, err := io.ReadAll(src)
		src.Close()
		if err != nil {
			return nil, err
		}
		switch filepath.Ext(f.Name) {
		case ".md", ".yaml", ".json":
			body = hackInstanceText(body)
		}
		dst, err := zw.Create(f.Name)
		if err != nil {
			return nil, err
		}
		if _, err = dst.Write(body); err != nil {
			return nil, err
		}
	}
	if err = zw.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}
