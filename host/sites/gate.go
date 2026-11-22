package sites

import (
	"context"
	"database/sql"
	"errors"
	"net/http"

	"github.com/vsriram/simple-host/host/model"
	"github.com/vsriram/simple-host/host/sites/repo"
)

// GroupGate decides writes to a held site. Nil on a platform-only install.
type GroupGate interface {
	// Check runs in the caller's transaction at the existing gate position,
	// before LockUserHandle on create/update. It locks the event and organiser
	// membership FOR SHARE, or the approved member followed by team write state
	// FOR SHARE. A refusal preserves the product's existing codes and messages.
	Check(ctx context.Context, tx *sql.Tx, user *model.User, siteName string) *GateError
	// Suspension reports the product's view during restore, after the site row
	// is locked. It locks the team row FOR SHARE: site first, team second,
	// matching SuspendHeldSite. Prefix identifies product-owned reasons;
	// an empty prefix leaves the site unchanged, an empty reason lifts only
	// a product-owned suspension. Operator and owner suspensions still win.
	Suspension(ctx context.Context, tx *sql.Tx, site repo.Site) (prefix, reason string, err error)
}

func (s *Service) CreateGate(ctx context.Context, tx *sql.Tx, user *model.User, siteName string) *GateError {
	if s.Deps.GroupGate == nil || user.Member == nil {
		return nil
	}
	return s.Deps.GroupGate.Check(ctx, tx, user, siteName)
}

// CheckWrite runs the product's gate for a change made for an account
// (owner=<handle>) outside a deploy: saved data, a subscription, a rollback.
// Deploys run it again under their own lock. Nil gate: every change passes.
func CheckWrite(ctx context.Context, db *sql.DB, gate GroupGate, user *model.User, site string) (status int, code, msg string, err error) {
	if gate == nil || user.Member == nil {
		return 0, "", "", nil
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, "", "", err
	}
	defer tx.Rollback()
	e := gate.Check(ctx, tx, user, site)
	if e == nil {
		return 0, "", "", nil
	}
	if e.Code == "" {
		return 0, "", "", errors.New(e.Message)
	}
	if e.Status == 0 {
		e.Status = http.StatusConflict
	}
	return e.Status, e.Code, e.Message, nil
}
