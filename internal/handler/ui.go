package handler

import (
	"archive/zip"
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"sync"
	"time"

	plugin "github.com/vsriram/simple-host/simple-host-website"
)

//go:embed all:static
var staticFiles embed.FS

var (
	skillsZipOnce  sync.Once
	skillsZipBytes []byte
	skillsZipErr   error

	pluginZipOnce  sync.Once
	pluginZipBytes []byte
	pluginZipErr   error

	skillsModTime = time.Now().UTC()
)

// PluginVersion reads the version field from the embedded plugin.json
// once and caches it. Used at boot to construct the notice middleware
// and served at /skills/version for explicit checks by the MCP client.
func PluginVersion() (string, error) {
	pluginVersionOnce.Do(func() {
		src, err := plugin.FS.Open(".claude-plugin/plugin.json")
		if err != nil {
			pluginVersionErr = fmt.Errorf("open plugin.json: %w", err)
			return
		}
		defer src.Close()

		var manifest struct {
			Version string `json:"version"`
		}
		if err := json.NewDecoder(src).Decode(&manifest); err != nil {
			pluginVersionErr = fmt.Errorf("decode plugin.json: %w", err)
			return
		}
		if manifest.Version == "" {
			pluginVersionErr = fmt.Errorf("plugin.json missing version")
			return
		}
		pluginVersionStr = manifest.Version
	})
	return pluginVersionStr, pluginVersionErr
}

var (
	pluginVersionOnce sync.Once
	pluginVersionStr  string
	pluginVersionErr  error
)

func RegisterUIRoutes(mux *http.ServeMux) {
	sub, _ := fs.Sub(staticFiles, "static")
	fileServer := http.FileServerFS(sub)

	mux.HandleFunc("GET /skills.zip", serveSkillsZip)
	mux.HandleFunc("GET /skills/version", serveSkillsVersion)
	mux.HandleFunc("GET /skills/website-deploy.zip", serveSkillZip("website-deploy"))
	mux.HandleFunc("GET /skills/website-deploy/SKILL.md", serveSkillMarkdown("website-deploy"))
	mux.HandleFunc("GET /skills/website-deploy-builder.zip", serveSkillZip("website-deploy-builder"))
	mux.HandleFunc("GET /skills/website-deploy-builder/SKILL.md", serveSkillMarkdown("website-deploy-builder"))
	mux.HandleFunc("GET /plugin.zip", servePluginZip)
	mux.HandleFunc("GET /install.sh", serveInstallScript)
	mux.Handle("GET /", fileServer)
}

// serveSkillsVersion returns {"version":"X"} from the embedded plugin.json.
// Used by the MCP client for explicit version checks; the inline notice
// middleware also reads the same value at boot.
func serveSkillsVersion(w http.ResponseWriter, r *http.Request) {
	version, err := PluginVersion()
	if err != nil {
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"version":"` + version + `"}`))
}

// serveSkillsZip returns a flat zip of the skill folders, suitable for
// extraction directly into ~/.claude/skills or ~/.agents/skills.
func serveSkillsZip(w http.ResponseWriter, r *http.Request) {
	data, err := buildSkillsZip()
	if err != nil {
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="simple-host-skills.zip"`)
	http.ServeContent(w, r, "simple-host-skills.zip", skillsModTime, bytes.NewReader(data))
}

func serveSkillZip(skillName string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		data, err := buildSingleSkillZip(skillName)
		if err != nil {
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}

		filename := skillName + ".zip"
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
		http.ServeContent(w, r, filename, skillsModTime, bytes.NewReader(data))
	}
}

func serveSkillMarkdown(skillName string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		src, err := plugin.FS.Open("skills/" + skillName + "/SKILL.md")
		if err != nil {
			http.NotFound(w, r)
			return
		}
		defer src.Close()

		data, err := io.ReadAll(src)
		if err != nil {
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}

		filename := skillName + "-SKILL.md"
		w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
		http.ServeContent(w, r, filename, skillsModTime, bytes.NewReader(data))
	}
}

// servePluginZip returns the wrapped plugin layout (.claude-plugin/plugin.json
// + skills/<name>/SKILL.md), suitable for `claude --plugin-url <url>`.
func servePluginZip(w http.ResponseWriter, r *http.Request) {
	data, err := buildPluginZip()
	if err != nil {
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="simple-host-website.zip"`)
	http.ServeContent(w, r, "simple-host-website.zip", skillsModTime, bytes.NewReader(data))
}

// serveInstallScript returns a small shell script that fetches /skills.zip
// and extracts it into ~/.claude/skills (or a path passed as the first arg).
// Designed to be either run directly (`curl … | sh`) or pasted to an agent
// who will execute it via Bash.
func serveInstallScript(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	scheme := "https"
	if r.TLS == nil && r.Header.Get("X-Forwarded-Proto") == "" {
		scheme = "http"
	}
	host := r.Host
	w.Write([]byte(installScript(scheme + "://" + host)))
}

func installScript(base string) string {
	return `#!/bin/sh
# Install Simple Host skills into your agent's skills directory.
# Pure HTTPS, no git, no npm.
#
# Usage:
#   curl -fsSL ` + base + `/install.sh | sh
#
# Override the destination (e.g. for Codex CLI / generic agents):
#   curl -fsSL ` + base + `/install.sh | sh -s -- ~/.agents/skills

set -e

DEST="${1:-$HOME/.claude/skills}"
TMP="$(mktemp -d)"
ZIP="$TMP/simple-host-skills.zip"

mkdir -p "$DEST"

echo "Downloading skills bundle..."
curl -fsSL '` + base + `/skills.zip' -o "$ZIP"

echo "Extracting into $DEST"
unzip -oq "$ZIP" -d "$DEST"

rm -rf "$TMP"

echo
echo "Installed:"
ls -1 "$DEST" | sed 's/^/    /'
echo
echo "Restart your agent if it caches the skills directory."
`
}

// buildSkillsZip walks the embedded skills/ tree and emits a flat zip.
func buildSkillsZip() ([]byte, error) {
	skillsZipOnce.Do(func() {
		var buf bytes.Buffer
		zw := zip.NewWriter(&buf)

		skillsRoot, err := fs.Sub(plugin.FS, "skills")
		if err != nil {
			skillsZipErr = err
			return
		}

		err = fs.WalkDir(skillsRoot, ".", func(path string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if path == "." || d.IsDir() {
				return nil
			}

			src, err := skillsRoot.Open(path)
			if err != nil {
				return err
			}
			defer src.Close()

			dst, err := zw.Create(path)
			if err != nil {
				return err
			}

			_, err = io.Copy(dst, src)
			return err
		})
		if err != nil {
			skillsZipErr = err
			return
		}

		if err := zw.Close(); err != nil {
			skillsZipErr = err
			return
		}

		skillsZipBytes = buf.Bytes()
	})

	return skillsZipBytes, skillsZipErr
}

func buildSingleSkillZip(skillName string) ([]byte, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	skillRoot, err := fs.Sub(plugin.FS, "skills/"+skillName)
	if err != nil {
		return nil, err
	}

	err = fs.WalkDir(skillRoot, ".", func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == "." || d.IsDir() {
			return nil
		}

		src, err := skillRoot.Open(path)
		if err != nil {
			return err
		}
		defer src.Close()

		dst, err := zw.Create(skillName + "/" + path)
		if err != nil {
			return err
		}

		_, err = io.Copy(dst, src)
		return err
	})
	if err != nil {
		zw.Close()
		return nil, err
	}

	if err := zw.Close(); err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}

// buildPluginZip emits the plugin layout: .claude-plugin/plugin.json and
// skills/<name>/SKILL.md at the zip root.
func buildPluginZip() ([]byte, error) {
	pluginZipOnce.Do(func() {
		var buf bytes.Buffer
		zw := zip.NewWriter(&buf)

		err := fs.WalkDir(plugin.FS, ".", func(path string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if path == "." || d.IsDir() {
				return nil
			}

			src, err := plugin.FS.Open(path)
			if err != nil {
				return err
			}
			defer src.Close()

			dst, err := zw.Create(path)
			if err != nil {
				return err
			}

			_, err = io.Copy(dst, src)
			return err
		})
		if err != nil {
			pluginZipErr = err
			return
		}

		if err := zw.Close(); err != nil {
			pluginZipErr = err
			return
		}

		pluginZipBytes = buf.Bytes()
	})

	return pluginZipBytes, pluginZipErr
}
