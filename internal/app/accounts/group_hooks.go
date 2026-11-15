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

// GroupKeys resolves credentials that act for a holding account. Nil means no
// key is a group key. Implementations keep membership and write checks live.
type GroupKeys interface {
	// Binding returns the stored binding, or sql.ErrNoRows when unbound.
	Binding(ctx context.Context, q sqlkit.Querier, keyHash string) (teamID, memberID string, err error)
	// Identity returns the member's live place, or sql.ErrNoRows when no longer on it.
	Identity(ctx context.Context, q sqlkit.Querier, memberID, teamID string) (model.TeamIdentity, error)
	// WriteRefusal allows when code is empty; otherwise the response is unchanged.
	WriteRefusal(ctx context.Context, q sqlkit.Querier, teamID string) (status int, code, msg string, err error)
	// FileLinkAllows checks team membership or the group's own website organiser.
	FileLinkAllows(ctx context.Context, q sqlkit.Querier, site model.Site, kind, id, memberID string) bool
	Messages() (scopeOnly, inactive string)
}

type GroupHooks struct {
	Erasure Erasure
	Keys    GroupKeys
	Target  ConnectorTarget
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
