package handler

// The owner's product knowledge server: /brain/mcp (MCP over the connector's
// OAuth) and /v1/brain (the same queries over REST, for agents that only have
// skills). Hosted simple-host.app only, off unless BRAIN_DIR is set. Only the
// platform admin's account may use it; everyone else gets 403 and never sees
// the tool list. Read-only.
//
// Tokens are audience-bound (RFC 8707): an app connecting to /brain/mcp asks
// for resource <apex>/brain/mcp and its token works only there. A token from
// the public connector (resource <apex>/mcp or the apex) is refused here, and
// a brain token is refused at /mcp and on /v1.

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"strings"

	"github.com/vsriram/simple-host/internal/auth"
	"github.com/vsriram/simple-host/internal/brain"
	db "github.com/vsriram/simple-host/internal/db"
	"github.com/vsriram/simple-host/internal/mcp"
)

const brainPath = "/brain/mcp"

// brainOwnerOnly is the one refusal everyone but the owner gets.
const brainOwnerOnly = "This is the Simple Host owner's private product knowledge server. It is not available to other accounts."

// BrainHandler serves the knowledge server.
type BrainHandler struct {
	connector *ConnectorHandler
	store     *brain.Store
	server    *mcp.Server
	tools     []mcp.Tool
}

// EnableBrain turns on the knowledge server, reading the bundle at
// bundlePath, and registers its routes. Call once at startup, after
// connector.Register.
func (h *ConnectorHandler) EnableBrain(mux *http.ServeMux, authMiddleware func(http.Handler) http.Handler, bundlePath string) *BrainHandler {
	store := brain.Open(bundlePath)
	b := &BrainHandler{connector: h, store: store}
	b.server = mcp.NewBrainServer(mcp.BrainConfig{
		ServerName: "simple-host-brain",
		Version:    h.skillVersion,
		Query: func(name string, args map[string]any) (string, map[string]any, error) {
			res, err := store.Call(name, args)
			return res.Text, res.Structured, err
		},
	})
	b.tools = mcp.BrainTools(func(string, map[string]any) (string, map[string]any, error) { return "", nil, nil })
	h.brainResource = h.issuer + brainPath

	mux.HandleFunc("GET /.well-known/oauth-protected-resource/brain/mcp", h.brainResourceMetadata)
	mux.HandleFunc("GET /.well-known/oauth-authorization-server/brain/mcp", h.authorizationServerMetadata)
	// GET and DELETE are answered 405 by the MCP server, after the checks.
	mux.HandleFunc("POST /brain/mcp", b.serveMCP)
	mux.HandleFunc("GET /brain/mcp", b.serveMCP)
	mux.HandleFunc("DELETE /brain/mcp", b.serveMCP)
	mux.Handle("GET /v1/brain", authMiddleware(http.HandlerFunc(b.restIndex)))
	mux.Handle("POST /v1/brain/{tool}", authMiddleware(http.HandlerFunc(b.restCall)))
	log.Printf("brain: owner knowledge server on %s, bundle %s", h.brainResource, bundlePath)
	return b
}

func (h *ConnectorHandler) brainResourceMetadata(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "public, max-age=3600")
	writeJSON(w, http.StatusOK, map[string]any{
		"resource":                 h.brainResource,
		"authorization_servers":    []string{h.issuer},
		"bearer_methods_supported": []string{"header"},
		"scopes_supported":         h.scopesSupported(),
		"resource_name":            "Simple Host product knowledge (owner only)",
	})
}

func (h *ConnectorHandler) brainUnauthorized(w http.ResponseWriter, presented bool) {
	meta := h.issuer + "/.well-known/oauth-protected-resource" + brainPath
	challenge := `Bearer resource_metadata="` + meta + `", scope="` + oauthScope + `"`
	if presented {
		challenge = `Bearer error="invalid_token", error_description="The access token is invalid, expired, revoked or not for this server", resource_metadata="` + meta + `", scope="` + oauthScope + `"`
	}
	w.Header().Set("WWW-Authenticate", challenge)
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "sign in as the Simple Host owner to use this server"})
}

func brainForbidden(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusForbidden, errorResponse{Error: brainOwnerOnly, Code: "owner_only"})
}

// brainOwner is the one rule: the platform admin, as a person. The built-in
// admin-key account is refused by tokenAccess before this.
func brainOwner(u db.User) bool { return u.IsAdmin && !u.Suspended && u.Username != "" }

func (b *BrainHandler) serveMCP(w http.ResponseWriter, r *http.Request) {
	h := b.connector
	token, ok := bearerToken(r)
	if !ok {
		// An API key is not accepted here: this endpoint is for apps that
		// connect with OAuth. Coding agents with a key use /v1/brain.
		h.brainUnauthorized(w, false)
		return
	}
	// Only a token issued for this resource; a public connector token
	// (resource /mcp or the apex) fails the audience check.
	user, _, _, _, valid := h.tokenAccess(r.Context(), token, h.brainResource)
	if !valid && user.Suspended {
		writeAccountSuspended(w)
		return
	}
	if !valid {
		h.brainUnauthorized(w, true)
		return
	}
	if !brainOwner(user) {
		brainForbidden(w)
		return
	}
	// The knowledge server makes no REST calls, so the caller carries no
	// credential of the person's; the marker only satisfies the transport.
	b.server.ServeHTTP(w, r.WithContext(mcp.WithCaller(r.Context(), mcp.Caller{APIKey: "brain", Owner: user.ID})))
}

func (b *BrainHandler) restOwner(w http.ResponseWriter, r *http.Request) bool {
	user := auth.GetUser(r.Context())
	if user == nil {
		writeJSON(w, http.StatusUnauthorized, errorResponse{Error: "unauthorized"})
		return false
	}
	if !user.IsAdmin || user.Suspended {
		brainForbidden(w)
		return false
	}
	return true
}

// restIndex: what the mirror offers and how fresh it is.
func (b *BrainHandler) restIndex(w http.ResponseWriter, r *http.Request) {
	if !b.restOwner(w, r) {
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	tools := []map[string]any{}
	for _, t := range b.tools {
		tools = append(tools, map[string]any{"name": t.Name, "description": t.Description, "input_schema": t.InputSchema})
	}
	out := map[string]any{"instructions": mcp.BrainInstructions, "tools": tools}
	if fresh, err := b.store.Freshness(); err == nil {
		out["freshness"] = fresh
	} else {
		out["freshness"] = nil
		out["error"] = err.Error()
	}
	writeJSON(w, http.StatusOK, out)
}

// restCall runs one tool with a JSON object of arguments, as the MCP tool
// would, and answers {text, result}.
func (b *BrainHandler) restCall(w http.ResponseWriter, r *http.Request) {
	if !b.restOwner(w, r) {
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	name := r.PathValue("tool")
	known := false
	var schema map[string]any
	for _, t := range b.tools {
		if t.Name == name {
			known, schema = true, t.InputSchema
		}
	}
	if !known {
		writeJSON(w, http.StatusNotFound, errorResponse{Error: "unknown brain tool; GET /v1/brain lists them", Code: "unknown_tool"})
		return
	}
	args := map[string]any{}
	body := http.MaxBytesReader(w, r.Body, 64<<10)
	dec := json.NewDecoder(body)
	if err := dec.Decode(&args); err != nil && !errors.Is(err, io.EOF) {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "send a JSON object of the tool's arguments", Code: "invalid_body"})
		return
	}
	if args == nil {
		args = map[string]any{}
	}
	props, _ := schema["properties"].(map[string]any)
	for k := range args {
		if _, ok := props[k]; !ok {
			writeJSON(w, http.StatusBadRequest, errorResponse{Error: "unexpected argument " + k + " for " + name, Code: "invalid_argument"})
			return
		}
	}
	res, err := b.store.Call(name, args)
	if err != nil {
		var ae brain.ArgError
		if errors.As(err, &ae) {
			writeJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error(), Code: "invalid_argument"})
			return
		}
		if errors.Is(err, brain.ErrNotReady) {
			writeJSON(w, http.StatusServiceUnavailable, errorResponse{Error: err.Error(), Code: "not_ready"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"text": res.Text, "result": res.Structured})
}

// isBrainResource reports whether a requested resource names the knowledge
// server (only when it is on).
func (h *ConnectorHandler) isBrainResource(raw string) bool {
	return h.brainResource != "" && strings.EqualFold(strings.TrimRight(raw, "/"), h.brainResource)
}
