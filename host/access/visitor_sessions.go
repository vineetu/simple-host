package access

import (
	"time"
)

// VisitorSessionStart is a visitor session just written: its cookie value's
// raw ID, when it lapses unused, and whether it is the person's first
// sign-in on the site.
type VisitorSessionStart struct {
	ID            []byte
	IdleExpiresAt time.Time
	First         bool
}
