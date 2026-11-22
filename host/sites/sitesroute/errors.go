package sitesroute

import (
	"net/http"

	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/sites"
)

func WriteTooLarge(w http.ResponseWriter, limit int64, detail string) {
	e := sites.TooLarge(limit, detail)
	httpx.WriteJSON(w, http.StatusRequestEntityTooLarge, httpx.ErrorResponse{Error: e.Message, Code: e.Code, Hint: sites.StorageTips})
}
