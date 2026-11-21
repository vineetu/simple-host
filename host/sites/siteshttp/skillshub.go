package siteshttp

import (
	"net/http"
)

// RegisterSkillsHub mounts the public skills catalog + per-skill markdown fetch,
// plus the /.well-known/skills discovery endpoint used by agent skill hubs,
// for sh's edition.
func RegisterSkillsHub(mux *http.ServeMux, publicBaseURL string, sh *SiteHandler) {
	sh.Pipeline().RegisterSkillsHub(mux, publicBaseURL)
}
