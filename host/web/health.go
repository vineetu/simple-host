package web

import (
	"context"
	"database/sql"
	"net/http"
	"time"

	"github.com/vsriram/simple-host/host/httpx"
)

type healthResponse struct {
	Status string `json:"status"`
}

func RegisterHealthRoutes(mux *http.ServeMux, db *sql.DB) {
	mux.HandleFunc("GET /healthz", healthz)
	mux.HandleFunc("GET /readyz", readyz(db))
}

func healthz(w http.ResponseWriter, _ *http.Request) {
	httpx.WriteJSON(w, http.StatusOK, healthResponse{Status: "ok"})
}

func readyz(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()

		if err := db.PingContext(ctx); err != nil {
			httpx.WriteJSON(w, http.StatusServiceUnavailable, healthResponse{Status: "unready"})
			return
		}

		httpx.WriteJSON(w, http.StatusOK, healthResponse{Status: "ok"})
	}
}
