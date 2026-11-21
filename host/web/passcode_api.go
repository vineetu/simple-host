package web

import (
	"database/sql"
	"errors"
	"net/http"

	"github.com/vsriram/simple-host/host/httpx"
)

func WriteSiteLookupError(w http.ResponseWriter, err error) {
	if errors.Is(err, sql.ErrNoRows) {
		httpx.WriteJSON(w, http.StatusNotFound, httpx.ErrorResponse{Error: "site not found", Code: "site_not_found"})
		return
	}
	httpx.WriteInternal(w)
}
