package access

import (
	"errors"
)

// ErrCallerKeyGone: the key the request came with was revoked or rotated
// away meanwhile, so it cannot vouch for a new one.
var ErrCallerKeyGone = errors.New("the calling key no longer works")
