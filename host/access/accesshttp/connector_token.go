package accesshttp

import (
	"context"
	"crypto/subtle"
	"errors"
	"net/http"
	"net/url"
	"strings"

	authrules "github.com/vsriram/simple-host/host/access/rules"

	accounts "github.com/vsriram/simple-host/host/access"
	"github.com/vsriram/simple-host/host/access/auth"
	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/mcp"
	"github.com/vsriram/simple-host/host/model"
)

// ---- token endpoint ----------------------------------------------------------------

// clientCredentials is the client a token or revocation request presents
// (RFC 6749 §2.3): Basic credentials, form-encoded before base64 (§2.3.1),
// or client_id and client_secret in the body. why says what is wrong with a
// request that names no client or uses both methods.
func clientCredentials(r *http.Request) (id, secret, why string) {
	id, secret = r.PostForm.Get("client_id"), r.PostForm.Get("client_secret")
	basicID, basicSecret, hasBasic := r.BasicAuth()
	if hasBasic {
		if v, err := url.QueryUnescape(basicID); err == nil {
			basicID = v
		}
		if v, err := url.QueryUnescape(basicSecret); err == nil {
			basicSecret = v
		}
		if secret != "" || (id != "" && id != basicID) {
			return "", "", "use one client authentication method"
		}
		id, secret = basicID, basicSecret
	}
	if id == "" {
		return "", "", "client_id is required"
	}
	return id, secret, ""
}

// token is POST /oauth/token (accounts.Connector.Token).
func (h *ConnectorHandler) token(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if err := r.ParseForm(); err != nil {
		oauthError(w, http.StatusBadRequest, "invalid_request", "body must be application/x-www-form-urlencoded")
		return
	}
	for k, v := range r.PostForm {
		if len(v) > 1 {
			oauthError(w, http.StatusBadRequest, "invalid_request", "parameter "+k+" is repeated")
			return
		}
	}
	id, secret, why := clientCredentials(r)
	var reply accounts.ConnectorTokens
	err := error(&accounts.ConnectorError{Code: "invalid_client", Description: why})
	if why == "" {
		f := r.PostForm
		in := accounts.ConnectorTokenInput{Code: f.Get("code"), RedirectURI: f.Get("redirect_uri"), Verifier: f.Get("code_verifier"), Resource: f.Get("resource"), RefreshToken: f.Get("refresh_token"), Scope: f.Get("scope")}
		reply, err = h.connectorSvc().Token(r.Context(), id, secret, f.Get("grant_type"), in)
	}
	if _, _, basic := r.BasicAuth(); basic && connectorErrorStatus(err) == http.StatusUnauthorized {
		w.Header().Set("WWW-Authenticate", `Basic realm="simple-host"`)
	}
	h.writeConnectorTokens(w, reply, err)
}

// revoke is RFC 7009 (accounts.Connector.Revoke). It answers 200 whether or
// not the token was known, so it cannot be used to test tokens.
func (h *ConnectorHandler) revoke(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if err := r.ParseForm(); err != nil {
		oauthError(w, http.StatusBadRequest, "invalid_request", "")
		return
	}
	id, secret, why := clientCredentials(r)
	err := error(&accounts.ConnectorError{Code: "invalid_client", Description: why})
	if why == "" {
		err = h.connectorSvc().Revoke(r.Context(), id, secret, r.PostForm.Get("token"))
	}
	if err != nil {
		writeConnectorError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
}

// ---- bearer tokens as identity -------------------------------------------------------

// userForAccessToken resolves an access token to its person. audiences lists
// the grant resources acceptable where it is being presented.
func (h *ConnectorHandler) userForAccessToken(ctx context.Context, token string, audiences ...string) (model.User, bool) {
	u, _, _, ok := h.tokenAccess(ctx, token, audiences...)
	return u, ok
}

// tokenAccess resolves an access token. mode is mcp.CallerModeEvents for a
// product connection and "" otherwise. A malformed product grant cannot
// authenticate.
func (h *ConnectorHandler) tokenAccess(ctx context.Context, token string, audiences ...string) (model.User, string, model.OAuthTokenGrant, bool) {
	return h.accountsSvc().ConnectorAccess(ctx, token, h.now, h.teamConnections, audiences...)
}

func bearerToken(r *http.Request) (string, bool) {
	authz := r.Header.Get("Authorization")
	if len(authz) < 7 || !strings.EqualFold(authz[:7], "bearer ") {
		return "", false
	}
	return strings.TrimSpace(authz[7:]), true
}

// BearerAuth lets a connector access token stand in for the person's API key
// on the REST API. The token is exchanged in process for an internal
// credential that resolves to the same person (their key itself is stored
// only as a hash), so the request then meets exactly the checks, limits and
// permissions a request with their key meets. A bearer value that is not one of this server's
// tokens is left alone (the key middleware then explains X-API-Key).
func (h *ConnectorHandler) BearerAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/v1/") || r.Header.Get("X-API-Key") != "" {
			next.ServeHTTP(w, r)
			return
		}
		token, ok := bearerToken(r)
		if !ok || !strings.HasPrefix(token, accounts.PrefixAccess) {
			next.ServeHTTP(w, r)
			return
		}
		user, ok := h.userForAccessToken(r.Context(), token, h.Issuer)
		if !ok && user.Suspended {
			auth.WriteSuspended(w, "", "")
			return
		}
		if !ok {
			w.Header().Set("WWW-Authenticate", `Bearer error="invalid_token", resource_metadata="`+h.Issuer+`/.well-known/oauth-protected-resource"`)
			httpx.WriteJSON(w, http.StatusUnauthorized, map[string]string{
				"error": "the access token is invalid, expired or revoked; refresh it or reconnect",
				"code":  "invalid_token",
			})
			return
		}
		key, revoke, err := h.accountsSvc().InternalKeyFor(user.ID)
		if err != nil {
			httpx.WriteInternal(w)
			return
		}
		defer revoke()
		// Marked so a route that must refuse a connected app (changing the
		// account's own password) can tell this in-process key apart from
		// one the person actually typed in (isConnectorRequest).
		r2 := r.Clone(context.WithValue(r.Context(), connectorTokenKey{}, true))
		r2.Header.Del("Authorization")
		r2.Header.Set("X-API-Key", key)
		next.ServeHTTP(w, r2)
	})
}

// connectorTokenKey marks a request whose X-API-Key was minted in process
// from a connector's OAuth access token (BearerAuth above), not sent by the
// person. isConnectorRequest reads it; checked by routes a connected app
// must never reach even though it otherwise carries the person's full REST
// permissions, such as changing the account's own password.
type connectorTokenKey struct{}

func isConnectorRequest(ctx context.Context) bool {
	v, _ := ctx.Value(connectorTokenKey{}).(bool)
	return v
}

// ---- /mcp -------------------------------------------------------------------------------

func (h *ConnectorHandler) MCPUnauthorized(w http.ResponseWriter, presented bool) {
	code := "sign_in_required"
	challenge := `Bearer resource_metadata="` + h.Issuer + `/.well-known/oauth-protected-resource/mcp", scope="` + authrules.OAuthScope + `"`
	if presented {
		code = "invalid_token"
		challenge = `Bearer error="invalid_token", error_description="The access token is invalid, expired or revoked", resource_metadata="` +
			h.Issuer + `/.well-known/oauth-protected-resource/mcp", scope="` + authrules.OAuthScope + `"`
	}
	w.Header().Set("WWW-Authenticate", challenge)
	w.Header().Set("Cache-Control", "no-store")
	httpx.WriteJSON(w, http.StatusUnauthorized, httpx.ErrorResponse{Error: "sign in to Simple Host to use this connector", Code: code})
}

// serveMCP serves the connector's MCP endpoint as the caller a connector
// token or an API key names; anything else is asked to sign in.
func (h *ConnectorHandler) serveMCP(w http.ResponseWriter, r *http.Request) {
	var caller mcp.Caller
	if token, ok := bearerToken(r); ok {
		var revoke func()
		if r, caller, revoke, ok = h.tokenCaller(w, r, token); !ok {
			return
		}
		defer revoke()
	} else if key := r.Header.Get("X-API-Key"); key != "" {
		if caller, ok = h.keyCaller(w, r, key); !ok {
			return
		}
	} else {
		h.MCPUnauthorized(w, false)
		return
	}
	h.mcp.ServeHTTP(w, r.WithContext(mcp.WithCaller(r.Context(), caller)))
}

// tokenCaller is the caller a connector token stands for: the person, with
// a minted key (revoke ends it) that stands for the connector, not the
// person. The request is marked so REST calls bridged from MCP are refused
// where a connected app must never act (isConnectorRequest). It writes the
// refusal and reports false when the request stops here.
func (h *ConnectorHandler) tokenCaller(w http.ResponseWriter, r *http.Request, token string) (*http.Request, mcp.Caller, func(), bool) {
	user, grantMode, grant, valid := h.tokenAccess(r.Context(), token, h.mcpResource, h.Issuer)
	if !valid && user.Suspended {
		auth.WriteSuspended(w, "", "")
		return r, mcp.Caller{}, nil, false
	}
	if !valid {
		h.MCPUnauthorized(w, true)
		return r, mcp.Caller{}, nil, false
	}
	key, revoke, err := h.accountsSvc().InternalKeyFor(user.ID)
	if err != nil {
		httpx.WriteInternal(w)
		return r, mcp.Caller{}, nil, false
	}
	r = r.WithContext(context.WithValue(r.Context(), connectorTokenKey{}, true))
	client, err := h.accountsSvc().GetOAuthClient(r.Context(), grant.ClientID)
	if err != nil {
		revoke()
		httpx.WriteInternal(w)
		return r, mcp.Caller{}, nil, false
	}
	caller := mcp.Caller{
		APIKey: key, Mode: grantMode, Owner: user.ID, Handle: user.Handle.String,
		OAuthGrantID: grant.GrantID, CredentialExpiresAt: grant.ExpiresAt, AppName: httpx.CleanClientName(client.Name),
		MintPhoto: func(ctx context.Context) (string, func(), error) {
			return h.accountsSvc().PhotoCredential(ctx, grant.GrantID, user.ID, grant.ClientID, h.mcpResource)
		},
		MintUpload: func(ctx context.Context) (string, func(), error) {
			u, _, _, valid := h.tokenAccess(ctx, token, h.mcpResource, h.Issuer)
			if !valid {
				return "", nil, errors.New("upload sign-in ended")
			}
			return h.accountsSvc().InternalKeyFor(u.ID)
		},
	}
	if h.teamConnections && grantMode == mcp.CallerModeEvents {
		caller.GrantID, caller.UserID = grant.GrantID, user.ID
	}
	return r, caller, revoke, true
}

// keyCaller is the caller an API key stands for: coding agents that already
// hold a key can use the endpoint too, with the key's own scope unchanged.
// On the hackathon platform a personal key is the same identity as an
// events grant, so it sees the event tools. It writes the refusal and
// reports false when the request stops here.
func (h *ConnectorHandler) keyCaller(w http.ResponseWriter, r *http.Request, key string) (mcp.Caller, bool) {
	caller := mcp.Caller{APIKey: key, MintUpload: func(ctx context.Context) (string, func(), error) {
		if subtle.ConstantTimeCompare([]byte(key), []byte(h.adminAPIKey)) != 1 {
			if _, err := h.accountsSvc().GetUserByAPIKey(ctx, key, h.Limits.KeyIdleExpiry); err != nil {
				return "", nil, err
			}
		}
		return key, func() {}, nil
	}}
	if subtle.ConstantTimeCompare([]byte(key), []byte(h.adminAPIKey)) == 1 {
		return caller, true
	}
	u, err := h.accountsSvc().GetUserByAPIKey(r.Context(), key, h.Limits.KeyIdleExpiry)
	if err != nil {
		h.writeKeyCallerRefused(w, err, u)
		return caller, false
	}
	caller.Owner, caller.Handle = u.ID, u.Handle.String
	if u.KeyExpiresAt != nil {
		caller.CredentialExpiresAt = *u.KeyExpiresAt
	}
	caller.DeployOnly = u.DeployOnly()
	if h.teamConnections && !caller.DeployOnly {
		caller.Mode = mcp.CallerModeEvents
	}
	return caller, true
}

// writeKeyCallerRefused answers a key that signs no one in. An expired key
// is told why, with no OAuth challenge: the caller holds a key and needs a
// new one, not a sign-in flow.
func (h *ConnectorHandler) writeKeyCallerRefused(w http.ResponseWriter, err error, u model.User) {
	switch {
	case errors.Is(err, model.ErrAccountSuspended):
		auth.WriteSuspended(w, "", "")
	case errors.Is(err, model.ErrKeyExpired) || errors.Is(err, model.ErrKeyExpiredIdle):
		msg, code := auth.ExpiredKeyMessage(err, u.KeyExpiresAt, h.Limits.KeyIdleExpiry)
		w.Header().Set("Cache-Control", "no-store")
		httpx.WriteJSON(w, http.StatusUnauthorized, httpx.ErrorResponse{Error: msg, Code: code})
	default:
		h.MCPUnauthorized(w, true)
	}
}
