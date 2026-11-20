package handler

import (
	"context"
	accounts "github.com/vsriram/simple-host/host/access"
	"net/http"
	"strings"

	"github.com/vsriram/simple-host/host/access/auth"
)

// Choosing an address (owner decision 2026-09-28). A handle is also the
// account's address, <handle>.<SITE_DOMAIN>, so it shares one namespace with
// every other account's handle and old handles, the free names sites have
// claimed and the reserved names. One verdict, worded the same everywhere:
// the address check while someone types (GET /v1/handles/check), a new
// account's chosen address (POST /v1/auth/verify "handle") and a change of
// address (PATCH /v1/me).

// handleVerdict is why a handle cannot be had. Status 0 means it can.
type handleVerdict = accounts.HandleVerdict

// handleAddress is how a handle reads as an address: rose.simple-host.app,
// or the bare handle on an instance without a platform domain.
func handleAddress(handle string) string { return accounts.HandleAddress(handle) }

const handleFormatMsg = "Use 1 to 39 lowercase letters, numbers or hyphens, not starting or ending with a hyphen."

// JudgeHandle checks the shape, the reserved names and who holds it. userID
// is the asking account ("" for a new one): its own handle and old handles
// count as its own. A claim still decides under the namespace lock; this is
// the early, readable refusal.
func JudgeHandle(ctx context.Context, q accountQuerier, userID, handle string) (handleVerdict, error) {
	return accounts.JudgeHandle(ctx, q, userID, handle)
}

func handleTakenMsg(handle string) string { return accounts.HandleTakenMessage(handle) }

// suggestHandle is the address a new account would have been given
// (assignHandle's first free candidate), for the sign-up step to prefill.
func suggestHandle(ctx context.Context, q accountQuerier, email string) string {
	return accounts.SuggestHandle(ctx, q, email)
}

// handleCheck is GET /v1/handles/check?handle=<name>: can this address be
// had? With the account's X-API-Key its own handle counts as available. Per
// IP rate limited; no CORS, and a cross-site browser request is refused, so
// only this site's pages (and agents calling directly) use it.
func (h *UserHandler) handleCheck(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if crossSite(r) {
		writeJSON(w, http.StatusForbidden, errorResponse{Error: "same-origin only", Code: "origin_not_allowed"})
		return
	}
	handle := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("handle")))
	userID, own := "", ""
	if u := auth.GetUser(r.Context()); u != nil {
		userID, own = u.ID, u.Handle.String
	}
	v, err := JudgeHandle(r.Context(), h.database, userID, handle)
	if own != "" && handle == own {
		v = handleVerdict{Address: handleAddress(handle)} // your own address, as it is
	}
	if err != nil {
		writeInternal(w)
		return
	}
	out := map[string]any{"handle": handle, "available": v.Status == 0, "address": v.Address}
	if v.Status != 0 {
		out["code"], out["error"] = v.Code, v.Error
	}
	writeJSON(w, http.StatusOK, out)
}
