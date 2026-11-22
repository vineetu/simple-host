package content

import (
	"database/sql"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"strings"
	"unicode"

	core "github.com/vsriram/simple-host/products/hack/core"

	"github.com/vsriram/simple-host/host/httpx"
)

func (h *handler) getEventIcon(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	ev, err := h.Events.EventBySlug(r.Context(), strings.ToLower(r.PathValue("slug")))
	if err == nil {
		core.PaperEvent(r, ev)
	}
	if errors.Is(err, sql.ErrNoRows) {
		core.WriteEventNotFound(w)
		return
	}
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	if ev.TakenDown() {
		core.WriteEventNotFound(w)
		return
	}
	typ, body, err := h.Events.EventIcon(r.Context(), ev.ID)
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	if typ == "" || len(body) == 0 {
		color := core.EventAccent(ev)
		ink := core.EventInk(color)
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

func (h *handler) putEventIcon(w http.ResponseWriter, r *http.Request) {
	a, ok := core.LoadMember(w, r, h.Members, r.PathValue("slug"), true, "organiser")
	if !ok {
		return
	}
	if a.Event.Stage == "archived" {
		httpx.WriteError(w, http.StatusConflict, "event_closed", "this event has ended")
		return
	}
	max := int64(h.Limits.HackEventIconMaxBytes)
	raw, err := io.ReadAll(io.LimitReader(r.Body, max+1))
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_icon", "could not read the image")
		return
	}
	if len(raw) == 0 || int64(len(raw)) > max {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_icon", "image exceeds the event icon size limit")
		return
	}
	typ := http.DetectContentType(raw)
	if typ != "image/png" && typ != "image/jpeg" && typ != "image/webp" {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_icon", "use a PNG, JPEG or WebP image")
		return
	}
	declared := strings.TrimSpace(strings.Split(r.Header.Get("Content-Type"), ";")[0])
	if declared != typ {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_icon", "Content-Type must match the image")
		return
	}
	if err := h.Events.SetEventIconLookup(r.Context(), a.Event.ID, typ, raw); err != nil {
		httpx.WriteInternal(w)
		return
	}
	a.Event.IconMediaType = typ
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"icon_url": h.Links.IconURL(a.Event), "content_type": typ, "bytes": len(raw)})
}

func (h *handler) deleteEventIcon(w http.ResponseWriter, r *http.Request) {
	a, ok := core.LoadMember(w, r, h.Members, r.PathValue("slug"), true, "organiser")
	if !ok {
		return
	}
	if a.Event.Stage == "archived" {
		httpx.WriteError(w, http.StatusConflict, "event_closed", "this event has ended")
		return
	}
	if err := h.Events.SetEventIconLookup(r.Context(), a.Event.ID, "", nil); err != nil {
		httpx.WriteInternal(w)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"icon_url": ""})
}
