package model

import (
	"database/sql"
	"errors"
	"fmt"
)

// ErrNotOrganiserTakedown reports that a Hack team site is down by the
// platform admin's hand, not the event organiser's: an organiser may only
// undo their own take-down. Moved out of host/handler/hack_sites.go in
// cleanup 3.2 slice 1c so both handler (which returns it) and the future
// host/hackhttp package (which matches it with errors.Is) can see it
// without one importing the other.
var ErrNotOrganiserTakedown = errors.New("taken down by the platform")

// ErrTeamKeyInactive reports a credential whose group membership is no longer live.
var ErrTeamKeyInactive = fmt.Errorf("team key no longer active: %w", sql.ErrNoRows)
