package httpx

import (
	"encoding/json"
	"net/http"
	"strings"

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

// WriteJSON encodes v as status. An ErrorResponse with no Code gets the
// common code for 401, 429 and 413; any other status keeps it empty.
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

// refusalStatus is the one code→status table for host and product refusals.
// Codes ending in _not_found are 404 and codes starting invalid_ are 400
// without being listed; other unlisted codes take the caller's fallback.
var refusalStatus = map[string]int{
	"invalid_code": 404, "not_found": 404, "visitor_unknown": 404, "invalid_reference": 404, "for_not_available": 404,
	"method_not_allowed": 405,
	"sign_in_required":   401,
	"forbidden":          403, "event_taken_down": 403, "team_site_taken_down": 403, "checklist_role_forbidden": 403,
	"team_admin_only": 403, "team_action_unavailable": 403, "vote_ineligible": 403, "organiser_read_only": 403,
	"rules_invalid": 403, "site_locked": 403, "site_private": 403, "csrf_required": 403, "for_requires_own": 403,
	"sql_visitor_rule": 403, "fixed_routes_required": 403, "account_suspended": 403, "add_only": 403,
	"spam_check_required": 403, "spam_check_failed": 403, "spam_check_unavailable": 403,
	"resource_changed": 409, "resource_kind_conflict": 409, "key_exists": 409, "file_exists": 409, "row_conflict": 409,
	"written_by_blocked": 409, "written_by_tampered": 409, "table_preset_rename": 409, "sql_unique_failed": 409,
	"too_many_questions": 400, "too_many_tracks": 400, "answer_required": 400, "coc_required": 400,
	"too_few_judges": 400, "too_few_teams": 400, "deadline_not_later": 400,
	"version_live": 409, "version_published": 409,
	"version_changed": 412, "post_too_large": 413,
	"query_too_expensive": 422, "entry_incomplete": 422, "entry_would_be_incomplete": 422,
	"too_many_events_today": 429, "rate_limited": 429,
	"internal_error": 500, "internal": 500, "storage_failed": 500, "marker_failed": 500, "markers_failed": 500,
	"storage_busy": 503,
	"site_full":    507, "sqlite_full": 507, "bucket_full": 507,
}

// RefusalStatus is the HTTP status of a refusal code; fallback answers codes
// the table does not decide (409 for hack workflows, 400 for storage input,
// 0 where an unknown code is an internal error).
func RefusalStatus(code string, fallback int) int {
	if status, ok := refusalStatus[code]; ok {
		return status
	}
	switch {
	case strings.HasSuffix(code, "_not_found"):
		return http.StatusNotFound
	case strings.HasPrefix(code, "invalid_"):
		return http.StatusBadRequest
	}
	return fallback
}
