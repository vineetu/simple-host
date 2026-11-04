package model

import (
	"encoding/json"
	"time"
)

// CollectionItem is one row in an append-only collection.
type CollectionItem struct {
	ID        int64           `json:"id"`
	Data      json.RawMessage `json:"data"`
	CreatedAt time.Time       `json:"created_at"`
	// By is who sent the item (the address they were signed in with), for
	// the owner's reads only; empty everywhere else.
	By string `json:"by,omitempty"`
	// Version counts the item's changes (from 1). Shown for Shared board
	// items only, where an edit can name the version it changes (If-Match).
	Version int64 `json:"version,omitempty"`
}

// CollectionSummary is one named collection on a site: how many rows it
// holds, when the most recent write landed (null for a private collection that
// has nothing in it yet) and whether it is private.
type CollectionSummary struct {
	Name    string     `json:"name"`
	Count   int64      `json:"count"`
	LastAt  *time.Time `json:"last_at"`
	Private bool       `json:"private"`
	// Deleted counts the items in the list's Recently deleted.
	Deleted int64 `json:"deleted"`
	// Bytes is the size of the live items (for Personal names, all that the
	// owner sees besides the count: how many people have a record).
	Bytes int64 `json:"bytes"`
	// Kind is what the name was declared as (content = Page info, entries =
	// Submissions), "" when nobody declared it; the Submissions options with it.
	Kind         string `json:"kind"`
	OnePerPerson bool   `json:"one_per_person"`
	Notify       string `json:"notify"`
	// Few (Personal only): one or two people have a record, so count and
	// bytes are withheld (0); DeletedFew is the same for Recently deleted.
	// With so few, a size or its change would point at one person.
	Few        bool `json:"few,omitempty"`
	DeletedFew bool `json:"deleted_few,omitempty"`
}

// Actor is who made a change. ID is "" when nobody was signed in.
type Actor struct {
	ID    string
	Kind  string // owner | admin | visitor | anonymous
	Email string
}
