package httpx

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
)

// ErrorResponse is the shared JSON error body. Keep every field, tag and
// comment: MCP tools key their hints on Code, and sign-in UI keys cards on
// Reason.
type ErrorResponse struct {
	Error string `json:"error"`
	// Code is a stable snake_case name for the failure, set where one error
	// status has more than one meaning (the MCP tools key their hints on it).
	Code string `json:"code,omitempty"`
	// Reason is a sign-in card key (code_wrong, code_used_up, code_expired,
	// link_other_browser) on 401 invalid_code answers.
	Reason string `json:"reason,omitempty"`
	// TriesLeft is remaining guesses on a code_wrong answer.
	TriesLeft *int `json:"tries_left,omitempty"`
	// SuggestedHandle and Address come with code choose_handle (a new
	// account's sign-in asked to choose its address first).
	SuggestedHandle string `json:"suggested_handle,omitempty"`
	Address         string `json:"address,omitempty"`
	// Hint says how to fix the request, in plain words, where there is a
	// concrete next step (e.g. a site that does not exist: how to create it,
	// or where to list the sites there are).
	Hint string `json:"hint,omitempty"`
	// Storage is the storage-resources path on code legacy_storage_closed.
	Storage string `json:"storage,omitempty"`
}

// WriteJSON encodes v as status.
//
// Older refusals omitted a code when the status had only one meaning. Keep
// specific route codes, and fill these existing common codes only.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	if response, ok := v.(ErrorResponse); ok && response.Code == "" {
		switch status {
		case http.StatusUnauthorized:
			response.Code = "sign_in_required"
		case http.StatusTooManyRequests:
			response.Code = "rate_limited"
		case http.StatusRequestEntityTooLarge:
			response.Code = "item_too_large"
		}
		v = response
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// WriteError answers a hack-style {"error", "code"} body.
func WriteError(w http.ResponseWriter, status int, code, msg string) {
	WriteJSON(w, status, ErrorResponse{Error: msg, Code: code})
}

// WriteInternal answers 500 with the generic internal-error body.
func WriteInternal(w http.ResponseWriter) {
	WriteJSON(w, http.StatusInternalServerError, ErrorResponse{Error: "internal server error"})
}

// WriteJSONETag answers v with a weak ETag of its bytes, and 304 when the
// request's If-None-Match already names it (pages poll a board or a record
// cheaply with it).
func WriteJSONETag(w http.ResponseWriter, r *http.Request, v any) {
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(v); err != nil {
		WriteInternal(w)
		return
	}
	sum := sha256.Sum256(buf.Bytes())
	etag := `W/"` + hex.EncodeToString(sum[:12]) + `"`
	w.Header().Set("ETag", etag)
	for _, t := range strings.Split(r.Header.Get("If-None-Match"), ",") {
		if t = strings.TrimSpace(t); t == etag || t == "*" || "W/"+t == etag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(buf.Bytes())
}
