package storage

import (
	"errors"
)

// ErrSQLiteBusy indicates that a slot could not be acquired in time.
var ErrSQLiteBusy = errors.New("storage is busy")
