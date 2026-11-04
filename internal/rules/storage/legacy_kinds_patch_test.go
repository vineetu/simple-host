package storage

import (
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func TestLegacyWithdrawalRestorable(t *testing.T) {
	at := time.Now().Add(-time.Minute)
	for _, tc := range []struct {
		op, actor, user string
		window          time.Duration
		want            bool
	}{
		{"delete", "a", "a", time.Hour, true},
		{"edit", "a", "a", time.Hour, false},
		{"delete", "b", "a", time.Hour, false},
		{"delete", "a", "a", time.Second, false},
	} {
		if got := LegacyWithdrawalRestorable(tc.op, tc.actor, tc.user, at, tc.window); got != tc.want {
			t.Errorf("LegacyWithdrawalRestorable(%q, %q, %q, %v) = %v", tc.op, tc.actor, tc.user, tc.window, got)
		}
	}
}

func TestLegacyEntriesLike(t *testing.T) {
	for _, tc := range []struct {
		kind         string
		shared, want bool
	}{{"entries", false, true}, {"", true, true}, {"", false, false}, {"content", true, false}} {
		if got := LegacyEntriesLike(tc.kind, tc.shared); got != tc.want {
			t.Errorf("LegacyEntriesLike(%q, %v) = %v", tc.kind, tc.shared, got)
		}
	}
}

func TestPatchLegacyEntry(t *testing.T) {
	old := json.RawMessage(`{"_submitted_by":"a","_submitted_at":"then","remove":1,"keep":2}`)
	patch := map[string]json.RawMessage{
		"_submitted_by": json.RawMessage(`"b"`),
		"_submitted_at": json.RawMessage(`null`),
		"remove":        json.RawMessage(`null`),
		"add":           json.RawMessage(`3`),
	}
	got, err := PatchLegacyEntry(old, patch, 1024)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != `{"_submitted_at":"then","_submitted_by":"a","add":3,"keep":2}` {
		t.Fatalf("unexpected patch: %s", got)
	}
	if _, err := PatchLegacyEntry(json.RawMessage(`[]`), patch, 1024); !errors.Is(err, ErrLegacyEntryNotObject) {
		t.Fatalf("non-object error = %v", err)
	}
	if _, err := PatchLegacyEntry(json.RawMessage(`{}`), patch, 2); !errors.Is(err, ErrLegacyEntryTooLarge) {
		t.Fatalf("growing entry error = %v", err)
	}
	if _, err := PatchLegacyEntry(old, nil, 1); err != nil {
		t.Fatalf("unchanged oversized entry: %v", err)
	}
	// A patch may shrink an already oversized entry even if it remains over
	// the current limit; growing to exactly the limit is also permitted.
	if got, err := PatchLegacyEntry(json.RawMessage(`{"long":12345,"x":1}`), map[string]json.RawMessage{"long": json.RawMessage(`null`)}, 2); err != nil || string(got) != `{"x":1}` {
		t.Fatalf("shrinking oversized entry = %s, %v", got, err)
	}
	if got, err := PatchLegacyEntry(json.RawMessage(`{}`), map[string]json.RawMessage{"x": json.RawMessage(`1`)}, 7); err != nil || string(got) != `{"x":1}` {
		t.Fatalf("entry at size limit = %s, %v", got, err)
	}
}

func TestDecodeLegacyEntryPatch(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want bool
	}{
		{`{"a":1}`, true},
		{`{}`, true},
		{`null`, false},
		{`[]`, false},
		{`{`, false},
	} {
		_, ok := DecodeLegacyEntryPatch([]byte(tc.raw))
		if ok != tc.want {
			t.Errorf("DecodeLegacyEntryPatch(%q) = %v, want %v", tc.raw, ok, tc.want)
		}
	}
}

func TestLegacyEntryOwned(t *testing.T) {
	for _, tc := range []struct {
		name                       string
		author, user               string
		deleted, requireLive, want bool
	}{
		{"live author", "a", "a", false, true, true},
		{"other visitor", "a", "b", false, true, false},
		{"deleted live required", "a", "a", true, true, false},
		{"deleted allowed", "a", "a", true, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := LegacyEntryOwned(tc.author, tc.user, tc.deleted, tc.requireLive); got != tc.want {
				t.Errorf("LegacyEntryOwned() = %v, want %v", got, tc.want)
			}
		})
	}
}
