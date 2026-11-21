package web

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"sort"
	"strings"

	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/webassets"
)

// servedSkillFS keeps Simple Host's maintained skill bundle intact while Simple Hack
// serves the reviewed connector-only snapshot bundled with its toolkit.
func (p *AssetPipeline) servedSkillFS() webassets.Files {
	if p.Product.Skills != nil {
		return assetFiles{p.Product.Skills}
	}
	return webassets.Host
}

// Skills hub: a tiny public HTTP catalog so any agent (Hermes, OpenClaw, a raw
// script, …) can DISCOVER and FETCH the bundled skills over plain HTTPS —
// no git, no marketplace, no auth.
//
//	GET /v1/skills            → JSON catalog {plugin, version, skills:[{name,description,url}]}
//	GET /v1/skills/{name}     → that skill's raw SKILL.md (text/markdown)
//	GET /v1/skills/{name}/SKILL.md → the same
//
// It reads the embedded skill bundle and lists only this service's guides.

type skillEntry struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	URL         string `json:"url"`
	dir         string
}

type skillsCatalog struct {
	Plugin  string       `json:"plugin"`
	Version string       `json:"version"`
	Count   int          `json:"count"`
	Skills  []skillEntry `json:"skills"`
}

func (p *AssetPipeline) SkillAvailable(name string) bool {
	if hook := p.Product.Hooks.SkillAvailable; hook != nil {
		return hook(name)
	}
	return name != "join-hackathon" && name != "judge-hackathon" && name != "run-hackathon"
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

// listBundledSkills returns each bundled skill's frontmatter.
func (p *AssetPipeline) listBundledSkills() []skillEntry {
	var out []skillEntry
	for _, dir := range p.BundledSkillDirs() {
		data, err := p.servedSkillFS().ReadFile("skills/" + dir + "/SKILL.md")
		if err != nil {
			continue
		}
		name, desc := parseSkillFrontmatter(string(data))
		if name == "" {
			name = dir
		}
		out = append(out, skillEntry{Name: name, Description: desc, dir: dir})
	}
	return out
}

// BundledSkillDirs returns the directory name of every embedded skill, sorted.
// Directory names — not frontmatter names — because they are what the download
// routes and the on-disk install layout are keyed on.
func (p *AssetPipeline) BundledSkillDirs() []string {
	entries, err := fs.ReadDir(p.servedSkillFS(), "skills")
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() || !p.SkillAvailable(e.Name()) {
			continue
		}
		if _, err := fs.Stat(p.servedSkillFS(), "skills/"+e.Name()+"/SKILL.md"); err != nil {
			continue
		}
		out = append(out, e.Name())
	}
	sort.Strings(out)
	return out
}

// SkillFiles lists every file a skill ships, as slash-separated paths relative
// to the skill directory ("SKILL.md", "references/backend.md", …). A skill is no
// longer a single file: SKILL.md routes to references/*.md, and a discovery hub
// that fetches only SKILL.md would hand the agent a map with no territory.
func (p *AssetPipeline) SkillFiles(dir string) []string {
	root, err := fs.Sub(p.servedSkillFS(), "skills/"+dir)
	if err != nil {
		return nil
	}
	var out []string
	_ = fs.WalkDir(root, ".", func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil || path == "." || d.IsDir() {
			return walkErr
		}
		out = append(out, path)
		return nil
	})
	sort.Strings(out)
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

func (p *AssetPipeline) ServeSkillsCatalog(publicBaseURL string) http.HandlerFunc {
	base := strings.TrimRight(publicBaseURL, "/")
	return func(w http.ResponseWriter, r *http.Request) {
		version := p.PluginVersion()
		skills := p.listBundledSkills()
		for i := range skills {
			skills[i].URL = base + "/v1/skills/" + skills[i].Name
		}
		pluginName := "website-deploy"
		if name := p.Product.Hooks.CatalogPlugin; name != "" {
			pluginName = name
		}
		cat := skillsCatalog{Plugin: pluginName, Version: version, Count: len(skills), Skills: skills}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "public, max-age=300")
		_ = json.NewEncoder(w).Encode(cat)
	}
}

func (p *AssetPipeline) serveSkillDoc(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.PathValue("name"))
	if !validSkillName(name) {
		http.Error(w, "invalid skill name", http.StatusBadRequest)
		return
	}
	if !p.SkillAvailable(name) {
		writeSkillNotFound(w, r, "404 page not found")
		return
	}
	data, err := p.servedSkillFS().ReadFile("skills/" + name + "/SKILL.md")
	if err != nil {
		writeSkillNotFound(w, r, "skill not found")
		return
	}
	writeSkillMarkdown(w, p.SkillServedText(name+"/SKILL.md", data))
}

func writeSkillMarkdown(w http.ResponseWriter, data []byte) {
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=300")
	_, _ = w.Write(data)
}

// SkillServedText is a skill file as this instance serves it: its hostnames
// and limits (MAX_ARCHIVE_MB and the knobs) put in, as the zips and
// /skills/<name>/SKILL.md do, except for a control-plane skill, which states
// the public instance's (see copyRewritten). path is relative to skills/.
func (p *AssetPipeline) SkillServedText(path string, data []byte) []byte {
	if p.Pres.ProductAssets {
		return p.ProductInstanceText(p.limitsText.apply(data))
	}
	if controlPlaneSkill(path) {
		return data
	}
	return rewriteServedText(data, p.limitsText)
}

// validReferenceFile guards the {file} path param. References are flat markdown
// files inside the skill's references/ directory — no nesting, no traversal.
func validReferenceFile(s string) bool {
	if !strings.HasSuffix(s, ".md") {
		return false
	}
	return validSkillName(strings.TrimSuffix(s, ".md"))
}

// ServeSkillReference serves one references/*.md from a skill. SKILL.md points
// at these both by relative path (for a folder install) and by absolute URL —
// this is what makes the URL-only install methods (`copilot skill add <url>`,
// `hermes skills install <url>`) still work after a skill is split.
func (p *AssetPipeline) ServeSkillReference(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.PathValue("name"))
	file := strings.TrimSpace(r.PathValue("file"))
	if !validSkillName(name) || !validReferenceFile(file) {
		http.Error(w, "invalid reference", http.StatusBadRequest)
		return
	}
	if !p.SkillAvailable(name) {
		writeSkillNotFound(w, r, "404 page not found")
		return
	}
	data, err := p.servedSkillFS().ReadFile("skills/" + name + "/references/" + file)
	if err != nil {
		writeSkillNotFound(w, r, "reference not found")
		return
	}
	writeSkillMarkdown(w, p.SkillServedText(name+"/references/"+file, data))
}

// wellKnownEntry is one item in /.well-known/skills/index.json (the convention
// agent skill hubs — e.g. Hermes — probe to auto-discover a site's skills).
type wellKnownEntry struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Files       []string `json:"files"`
}

// serveWellKnownSkillsIndex serves /.well-known/skills/index.json so any hub can
// discover this site's skills. Each skill's files live at
// /.well-known/skills/<name>/SKILL.md.
func (p *AssetPipeline) serveWellKnownSkillsIndex(w http.ResponseWriter, r *http.Request) {
	skills := p.listBundledSkills()
	entries := make([]wellKnownEntry, 0, len(skills))
	for _, s := range skills {
		entries = append(entries, wellKnownEntry{Name: s.Name, Description: s.Description, Files: p.SkillFiles(s.dir)})
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=300")
	_ = json.NewEncoder(w).Encode(map[string]any{"skills": entries})
}

func writeSkillNotFound(w http.ResponseWriter, r *http.Request, message string) {
	if isAPIPath(r.URL.Path) {
		httpx.WriteJSON(w, http.StatusNotFound, httpx.ErrorResponse{
			Error: message, Code: "not_found", Hint: "Use GET /v1/skills to find available skills and their references.",
		})
		return
	}
	http.Error(w, message, http.StatusNotFound)
}

func (p *AssetPipeline) RegisterSkillsHub(mux *http.ServeMux, publicBaseURL string) {
	mux.HandleFunc("GET /v1/skills", p.ServeSkillsCatalog(publicBaseURL))
	mux.HandleFunc("GET /v1/skills/{name}", p.serveSkillDoc)
	// Agents append /SKILL.md by habit (it works under /skills/ and
	// /.well-known/skills/), so accept it here too.
	mux.HandleFunc("GET /v1/skills/{name}/SKILL.md", p.serveSkillDoc)
	mux.HandleFunc("GET /v1/skills/{name}/references/{file}", p.ServeSkillReference)
	// Well-known agent-skills discovery (Hermes/OpenClaw/etc.).
	mux.HandleFunc("GET /.well-known/skills/index.json", p.serveWellKnownSkillsIndex)
	mux.HandleFunc("GET /.well-known/skills/{name}/SKILL.md", p.serveSkillDoc)
	mux.HandleFunc("GET /.well-known/skills/{name}/references/{file}", p.ServeSkillReference)
}
