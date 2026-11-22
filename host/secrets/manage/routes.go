package manage

import (
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"net/http"

	"github.com/vsriram/simple-host/host/access/auth"
	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/secrets"
	"github.com/vsriram/simple-host/host/sites"
	"github.com/vsriram/simple-host/host/web"
)

// Register mounts the owner's secret routes. A value goes in on PUT and is
// never answered back: every route answers the site's list without values.
func Register(mux *http.ServeMux, db *sql.DB, hosts *sites.Hosts, store *secrets.Store, authMiddleware, noticeMiddleware func(http.Handler) http.Handler) {
	h := handler{db, hosts, store}
	mux.Handle("GET /v1/sites/{sitename}/secrets", noticeMiddleware(authMiddleware(http.HandlerFunc(h.list))))
	mux.Handle("PUT /v1/sites/{sitename}/secrets/{name}", noticeMiddleware(authMiddleware(http.HandlerFunc(h.save))))
	mux.Handle("DELETE /v1/sites/{sitename}/secrets/{name}", noticeMiddleware(authMiddleware(http.HandlerFunc(h.remove))))
}

type handler struct {
	db    *sql.DB
	hosts *sites.Hosts
	store *secrets.Store
}

func (h handler) list(w http.ResponseWriter, r *http.Request) {
	list, err := List(r.Context(), h.db, h.store, auth.GetUser(r.Context()).ID, r.PathValue("sitename"))
	if err != nil {
		writeError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"secrets": list, "limit": secrets.MaxPerSite})
}

func (h handler) save(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Value        string `json:"value"`
		Destination  string `json:"destination"`
		Who          string `json:"who"`
		Attach       string `json:"attach"`
		AttachName   string `json:"attach_name"`
		AttachPrefix string `json:"attach_prefix"`
	}
	// The decoder's own message could quote the body: never echoed.
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4*MaxValueBytes)).Decode(&body); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", `send a JSON body: {"value": "...", "destination": "api.example.com", "who": "owner", "attach": "header", "attach_name": "Authorization", "attach_prefix": "Bearer "}`)
		return
	}
	settings := secrets.Secret{Name: r.PathValue("name"), Destination: body.Destination, Who: body.Who, Attach: body.Attach, AttachName: body.AttachName, AttachPrefix: body.AttachPrefix}
	if err := Save(r.Context(), h.db, h.hosts, h.store, auth.GetUser(r.Context()).ID, r.PathValue("sitename"), body.Value, settings); err != nil {
		writeError(w, err)
		return
	}
	h.list(w, r)
}

func (h handler) remove(w http.ResponseWriter, r *http.Request) {
	if err := Delete(r.Context(), h.db, h.store, auth.GetUser(r.Context()).ID, r.PathValue("sitename"), r.PathValue("name")); err != nil {
		writeError(w, err)
		return
	}
	h.list(w, r)
}

// writeError answers a refusal with its code; anything else is logged
// without the request (which may hold a value) and answered as internal.
func writeError(w http.ResponseWriter, err error) {
	var refusal *Refusal
	if errors.As(err, &refusal) && refusal.Code == "site_suspended" {
		web.WriteSiteSuspended(w, refusal.Message)
		return
	}
	if errors.As(err, &refusal) {
		httpx.WriteError(w, httpx.RefusalStatus(refusal.Code, http.StatusConflict), refusal.Code, refusal.Message)
		return
	}
	log.Printf("secrets: %v", err)
	httpx.WriteInternal(w)
}
