package deploy

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/vsriram/simple-host/host/model"
	rules "github.com/vsriram/simple-host/host/rules/sites"
	"github.com/vsriram/simple-host/host/sites"
	"github.com/vsriram/simple-host/host/sites/repo"
)

// UpdatePreflightInput describes an upload before its body is read.
type UpdatePreflightInput struct {
	User                  *model.User
	Name, Publish, Create string
}

// UpdatePreflightResult selects the existing-site or first-deploy workflow.
type UpdatePreflightResult struct{ Publish, Create bool }

// UpdatePreflight validates the target and options before checking creation eligibility.
// Reserved names remain updateable when the account already owns the site.
func (s Service) UpdatePreflight(ctx context.Context, in UpdatePreflightInput) (out UpdatePreflightResult, err error) {
	if err := rules.ValidateShape(in.Name); err != nil {
		return out, &sites.Error{Code: "invalid_name", Message: err.Error()}
	}
	switch strings.ToLower(strings.TrimSpace(in.Publish)) {
	case "", "true", "1", "yes":
		out.Publish = true
	case "false", "0", "no":
	default:
		return out, &sites.Error{Code: "invalid_request", Message: "publish must be true or false"}
	}
	switch in.Create {
	case "", "0", "false":
		return out, nil
	case "1", "true":
	default:
		return out, &sites.Error{Code: "invalid_request", Message: "create must be 1 or 0"}
	}
	err = s.CreatePreflight(ctx, PreflightInput{User: in.User, Name: in.Name, CheckExisting: true, Publish: in.Publish})
	var e *sites.Error
	if errors.As(err, &e) && e.Code == "preflight_exists" {
		return out, nil
	}
	out.Create = err == nil
	return out, err
}

// PreflightInput contains facts needed before reading a create upload.
type PreflightInput struct {
	User          *model.User
	Name          string
	CheckExisting bool
	CheckName     bool
	Publish       string
}

// CreatePreflight checks whether a new site may be uploaded.
func (s Service) CreatePreflight(ctx context.Context, in PreflightInput) error {
	user := in.User
	if in.CheckName {
		if err := rules.ValidateShape(in.Name); err != nil {
			return &sites.Error{Code: "invalid_name", Message: err.Error()}
		}
	}
	if in.CheckExisting {
		_, err := repo.GetSiteByUser(ctx, s.DB, user.ID, in.Name)
		if err == nil {
			return &sites.Error{Code: "preflight_exists"}
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
	}
	if in.CheckName || in.CheckExisting {
		if err := rules.ValidateReserved(in.Name); err != nil {
			return &sites.Error{Code: "name_reserved", Message: err.Error()}
		}
	}
	switch strings.ToLower(strings.TrimSpace(in.Publish)) {
	case "", "true", "1", "yes":
	case "false", "0", "no":
		return &sites.Error{Code: "invalid_request", Message: "publish=false is for a new version of an existing site; a new site's first version always goes live"}
	default:
		return &sites.Error{Code: "invalid_request", Message: "publish must be true or false"}
	}
	if err := authorizePersonalSite(user, s.Deps.PersonalSitesEnabled); err != nil {
		return err
	}
	// A product's accounts (a Simple Hack event) hold as many sites as the
	// product allows, decided by its write gate.
	if user.IsAdmin || (user.Member != nil && s.Deps.GroupGate != nil) {
		return nil
	}
	existing, err := repo.CountSitesByUser(ctx, s.DB, user.ID)
	if err != nil {
		return err
	}
	max := s.Accounts.MaxSites(ctx, user, s.Deps.Limits)
	if existing >= max {
		return &sites.Error{Code: "site_quota_reached", Message: fmt.Sprintf("site quota reached: this account holds at most %d sites (sites in Recently deleted count until they are removed)", max)}
	}
	return nil
}
