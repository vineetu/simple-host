package handler

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

// templateFiles holds the starter-template catalog, embedded at build time.
// Each template lives under templates/<id>/ as one or more deployable files.
//
//go:embed all:templates
var templateFiles embed.FS

type templateMeta struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description"`
}

// templateCatalog is the ordered, curated list of starter templates. Each ID
// must match a directory templates/<id>/ in the embedded FS. Keep this small and
// simple — these are one-shot starters an LLM (down to small models) fills in.
var templateCatalog = []templateMeta{
	{
		ID:          "ui-prototype",
		Title:       "UI prototype + tap-to-comment review",
		Description: "Ship a UI mockup for review: a polished app-screen prototype with the feedback overlay wired in — reviewers browse normally, then tap the Feedback button (or long-press on phones) to pin a note to any element. The building agent reads the notes back from state (sel/text/nx/ny anchors) and applies them.",
	},
	{
		ID:          "event-rsvp",
		Title:       "Event / RSVP",
		Description: "Letterpress-invitation event page with a public RSVP form (append-only collection), recent-guest list, and a live headcount via atomic state ops. Note: entries are publicly readable.",
	},
	{
		ID:          "waitlist",
		Title:       "Waitlist / Coming soon",
		Description: "Dark launch-countdown page that collects signups into an append-only collection and shows a live \"join N others\" count via atomic state ops. Note: the store is public — the page shows the count, not the emails.",
	},
	{
		ID:          "landing",
		Title:       "Product landing + email capture",
		Description: "Swiss-poster product landing page with numbered features, an early-access form (append-only collection), and a live interest count via atomic state ops (public store).",
	},
	{
		ID:          "architecture",
		Title:       "Architecture / system-design doc (PWA)",
		Description: "Engineering-brief technical explainer with swim-lane flow diagrams, installable + offline (PWA: manifest + service worker), and a themed threaded comments section (reply + upvote) via the public state KV.",
	},
	{
		ID:          "travel",
		Title:       "Group travel itinerary",
		Description: "Static group-trip page (a Google-Sheets replacement): per-traveler flights, a per-day timeline (location + map + lodging + activities + transport), and a traveler-group filter. Data-driven from one editable object, with a themed trip-chatter comments section. Pairs with view-lock for a private trip.",
	},
	{
		ID:          "resume",
		Title:       "Résumé / CV",
		Description: "Clean single-page résumé/CV with summary, experience, projects, skills, education, and a print button. Sourced from an MIT template (CurriculumVitae by ViniciusCarvalhoLima), adapted self-contained, with a visitor comments section.",
	},
}

// RegisterTemplateRoutes mounts the public, read-only templates catalog. An LLM
// flow is: GET /v1/templates (discover) -> GET /v1/templates/{id} (fetch the
// deployable files) -> POST those files to /v1/sites/{name}/files.
func RegisterTemplateRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/templates", listTemplates)
	mux.HandleFunc("GET /v1/templates/{id}", getTemplate)
}

func listTemplates(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, templateCatalog)
}

// templateDetail is a template's metadata plus its deployable files, shaped so
// the "files" map can be POSTed straight to /v1/sites/{name}/files.
type templateDetail struct {
	templateMeta
	Files map[string]string `json:"files"`
}

func getTemplate(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))

	var meta *templateMeta
	for i := range templateCatalog {
		if templateCatalog[i].ID == id {
			meta = &templateCatalog[i]
			break
		}
	}
	if meta == nil {
		writeJSON(w, http.StatusNotFound, errorResponse{Error: "template not found"})
		return
	}

	files, err := loadTemplateFiles(id)
	if err != nil || len(files) == 0 {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}

	writeJSON(w, http.StatusOK, templateDetail{templateMeta: *meta, Files: files})
}

func loadTemplateFiles(id string) (map[string]string, error) {
	root := "templates/" + id
	files := make(map[string]string)
	err := fs.WalkDir(templateFiles, root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		b, err := templateFiles.ReadFile(path)
		if err != nil {
			return err
		}
		files[strings.TrimPrefix(path, root+"/")] = string(b)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return files, nil
}
