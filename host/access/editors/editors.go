package editors

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"

	"github.com/vsriram/simple-host/host/access"
	"github.com/vsriram/simple-host/host/access/repo"
	"github.com/vsriram/simple-host/host/model"
	rules "github.com/vsriram/simple-host/host/rules/sites"
)

// ListSiteEditors lists the editors of the account's site name.
func ListSiteEditors(ctx context.Context, db *sql.DB, accountID, name string) ([]model.TeamMember, error) {
	if err := rules.ValidateShape(name); err != nil {
		return nil, access.TeamFail(http.StatusBadRequest, "invalid_name", err.Error())
	}
	return repo.ListSiteEditors(ctx, db, accountID, name)
}

// AddSiteEditor makes the person whose handle is who an editor of the
// account's site name (it need not exist yet), for actorID, the account
// itself or a member of the team.
func AddSiteEditor(ctx context.Context, db *sql.DB, accountID, actorID, name, who string) error {
	person, err := siteEditorPerson(ctx, db, accountID, name, who)
	if err != nil {
		return err
	}
	_, err = repo.SetSiteEditor(ctx, db, accountID, name, person, actorID, true)
	return err
}

// RemoveSiteEditor stops the person whose handle is who editing the
// account's site name; their access ends on their next request.
func RemoveSiteEditor(ctx context.Context, db *sql.DB, accountID, name, who string) error {
	person, err := siteEditorPerson(ctx, db, accountID, name, who)
	if err != nil {
		return err
	}
	removed, err := repo.SetSiteEditor(ctx, db, accountID, name, person, "", false)
	if err == nil && !removed {
		return access.TeamFail(http.StatusNotFound, "editor_not_found", "that person is not an editor of this site")
	}
	return err
}

func siteEditorPerson(ctx context.Context, db *sql.DB, accountID, name, who string) (string, error) {
	if err := rules.ValidateShape(name); err != nil {
		return "", access.TeamFail(http.StatusBadRequest, "invalid_name", err.Error())
	}
	notFound := access.TeamFail(http.StatusNotFound, "person_not_found", "no person has the handle "+strings.ToLower(strings.TrimSpace(who))+"; an editor is a person, named by their handle (their address)")
	person, err := repo.GetAccountByHandle(ctx, db, who)
	if errors.Is(err, sql.ErrNoRows) || err == nil && person.ID == accountID {
		return "", notFound
	}
	if err != nil {
		return "", err
	}
	if team, err := repo.IsTeamAccount(ctx, db, person.ID); err != nil || team {
		if err == nil {
			err = notFound
		}
		return "", err
	}
	return person.ID, nil
}
