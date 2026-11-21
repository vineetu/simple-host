package siteshttp

import (
	"net/http"
)

// RegisterSkillsHub mounts the public skills catalog, per-skill markdown and /.well-known/skills discovery.
func RegisterSkillsHub(mux *http.ServeMux, publicBaseURL string, sh *SiteHandler) {
	sh.Pages().RegisterSkillsHub(mux, publicBaseURL)
}
