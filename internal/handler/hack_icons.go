package handler

import (
	"database/sql"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"strings"
	"unicode"

	"github.com/vsriram/simple-host/internal/config"
	"github.com/vsriram/simple-host/internal/db"
)

func hackIconLimit() int64 {
	return int64(config.Active().HackEventIconMaxBytes)
}

func (h *HackHandler) iconURL(ev db.Event) string {
	return h.iconURLSlug(ev.Slug)
}

func (h *HackHandler) iconURLSlug(slug string) string {
	return strings.TrimRight(h.publicBaseURL, "/") + "/v1/hack/events/" + slug + "/icon"
}

func (h *HackHandler) registerEventIcon(mux *http.ServeMux, wrap func(http.HandlerFunc) http.Handler) {
	mux.Handle("GET /v1/hack/events/{slug}/icon", http.HandlerFunc(h.getEventIcon))
	mux.Handle("PUT /v1/hack/events/{slug}/icon", wrap(h.putEventIcon))
	mux.Handle("DELETE /v1/hack/events/{slug}/icon", wrap(h.deleteEventIcon))
}

func (h *HackHandler) getEventIcon(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	ev, err := db.GetEventBySlug(r.Context(), h.database, strings.ToLower(r.PathValue("slug")))
	if errors.Is(err, sql.ErrNoRows) {
		writeEventNotFound(w)
		return
	}
	if err != nil {
		writeInternal(w)
		return
	}
	if ev.TakenDown() {
		writeEventNotFound(w)
		return
	}
	typ, body, err := db.GetEventIcon(r.Context(), h.database, ev.ID)
	if err != nil {
		writeInternal(w)
		return
	}
	if typ == "" || len(body) == 0 {
		color := hackEventAccent(ev)
		ink := "#141413"
		if color == "#267b79" || color == "#4a49a8" {
			ink = "white"
		}
		initial := "?"
		if letters := []rune(strings.TrimSpace(ev.Title)); len(letters) > 0 {
			initial = string(unicode.ToUpper(letters[0]))
		}
		w.Header().Set("Content-Type", "image/svg+xml")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "public, max-age=300")
		_, _ = fmt.Fprintf(w, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 64 64"><circle cx="32" cy="32" r="32" fill="%s"/><text x="32" y="43" text-anchor="middle" font-family="system-ui,sans-serif" font-size="35" font-weight="700" fill="%s">%s</text></svg>`, color, ink, html.EscapeString(initial))
		return
	}
	w.Header().Set("Content-Type", typ)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "public, max-age=300")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

func (h *HackHandler) putEventIcon(w http.ResponseWriter, r *http.Request) {
	a, ok := h.loadMember(w, r, r.PathValue("slug"), true, "organiser")
	if !ok {
		return
	}
	if a.event.Stage == "archived" {
		writeHackErr(w, http.StatusConflict, "event_closed", "this event has ended")
		return
	}
	max := hackIconLimit()
	raw, err := io.ReadAll(io.LimitReader(r.Body, max+1))
	if err != nil {
		writeHackErr(w, http.StatusBadRequest, "invalid_icon", "could not read the image")
		return
	}
	if len(raw) == 0 || int64(len(raw)) > max {
		writeHackErr(w, http.StatusBadRequest, "invalid_icon", "image exceeds the event icon size limit")
		return
	}
	typ := http.DetectContentType(raw)
	if typ != "image/png" && typ != "image/jpeg" && typ != "image/webp" {
		writeHackErr(w, http.StatusBadRequest, "invalid_icon", "use a PNG, JPEG or WebP image")
		return
	}
	declared := strings.TrimSpace(strings.Split(r.Header.Get("Content-Type"), ";")[0])
	if declared != typ {
		writeHackErr(w, http.StatusBadRequest, "invalid_icon", "Content-Type must match the image")
		return
	}
	if err := db.SetEventIcon(r.Context(), h.database, a.event.ID, typ, raw); err != nil {
		writeInternal(w)
		return
	}
	a.event.IconMediaType = typ
	writeJSON(w, http.StatusOK, map[string]any{"icon_url": h.iconURL(a.event), "content_type": typ, "bytes": len(raw)})
}

func (h *HackHandler) deleteEventIcon(w http.ResponseWriter, r *http.Request) {
	a, ok := h.loadMember(w, r, r.PathValue("slug"), true, "organiser")
	if !ok {
		return
	}
	if a.event.Stage == "archived" {
		writeHackErr(w, http.StatusConflict, "event_closed", "this event has ended")
		return
	}
	if err := db.SetEventIcon(r.Context(), h.database, a.event.ID, "", nil); err != nil {
		writeInternal(w)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"icon_url": ""})
}
