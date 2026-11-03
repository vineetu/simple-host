package model

import "errors"

// ErrNotOrganiserTakedown reports that a Hack team site is down by the
// platform admin's hand, not the event organiser's: an organiser may only
// undo their own take-down. Moved out of internal/handler/hack_sites.go in
// cleanup 3.2 slice 1c so both handler (which returns it) and the future
// internal/hackhttp package (which matches it with errors.Is) can see it
// without one importing the other.
var ErrNotOrganiserTakedown = errors.New("taken down by the platform")
