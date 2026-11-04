package storage

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestValidateLegacySQLOrder(t *testing.T) {
	invalid := json.RawMessage(`{"nested":true}`)
	tests := []struct {
		name, sql string
		params    []json.RawMessage
		code, msg string
	}{
		{"empty before params", "  ", []json.RawMessage{invalid}, "sql_request", "sql is empty: send one SQL statement"},
		{"length before params", strings.Repeat("x", 32769), []json.RawMessage{invalid}, "sql_request", "sql is longer than 32,768 characters: send a shorter statement, with values in params"},
		{"count before values", "SELECT 1", make([]json.RawMessage, 101), "sql_request", "at most 100 params per call: split the change into several calls"},
		{"invalid value index", "SELECT ?", []json.RawMessage{json.RawMessage(`null`), invalid}, "sql_params", "params[1] is not a single value: each param must be a string, number, true, false or null (store an object or list as a JSON string)"},
		{"valid scalars", "SELECT ?", []json.RawMessage{json.RawMessage(`null`), json.RawMessage(`true`), json.RawMessage(`1`), json.RawMessage(`"x"`)}, "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateLegacySQL(tt.sql, tt.params)
			if tt.code == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || err.Code != tt.code || err.Message != tt.msg {
				t.Fatalf("got %#v; want %q %q", err, tt.code, tt.msg)
			}
		})
	}
}
