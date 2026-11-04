package mcp

import (
	"reflect"
	"testing"
)

func TestWrappedPasscodeScreenRejectedWhenDisabled(t *testing.T) {
	t.Setenv("MCP_APP_PASSCODE", "off")
	s := NewServer(Config{})
	caller := screenCaller()
	_, _, byName := s.forCaller(caller)
	if _, ok := byName["passcode_screen"]; ok {
		t.Fatal("disabled screen is callable")
	}
	for _, tc := range []struct {
		name string
		args map[string]any
	}{
		{"object", map[string]any{"name": "passcode_screen", "arguments": map[string]any{}}},
		{"array", map[string]any{"name": "passcode_screen", "arguments": []any{}}},
		{"string", map[string]any{"name": "passcode_screen", "arguments": "invalid"}},
		{"null", map[string]any{"name": "passcode_screen", "arguments": nil}},
		{"omitted", map[string]any{"name": "passcode_screen"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			text, _, bad := resultOf(t, passcodeWire(t, s, caller, "run_tool", tc.args))
			if !bad || text != "passcode_screen is only for the passcode screen" {
				t.Fatalf("got error=%v, text=%q", bad, text)
			}
		})
	}
}

func TestStorageFileAnalyticsDataEditionBoundary(t *testing.T) {
	s := NewServer(Config{})
	const text = "ordinary upload text"
	for _, tc := range []struct {
		name string
		want any
	}{
		{"storage_put_file", map[string]any{"removed": "file", "bytes": len(text), "type": "unknown"}},
		{"hack_event_storage_put_file", text},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := dropAnalyticsValuesForPolicy(s.policy(tc.name), map[string]any{"data": text}).(map[string]any)["data"]
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("data=%#v, want %#v", got, tc.want)
			}
		})
	}
}
