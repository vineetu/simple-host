package accounts

import (
	"context"

	"github.com/lib/pq"
	"github.com/vsriram/simple-host/internal/model"
)

type OAuthClient = model.OAuthClient

func GetOAuthClient(ctx context.Context, q Querier, clientID string) (OAuthClient, error) {
	var c OAuthClient
	err := q.QueryRowContext(ctx, `
		SELECT client_id, client_secret_hash, client_name, redirect_uris, token_endpoint_auth_method, pkce_required, dynamic, created_at
		  FROM oauth_clients WHERE client_id = $1`, clientID).
		Scan(&c.ClientID, &c.SecretHash, &c.Name, pq.Array(&c.RedirectURIs), &c.TokenEndpointAuthMethod, &c.PKCERequired, &c.Dynamic, &c.CreatedAt)
	return c, err
}
