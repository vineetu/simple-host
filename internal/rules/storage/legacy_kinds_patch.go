package storage

import (
	"bytes"
	"encoding/json"
	"errors"
	"time"
)

// LegacyEntriesLike reports whether a declaration behaves as submissions.
func LegacyEntriesLike(kind string, shared bool) bool {
	return kind == "entries" || (kind == "" && shared)
}

// LegacyEntryOwned reports whether a visitor may address their entry.
func LegacyEntryOwned(submittedBy, userID string, deleted, requireLive bool) bool {
	return submittedBy == userID && (!requireLive || !deleted)
}

// LegacyWithdrawalRestorable reports whether the latest change permits undo.
func LegacyWithdrawalRestorable(op, actor, userID string, at, now time.Time, window time.Duration) bool {
	return op == "delete" && actor == userID && now.Sub(at) <= window
}

// ErrLegacyEntryTooLarge indicates that a patch grew an entry beyond its limit.
var ErrLegacyEntryTooLarge = errors.New("too large")

// ErrLegacyEntryNotObject indicates that an entry cannot be patched as an object.
var ErrLegacyEntryNotObject = errors.New("not an object")

// DecodeLegacyEntryPatch accepts the object format used for entry patches.
func DecodeLegacyEntryPatch(raw []byte) (map[string]json.RawMessage, bool) {
	var patch map[string]json.RawMessage
	dec := json.NewDecoder(bytes.NewReader(raw))
	if err := dec.Decode(&patch); err != nil || patch == nil || dec.More() {
		return nil, false
	}
	return patch, true
}

// PatchLegacyEntry applies fields while preserving server-owned submission stamps.
func PatchLegacyEntry(old json.RawMessage, patch map[string]json.RawMessage, maxBytes int64) (json.RawMessage, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(old, &fields); err != nil || fields == nil {
		return nil, ErrLegacyEntryNotObject
	}
	for k, v := range patch {
		if k == "_submitted_by" || k == "_submitted_at" {
			continue
		}
		if string(bytes.TrimSpace(v)) == "null" {
			delete(fields, k)
			continue
		}
		fields[k] = v
	}
	next, err := json.Marshal(fields)
	if err != nil {
		return nil, err
	}
	if int64(len(next)) > maxBytes && len(next) > len(old) {
		return nil, ErrLegacyEntryTooLarge
	}
	return next, nil
}
