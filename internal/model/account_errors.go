package model

import (
	"database/sql"
	"errors"
	"fmt"
)

var ErrKeyExpired = fmt.Errorf("api key expired: %w", sql.ErrNoRows)
var ErrKeyExpiredIdle = fmt.Errorf("api key expired after going unused: %w", sql.ErrNoRows)
var ErrAccountSuspended = errors.New("account suspended")
