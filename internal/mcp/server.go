// Package mcp serves the Model Context Protocol over Streamable HTTP, so a chat
// app (ChatGPT, Claude, Grok) can publish and manage Simple Host sites through
// tool calls.
//
// Every tool is an adapter over the REST handler that already exists: a tool
// builds one or more /v1 requests and serves them, in process, into the same
// router the public API uses, carrying the caller's own credential. Nothing
// here reimplements deploy, rollback or state, so an MCP call meets exactly the
// limits, validation and permissions of the equivalent REST call.
//
// Authentication is not this package's job. The caller of ServeHTTP has
// already proved who the request is for (an OAuth access token, or an API key)
// and attached a Caller to the request context with WithCaller. This package
// is a leaf: it imports nothing else from the module.
package mcp

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// protocolVersion is the newest revision this server implements. It is the
// stateless revision: no initialize handshake, a version declared on every
// request. Older, initialize-era revisions are still what most chat apps
// speak, so they are answered in their own idiom (see initializeVersions).
const protocolVersion = "2026-07-28"

// latestInitializeVersion is what an initialize-era client that asks for a
// revision this server does not know is offered instead.
const latestInitializeVersion = "2025-11-25"

// supportedVersions are answered for, newest first.
var supportedVersions = []string{"2026-07-28", "2025-11-25", "2025-06-18", "2025-03-26"}

// Caller is the identity a request acts as. APIKey is the person's own key: it
// is sent only on the in-process REST requests a tool makes, and never appears
// in any output.
type Caller struct {
	APIKey string
}

type callerKey struct{}

// WithCaller attaches the authenticated caller to a request context.
func WithCaller(ctx context.Context, c Caller) context.Context {
	return context.WithValue(ctx, callerKey{}, c)
}

func callerFrom(ctx context.Context) (Caller, bool) {
	c, ok := ctx.Value(callerKey{}).(Caller)
	return c, ok && c.APIKey != ""
}

// Config wires the server to the application.
type Config struct {
	// Upstream is the application router (the bare mux, not the edge
	// middleware). Tool calls are served into it in process.
	Upstream http.Handler
	// APIHost is the Host the in-process requests carry: the apex, which is
	// where agents talk to the API.
	APIHost string
	// ContentOrigin is the shared content host's origin, e.g.
	// https://sites.simple-host.app. Per-site state and collection routes are
	// Origin-gated, and this is the origin every site accepts.
	ContentOrigin string
	// SkillVersion is sent as X-Skill-Version so the REST layer does not
	// append a "your skill is stale" notice to answers meant for this server.
	SkillVersion string
	// ServerName and Version identify this server to clients.
	ServerName string
	Version    string
	// MaxBodyBytes bounds one message. Inline deploys travel in the body, so
	// it sits above the per-site upload limit.
	MaxBodyBytes int64
}

type Server struct {
	cfg    Config
	tools  []Tool
	byName map[string]Tool
}

func NewServer(cfg Config) *Server {
	if cfg.MaxBodyBytes <= 0 {
		cfg.MaxBodyBytes = 16 << 20
	}
	tools := Tools()
	byName := make(map[string]Tool, len(tools))
	for _, tool := range tools {
		byName[tool.Name] = tool
	}
	return &Server{cfg: cfg, tools: tools, byName: byName}
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// No standalone SSE stream and no sessions: every answer is one JSON
	// document on the POST that asked. The transport asks for 405 on GET (and
	// DELETE, which ends a session this server never started).
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	caller, ok := callerFrom(r.Context())
	if !ok {
		// The wrapper authenticates before calling in; reaching here without
		// a caller is a wiring bug, and it must fail closed.
		writeRPC(w, http.StatusUnauthorized, failure(nil, codeInvalidRequest, "not authenticated", nil))
		return
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, s.cfg.MaxBodyBytes))
	if err != nil {
		writeRPC(w, http.StatusRequestEntityTooLarge, failure(nil, codeParseError, "request body could not be read (too large?)", nil))
		return
	}

	var raw json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		writeRPC(w, http.StatusBadRequest, failure(nil, codeParseError, "invalid JSON", nil))
		return
	}
	if trimmed := bytes.TrimSpace(raw); len(trimmed) > 0 && trimmed[0] == '[' {
		writeRPC(w, http.StatusBadRequest, failure(nil, codeInvalidRequest,
			"batched messages are not supported; send one JSON-RPC message per request", nil))
		return
	}

	var req request
	if err := json.Unmarshal(body, &req); err != nil {
		writeRPC(w, http.StatusBadRequest, failure(nil, codeInvalidRequest, "not a JSON-RPC 2.0 request object", nil))
		return
	}
	if !req.wellFormed() {
		// A JSON-RPC response (a client answering a server request) is valid
		// traffic on this transport; this server never sends requests, so it
		// is acknowledged and ignored.
		if req.Method == "" && len(req.ID) > 0 && bytes.Contains(body, []byte(`"result"`)) {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		writeRPC(w, http.StatusBadRequest, failure(req.ID, codeInvalidRequest,
			"a JSON-RPC 2.0 request needs jsonrpc:\"2.0\" and a method", nil))
		return
	}
	if req.hasNullID() {
		writeRPC(w, http.StatusBadRequest, failure(nil, codeInvalidRequest, "id must not be null", nil))
		return
	}

	if status, rpcErr := s.validate(r, req); rpcErr != nil {
		id := req.ID
		if req.isNotification() {
			id = nil
		}
		writeRPC(w, status, failure(id, rpcErr.Code, rpcErr.Message, rpcErr.Data))
		return
	}
	// A notification is acknowledged with 202 and no body, whatever it names.
	if req.isNotification() {
		w.WriteHeader(http.StatusAccepted)
		return
	}

	resp := s.dispatch(r, req, caller)
	status := http.StatusOK
	if resp.Error != nil && resp.Error.Code == codeMethodNotFound && s.isModern(r, req) {
		// The stateless revision distinguishes an unimplemented method from a
		// server with no MCP endpoint by a 404 carrying a JSON-RPC body. An
		// initialize-era client reads 404 as "session gone", so it gets 200.
		status = http.StatusNotFound
	}
	writeRPC(w, status, resp)
}

// requestedVersion is the revision a request declares, by header or in _meta.
func requestedVersion(r *http.Request, req request) string {
	if v := strings.TrimSpace(r.Header.Get("MCP-Protocol-Version")); v != "" {
		return v
	}
	return metaProtocolVersion(req.Params)
}

func (s *Server) isModern(r *http.Request, req request) bool {
	return requestedVersion(r, req) == protocolVersion && req.Method != "initialize"
}

// validate enforces the header/body agreement the transport requires.
func (s *Server) validate(r *http.Request, req request) (int, *rpcError) {
	declared := strings.TrimSpace(r.Header.Get("MCP-Protocol-Version"))
	inBody := metaProtocolVersion(req.Params)

	if declared != "" && inBody != "" && declared != inBody && req.Method != "initialize" {
		return http.StatusBadRequest, &rpcError{
			Code:    codeHeaderMismatch,
			Message: fmt.Sprintf("MCP-Protocol-Version header %q does not match the body's %q", declared, inBody),
		}
	}
	requested := declared
	if requested == "" {
		requested = inBody
	}
	// initialize negotiates, so an unknown version there is answered with one
	// this server speaks rather than refused.
	if requested != "" && !supported(requested) && req.Method != "initialize" {
		return http.StatusBadRequest, &rpcError{
			Code:    codeUnsupportedVersion,
			Message: "Unsupported protocol version",
			Data:    map[string]any{"supported": supportedVersions, "requested": requested},
		}
	}

	// Only a client declaring the stateless revision is held to its header
	// and _meta rules. An initialize-era client predates both.
	modern := requested == protocolVersion && req.Method != "initialize"

	method := strings.TrimSpace(r.Header.Get("Mcp-Method"))
	if method == "" && modern {
		return http.StatusBadRequest, &rpcError{Code: codeHeaderMismatch, Message: "Mcp-Method header is required"}
	}
	if method != "" && method != req.Method {
		return http.StatusBadRequest, &rpcError{
			Code:    codeHeaderMismatch,
			Message: fmt.Sprintf("Mcp-Method header %q does not match the body's %q", method, req.Method),
		}
	}

	if req.Method == "tools/call" {
		params, err := parseCallParams(req.Params)
		if err != nil {
			return http.StatusBadRequest, &rpcError{Code: codeInvalidParams, Message: "params could not be read"}
		}
		name := strings.TrimSpace(r.Header.Get("Mcp-Name"))
		if name == "" && modern {
			return http.StatusBadRequest, &rpcError{Code: codeHeaderMismatch, Message: "Mcp-Name header is required on tools/call"}
		}
		if name != "" && decodeHeaderValue(name) != params.Name {
			return http.StatusBadRequest, &rpcError{
				Code:    codeHeaderMismatch,
				Message: fmt.Sprintf("Mcp-Name header %q does not match the tool being called, %q", decodeHeaderValue(name), params.Name),
			}
		}
	}

	if modern && !req.isNotification() && !hasClientCapabilities(req.Params) {
		return http.StatusBadRequest, &rpcError{
			Code:    codeInvalidParams,
			Message: "params._meta must carry io.modelcontextprotocol/clientCapabilities",
		}
	}
	return http.StatusOK, nil
}

func parseCallParams(raw json.RawMessage) (callParams, error) {
	var params callParams
	if len(raw) == 0 {
		return params, nil
	}
	err := json.Unmarshal(raw, &params)
	return params, err
}

func hasClientCapabilities(params json.RawMessage) bool {
	if len(params) == 0 {
		return false
	}
	var envelope struct {
		Meta map[string]json.RawMessage `json:"_meta"`
	}
	if err := json.Unmarshal(params, &envelope); err != nil {
		return false
	}
	_, present := envelope.Meta["io.modelcontextprotocol/clientCapabilities"]
	return present
}

func (s *Server) serverInfo() map[string]any {
	return map[string]any{"name": s.cfg.ServerName, "title": "Simple Host", "version": s.cfg.Version}
}

func (s *Server) dispatch(r *http.Request, req request, caller Caller) response {
	modern := s.isModern(r, req)
	switch req.Method {
	case "initialize":
		if strings.TrimSpace(r.Header.Get("MCP-Protocol-Version")) == protocolVersion {
			return failure(req.ID, codeMethodNotFound, "initialize is not part of "+protocolVersion+"; send requests directly", nil)
		}
		return result(req.ID, map[string]any{
			"protocolVersion": negotiateInitialize(metaProtocolVersion(req.Params)),
			"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
			"serverInfo":      s.serverInfo(),
			"instructions":    Instructions,
		})

	// ping is a keepalive in every revision that has it, and a failure reads
	// as a dead connection, so it is always answered.
	case "ping":
		if modern {
			return result(req.ID, map[string]any{"resultType": "complete"})
		}
		return result(req.ID, map[string]any{})

	case "server/discover":
		return result(req.ID, s.withMeta(map[string]any{
			"resultType":        "complete",
			"supportedVersions": supportedVersions,
			"capabilities":      map[string]any{"tools": map[string]any{"listChanged": false}},
			"serverInfo":        s.serverInfo(),
			"instructions":      Instructions,
			"ttlMs":             cacheTTLMillis,
			"cacheScope":        "private",
		}))

	case "tools/list":
		if modern {
			return result(req.ID, s.withMeta(map[string]any{
				"resultType": "complete",
				"tools":      s.tools,
				"ttlMs":      cacheTTLMillis,
				"cacheScope": "private",
			}))
		}
		return result(req.ID, map[string]any{"tools": s.tools})

	// Nothing but tools is offered. Some clients list these regardless of
	// the declared capabilities; an empty list is the true answer and keeps
	// them from treating the connector as broken.
	case "resources/list":
		return result(req.ID, map[string]any{"resources": []any{}})
	case "resources/templates/list":
		return result(req.ID, map[string]any{"resourceTemplates": []any{}})
	case "prompts/list":
		return result(req.ID, map[string]any{"prompts": []any{}})

	case "tools/call":
		return s.callTool(r, req, caller, modern)

	default:
		return failure(req.ID, codeMethodNotFound, "unknown method: "+req.Method, nil)
	}
}

// cacheTTLMillis is how long a client may reuse a discovery or tool listing.
const cacheTTLMillis = 300000

func (s *Server) withMeta(payload map[string]any) map[string]any {
	payload["_meta"] = map[string]any{"io.modelcontextprotocol/serverInfo": s.serverInfo()}
	return payload
}

type callParams struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
}

func (s *Server) callTool(r *http.Request, req request, caller Caller, modern bool) response {
	params, err := parseCallParams(req.Params)
	if err != nil {
		return failure(req.ID, codeInvalidParams, "params could not be read", nil)
	}
	tool, known := s.byName[params.Name]
	if !known {
		return failure(req.ID, codeInvalidParams, "unknown tool: "+params.Name, nil)
	}
	if params.Arguments == nil {
		params.Arguments = map[string]any{}
	}

	if err := unexpectedArgument(tool, params.Arguments); err != nil {
		return toolResult(req.ID, err.Error(), nil, true, modern)
	}
	c := &call{server: s, orig: r, caller: caller}
	out, err := tool.run(c, params.Arguments)
	if err != nil {
		// A bad argument or a refused REST call is the model's to correct, so
		// it comes back as a failed tool result it can read, not a protocol
		// error that ends the turn.
		return toolResult(req.ID, err.Error(), nil, true, modern)
	}
	return toolResult(req.ID, out.Text, out.Structured, false, modern)
}

// unexpectedArgument refuses an argument the tool's schema does not declare.
// Every schema is closed (additionalProperties: false), and ignoring a stray
// argument would let a call quietly mean something other than what the model
// asked (e.g. a mode it thought it was choosing).
func unexpectedArgument(tool Tool, args map[string]any) error {
	props, _ := tool.InputSchema["properties"].(map[string]any)
	for key := range args {
		if _, ok := props[key]; !ok {
			return fmt.Errorf("unexpected argument %q for %s; it takes only the arguments in its schema", key, tool.Name)
		}
	}
	return nil
}

func toolResult(id json.RawMessage, text string, structured map[string]any, isError, modern bool) response {
	payload := map[string]any{
		"content": []any{map[string]any{"type": "text", "text": text}},
		"isError": isError,
	}
	if structured != nil {
		payload["structuredContent"] = structured
	}
	if modern {
		payload["resultType"] = "complete"
	}
	return result(id, payload)
}

func metaProtocolVersion(params json.RawMessage) string {
	if len(params) == 0 {
		return ""
	}
	var envelope struct {
		Meta map[string]any `json:"_meta"`
	}
	if err := json.Unmarshal(params, &envelope); err == nil {
		if value, ok := envelope.Meta["io.modelcontextprotocol/protocolVersion"].(string); ok {
			return value
		}
	}
	// initialize carries it as a plain field rather than in _meta.
	var legacy struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	if err := json.Unmarshal(params, &legacy); err == nil {
		return legacy.ProtocolVersion
	}
	return ""
}

// negotiateInitialize answers an initialize: the client's version when this
// server speaks it, otherwise the newest initialize-era revision.
func negotiateInitialize(requested string) string {
	if requested != protocolVersion && supported(requested) {
		return requested
	}
	return latestInitializeVersion
}

func supported(version string) bool {
	for _, candidate := range supportedVersions {
		if candidate == version {
			return true
		}
	}
	return false
}

// decodeHeaderValue unwraps the base64 sentinel the transport uses for values
// that cannot travel as plain ASCII.
func decodeHeaderValue(value string) string {
	if !strings.HasPrefix(value, "=?base64?") || !strings.HasSuffix(value, "?=") {
		return value
	}
	encoded := strings.TrimSuffix(strings.TrimPrefix(value, "=?base64?"), "?=")
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return value
	}
	return string(decoded)
}

func writeRPC(w http.ResponseWriter, status int, resp response) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(resp)
}
