package db

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
)

// A PATCH's history row keeps only what the change touched (review fix M2,
// 2026-09-27): a reverse diff, the few edits that turn the document after the
// change back into the one before it. The document before any change is
// rebuilt from the nearest newer full copy (a row with prev, or the current
// document) by applying the reverse diffs newest first. A full copy is still
// written on each day's first change and at least every
// SAVED_DATA_SNAPSHOT_EVERY changes, so a rebuild walks a bounded chain.
//
// Why this survives the sweep and thinning: the sweep drops the oldest rows
// (a prefix), and thinning drops the oldest rows that are not a day's first
// full copy. Either way every row still held has every newer row it needs.

// diffEntry is one edit at path P (object keys from the root): set it to V,
// delete it (D), or cut an array there back to T items. P empty with V is the
// whole document.
type diffEntry struct {
	P []string        `json:"p"`
	V json.RawMessage `json:"v,omitempty"`
	D bool            `json:"d,omitempty"`
	T *int            `json:"t,omitempty"`
}

var errBrokenHistory = errors.New("history chain does not match the document")

// reverseDiff returns the edits that turn after back into before. Both are
// decoded JSON (objects as map[string]any, numbers as json.Number). Empty when
// they are equal.
func reverseDiff(before, after any) ([]diffEntry, error) {
	var out []diffEntry
	err := diffInto(&out, nil, before, after)
	return out, err
}

func diffInto(out *[]diffEntry, path []string, before, after any) error {
	child := func(k string) []string {
		p := make([]string, len(path)+1)
		copy(p, path)
		p[len(path)] = k
		return p
	}
	bm, bok := before.(map[string]any)
	am, aok := after.(map[string]any)
	if bok && aok {
		for k, bv := range bm {
			if av, ok := am[k]; ok {
				if err := diffInto(out, child(k), bv, av); err != nil {
					return err
				}
				continue
			}
			v, err := json.Marshal(bv)
			if err != nil {
				return err
			}
			*out = append(*out, diffEntry{P: child(k), V: v})
		}
		for k := range am {
			if _, ok := bm[k]; !ok {
				*out = append(*out, diffEntry{P: child(k), D: true})
			}
		}
		return nil
	}
	// An array that only grew at the end (append) goes back by cutting it.
	if ba, ok := before.([]any); ok {
		if aa, ok := after.([]any); ok && len(aa) > len(ba) && reflect.DeepEqual(ba, aa[:len(ba)]) {
			n := len(ba)
			*out = append(*out, diffEntry{P: path, T: &n})
			return nil
		}
	}
	if reflect.DeepEqual(before, after) {
		return nil
	}
	v, err := json.Marshal(before)
	if err != nil {
		return err
	}
	*out = append(*out, diffEntry{P: path, V: v})
	return nil
}

// applyReverse applies one reverse diff to doc (decoded) and returns the
// document from before that change.
func applyReverse(doc any, entries []diffEntry) (any, error) {
	for _, e := range entries {
		if len(e.P) == 0 {
			if e.T != nil {
				arr, ok := doc.([]any)
				if !ok || *e.T > len(arr) {
					return nil, errBrokenHistory
				}
				doc = arr[:*e.T]
				continue
			}
			v, err := decodeNumbers(e.V)
			if err != nil {
				return nil, err
			}
			doc = v
			continue
		}
		parent, ok := doc.(map[string]any)
		if !ok {
			return nil, errBrokenHistory
		}
		for _, k := range e.P[:len(e.P)-1] {
			next, ok := parent[k].(map[string]any)
			if !ok {
				return nil, errBrokenHistory
			}
			parent = next
		}
		last := e.P[len(e.P)-1]
		switch {
		case e.D:
			delete(parent, last)
		case e.T != nil:
			arr, ok := parent[last].([]any)
			if !ok || *e.T > len(arr) {
				return nil, errBrokenHistory
			}
			parent[last] = arr[:*e.T]
		default:
			v, err := decodeNumbers(e.V)
			if err != nil {
				return nil, err
			}
			parent[last] = v
		}
	}
	return doc, nil
}

// decodeNumbers decodes JSON keeping every number's exact digits.
func decodeNumbers(raw []byte) (any, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, nil
	}
	var v any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	return v, nil
}
