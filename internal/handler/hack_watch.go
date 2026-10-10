package handler

import (
	"database/sql"
	"errors"
	"net/http"
	"strings"

	"github.com/vsriram/simple-host/internal/db"
)

// RegisterHackWatch is mounted only by the hosted-events homepage.
func RegisterHackWatch(mux *http.ServeMux) {
	page := serveStaticPage("hack-watch.html")
	mux.Handle("GET /watch", adminUICSP(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		page.ServeHTTP(w, r)
	})))
}

// RegisterDemo is called only in hosted mode. Resolve the event on every
// request so the weekly replacement (slug demo, then demo-YYYYWW once the name is
// reserved) and regenerated codes take effect at once. Only the admin's events count.
func (h *HackHandler) RegisterDemo(mux *http.ServeMux, slug string) {
	slug = strings.TrimSpace(slug)
	if slug == "" {
		slug = "demo"
	}
	lookup := func(w http.ResponseWriter, r *http.Request, page bool) (db.Event, bool) {
		w.Header().Set("Cache-Control", "no-store")
		ev, err := db.GetDemoEvent(r.Context(), h.database, slug)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			writeInternal(w)
			return db.Event{}, false
		}
		if err != nil {
			if page {
				writeMessagePage(w, r, "", http.StatusNotFound, "No demo event",
					"Ask your organizer for the event's link", "/watch", "Watch how it works", "")
			} else {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "no demo event"})
			}
			return db.Event{}, false
		}
		return ev, true
	}
	mux.HandleFunc("GET /v1/hack/demo", func(w http.ResponseWriter, r *http.Request) {
		if ev, ok := lookup(w, r, false); ok {
			writeJSON(w, http.StatusOK, map[string]string{
				"event_url": h.EventURL(ev.Slug), "join_url": h.joinURL(ev.JoinCode),
				"judge_url": h.judgeURL(ev.JudgeCode), "title": ev.Title,
			})
		}
	})
	mux.Handle("GET /demo/join", adminUICSP(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if ev, ok := lookup(w, r, true); ok {
			http.Redirect(w, r, h.joinURL(ev.JoinCode), http.StatusFound)
		}
	})))
	mux.Handle("GET /demo/judge", adminUICSP(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if ev, ok := lookup(w, r, true); ok {
			http.Redirect(w, r, h.judgeURL(ev.JudgeCode), http.StatusFound)
		}
	})))
}
