package siteshttp

import (
	"bytes"
	"net/http"

	"github.com/vsriram/simple-host/host/web"
)

func (h *SiteHandler) serveAuthMarkdown(w http.ResponseWriter, r *http.Request) {
	name := "auth.md"
	body, err := h.Pages().TextFS().ReadFile("static/" + name)
	if err != nil {
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	if h.Ed.Presentation().ProductAssets {
		body = h.Pages().ProductInstanceText(bytes.ReplaceAll(body, []byte("{sign_in}"), []byte(web.AuthSignInMethods())))
	} else if !web.EmailSignInEnabled {
		body = web.HostAuthWithoutEmail(body)
	}
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	http.ServeContent(w, r, "auth.md", web.SkillsModTime, bytes.NewReader(body))
}
