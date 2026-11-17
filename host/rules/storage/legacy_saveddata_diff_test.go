package storage

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"
)

func TestLegacyReverseDiffRestoresNestedState(t *testing.T) {
	before := map[string]any{"list": []any{json.Number("9007199254740993")}, "old": "kept"}
	after := map[string]any{"list": []any{json.Number("2"), true}, "new": "removed"}
	diff, err := LegacyReverseDiff(before, after)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := LegacyApplyReverse(after, diff)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(restored, before) {
		t.Fatalf("restored = %#v, want %#v", restored, before)
	}
	_, err = LegacyApplyReverse(map[string]any{}, []LegacyDiffEntry{{P: []string{"missing", "field"}, D: true}})
	if !errors.Is(err, ErrLegacyBrokenHistory) {
		t.Fatalf("broken history error = %v", err)
	}
}

func TestLegacyReverseDiffRestoresTruncatedArray(t *testing.T) {
	before := []any{json.Number("1"), map[string]any{"keep": true}, "third"}
	after := []any{json.Number("1")}
	diff, err := LegacyReverseDiff(before, after)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := LegacyApplyReverse(after, diff)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(restored, before) {
		t.Fatalf("restored = %#v, want %#v", restored, before)
	}
}
