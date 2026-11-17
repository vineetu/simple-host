package model

import "time"

// LegacyIdempotentResponse records the first response to an idempotent write.
type LegacyIdempotentResponse struct {
	Status   int
	ETag     string
	Ref      int64
	BodyHash []byte
}

// LegacyDataWatchSite holds one site's counts over the watch window.
type LegacyDataWatchSite struct {
	SiteID  string           `json:"site_id"`
	Site    string           `json:"site"`
	Handle  string           `json:"handle"`
	Metrics map[string]int64 `json:"metrics"`
	LastAt  time.Time        `json:"last_at"`
}
