package access

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/vsriram/simple-host/host/model"
)

// KeyHolder is the account apiKey signs in (GetUserByAPIKey). ok=false with
// no error for a key that is unknown or whose account is suspended, refused
// alike; an expired key (model.ErrKeyExpired, ErrKeyExpiredIdle) is an
// error, so the caller can say it expired.
func (s *Service) KeyHolder(ctx context.Context, apiKey string, keyIdle time.Duration) (model.User, bool, error) {
	u, err := s.GetUserByAPIKey(ctx, apiKey, keyIdle)
	if err != nil {
		if (errors.Is(err, sql.ErrNoRows) && !errors.Is(err, model.ErrKeyExpired) && !errors.Is(err, model.ErrKeyExpiredIdle)) || errors.Is(err, model.ErrAccountSuspended) {
			return model.User{}, false, nil
		}
		return u, false, err
	}
	return u, true, nil
}
