package connector

import (
	"context"
	"database/sql"
	"log"

	accounts "github.com/vsriram/simple-host/host/access"

	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/model"
)

// RegisterClient is dynamic client registration (RFC 7591, POST
// /oauth/register): it checks the metadata (CheckClientMetadata), makes the
// client's id and, for a confidential client, its secret (returned once,
// stored only as a hash), and records it. Every failure is a
// *ConnectorError; Internal when the record could not be written.
func RegisterClient(ctx context.Context, db *sql.DB, in ClientMetadata, maxRedirects int) (model.OAuthClient, string, error) {
	method, cerr := CheckClientMetadata(in, maxRedirects)
	if cerr != nil {
		return model.OAuthClient{}, "", cerr
	}
	client := model.OAuthClient{
		ClientID:                RandomToken(PrefixClient, 16),
		Name:                    httpx.CleanClientName(in.ClientName),
		RedirectURIs:            in.RedirectURIs,
		TokenEndpointAuthMethod: method,
		PKCERequired:            true,
		Dynamic:                 true,
	}
	var secret string
	if method != "none" {
		secret = RandomToken(PrefixSecret, 32)
		client.SecretHash = sql.NullString{String: accounts.HashSecret(secret), Valid: true}
	}
	if err := InsertOAuthClient(ctx, db, client); err != nil {
		log.Printf("connector: register client: %v", err)
		return model.OAuthClient{}, "", &ConnectorError{Code: "server_error", Internal: true}
	}
	return client, secret, nil
}
