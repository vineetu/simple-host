package hackhttp

import (
	"database/sql"
	"errors"
	"github.com/vsriram/simple-host/internal/httpx"
	"net/http"
	"strings"

	"github.com/vsriram/simple-host/internal/db"
)

// RegisterHackWatch is mounted only by the hosted-events homepage. Takes
// the hackPages interface, not *SiteHandler (cleanup 3.2 slice 2a): this
// only ever called sh.pipeline().
func RegisterHackWatch(mux *http.ServeMux, pages hackPages) {
	assets := pages
	page := assets.ServeStaticPage("hack-watch.html")
	mux.Handle("GET /watch", assets.AdminUICSP(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	h.demoSlug = slug
	lookup := func(w http.ResponseWriter, r *http.Request, page bool) (db.Event, bool) {
		w.Header().Set("Cache-Control", "no-store")
		ev, err := db.GetDemoEvent(r.Context(), h.database, slug)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			httpx.WriteInternal(w)
			return db.Event{}, false
		}
		if err != nil {
			if page {
				h.pipeline().WriteMessagePage(w, r, "", http.StatusNotFound, "No demo event",
					"Ask your organizer for the event's link", "/watch", "Watch how it works", "")
			} else {
				httpx.WriteJSON(w, http.StatusNotFound, map[string]string{"error": "no demo event"})
			}
			return db.Event{}, false
		}
		return ev, true
	}
	mux.HandleFunc("GET /v1/hack/demo", func(w http.ResponseWriter, r *http.Request) {
		if ev, ok := lookup(w, r, false); ok {
			httpx.WriteJSON(w, http.StatusOK, map[string]string{
				"event_url": h.EventURL(ev.Slug), "join_url": h.joinURL(ev.JoinCode),
				"judge_url": h.judgeURL(ev.JudgeCode), "title": ev.Title,
			})
		}
	})
	assets := h.pipeline()
	mux.Handle("GET /demo/join", assets.AdminUICSP(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if ev, ok := lookup(w, r, true); ok {
			http.Redirect(w, r, h.joinURL(ev.JoinCode), http.StatusFound)
		}
	})))
	mux.Handle("GET /demo/judge", assets.AdminUICSP(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if ev, ok := lookup(w, r, true); ok {
			http.Redirect(w, r, h.judgeURL(ev.JudgeCode), http.StatusFound)
		}
	})))
}
