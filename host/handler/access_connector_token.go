package handler

import (
	"context"
	"crypto/subtle"
	"errors"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	accounts "github.com/vsriram/simple-host/host/access"
	"github.com/vsriram/simple-host/host/access/auth"
	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/mcp"
	"github.com/vsriram/simple-host/host/model"
)

// ---- token endpoint ----------------------------------------------------------------

// authenticateClient applies RFC 6749 §2.3 to a token or revocation request.
// A confidential client must prove its secret (Basic or in the body); a
// public client identifies itself by client_id alone and is held to PKCE.
func (h *ConnectorHandler) authenticateClient(r *http.Request) (model.OAuthClient, bool, string) {
	formID, formSecret := r.PostForm.Get("client_id"), r.PostForm.Get("client_secret")
	basicID, basicSecret, hasBasic := r.BasicAuth()
	if hasBasic {
		// Basic credentials are form-encoded before base64 (RFC 6749 §2.3.1).
		if v, err := url.QueryUnescape(basicID); err == nil {
			basicID = v
		}
		if v, err := url.QueryUnescape(basicSecret); err == nil {
			basicSecret = v
		}
		if formSecret != "" || (formID != "" && formID != basicID) {
			return model.OAuthClient{}, false, "use one client authentication method"
		}
		formID, formSecret = basicID, basicSecret
	}
	if formID == "" {
		return model.OAuthClient{}, false, "client_id is required"
	}
	client, err := h.accountsSvc().GetOAuthClient(r.Context(), formID)
	if err != nil {
		// Hash anyway so an unknown client costs what a known one does.
		_ = hashSecret(formSecret)
		return model.OAuthClient{}, false, "unknown client"
	}
	if client.SecretHash.Valid {
		if formSecret == "" || subtle.ConstantTimeCompare([]byte(hashSecret(formSecret)), []byte(client.SecretHash.String)) != 1 {
			return model.OAuthClient{}, false, "client authentication failed"
		}
	}
	return client, true, ""
}

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
	client, ok, why := h.authenticateClient(r)
	if !ok {
		if _, _, basic := r.BasicAuth(); basic {
			w.Header().Set("WWW-Authenticate", `Basic realm="simple-host"`)
		}
		oauthError(w, http.StatusUnauthorized, "invalid_client", why)
		return
	}
	_ = h.accountsSvc().TouchOAuthClient(r.Context(), client.ClientID)
	switch r.PostForm.Get("grant_type") {
	case "authorization_code":
		h.redeemCode(w, r, client)
	case "refresh_token":
		h.refresh(w, r, client)
	case "":
		oauthError(w, http.StatusBadRequest, "invalid_request", "grant_type is required")
	default:
		oauthError(w, http.StatusBadRequest, "unsupported_grant_type", "")
	}
}

func (h *ConnectorHandler) redeemCode(w http.ResponseWriter, r *http.Request, client model.OAuthClient) {
	reply, err := h.connectorSvc().RedeemCode(r.Context(), accounts.ConnectorTokenInput{Code: r.PostForm.Get("code"), RedirectURI: r.PostForm.Get("redirect_uri"), Verifier: r.PostForm.Get("code_verifier"), Resource: r.PostForm.Get("resource"), RefreshToken: r.PostForm.Get("refresh_token"), Scope: r.PostForm.Get("scope")}, client)
	h.writeConnectorTokens(w, reply, err)
}

func (h *ConnectorHandler) refresh(w http.ResponseWriter, r *http.Request, client model.OAuthClient) {
	reply, err := h.connectorSvc().Refresh(r.Context(), accounts.ConnectorTokenInput{Code: r.PostForm.Get("code"), RedirectURI: r.PostForm.Get("redirect_uri"), Verifier: r.PostForm.Get("code_verifier"), Resource: r.PostForm.Get("resource"), RefreshToken: r.PostForm.Get("refresh_token"), Scope: r.PostForm.Get("scope")}, client)
	h.writeConnectorTokens(w, reply, err)
}

// revoke is RFC 7009. It answers 200 whether or not the token was known, so it
// cannot be used to test tokens.
func (h *ConnectorHandler) revoke(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if err := r.ParseForm(); err != nil {
		oauthError(w, http.StatusBadRequest, "invalid_request", "")
		return
	}
	client, ok, why := h.authenticateClient(r)
	if !ok {
		oauthError(w, http.StatusUnauthorized, "invalid_client", why)
		return
	}
	token := r.PostForm.Get("token")
	w.Header().Set("Cache-Control", "no-store")
	if token == "" {
		oauthError(w, http.StatusBadRequest, "invalid_request", "token is required")
		return
	}
	tokenHash := hashSecret(token)
	if tok, err := h.accountsSvc().GetOAuthToken(r.Context(), tokenHash); err == nil && tok.ClientID == client.ClientID {
		if tok.Kind == "refresh" {
			err = h.accountsSvc().DeleteOAuthGrant(r.Context(), tok.GrantID)
		} else {
			err = h.accountsSvc().DeleteOAuthToken(r.Context(), tokenHash)
		}
		if err != nil {
			log.Printf("connector: revoke: %v", err)
			oauthError(w, http.StatusServiceUnavailable, "temporarily_unavailable", "")
			return
		}
	}
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
		if !ok || !strings.HasPrefix(token, prefixAccess) {
			next.ServeHTTP(w, r)
			return
		}
		user, ok := h.userForAccessToken(r.Context(), token, h.issuer)
		if !ok && user.Suspended {
			auth.WriteSuspended(w, "", "")
			return
		}
		if !ok {
			w.Header().Set("WWW-Authenticate", `Bearer error="invalid_token", resource_metadata="`+h.issuer+`/.well-known/oauth-protected-resource"`)
			writeJSON(w, http.StatusUnauthorized, map[string]string{
				"error": "the access token is invalid, expired or revoked; refresh it or reconnect",
				"code":  "invalid_token",
			})
			return
		}
		key, revoke, err := h.accountsSvc().InternalKeyFor(user.ID)
		if err != nil {
			writeInternal(w)
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

func (h *ConnectorHandler) mcpUnauthorized(w http.ResponseWriter, presented bool) {
	code := "sign_in_required"
	challenge := `Bearer resource_metadata="` + h.issuer + `/.well-known/oauth-protected-resource/mcp", scope="` + auth.OAuthScope + `"`
	if presented {
		code = "invalid_token"
		challenge = `Bearer error="invalid_token", error_description="The access token is invalid, expired or revoked", resource_metadata="` +
			h.issuer + `/.well-known/oauth-protected-resource/mcp", scope="` + auth.OAuthScope + `"`
	}
	w.Header().Set("WWW-Authenticate", challenge)
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusUnauthorized, errorResponse{Error: "sign in to Simple Host to use this connector", Code: code})
}

func (h *ConnectorHandler) serveMCP(w http.ResponseWriter, r *http.Request) {
	var apiKey, mode, grantID, userID, owner, oauthGrantID, appName, accountHandle string
	var deployOnly bool
	var credentialExpiresAt time.Time
	var mintUpload func(context.Context) (string, func(), error)
	var mintPhoto func(context.Context) (string, func(), error)
	if token, ok := bearerToken(r); ok {
		user, grantMode, grant, valid := h.tokenAccess(r.Context(), token, h.mcpResource, h.issuer)
		if !valid && user.Suspended {
			auth.WriteSuspended(w, "", "")
			return
		}
		if !valid {
			h.mcpUnauthorized(w, true)
			return
		}
		key, revoke, err := h.accountsSvc().InternalKeyFor(user.ID)
		if err != nil {
			writeInternal(w)
			return
		}
		defer revoke()
		// The minted key stands for the connector, not the person: mark the
		// request so REST calls bridged from MCP are refused where a
		// connected app must never act (isConnectorRequest).
		r = r.WithContext(context.WithValue(r.Context(), connectorTokenKey{}, true))
		apiKey, mode, owner = key, grantMode, user.ID
		accountHandle = user.Handle.String
		oauthGrantID = grant.GrantID
		credentialExpiresAt = grant.ExpiresAt
		client, err := h.accountsSvc().GetOAuthClient(r.Context(), grant.ClientID)
		if err != nil {
			writeInternal(w)
			return
		}
		appName = httpx.CleanClientName(client.Name)
		mintPhoto = func(ctx context.Context) (string, func(), error) {
			return h.accountsSvc().PhotoCredential(ctx, grant.GrantID, user.ID, grant.ClientID, h.mcpResource)
		}
		mintUpload = func(ctx context.Context) (string, func(), error) {
			u, _, _, valid := h.tokenAccess(ctx, token, h.mcpResource, h.issuer)
			if !valid {
				return "", nil, errors.New("upload sign-in ended")
			}
			return h.accountsSvc().InternalKeyFor(u.ID)
		}
		if h.teamConnections && grantMode == mcp.CallerModeEvents {
			grantID, userID = grant.GrantID, user.ID
		}
	} else if key := r.Header.Get("X-API-Key"); key != "" {
		// Coding agents that already hold a key can use the endpoint too.
		// The key's own scope is unchanged. On the hackathon platform a
		// personal key is the same identity as an events grant, so it sees
		// the event tools.
		if subtle.ConstantTimeCompare([]byte(key), []byte(h.adminAPIKey)) != 1 {
			if u, err := h.accountsSvc().GetUserByAPIKey(r.Context(), key, h.limits.KeyIdleExpiry); err != nil {
				if errors.Is(err, model.ErrAccountSuspended) {
					auth.WriteSuspended(w, "", "")
					return
				}
				if errors.Is(err, model.ErrKeyExpired) || errors.Is(err, model.ErrKeyExpiredIdle) {
					// Say why, with no OAuth challenge: the caller holds a key
					// and needs a new one, not a sign-in flow.
					msg, code := auth.ExpiredKeyMessage(err, u.KeyExpiresAt, h.limits.KeyIdleExpiry)
					w.Header().Set("Cache-Control", "no-store")
					writeJSON(w, http.StatusUnauthorized, errorResponse{Error: msg, Code: code})
					return
				}
				h.mcpUnauthorized(w, true)
				return
			} else {
				owner = u.ID
				accountHandle = u.Handle.String
				if u.KeyExpiresAt != nil {
					credentialExpiresAt = *u.KeyExpiresAt
				}
				deployOnly = u.DeployOnly()
				if h.teamConnections && !deployOnly {
					mode = mcp.CallerModeEvents
				}
			}
		}
		apiKey = key
		mintUpload = func(ctx context.Context) (string, func(), error) {
			if subtle.ConstantTimeCompare([]byte(key), []byte(h.adminAPIKey)) != 1 {
				if _, err := h.accountsSvc().GetUserByAPIKey(ctx, key, h.limits.KeyIdleExpiry); err != nil {
					return "", nil, err
				}
			}
			return key, func() {}, nil
		}
	} else {
		h.mcpUnauthorized(w, false)
		return
	}
	h.mcp.ServeHTTP(w, r.WithContext(mcp.WithCaller(r.Context(), mcp.Caller{
		APIKey: apiKey, Mode: mode, GrantID: grantID, UserID: userID, Owner: owner, MintUpload: mintUpload,
		OAuthGrantID: oauthGrantID, AppName: appName, DeployOnly: deployOnly,
		CredentialExpiresAt: credentialExpiresAt, MintPhoto: mintPhoto, Handle: accountHandle,
	})))
}
