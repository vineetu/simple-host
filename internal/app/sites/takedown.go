package sites

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/vsriram/simple-host/internal/db"
	"github.com/vsriram/simple-host/internal/model"
	repo "github.com/vsriram/simple-host/internal/repository/sites"
)

// organiserTakedownPrefix distinguishes organiser takedowns from admin ones.
const organiserTakedownPrefix = "Taken down by the event organiser"

// TeamSiteTakedownInput describes an organiser's team site takedown change.
type TeamSiteTakedownInput struct {
	AccountID, TeamID, TeamSlug, Reason string
	On                                  bool
}

// SetTeamSiteTakenDown changes the team and site state in one transaction.
func (s *Service) SetTeamSiteTakenDown(ctx context.Context, in TeamSiteTakedownInput) error {
	unlock := s.deps.LockSite(in.AccountID, in.TeamSlug)
	defer unlock()
	tx, err := s.database.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	siteID, current, err := repo.LockSiteTakedown(ctx, tx, in.AccountID, in.TeamSlug)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if siteID != "" {
		switch {
		case in.On:
			full := organiserTakedownPrefix
			if in.Reason != "" {
				full += ": " + in.Reason
			}
			if current == "" || strings.HasPrefix(current, organiserTakedownPrefix) {
				if err := db.SetSiteSuspended(ctx, tx, siteID, full); err != nil {
					return err
				}
			}
		case current != "" && !strings.HasPrefix(current, organiserTakedownPrefix):
			return model.ErrNotOrganiserTakedown
		case current != "":
			if err := db.SetSiteSuspended(ctx, tx, siteID, ""); err != nil {
				return err
			}
		}
	}
	err = repo.SetTeamSiteTakedown(ctx, tx, in.TeamID, in.On, in.Reason)
	if err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if siteID != "" {
		fresh, err := db.GetSiteByID(ctx, s.database, siteID)
		if err != nil {
			return err
		}
		return s.disk.SetSuspended(fresh.UserID, fresh.Name, fresh.Suspended())
	}
	return nil
}
