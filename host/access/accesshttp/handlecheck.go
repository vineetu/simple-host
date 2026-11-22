package accesshttp

import (
	"net/http"
	"strings"

	accounts "github.com/vsriram/simple-host/host/access"
	"github.com/vsriram/simple-host/host/httpx"

	"github.com/vsriram/simple-host/host/access/auth"
)

// Choosing an address. A handle is also the
// account's address, <handle>.<SITE_DOMAIN>, so it shares one namespace with
// every other account's handle and old handles, the free names sites have
// claimed and the reserved names. One verdict, worded the same everywhere:
// the address check while someone types (GET /v1/handles/check), a new
// account's chosen address (POST /v1/auth/verify "handle") and a change of
// address (PATCH /v1/me).

// handleCheck is GET /v1/handles/check?handle=<name>: can this address be
// had? With the account's X-API-Key its own handle counts as available. Per
// IP rate limited; no CORS, and a cross-site browser request is refused, so
// only this site's pages (and agents calling directly) use it.
func (h *UserHandler) handleCheck(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if httpx.CrossSite(r) {
		httpx.WriteJSON(w, http.StatusForbidden, httpx.ErrorResponse{Error: "same-origin only", Code: "origin_not_allowed"})
		return
	}
	handle := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("handle")))
	v, err := accounts.CheckAddress(r.Context(), h.database, auth.GetUser(r.Context()), handle)
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	out := map[string]any{"handle": handle, "available": v.Status == 0, "address": v.Address}
	if v.Status != 0 {
		out["code"], out["error"] = v.Code, v.Error
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}
