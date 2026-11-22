package access

import (
	"context"
	"crypto/subtle"
	"log"

	"github.com/vsriram/simple-host/host/access/repo"
	"github.com/vsriram/simple-host/host/model"
)

// The connector's token and revocation endpoints (RFC 6749, RFC 7009) once
// the request is decoded: clientID and secret are what it presented, by
// Basic or in the body. Every refusal is a *ConnectorError whose Code is the
// OAuth error: invalid_client (401), temporarily_unavailable (503), and an
// Internal one (500); the rest are 400.

// authenticateClient applies RFC 6749 §2.3: a confidential client must prove
// its secret; a public client identifies itself by client_id alone and is
// held to PKCE.
func (h *Connector) authenticateClient(ctx context.Context, clientID, secret string) (model.OAuthClient, error) {
	client, err := repo.GetOAuthClient(ctx, h.database, clientID)
	if err != nil {
		// Hash anyway so an unknown client costs what a known one does.
		_ = HashSecret(secret)
		return model.OAuthClient{}, &ConnectorError{Code: "invalid_client", Description: "unknown client"}
	}
	if client.SecretHash.Valid {
		if secret == "" || subtle.ConstantTimeCompare([]byte(HashSecret(secret)), []byte(client.SecretHash.String)) != 1 {
			return model.OAuthClient{}, &ConnectorError{Code: "invalid_client", Description: "client authentication failed"}
		}
	}
	return client, nil
}

// Token is the token endpoint: the client authenticated and its use noted,
// then grantType (authorization_code or refresh_token) redeemed.
func (h *Connector) Token(ctx context.Context, clientID, secret, grantType string, in ConnectorTokenInput) (ConnectorTokens, error) {
	client, err := h.authenticateClient(ctx, clientID, secret)
	if err != nil {
		return ConnectorTokens{}, err
	}
	_ = repo.TouchOAuthClient(ctx, h.database, client.ClientID)
	switch grantType {
	case "authorization_code":
		return h.RedeemCode(ctx, in, client)
	case "refresh_token":
		return h.Refresh(ctx, in, client)
	case "":
		return ConnectorTokens{}, &ConnectorError{Code: "invalid_request", Description: "grant_type is required"}
	}
	return ConnectorTokens{}, &ConnectorError{Code: "unsupported_grant_type"}
}

// Revoke is RFC 7009: the client authenticated, then token (a refresh
// token's whole grant, or one access token) revoked when it is this
// client's. An unknown token is no refusal, so revocation cannot be used to
// test tokens.
func (h *Connector) Revoke(ctx context.Context, clientID, secret, token string) error {
	client, err := h.authenticateClient(ctx, clientID, secret)
	if err != nil {
		return err
	}
	if token == "" {
		return &ConnectorError{Code: "invalid_request", Description: "token is required"}
	}
	tokenHash := HashSecret(token)
	tok, err := repo.GetOAuthToken(ctx, h.database, tokenHash)
	if err != nil || tok.ClientID != client.ClientID {
		return nil
	}
	if tok.Kind == "refresh" {
		err = repo.DeleteOAuthGrant(ctx, h.database, tok.GrantID)
	} else {
		err = repo.DeleteOAuthToken(ctx, h.database, tokenHash)
	}
	if err != nil {
		log.Printf("connector: revoke: %v", err)
		return &ConnectorError{Code: "temporarily_unavailable"}
	}
	return nil
}
