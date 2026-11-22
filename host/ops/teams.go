package ops

import (
	"context"
	"database/sql"

	"github.com/vsriram/simple-host/host/access"
	"github.com/vsriram/simple-host/host/model"
	sitesrepo "github.com/vsriram/simple-host/host/sites/repo"
)

// MyTeams is GET /v1/teams: the signed-in person's teams, each with its
// live-site count (Sites owns those), and the invitations waiting for them.
func MyTeams(ctx context.Context, db *sql.DB, user *model.User) ([]model.Team, []model.TeamInvitation, error) {
	teams, err := access.ListTeams(ctx, db, user.ID)
	if err != nil {
		return nil, nil, err
	}
	ids := make([]string, len(teams))
	for i, t := range teams {
		ids[i] = t.ID
	}
	counts, err := sitesrepo.LiveSiteCounts(ctx, db, ids)
	for i := range teams {
		teams[i].Sites = counts[teams[i].ID]
	}
	if err != nil {
		return nil, nil, err
	}
	invitations, err := access.MyInvitations(ctx, db, user)
	if err != nil {
		return nil, nil, err
	}
	return teams, invitations, nil
}
