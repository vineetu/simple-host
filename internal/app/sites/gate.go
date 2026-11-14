package sites

import (
	"context"
	"database/sql"

	"github.com/vsriram/simple-host/internal/model"
)

// GroupGate decides writes to a held site. Nil on a platform-only install.
type GroupGate interface {
	// Check runs in the caller's transaction at the existing gate position,
	// before LockUserHandle on create/update. It locks the event and organiser
	// membership FOR SHARE, or the approved member followed by team write state
	// FOR SHARE. A refusal preserves the product's existing codes and messages.
	Check(ctx context.Context, tx *sql.Tx, user *model.User, siteName string) *GateError
}

func (s *Service) createGate(ctx context.Context, tx *sql.Tx, user *model.User, siteName string) *GateError {
	if s.deps.GroupGate == nil || (user.EventWebsite == nil && user.Team == nil) {
		return nil
	}
	return s.deps.GroupGate.Check(ctx, tx, user, siteName)
}
