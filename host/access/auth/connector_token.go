package auth

import "context"

// connectorTokenKey marks a request whose X-API-Key was minted in process
// from a connector's OAuth access token (the connector's BearerAuth), not
// sent by the person. IsConnectorRequest reads it; checked by routes a
// connected app must never reach even though it otherwise carries the
// person's full REST permissions, such as changing the account's own
// password.
type connectorTokenKey struct{}

// WithConnectorToken marks ctx as a connector-token request.
func WithConnectorToken(ctx context.Context) context.Context {
	return context.WithValue(ctx, connectorTokenKey{}, true)
}

// IsConnectorRequest reports whether ctx is a connector-token request.
func IsConnectorRequest(ctx context.Context) bool {
	v, _ := ctx.Value(connectorTokenKey{}).(bool)
	return v
}
