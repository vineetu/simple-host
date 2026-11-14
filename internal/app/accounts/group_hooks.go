package accounts

import (
	"context"
	"database/sql"

	"github.com/vsriram/simple-host/internal/model"
	repo "github.com/vsriram/simple-host/internal/repository/accounts"
	"github.com/vsriram/simple-host/internal/repository/sqlkit"
)

// ConnectorTarget binds a connected app's grant to a group's site.
// With no hook every grant is personal.
type ConnectorTarget interface {
	Selected(ctx context.Context, q sqlkit.Querier, grantID string) (sql.NullString, error)
	Select(ctx context.Context, q sqlkit.Querier, grantID, userID, teamID string) (bool, error)
	Labels(ctx context.Context, q sqlkit.Querier, userID string) (map[string][]model.OAuthConnectionTeam, error)
}

type GroupHooks struct {
	Target ConnectorTarget
}

var groupHooks GroupHooks

// SetGroupHooks installs the process's product hooks before services or request
// handlers start. Services read them at call time rather than copying them.
func SetGroupHooks(hooks GroupHooks) { groupHooks = hooks }

func getOAuthToken(ctx context.Context, q sqlkit.Querier, tokenHash string) (model.OAuthTokenGrant, error) {
	tok, err := repo.GetOAuthToken(ctx, q, tokenHash)
	if err == nil && groupHooks.Target != nil {
		tok.SelectedTeamID, err = groupHooks.Target.Selected(ctx, q, tok.GrantID)
	}
	return tok, err
}
