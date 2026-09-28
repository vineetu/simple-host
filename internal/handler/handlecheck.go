package handler

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"strconv"
	"strings"

	db "github.com/vsriram/simple-host/internal/db"
)

// Choosing an address (owner decision 2026-09-28). A handle is also the
// account's address, <handle>.<SITE_DOMAIN>, so it shares one namespace with
// every other account's handle and old handles, the free names sites have
// claimed and the reserved names. One verdict, worded the same everywhere:
// the address check while someone types (GET /v1/handles/check), a new
// account's chosen address (POST /v1/auth/verify "handle") and a change of
// address (PATCH /v1/me).

// handleVerdict is why a handle cannot be had. Status 0 means it can.
type handleVerdict struct {
	Status  int
	Code    string
	Error   string
	Address string
}

// handleAddress is how a handle reads as an address: rose.simple-host.app,
// or the bare handle on an instance without a platform domain.
func handleAddress(handle string) string {
	if d := db.PlatformDomain(); d != "" {
		return handle + "." + d
	}
	return handle
}

const handleFormatMsg = "Use 1 to 39 lowercase letters, numbers or hyphens, not starting or ending with a hyphen."

// judgeHandle checks the shape, the reserved names and who holds it. userID
// is the asking account ("" for a new one): its own handle and old handles
// count as its own. A claim still decides under the namespace lock; this is
// the early, readable refusal.
func judgeHandle(ctx context.Context, q db.Querier, userID, handle string) (handleVerdict, error) {
	v := handleVerdict{Address: handleAddress(handle)}
	if !visitorHandleRe.MatchString(handle) || !handleIsLabel(handle) {
		v.Status, v.Code, v.Error = http.StatusBadRequest, "invalid_handle", handleFormatMsg
		return v, nil
	}
	if labelReservedForNew(handle) {
		v.Status, v.Code = http.StatusConflict, "handle_reserved"
		v.Error = "That address is reserved: " + v.Address + " can't be used. Try another."
		return v, nil
	}
	taken, err := db.HandleInUse(ctx, q, userID, handle)
	if err != nil {
		return v, err
	}
	if taken {
		v.Status, v.Code, v.Error = http.StatusConflict, "handle_taken", handleTakenMsg(handle)
	}
	return v, nil
}

func handleTakenMsg(handle string) string {
	return "That address is taken: " + handleAddress(handle) + " is already in use. Try another."
}

// suggestHandle is the address a new account would have been given
// (assignHandle's first free candidate), for the sign-up step to prefill.
func suggestHandle(ctx context.Context, q db.Querier, email string) string {
	base := sanitizeHandleBase(email)
	candidates := []string{base}
	for n := 2; n <= 20; n++ {
		candidates = append(candidates, base+"-"+strconv.Itoa(n))
	}
	var b [4]byte
	_, _ = rand.Read(b[:])
	candidates = append(candidates, base+"-"+hex.EncodeToString(b[:]))
	for _, c := range candidates {
		if v, err := judgeHandle(ctx, q, "", c); err == nil && v.Status == 0 {
			return c
		}
	}
	return ""
}

// handleCheck is GET /v1/handles/check?handle=<name>: can this address be
// had? With the account's X-API-Key its own handle counts as available. Per
// IP rate limited; no CORS, and a cross-site browser request is refused, so
// only this site's pages (and agents calling directly) use it.
func (h *UserHandler) handleCheck(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if s := r.Header.Get("Sec-Fetch-Site"); s != "" && s != "same-origin" && s != "none" {
		writeJSON(w, http.StatusForbidden, errorResponse{Error: "same-origin only", Code: "cross_site"})
		return
	}
	handle := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("handle")))
	userID, own := "", ""
	if key := r.Header.Get("X-API-Key"); key != "" {
		if u, err := db.GetUserByAPIKey(r.Context(), h.database, key); err == nil {
			userID, own = u.ID, u.Handle.String
		}
	}
	v, err := judgeHandle(r.Context(), h.database, userID, handle)
	if own != "" && handle == own {
		v = handleVerdict{Address: handleAddress(handle)} // your own address, as it is
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	out := map[string]any{"handle": handle, "available": v.Status == 0, "address": v.Address}
	if v.Status != 0 {
		out["code"], out["error"] = v.Code, v.Error
	}
	writeJSON(w, http.StatusOK, out)
}
