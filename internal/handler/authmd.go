package handler

import (
	"bytes"
	"net/http"
)

func serveAuthMarkdown(w http.ResponseWriter, r *http.Request) {
	body, err := staticFiles.ReadFile("static/auth.md")
	if err != nil {
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	http.ServeContent(w, r, "auth.md", skillsModTime, bytes.NewReader(body))
}
