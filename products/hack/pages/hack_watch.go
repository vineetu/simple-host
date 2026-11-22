package pages

import (
	"database/sql"
	"errors"
	"net/http"
	"strings"

	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/products/hack/core"
	"github.com/vsriram/simple-host/products/hack/model"
)

// RegisterHackWatch is mounted only by the hosted-events homepage.
func RegisterHackWatch(mux *http.ServeMux, pages Pages) {
	assets := pages
	page := assets.ServeStaticPage("hack-watch.html")
	mux.Handle("GET /watch", assets.AdminUICSP(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		page.ServeHTTP(w, r)
	})))
}

// RegisterDemo serves the demo event links; called only in hosted mode.
func RegisterDemo(mux *http.ServeMux, h *core.Hack, assets Pages, slug string) {
	slug = strings.TrimSpace(slug)
	if slug == "" {
		slug = "demo"
	}
	h.DemoSlug = slug
	lookup := func(w http.ResponseWriter, r *http.Request, page bool) (model.Event, bool) {
		w.Header().Set("Cache-Control", "no-store")
		ev, err := h.Events.DemoEvent(r.Context(), slug)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			httpx.WriteInternal(w)
			return model.Event{}, false
		}
		if err != nil {
			if page {
				assets.WriteMessagePage(w, r, "", http.StatusNotFound, "No demo event",
					"Ask your organizer for the event's link", "/watch", "Watch how it works", "")
			} else {
				httpx.WriteJSON(w, http.StatusNotFound, map[string]string{"error": "no demo event"})
			}
			return model.Event{}, false
		}
		return ev, true
	}
	mux.HandleFunc("GET /v1/hack/demo", func(w http.ResponseWriter, r *http.Request) {
		if ev, ok := lookup(w, r, false); ok {
			httpx.WriteJSON(w, http.StatusOK, map[string]string{
				"event_url": h.Links.EventURL(ev.Slug), "join_url": h.Links.JoinURL(ev.JoinCode),
				"judge_url": h.Links.JudgeURL(ev.JudgeCode), "title": ev.Title,
			})
		}
	})
	mux.Handle("GET /demo/join", assets.AdminUICSP(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if ev, ok := lookup(w, r, true); ok {
			http.Redirect(w, r, h.Links.JoinURL(ev.JoinCode), http.StatusFound)
		}
	})))
	mux.Handle("GET /demo/judge", assets.AdminUICSP(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if ev, ok := lookup(w, r, true); ok {
			http.Redirect(w, r, h.Links.JudgeURL(ev.JudgeCode), http.StatusFound)
		}
	})))
}
