package handler

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"strings"

	plugin "github.com/vsriram/simple-host/simple-host-website"
)

// Skills hub: a tiny public HTTP catalog so any agent (Hermes, OpenClaw, a raw
// script, …) can DISCOVER and FETCH the bundled skills over plain HTTPS —
// no git, no marketplace, no auth.
//
//	GET /v1/skills            → JSON catalog {plugin, version, skills:[{name,description,url}]}
//	GET /v1/skills/{name}     → that skill's raw SKILL.md (text/markdown)
//
// It reads the embedded skill bundle, so it always reflects what this server
// actually ships (connect-domain included, plus anything added later).

type skillEntry struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	URL         string `json:"url"`
}

type skillsCatalog struct {
	Plugin  string       `json:"plugin"`
	Version string       `json:"version"`
	Count   int          `json:"count"`
	Skills  []skillEntry `json:"skills"`
}

// parseSkillFrontmatter pulls name + description out of a SKILL.md YAML
// frontmatter block (the leading `---` … `---`). Line-based; no YAML dep.
func parseSkillFrontmatter(md string) (name, desc string) {
	if !strings.HasPrefix(md, "---") {
		return "", ""
	}
	rest := md[3:]
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return "", ""
	}
	for _, line := range strings.Split(rest[:end], "\n") {
		if v, ok := strings.CutPrefix(line, "name:"); ok {
			name = strings.TrimSpace(v)
		} else if v, ok := strings.CutPrefix(line, "description:"); ok {
			desc = strings.TrimSpace(v)
		}
	}
	return name, desc
}

// listBundledSkills walks the embedded skills/ dir and returns each skill's
// frontmatter (skipping any dir without a readable SKILL.md).
func listBundledSkills() []skillEntry {
	var out []skillEntry
	entries, err := fs.ReadDir(plugin.FS, "skills")
	if err != nil {
		return out
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		data, err := plugin.FS.ReadFile("skills/" + e.Name() + "/SKILL.md")
		if err != nil {
			continue
		}
		name, desc := parseSkillFrontmatter(string(data))
		if name == "" {
			name = e.Name()
		}
		out = append(out, skillEntry{Name: name, Description: desc})
	}
	return out
}

// validSkillName guards the {name} path param against traversal — skill dirs
// are lowercase [a-z0-9-].
func validSkillName(s string) bool {
	if s == "" || len(s) > 64 {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-') {
			return false
		}
	}
	return true
}

func serveSkillsCatalog(publicBaseURL string) http.HandlerFunc {
	base := strings.TrimRight(publicBaseURL, "/")
	return func(w http.ResponseWriter, r *http.Request) {
		version, _ := PluginVersion()
		skills := listBundledSkills()
		for i := range skills {
			skills[i].URL = base + "/v1/skills/" + skills[i].Name
		}
		cat := skillsCatalog{Plugin: "website-deploy", Version: version, Count: len(skills), Skills: skills}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "public, max-age=300")
		_ = json.NewEncoder(w).Encode(cat)
	}
}

func serveSkillDoc(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.PathValue("name"))
	if !validSkillName(name) {
		http.Error(w, "invalid skill name", http.StatusBadRequest)
		return
	}
	data, err := plugin.FS.ReadFile("skills/" + name + "/SKILL.md")
	if err != nil {
		http.Error(w, "skill not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=300")
	_, _ = w.Write(data)
}

// RegisterSkillsHub mounts the public skills catalog + per-skill markdown fetch.
func RegisterSkillsHub(mux *http.ServeMux, publicBaseURL string) {
	mux.HandleFunc("GET /v1/skills", serveSkillsCatalog(publicBaseURL))
	mux.HandleFunc("GET /v1/skills/{name}", serveSkillDoc)
}
