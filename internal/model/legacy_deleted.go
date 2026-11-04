package model

import (
	"encoding/json"
	"time"
)

// DeletedItem is an item in a list's Recently deleted.
type LegacyDeletedItem struct {
	ID        int64           `json:"id"`
	Data      json.RawMessage `json:"data"`
	CreatedAt time.Time       `json:"created_at"`
	DeletedAt time.Time       `json:"deleted_at"`
	By        string          `json:"by,omitempty"`
}

// DeletedCursor is where a page of Recently deleted ended: the last item's
// deletion time and id (the page is ordered by both, newest first).
type LegacyDeletedCursor struct {
	At time.Time
	ID int64
}
