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
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"
)

// protocolVersion is the newest revision this server implements. It is the
// stateless revision: no initialize handshake, a version declared on every
// request. Older, initialize-era revisions are still what most chat apps
// speak, so they are answered in their own idiom (see initializeVersions).
const protocolVersion = "2026-07-28"

// ProtocolVersion is the newest MCP revision implemented by this server.
const ProtocolVersion = protocolVersion

// latestInitializeVersion is what an initialize-era client that asks for a
// revision this server does not know is offered instead.
const latestInitializeVersion = "2025-11-25"

// supportedVersions are answered for, newest first.
var supportedVersions = []string{"2026-07-28", "2025-11-25", "2025-06-18", "2025-03-26"}

// CallerModeEvents is a hosted personal connection. Its event tools act as
// the person; when an OAuth grant has a selected team it also offers site
// tools, which act through that team's separate scoped credential.
const CallerModeEvents = "events"

// CallerModeHackSite is a hosted Simple Hack team or admin website connection.
// It keeps the website tools except the retired saved-data model.
const CallerModeHackSite = "hack_site"

// Caller is the identity a request acts as. APIKey is the key the client
// presented or, for a connector token, a per-request internal credential for
// the same person: it is sent only on the in-process REST requests a tool
// makes, and never appears in any output.
type Caller struct {
	Handle     string // Authenticated account handle, for per-account Apps rollout.
	APIKey     string
	DeployOnly bool
	// Owner is the account user ID, independent of its request credential.
	Owner string
	// MintUpload re-checks sign-in and returns a credential for one upload.
	MintUpload func(ctx context.Context, team bool) (key string, revoke func(), err error)
	// MintPhoto re-checks an OAuth grant instead of its access token. Photo links
	// using it last one hour; archive uploads continue to use MintUpload.
	MintPhoto func(ctx context.Context, team bool) (key string, revoke func(), err error)
	// CredentialExpiresAt bounds photo links when MintUpload rechecks an API key.
	// Zero means the credential has no known expiry.
	CredentialExpiresAt time.Time
	// Mode selects the tool inventory. See CallerModeEvents.
	Mode string
	// Hosted OAuth grants can choose a team for site publishing. The site
	// credential is separate from APIKey, which remains the person's identity.
	TeamAPIKey string
	GrantID    string
	UserID     string
	// OAuthGrantID is the OAuth grant behind this request in any mode, ""
	// for an API key. Event subscriptions made through it end when the
	// grant is revoked.
	OAuthGrantID string
	// AppName is the connected app's cleaned name ("ChatGPT"), "" for an
	// API key. Shown to the owner next to subscriptions the app made.
	AppName string
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
	// Limits is the snapshot used for tool descriptions and results.
	Limits *Limits

	// VisitorTool runs with a fresh, site-bound visitor pass, never APIKey.
	VisitorTool func(*http.Request, Caller, string, map[string]any) (map[string]any, error)
	// VisitorRefs shares the site handler export key for MCP-only attribution.
	VisitorRefs *VisitorReferences
	// Import callbacks connect report lifecycle to the application service.
	ValidateImportNames bool
	ImportLinkTTL       time.Duration
	ImportCreated       func(ImportLink) error
	ImportReceiving     func(string)
	ImportReport        func(owner, id string) (map[string]any, error)

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
	// SiteBases are the configured bases serving person, site, and claimed hosts.
	SiteBases []string
	// SkillVersion is sent as X-Skill-Version so the REST layer does not
	// append a "your skill is stale" notice to answers meant for this server.
	SkillVersion string
	// ServerName and Version identify this server to clients.
	ServerName string
	Version    string
	// AnalyticsProjectID enables optional AgentCat tool usage events when non-empty.
	AnalyticsProjectID string
	// AnalyticsEnv tags those events: "prod" on simple-host.app, "dev" elsewhere.
	AnalyticsEnv string
	// AnalyticsPepper is a server secret mixed into the account hash, so
	// knowing an account ID is not enough to recompute it.
	AnalyticsPepper string
	// SupportContact replaces the hosted support address in hints on
	// another install ("" keeps it).
	SupportContact string
	// MaxBodyBytes bounds one message. Inline deploys travel in the body, so
	// it sits above the per-site upload limit.
	MaxBodyBytes int64
	// SelectHackTeam validates current event membership and saves a team's
	// selection on the authenticated OAuth grant. It is hosted-mode only.
	SelectHackTeam func(context.Context, Caller, string) error
	// NoSpamCheck: this server has no Turnstile keys, so the comments
	// preset is refused; its tools and instructions leave it out, as on
	// Simple Hack. A self-hosted box starts this way.
	NoSpamCheck bool
	// SelfHosted: this is not simple-host.app. The reconnect steps name a
	// custom connector, since Claude's directory and the plugin point at
	// simple-host.app.
	SelfHosted bool
	// Rewrite names this server instead of simple-host.app in the
	// connector's text (tools, instructions, recipes, page checks, hints).
	// Nil on simple-host.app and on Simple Hack, which rewrites its own.
	Rewrite func(string) string
	// Events serves MCP Events webhook subscriptions. Nil: events are not
	// offered (no capability, no events/* methods, no notification tools).
	Events EventsBackend
	// MutationResultHook, when set, runs after every in-process tool call
	// with the REST method, path and the status it got back. It exists so
	// the Hack paper-metrics collector can see successful hack-event writes
	// without this leaf package importing internal/papermetrics; the
	// handler package wires it to papermetrics.HackMutationResult. Nil-safe.
	MutationResultHook func(ctx context.Context, method, path string, status int)
}

type Server struct {
	registry         *toolRegistry
	policies         map[string]toolPolicy
	passcodeSessions passcodeSessionStore
	importMu         sync.Mutex
	imports          map[[32]byte]ImportLink

	uploadMu  sync.Mutex
	uploads   map[[32]byte]uploadLink
	clientUI  clientUIMemory
	cfg       Config
	tools     []Tool
	byName    map[string]Tool
	analytics *analyticsSender
	// preOutputSchemaTools is the listing for a client that declared
	// 2025-03-26, the one supported revision without Tool.outputSchema.
	preOutputSchemaTools []Tool
	coreTools            []Tool
	deployByName         map[string]Tool
	search, deploySearch *toolSearch
	// hackTools is the hosted organiser inventory. It is listed only when
	// the caller mode is CallerModeEvents, so an ordinary instance and a
	// team connection keep the website tools.
	hackTools      []Tool
	hackByName     map[string]Tool
	hackBare       []Tool
	hackSiteTools  []Tool
	hackSiteBare   []Tool
	hackSiteByName map[string]Tool
	// fixed is set on a server that is not the site connector (the owner's
	// knowledge server): one tool set for every caller.
	fixed *inventory
}

func NewServer(cfg Config) *Server {
	if cfg.MaxBodyBytes <= 0 {
		cfg.MaxBodyBytes = 16 << 20
	}
	supportContact = cfg.SupportContact
	instanceRewrite = cfg.Rewrite
	if cfg.Limits == nil {
		l := DefaultLimits()
		cfg.Limits = &l
	} else {
		l := *cfg.Limits
		cfg.Limits = &l
	}
	r := newRegistry(cfg)
	host := r.view(cfg, editionHost, false, false, false)
	deploy := r.view(cfg, editionHost, false, false, true)
	hack := r.view(cfg, editionHack, false, false, false)
	site := r.view(cfg, editionHost, true, false, false)
	core, coreBare := make([]Tool, 0, len(coreToolNames)), make([]Tool, 0, len(coreToolNames))
	for _, name := range coreToolNames {
		tool := host.byName[name]
		core = append(core, tool)
		tool.OutputSchema = nil
		coreBare = append(coreBare, tool)
	}
	var analytics *analyticsSender
	if cfg.AnalyticsProjectID != "" {
		analytics = newAnalyticsSender(cfg)
	}
	return &Server{
		registry: r, policies: r.policyIndex(),
		coreTools: core, deployByName: deploy.byName,
		search: newToolSearch(host.tools), deploySearch: newToolSearch(deploy.tools),
		analytics: analytics, cfg: cfg, tools: host.tools, byName: host.byName, preOutputSchemaTools: coreBare,
		hackTools:     hackInstanceTools(hack.tools, cfg.APIHost),
		hackByName:    hackInstanceToolMap(hack.byName, cfg.APIHost),
		hackBare:      hackInstanceTools(hack.bare, cfg.APIHost),
		hackSiteTools: site.tools, hackSiteBare: site.bare, hackSiteByName: site.byName,
	}
}

// ListedTools returns the tool list a normal signed-in Host connector session
// sees from tools/list, with this instance's address and description rewrites.
// The returned slice is a copy of the server's listing.
func (s *Server) ListedTools() []Tool {
	full, _, _ := s.forCaller(Caller{})
	return append([]Tool(nil), full...)
}

// HackListedTools returns the generic personal Hack inventory, including selected-team
// tools. It reads no caller, grant, membership, or event data.
func (s *Server) HackListedTools() []Tool {
	full, _, _ := s.hackPersonalInventory()
	return full
}

func (s *Server) hackPersonalInventory() ([]Tool, []Tool, map[string]Tool) {
	// A selected Hack team acts as owner of its own website resources.
	all := append([]Tool{}, s.hackTools...)
	withoutSchemas := append([]Tool{}, s.hackBare...)
	byName := make(map[string]Tool, len(s.hackByName)+len(s.hackSiteByName))
	for name, tool := range s.hackByName {
		byName[name] = tool
	}
	site := s.registry.view(s.cfg, editionHost, true, true, false)
	all = append(all, site.tools...)
	withoutSchemas = append(withoutSchemas, site.bare...)
	for name, tool := range site.byName {
		byName[name] = tool
	}

	return all, withoutSchemas, byName
}

// forCaller is the tool inventory for this connection. Hosted personal OAuth
// grants see event tools and site tools; site calls still need a selected team.
func (s *Server) forCaller(caller Caller) (full, bare []Tool, byName map[string]Tool) {
	defer func() {
		if s.passcodeScreenEnabled(caller) {
			full = append([]Tool(nil), full...)
			bare = append([]Tool(nil), bare...)
			copied := make(map[string]Tool, len(byName)+1)
			for name, tool := range byName {
				copied[name] = tool
			}
			byName = copied
			if !flatListApp(caller.AppName) {
				for _, entry := range s.registry.entries {
					if !entry.policy.listWithPasscode {
						continue
					}
					tool, ok := byName[entry.tool.Name]
					if !ok {
						continue
					}
					full = append(full, tool)
					tool.OutputSchema = nil
					bare = append(bare, tool)
				}
			}
			tool := s.registry.view(s.cfg, editionScreen, false, false, false).tools[0]
			byName[tool.Name] = tool
			full = append(full, tool)
			tool.OutputSchema = nil
			bare = append(bare, tool)
		}
		if s.offersAnalyticsContext(caller) {
			full = analyticsContextTools(full)
			bare = analyticsContextTools(bare)
		}
		screens := s.enabledScreens(caller)
		if len(screens) > 0 {
			full = appToolMetadata(full, screens)
			bare = appToolMetadata(bare, screens)
		}
		hackScreens := s.hackEnabledScreens(caller)
		if len(hackScreens) > 0 {
			full = appToolMetadata(full, hackScreens)
			bare = appToolMetadata(bare, hackScreens)
		}
	}()
	if s.fixed != nil {
		return s.fixed.tools, s.fixed.bare, s.fixed.byName
	}
	if caller.Mode == CallerModeEvents {
		if caller.GrantID != "" {
			return s.hackPersonalInventory()
		}
		return s.hackTools, s.hackBare, s.hackByName
	}
	if caller.Mode == CallerModeHackSite {
		return s.hackSiteTools, s.hackSiteBare, s.hackSiteByName
	}
	if flatListApp(caller.AppName) {
		allowed := s.byName
		if caller.DeployOnly {
			allowed = s.deployByName
		}
		byName = make(map[string]Tool, len(allowed))
		for name, tool := range allowed {
			byName[name] = flatListTool(tool)
		}
		for _, tool := range s.tools {
			if _, ok := allowed[tool.Name]; !ok {
				continue
			}
			tool = byName[tool.Name]
			full = append(full, tool)
			tool.OutputSchema = nil
			bare = append(bare, tool)
		}
		return full, bare, byName
	}
	if caller.DeployOnly {
		return s.coreTools, s.preOutputSchemaTools, s.deployByName
	}
	return s.coreTools, s.preOutputSchemaTools, s.byName
}

func (s *Server) instructionsFor(caller Caller) string {
	if s.fixed != nil {
		return s.fixed.instructions
	}
	if caller.Mode == CallerModeEvents {
		return s.hackInstanceInstructions(HackInstructions())
	}
	if caller.Mode == CallerModeHackSite {
		return s.hackInstanceInstructions(HackSiteInstructions())
	}
	text := Instructions()
	if flatListApp(caller.AppName) {
		text = flatListInstructions()
	}
	return instanceText(s.hostInstructions(text))
}

// hostInstructions fits the Simple Host instructions to this server: no
// comments preset without the spam check, and custom connector steps off
// simple-host.app.
func (s *Server) hostInstructions(text string) string {
	if s.cfg.NoSpamCheck {
		text = withoutCommentsInstructions(text)
	}
	if s.cfg.SelfHosted {
		text = strings.Replace(text, claudeReconnectHosted, claudeReconnectSelf, 1)
	}
	return text
}

func (s *Server) hackInstanceInstructions(text string) string {
	return hackInstanceReplace(text, s.cfg.APIHost)
}

// hackInstanceReplace is hackInstanceInstructions' one-line replace, shared
// with hackInstanceTools/hackInstanceToolMap below. On simple-hack.app
// itself cfg.APIHost is "simple-hack.app" too, so this is an identity
// replace there -- same as hackInstanceInstructions already relied on for
// the top-level instructions text; a self-hosted instance's own APIHost
// makes it a real substitution.
func hackInstanceReplace(text, apiHost string) string {
	if apiHost != "" {
		text = strings.ReplaceAll(text, "simple-hack.app", apiHost)
	}
	return text
}

// hackInstanceTools applies hackInstanceReplace to every tool's text, once,
// at construction (apiHost: cfg.APIHost). Unlike the Host edition's
// instanceTools (basetext.go), the Hack tool set
// (hackTools/hackBare/hackSiteTools/...) is served with zero rewriting today
// -- CallerModeEvents returns s.hackTools straight from the registry
// (forCaller), with no per-request pass -- so this has to run once in
// NewServer instead. Only hack_apply_to_event's description currently names
// simple-hack.app (the one hardcoded hosted URL in the whole MCP surface,
// SIM connect-mcp audit item 25); this is the general-purpose fix so the
// next one is caught the same way the instructions text already is.
func hackInstanceTools(tools []Tool, apiHost string) []Tool {
	if apiHost == "" {
		return tools
	}
	out := make([]Tool, len(tools))
	for i, t := range tools {
		t.Title = hackInstanceReplace(t.Title, apiHost)
		t.Description = hackInstanceReplace(t.Description, apiHost)
		t.InputSchema = mapSchema(t.InputSchema, func(s string) string { return hackInstanceReplace(s, apiHost) })
		t.OutputSchema = mapSchema(t.OutputSchema, func(s string) string { return hackInstanceReplace(s, apiHost) })
		out[i] = t
	}
	return out
}

func hackInstanceToolMap(m map[string]Tool, apiHost string) map[string]Tool {
	if apiHost == "" {
		return m
	}
	out := make(map[string]Tool, len(m))
	for name, t := range m {
		t.Title = hackInstanceReplace(t.Title, apiHost)
		t.Description = hackInstanceReplace(t.Description, apiHost)
		t.InputSchema = mapSchema(t.InputSchema, func(s string) string { return hackInstanceReplace(s, apiHost) })
		t.OutputSchema = mapSchema(t.OutputSchema, func(s string) string { return hackInstanceReplace(s, apiHost) })
		out[name] = t
	}
	return out
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// No standalone SSE stream and no sessions: every answer is one JSON
	// document on the POST that asked. The transport asks for 405 on GET (and
	// DELETE, which ends a session this server never started).
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		writeRPC(w, http.StatusMethodNotAllowed, failure(nil, codeInvalidRequest, "method not allowed; send MCP messages with POST /mcp", nil))
		return
	}
	caller, ok := callerFrom(r.Context())

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
	if !ok && !isEventMethod(req.Method) {
		// The wrapper authenticates before calling in; reaching here without
		// a caller is a wiring bug, and it must fail closed.
		writeRPC(w, http.StatusUnauthorized, failure(nil, codeInvalidRequest, "not authenticated", nil))
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
	if resp.Error != nil && resp.Error.Code == codeMethodNotFound && (s.isModern(r, req) || modernInitialize(r, req)) {
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

// modernInitialize is an initialize sent under the stateless revision's
// header, which that revision removed. An initialize offering the revision in
// its body still negotiates.
func modernInitialize(r *http.Request, req request) bool {
	return req.Method == "initialize" && strings.TrimSpace(r.Header.Get("MCP-Protocol-Version")) == protocolVersion
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
		params, err := s.parseCallParams(req.Params)
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

	if req.Method == "resources/read" {
		var params struct {
			URI string `json:"uri"`
		}
		if json.Unmarshal(req.Params, &params) != nil {
			return http.StatusBadRequest, &rpcError{Code: codeInvalidParams, Message: "params could not be read"}
		}
		name := strings.TrimSpace(r.Header.Get("Mcp-Name"))
		if name == "" && modern {
			return http.StatusBadRequest, &rpcError{Code: codeHeaderMismatch, Message: "Mcp-Name header is required on resources/read"}
		}
		if name != "" && decodeHeaderValue(name) != params.URI {
			return http.StatusBadRequest, &rpcError{Code: codeHeaderMismatch, Message: "Mcp-Name header does not match the resource URI"}
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

func (s *Server) parseCallParams(raw json.RawMessage) (callParams, error) {
	var params callParams
	if len(raw) == 0 {
		return params, nil
	}
	var envelope struct {
		Name      string                     `json:"name"`
		Arguments map[string]json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return params, err
	}
	name := envelope.Name
	if s.policy(name).unwrap {
		_ = json.Unmarshal(envelope.Arguments["name"], &name)
	}
	if s.policy(name).preserveNumbers {
		// Preserve entry values and IDs in both direct and wrapped calls.
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.UseNumber()
		err := dec.Decode(&params)
		return params, err
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
	return map[string]any{"name": s.cfg.ServerName, "title": s.title(), "version": s.cfg.Version}
}

func (s *Server) dispatch(r *http.Request, req request, caller Caller) response {
	modern := s.isModern(r, req)
	switch req.Method {
	case "initialize":
		if modernInitialize(r, req) {
			return failure(req.ID, codeMethodNotFound, "initialize is not part of "+protocolVersion+"; send requests directly", nil)
		}
		s.clientUI.remember(caller, initializeDeclaresUI(req.Params), time.Now())
		return result(req.ID, map[string]any{
			"protocolVersion": negotiateInitialize(metaProtocolVersion(req.Params)),
			"capabilities":    s.capabilities(caller),
			"serverInfo":      s.serverInfo(),
			"instructions":    s.instructionsFor(caller),
		})

	// ping is a keepalive in the initialize-era revisions. The stateless
	// revision removed it along with the connection it kept alive.
	case "ping":
		if modern {
			return failure(req.ID, codeMethodNotFound, "ping is not part of "+protocolVersion, nil)
		}
		return result(req.ID, map[string]any{})

	case "server/discover":
		return result(req.ID, s.withMeta(map[string]any{
			"resultType":        "complete",
			"supportedVersions": supportedVersions,
			"capabilities":      s.capabilities(caller),
			"serverInfo":        s.serverInfo(),
			"instructions":      s.instructionsFor(caller),
			"ttlMs":             cacheTTLMillis,
			"cacheScope":        "private",
		}))

	case "tools/list":
		full, bare, _ := s.forCaller(caller)
		hackShowAllowed := s.hackShowToolsAllowed(caller, s.uiDeclaredForListing(caller, req.Params, modern))
		full, bare = filterHackShowTools(full, hackShowAllowed), filterHackShowTools(bare, hackShowAllowed)
		if modern {
			return result(req.ID, s.withMeta(map[string]any{
				"resultType": "complete",
				"tools":      full,
				"ttlMs":      cacheTTLMillis,
				"cacheScope": "private",
			}))
		}
		listed := full
		if requestedVersion(r, req) == "2025-03-26" {
			listed = bare
		}
		return result(req.ID, map[string]any{"tools": listed})

	case "events/list", "events/subscribe", "events/unsubscribe":
		return s.dispatchEvents(r, req, caller, modern)

	// Disabled resources and prompts return empty lists. Some clients list these regardless of
	// the declared capabilities; an empty list is the true answer and keeps
	// them from treating the connector as broken.
	case "resources/list":
		hackResourceAllowed := s.hackAppsGate(caller, s.uiDeclaredForListing(caller, req.Params, modern))
		payload := map[string]any{"resources": append(s.appResources(caller), s.hackAppResources(caller, hackResourceAllowed)...)}
		if modern && (len(s.enabledScreens(caller)) > 0 || (hackResourceAllowed && len(s.hackEnabledScreens(caller)) > 0)) {
			payload["resultType"] = "complete"
			payload["ttlMs"] = cacheTTLMillis
			payload["cacheScope"] = "private"
		}
		return result(req.ID, payload)
	case "resources/read":
		reply := s.readApp(req, caller)
		if reply.Error != nil {
			hackResourceAllowed := s.hackAppsGate(caller, s.uiDeclaredForListing(caller, req.Params, modern))
			reply = s.readHackApp(req, caller, hackResourceAllowed)
		}
		if modern && reply.Error == nil {
			payload := reply.Result.(map[string]any)
			payload["resultType"] = "complete"
			payload["ttlMs"] = cacheTTLMillis
			payload["cacheScope"] = "private"
		}
		return reply
	case "resources/templates/list":
		hackResourceAllowed := s.hackAppsGate(caller, s.uiDeclaredForListing(caller, req.Params, modern))
		payload := map[string]any{"resourceTemplates": []any{}}
		if modern && (len(s.enabledScreens(caller)) > 0 || (hackResourceAllowed && len(s.hackEnabledScreens(caller)) > 0)) {
			payload["resultType"] = "complete"
			payload["ttlMs"] = cacheTTLMillis
			payload["cacheScope"] = "private"
		}
		return result(req.ID, payload)
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
	Meta      map[string]json.RawMessage `json:"_meta"`
	Name      string                     `json:"name"`
	Arguments map[string]any             `json:"arguments"`
}

func (s *Server) callTool(r *http.Request, req request, caller Caller, modern bool) (reply response) {
	params, err := s.parseCallParams(req.Params)
	if err != nil {
		return failure(req.ID, codeInvalidParams, "params could not be read", nil)
	}
	_, _, byName := s.forCaller(caller)
	started := time.Now()
	secretValues := analyticsSecretValues(s.policy(params.Name), params.Arguments, s.policy)
	eventName := params.Name
	intent, _ := takeAnalyticsContext(byName[params.Name], params.Arguments)
	var recorded bool
	var runError error
	var completed time.Time
	var parameters any
	record := func(err error) {
		recorded, runError, completed = true, err, time.Now()
	}
	defer func() {
		if s.analytics != nil && recorded {
			if s.policy(eventName).analytics.redactAll {
				intent = "[removed]"
			}
			if parameters == nil {
				parameters = analyticsParametersForPolicy(s.policy(eventName), params.Arguments)
			}
			s.analytics.enqueue(caller, eventName, completed.Sub(started), completed, runError,
				redactAnalyticsSecrets(parameters, secretValues), redactAnalyticsSecrets(analyticsResponseForPolicy(s.policy(eventName), reply.Result), secretValues), capAnalyticsIntent(redactAnalyticsSecrets(intent, secretValues).(string)))
		}
	}()
	// Unwrap before lookup so direct and wrapped calls share the same checks.
	wrapped := byName[params.Name].policy.unwrap
	if _, allowed := byName["run_tool"]; wrapped && allowed {
		outer := params.Arguments
		name, nameErr := stringArg(outer, "name")
		innerArgs, objectArgs := outer["arguments"].(map[string]any)
		if inner, present := takeAnalyticsContext(byName[name], innerArgs); present {
			intent = inner
		}
		_, known := byName[name]
		validTarget := known && !byName[name].policy.discovery
		if validTarget {
			eventName = name
		}
		if s.analytics != nil {
			if _, present := outer["arguments"]; validTarget && (objectArgs || !present) {
				parameters = analyticsParametersForPolicy(s.policy(name), innerArgs)
			} else {
				parameters = map[string]any{"arguments": "[removed]"}
			}
		}
		if err := unexpectedArgument(byName["run_tool"], outer); err != nil {
			record(err)
			return toolResult(req.ID, err.Error(), nil, true, modern)
		}
		if nameErr != nil {
			record(nameErr)
			return toolResult(req.ID, nameErr.Error(), nil, true, modern)
		}
		if s.policy(name).screenOnly {
			err := errors.New("passcode_screen is only for the passcode screen")
			record(err)
			return toolResult(req.ID, err.Error(), nil, true, modern)
		}
		if byName[name].policy.discovery {
			err := errors.New("Nested discovery cannot call another discovery tool. Choose the required operation from the tool list.")
			record(err)
			return toolResult(req.ID, err.Error(), nil, true, modern)
		}
		params.Name = name
		params.Arguments = nil
		if raw, present := outer["arguments"]; present {
			var ok bool
			params.Arguments, ok = raw.(map[string]any)
			if !ok {
				err := errors.New("arguments must be an object")
				record(err)
				return toolResult(req.ID, argumentMessage(err, byName[name]), nil, true, modern)
			}
		}
	} else {
		wrapped = false
	}
	tool, known := byName[params.Name]
	if inner, present := takeAnalyticsContext(tool, params.Arguments); present {
		intent = inner
	}
	if !known {
		if params.Name == "hack_lock_judging" || params.Name == "hack_unlock_judging" {
			message := "Judging locks when you finish judging (move to Results) and unlocks when you move the event back. [judging_lock_removed]"
			if wrapped {
				record(codedError("judging_lock_removed", message))
			}
			return toolResult(req.ID, message, nil, true, modern)
		}
		if s.fixed == nil && retiredSavedDataTool(params.Name) {
			message := params.Name + " is no longer offered by this connector. Existing sites keep that saved data, readable in the person's dashboard. For anything new use the storage_* tools (get_page_recipe shows the page code)."
			if wrapped {
				record(errors.New(message))
			}
			return toolResult(req.ID, message, nil, true, modern)
		}
		if wrapped {
			message := "Unknown tool: " + params.Name + ". Call find_tools with the job."
			if flatListApp(caller.AppName) {
				message = "Unknown tool: " + params.Name + ". Choose a tool from the available tool list."
			}
			matches := s.searchFor(caller).rank(params.Name, 3)
			if len(matches) > 0 {
				names := make([]string, 0, len(matches))
				for _, match := range matches {
					names = append(names, match.tool.Name)
				}
				message += " Closest tools: " + strings.Join(names, ", ") + "."
			}
			message += " [unknown_tool]"
			record(codedError("unknown_tool", message))
			return toolResult(req.ID, message, nil, true, modern)
		}
		return failure(req.ID, codeInvalidParams, "unknown tool: "+params.Name, nil)
	}
	if params.Arguments == nil {
		params.Arguments = map[string]any{}
	}
	argErr := resolveArgAlias(tool, params.Arguments)
	if argErr == nil {
		argErr = unexpectedArgument(tool, params.Arguments)
	}
	if argErr == nil && wrapped {
		required, _ := tool.InputSchema["required"].([]string)
		for _, name := range required {
			if _, present := params.Arguments[name]; !present {
				argErr = fmt.Errorf("%s is required", name)
				break
			}
		}
	}
	if argErr != nil {
		record(codedError("invalid_argument", argErr.Error()))
		// Some apps (ChatGPT custom servers) keep the tool list from when the
		// person connected, so a stale schema is common: give the current one.
		message := argumentMessage(argErr, tool)
		if !wrapped {
			message += "\n" + staleToolsHint
		}
		return toolResult(req.ID, message, nil, true, modern)
	}
	// who_am_i reads only the signed-in person and names the selected team
	// itself, so it needs no team. Notifications also use the person's
	// credential. Other website tools use the team's scoped credential.
	if caller.Mode == CallerModeEvents && caller.GrantID != "" && !tool.policy.personalCredential {
		if caller.TeamAPIKey == "" {
			// My sites can read the person's event websites through the existing
			// hack_* tools without a team. Return no team data and never replay
			// this request with the person's credential against /v1/sites.
			if tool.policy.listWithoutTeam && s.mySitesEnabled(caller) {
				record(nil)
				return toolResult(req.ID, "No team selected. Call hack_get_my_teams to choose a team, or hack_list_events for your event websites.", map[string]any{"sites": []any{}, "count": 0}, false, modern)
			}
			record(errors.New("no team selected"))
			return toolResult(req.ID, "Choose a team with hack_select_team before publishing its site.", nil, true, modern)
		}
		caller.APIKey = caller.TeamAPIKey
	}
	c := &call{server: s, orig: r, caller: caller, policy: tool.policy}
	if raw, present := params.Meta["io.modelcontextprotocol/clientCapabilities"]; modern && present {
		var caps struct {
			Extensions map[string]json.RawMessage `json:"extensions"`
		}
		if json.Unmarshal(raw, &caps) == nil {
			_, ui := caps.Extensions["io.modelcontextprotocol/ui"]
			c.textOnlyClient = !ui
			c.uiClient = ui
		}
	} else if ui, known := s.clientUI.lookup(caller, time.Now()); !modern && known {
		c.textOnlyClient = !ui
		c.uiClient = ui
	}
	// A host draws a screen for the tool it listed, never for run_tool.
	if wrapped {
		c.wrapped = true
		c.textOnlyClient = true
	}
	if s.analytics != nil && parameters == nil {
		parameters = analyticsParametersForPolicy(s.policy(eventName), params.Arguments)
	}
	started = time.Now()
	out, err := tool.run(c, params.Arguments)
	record(err)
	if err != nil {
		// A bad argument or a refused REST call is the model's to correct, so
		// it comes back as a failed tool result it can read, not a protocol
		// error that ends the turn.
		message := err.Error()
		if caller.Mode == CallerModeEvents || caller.Mode == CallerModeHackSite {
			message = hackStorageErrorMessage(message)
		} else if s.fixed == nil {
			var refusal *refusalError
			if errors.As(err, &refusal) && (refusal.code == "key_expired" || refusal.code == "key_expired_idle" || refusal.code == "invalid_api_key" || refusal.status == http.StatusUnauthorized) {
				message, _, _ = strings.Cut(message, "\n")
				message += "\n" + instanceText(s.hostInstructions(reconnectText))
			}
		}
		var refusal *refusalError
		if wrapped && !errors.As(err, &refusal) {
			message = argumentMessage(err, tool)
		}
		return toolResult(req.ID, message, nil, true, modern)
	}
	reply = toolResult(req.ID, out.Text, out.Structured, false, modern)
	if out.Meta != nil {
		reply.Result.(map[string]any)["_meta"] = out.Meta
	}
	return reply
}

const staleToolsHint = "Call again with only these arguments. If your copy of this tool shows other arguments, the app's tool list is out of date: ask the person to refresh Simple Host in the app's connector settings."

func argumentMessage(err error, tool Tool) string {
	if tool.InputSchema == nil {
		return err.Error()
	}
	schema, _ := json.Marshal(tool.InputSchema)
	return err.Error() + "\nInput schema for " + tool.Name + ": " + string(schema)
}

// retiredSavedDataTool names the state, collection and declared-data tools the
// connector offered until 2026-10-10. Their HTTP routes still serve existing
// sites; the connector no longer lists them.
func retiredSavedDataTool(name string) bool {
	switch name {
	case "get_state", "update_state", "list_collections", "read_collection",
		"add_to_collection", "set_collection_privacy", "update_collection_item",
		"delete_collection_item", "clear_collection", "data_history", "restore_data",
		"list_deleted", "restore_item", "delete_forever", "declare_data",
		"list_data", "update_data", "set_who_can_save", "block_person":
		return true
	}
	return false
}

// Shared Host refusal hints are written for the owner's own connector. Keep
// the actual REST refusal and code; Hack callers reconnect in the app.
func hackStorageErrorMessage(message string) string {
	if strings.Contains(message, "[key_expired]") || strings.Contains(message, "[key_expired_idle]") {
		first, _, _ := strings.Cut(message, "\n")
		return first + "\nReconnect the Simple Hack connector in the app; do not share a key in chat."
	}
	return message
}

// unexpectedArgument refuses an argument the tool's schema does not declare.
// Every schema is closed (additionalProperties: false), and ignoring a stray
// argument would let a call quietly mean something other than what the model
// asked (e.g. a mode it thought it was choosing).
func unexpectedArgument(tool Tool, args map[string]any) error {
	props, _ := tool.InputSchema["properties"].(map[string]any)
	for key := range args {
		if _, ok := props[key]; !ok {
			// Name the fields the schema actually has: a near-miss guess
			// (a plausible-but-wrong name borrowed from a sibling tool)
			// deserves to be told what to use, not just that the call was
			// refused; argumentMessage/staleToolsHint cover the separate
			// case of a client whose cached copy of this tool is stale.
			names := make([]string, 0, len(props))
			for name := range props {
				names = append(names, name)
			}
			sort.Strings(names)
			return fmt.Errorf("unexpected argument %q for %s; it takes only %s", key, tool.Name, strings.Join(names, ", "))
		}
	}
	return nil
}

// argAliasFor names a one-off alias a model guesses from a sibling tool
// family's word for the same concept: storage_* and hack_event_storage_*
// tools call the saved-data resource "name", while visitor_read_site/
// visitor_add_entry/visitor_change_entry call the same concept "resource";
// find_tools's argument is "query", not the "task" a model familiar with
// other search tools may send; get_page_recipe's is "topic", not the
// "kind" a model familiar with the storage tools' kind argument may send.
func argAliasFor(name string) (canonical, alias string) {
	switch {
	case name == "find_tools":
		return "query", "task"
	case name == "get_page_recipe" || name == "hack_get_page_recipe":
		return "topic", "kind"
	case name == "visitor_read_site" || name == "visitor_add_entry" || name == "visitor_change_entry":
		return "resource", "name"
	case strings.HasPrefix(name, "storage_") || strings.HasPrefix(name, "hack_event_storage_"):
		return "name", "resource"
	}
	return "", ""
}

// resolveArgAlias renames args[alias] to args[canonical] in place, before
// the schema's additionalProperties check (unexpectedArgument) runs and
// before the advertised InputSchema is ever consulted, so the alias is
// accepted silently: it is never added to a tool's schema, which would
// change the frozen tool list some clients cache (ChatGPT keeps the one
// from the day it connected). Only applied when the tool's schema actually
// declares canonical and does not itself declare alias as a real, distinct
// argument (storage_get_usage has no "name", so is untouched either way; a
// hypothetical tool that legitimately takes both a "name" and a "resource"
// is also left alone, since the alias word is already spoken for there).
func resolveArgAlias(tool Tool, args map[string]any) error {
	canonical, alias := argAliasFor(tool.Name)
	if canonical == "" {
		return nil
	}
	props, _ := tool.InputSchema["properties"].(map[string]any)
	if _, ok := props[canonical]; !ok {
		return nil
	}
	if _, ok := props[alias]; ok {
		return nil
	}
	a, hasAlias := args[alias]
	if !hasAlias {
		return nil
	}
	if c, hasCanonical := args[canonical]; hasCanonical {
		if !reflect.DeepEqual(a, c) {
			return fmt.Errorf("%s and %s were both given with different values; use %s", canonical, alias, canonical)
		}
		delete(args, alias)
		return nil
	}
	args[canonical] = a
	delete(args, alias)
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
