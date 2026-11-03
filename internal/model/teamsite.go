// Package model holds shared records and value types used across HTTP
// transports and services, without SQL or transport concerns of their own
// (docs/architecture/target.md §2).
package model

import "time"

// TeamSite is a Hack team site's state as the events API shows it (moved
// out of internal/handler/hack_sites.go in cleanup 3.2 slice 1c, so both
// handler and the future internal/hackhttp package can read it without one
// importing the other).
type TeamSite struct {
	Exists        bool
	ActiveVersion int
	UpdatedAt     time.Time
	Suspended     bool
}
