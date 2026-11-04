package model

import (
	"encoding/json"
	"time"
)

// ItemAuthor identifies an entry author and deletion time.
type ItemAuthor struct {
	SubmittedBy string
	Email       string
	DeletedAt   *time.Time
}

// NotifyDue is one name whose owner should be emailed about new entries.
type NotifyDue struct {
	SiteID   string
	SiteName string
	Handle   string
	OwnerID  string
	Name     string
	Notify   string
	Since    time.Time
	// SentAt is notify_sent_at as read (nil before the first email): the
	// claim compares against it.
	SentAt     *time.Time
	Count      int64
	CheckUntil time.Time
	// LastID is the newest entry counted; the claim records it, and the
	// next email counts only entries after it.
	LastID int64
}

// Savers is a site's who-may-save setting.
type Savers struct {
	Mode  string   `json:"mode"`
	Allow []string `json:"allow"`
	Block []string `json:"block"`
}

// HistoryEntry is one change, newest first in lists.
type HistoryEntry struct {
	ID     int64           `json:"id"`
	ItemID *int64          `json:"item_id,omitempty"`
	Op     string          `json:"op"`
	By     string          `json:"by,omitempty"`
	ByKind string          `json:"by_kind"`
	At     time.Time       `json:"at"`
	Size   int64           `json:"size"`
	Value  json.RawMessage `json:"value,omitempty"`
}

// ChangeElsewhere describes a change made on another owner’s site.
type ChangeElsewhere struct {
	SiteID string
	Kind   string
	Name   string
	ItemID *int64
	Op     string
	At     time.Time
}
