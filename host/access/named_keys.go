package access

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/vsriram/simple-host/host/model"
)

// ErrCallerKeyGone: the key the request came with was revoked or rotated
// away meanwhile, so it cannot vouch for a new one.
var ErrCallerKeyGone = errors.New("the calling key no longer works")

// IssueKey is POST /v1/me/keys: it checks the request (CheckNewKey, a 400
// *Refusal) and records plain as the account's new named key (CreateKey).
// ErrCallerKeyGone when the calling key went meanwhile, ErrKeyLimit when the
// account holds maxKeys already.
func (s *Service) IssueKey(ctx context.Context, user *model.User, req NewKeyRequest, plain string, maxKeys int, idle time.Duration) (KeyInfo, error) {
	spec, err := CheckNewKey(req, time.Now())
	if err != nil {
		return KeyInfo{}, err
	}
	k, err := s.CreateKey(ctx, user.ID, user.KeyHash, plain, spec.Name, spec.Scope, spec.ExpiresAt, maxKeys, idle)
	if errors.Is(err, sql.ErrNoRows) {
		return KeyInfo{}, ErrCallerKeyGone
	}
	return k, err
}
