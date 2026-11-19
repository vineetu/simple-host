package httpx

import (
	"encoding/json"
	"net/http"

	ruleengine "github.com/vsriram/simple-host/host/storage/rules"
)

// RuleError exposes the wire diagnostic to transports without an engine import.
type RuleError = ruleengine.RuleError

// ErrorResponse is the shared JSON error body. Keep every field, tag and
// comment: MCP tools key their hints on Code, and sign-in UI keys cards on
// Reason.
type ErrorResponse struct {
	RuleError   *ruleengine.RuleError  `json:"rule_error,omitempty"`
	RulesErrors []ruleengine.RuleError `json:"rules_errors,omitempty"`
	Error       string                 `json:"error"`
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
