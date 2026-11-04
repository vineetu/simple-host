package mcp

// Legacy storage history result shapes.

import (
	"encoding/json"
	"strconv"
)

// restChange is one entry of a history answer.
type restChange struct {
	ID     int64           `json:"id"`
	ItemID *int64          `json:"item_id"`
	Op     string          `json:"op"`
	By     string          `json:"by"`
	ByKind string          `json:"by_kind"`
	At     string          `json:"at"`
	Size   int64           `json:"size"`
	Value  json.RawMessage `json:"value"`
}

func (e restChange) summary(withValue bool) map[string]any {
	m := map[string]any{"version": strconv.FormatInt(e.ID, 10), "op": e.Op, "by_kind": e.ByKind, "at": e.At, "size": e.Size}
	if e.By != "" {
		m["by"] = e.By
	}
	if e.ItemID != nil {
		m["item_id"] = strconv.FormatInt(*e.ItemID, 10)
	}
	if withValue {
		var v any
		_ = json.Unmarshal(e.Value, &v)
		m["value"] = v
	}
	return m
}
