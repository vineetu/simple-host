package data

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestValidateLegacySQLOrder(t *testing.T) {
	invalid := json.RawMessage(`{"nested":true}`)
	tests := []struct {
		name, sql   string
		params      []json.RawMessage
		code, field string
	}{
		{"empty before params", "  ", []json.RawMessage{invalid}, "sql_request", "sql is empty"},
		{"length before params", strings.Repeat("x", 32769), []json.RawMessage{invalid}, "sql_request", "32,768"},
		{"count before values", "SELECT 1", make([]json.RawMessage, 101), "sql_request", "100 params"},
		{"invalid value index", "SELECT ?", []json.RawMessage{json.RawMessage(`null`), invalid}, "sql_params", "params[1]"},
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
			if err == nil || err.Code != tt.code || !strings.Contains(err.Message, tt.field) {
				t.Fatalf("got %#v; want %q naming %q", err, tt.code, tt.field)
			}
		})
	}
}
