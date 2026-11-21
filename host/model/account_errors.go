package model

import (
	"database/sql"
	"errors"
	"fmt"
)

var ErrKeyExpired = fmt.Errorf("api key expired: %w", sql.ErrNoRows)
var ErrKeyExpiredIdle = fmt.Errorf("api key expired after going unused: %w", sql.ErrNoRows)
var ErrAccountSuspended = errors.New("account suspended")

var ErrKeyLimit = errors.New("account key limit reached")

var ErrUndoAddressTaken = errors.New("the old address belongs to another account now")

// ErrNotOrganiserTakedown reports that a Hack team site is down by the
// platform admin's hand, not the event organiser's: an organiser may only
// undo their own take-down.
var ErrNotOrganiserTakedown = errors.New("taken down by the platform")
