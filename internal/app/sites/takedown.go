package sites

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/vsriram/simple-host/internal/model"
	repo "github.com/vsriram/simple-host/internal/repository/sites"
)

// HeldSiteSuspension describes a product's suspension of a held site.
type HeldSiteSuspension struct {
	AccountID, Name string
	Prefix, Reason  string
	On              bool
	// Also updates product state after the site row, in the same transaction.
	Also func(*sql.Tx) error
}

// SuspendHeldSite updates the site and product state atomically, then its marker.
func (s *Service) SuspendHeldSite(ctx context.Context, in HeldSiteSuspension) error {
	unlock := s.deps.LockSite(in.AccountID, in.Name)
	defer unlock()
	tx, err := s.database.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	siteID, current, err := repo.LockSiteTakedown(ctx, tx, in.AccountID, in.Name)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if siteID != "" {
		switch {
		case in.On:
			full := in.Prefix
			if in.Reason != "" {
				full += ": " + in.Reason
			}
			if current == "" || strings.HasPrefix(current, in.Prefix) {
				if err := repo.SetSiteSuspended(ctx, tx, siteID, full); err != nil {
					return err
				}
			}
		case current != "" && !strings.HasPrefix(current, in.Prefix):
			return model.ErrNotOrganiserTakedown
		case current != "":
			if err := repo.SetSiteSuspended(ctx, tx, siteID, ""); err != nil {
				return err
			}
		}
	}
	if in.Also != nil {
		if err := in.Also(tx); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if siteID != "" {
		fresh, err := repo.GetSiteByID(ctx, s.database, siteID)
		if err != nil {
			return err
		}
		return s.disk.SetSuspended(fresh.UserID, fresh.Name, fresh.Suspended())
	}
	return nil
}
