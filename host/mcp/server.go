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
// and attached a Caller to the request context with WithCaller.
package mcp

import (
	"bytes"
	"cmp"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/vsriram/simple-host/host/access"
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
// the person, and so do its site tools, which name the event as owner.
const CallerModeEvents = "events"

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
	MintUpload func(ctx context.Context) (key string, revoke func(), err error)
	// MintPhoto re-checks an OAuth grant instead of its access token. Photo links
	// using it last one hour; archive uploads continue to use MintUpload.
	MintPhoto func(ctx context.Context) (key string, revoke func(), err error)
	// CredentialExpiresAt bounds photo links when MintUpload rechecks an API key.
	// Zero means the credential has no known expiry.
	CredentialExpiresAt time.Time
	// Mode selects the tool inventory. See CallerModeEvents.
	Mode string
	// GrantID is a hosted product connection's OAuth grant ("" otherwise).
	GrantID string
	UserID  string
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
	// Contribution is the product tool surface. Nil, like a pointer to the zero
	// value, adds no product tools.
	Contribution *Contribution

	// Limits is the snapshot used for tool descriptions and results.
	Limits *Limits

	// VisitorTool runs with a fresh, site-bound visitor pass, never APIKey.
	VisitorTool func(*http.Request, Caller, string, map[string]any) (map[string]any, error)
	// VisitorRefs shares the site handler export key for MCP-only attribution.
	VisitorRefs *access.VisitorReferences

	// Upstream is the application router (the bare mux, not the edge
	// middleware). Tool calls are served into it in process.
	Upstream http.Handler
	// APIHost is the Host the in-process requests carry: the apex, which is
	// where agents talk to the API.
	APIHost string
	// ContentOrigin is the shared content host's origin, e.g.
	// https://sites.simple-host.app. Per-site storage routes are
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
	// AnalyticsEnv tags those events: "prod" on the product's hosted
	// address, "dev" elsewhere.
	AnalyticsEnv string
	// AnalyticsProduct tags those events with the product ("simple-host",
	// "simple-hack"), so products sharing one AgentCat project stay apart.
	AnalyticsProduct string
	// AnalyticsEndpoint replaces AgentCat's public events address; tests
	// point it at a local server. It is not an operator setting.
	AnalyticsEndpoint string
	// AnalyticsPepper is a server secret mixed into the account hash, so
	// knowing an account ID is not enough to recompute it.
	AnalyticsPepper string
	// SupportContact replaces the hosted support address in hints on
	// another install ("" keeps it).
	SupportContact string
	// MaxBodyBytes bounds one message. Inline deploys travel in the body, so
	// it sits above the per-site upload limit.
	MaxBodyBytes int64
	// Teams: this program offers teams and site editors (the Simple Host
	// program): their tools are registered. Every site and storage tool takes
	// owner everywhere.
	Teams bool
	// NoSpamCheck: this server has no Turnstile keys, so spam-checked adds
	// (the comments recipe) are unavailable; its tools and instructions leave
	// them out, as on Simple Hack. A self-hosted box starts this way.
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
	// MutationResult, when set, runs after every in-process tool call with
	// the REST method, path and the status it got back. It exists so a
	// product's metrics collector can see successful writes made through a
	// tool without this leaf package importing host/papermetrics; the
	// handler package wires it to the product metrics recorder. Nil-safe.
	MutationResult func(ctx context.Context, method, path string, status int)
}

type Server struct {
	registry         *toolRegistry
	policies         map[string]toolPolicy
	passcodeSessions passcodeSessionStore

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
	// productTools is the contributed product's inventory. It is listed
	// only when the caller mode is CallerModeEvents, so an ordinary instance
	// keeps the website tools.
	productTools  []Tool
	productByName map[string]Tool
	productBare   []Tool
	apps          AppContribution
}

func newServer(cfg Config) *Server {
	if cfg.Contribution == nil {
		cfg.Contribution = &Contribution{}
	}
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
	// The product's screens are built once, before the registry: their
	// family of tools is registered with the product's own.
	s := &Server{cfg: cfg}
	if factory := cfg.Contribution.Apps; factory != nil {
		s.apps = factory(AppContext{APIHost: cfg.APIHost, Meta: s.appMeta})
	}
	r := buildRegistry(cfg, s.apps.AppFamily)
	host := r.view(cfg, editionHost, false, false, false)
	deploy := r.view(cfg, editionHost, false, false, true)
	product := r.view(cfg, editionProduct, false, false, false)
	core, coreBare := make([]Tool, 0, len(coreToolNames)), make([]Tool, 0, len(coreToolNames))
	for _, name := range coreToolNames {
		tool := host.byName[name]
		core = append(core, tool)
		tool.OutputSchema = nil
		coreBare = append(coreBare, tool)
	}
	if cfg.AnalyticsProjectID != "" {
		s.analytics = newAnalyticsSender(cfg)
	}
	s.registry, s.policies = r, r.policyIndex()
	s.coreTools, s.deployByName = core, deploy.byName
	s.search, s.deploySearch = newToolSearch(host.tools), newToolSearch(deploy.tools)
	s.tools, s.byName, s.preOutputSchemaTools = host.tools, host.byName, coreBare
	s.productTools = productInstanceTools(product.tools, cfg.APIHost, cfg.Contribution.Rewrite)
	s.productByName = productInstanceToolMap(product.byName, cfg.APIHost, cfg.Contribution.Rewrite)
	s.productBare = productInstanceTools(product.bare, cfg.APIHost, cfg.Contribution.Rewrite)
	return s
}

// ListedTools returns the tool list a normal signed-in Host connector session
// sees from tools/list, with this instance's address and description rewrites.
// The returned slice is a copy of the server's listing.
func (s *Server) ListedTools() []Tool {
	full, _, _ := s.forCaller(Caller{})
	return append([]Tool(nil), full...)
}

// ProductListedTools returns the generic personal inventory of the contributed
// product, including selected-target website tools. It reads no caller, grant,
// membership, or event data.
func (s *Server) ProductListedTools() []Tool {
	full, bare, byName := s.productPersonalInventory()
	full, _, _ = s.storageRulesInventory(full, bare, byName, Caller{Mode: CallerModeEvents})
	return full
}

func (s *Server) productPersonalInventory() ([]Tool, []Tool, map[string]Tool) {
	// A selected group acts as owner of its own website resources.
	all := append([]Tool{}, s.productTools...)
	withoutSchemas := append([]Tool{}, s.productBare...)
	byName := make(map[string]Tool, len(s.productByName))
	for name, tool := range s.productByName {
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

// toolListing is one connection's tools: with output schemas (full),
// without (bare, for the one revision without them), and by name.
type toolListing struct {
	full, bare []Tool
	byName     map[string]Tool
}

func (l *toolListing) add(tool Tool) {
	l.full = append(l.full, tool)
	tool.OutputSchema = nil
	l.bare = append(l.bare, tool)
}

// forCaller is the tool inventory for this connection. A product connection
// sees its event tools and the site tools.
func (s *Server) forCaller(caller Caller) (full, bare []Tool, byName map[string]Tool) {
	l := s.baseListing(caller)
	if s.passcodeScreenEnabled(caller) {
		l = s.withPasscodeScreen(l, caller)
	}
	if s.analytics != nil {
		l.full, l.bare = analyticsContextTools(l.full), analyticsContextTools(l.bare)
	}
	for _, screens := range [][]appScreen{s.enabledScreens(caller), s.productScreens(caller)} {
		if len(screens) > 0 {
			l.full, l.bare = appToolMetadata(l.full, screens), appToolMetadata(l.bare, screens)
		}
	}
	return s.storageRulesInventory(l.full, l.bare, l.byName, caller)
}

// baseListing is the inventory a caller's mode and app choose.
func (s *Server) baseListing(caller Caller) toolListing {
	allowed := s.byName
	if caller.DeployOnly {
		allowed = s.deployByName
	}
	switch {
	case caller.Mode == CallerModeEvents:
		// A product connection, OAuth or the person's own key: the product's
		// tools and the site tools, which name the account as owner.
		full, bare, byName := s.productPersonalInventory()
		return toolListing{full: full, bare: bare, byName: byName}
	case flatListApp(caller.AppName):
		l := toolListing{byName: make(map[string]Tool, len(allowed))}
		for name, tool := range allowed {
			l.byName[name] = flatListTool(tool)
		}
		for _, tool := range s.tools {
			if _, ok := allowed[tool.Name]; ok {
				l.add(l.byName[tool.Name])
			}
		}
		return l
	}
	return toolListing{full: s.coreTools, bare: s.preOutputSchemaTools, byName: allowed}
}

// withPasscodeScreen adds the passcode screen's tool and, outside the flat
// list, the tools listed alongside it, to a copy of l.
func (s *Server) withPasscodeScreen(l toolListing, caller Caller) toolListing {
	out := toolListing{full: append([]Tool(nil), l.full...), bare: append([]Tool(nil), l.bare...), byName: make(map[string]Tool, len(l.byName)+1)}
	for name, tool := range l.byName {
		out.byName[name] = tool
	}
	if !flatListApp(caller.AppName) {
		for _, entry := range s.registry.entries {
			if tool, ok := out.byName[entry.tool.Name]; ok && entry.tool.policy.listWithPasscode {
				out.add(tool)
			}
		}
	}
	tool := s.registry.view(s.cfg, editionScreen, false, false, false).tools[0]
	out.byName[tool.Name] = tool
	out.add(tool)
	return out
}

func (s *Server) instructionsFor(caller Caller) string {
	if instructions := s.cfg.Contribution.Instructions; instructions != nil {
		if text := instructions(caller.Mode); text != "" {
			return s.productInstructions(text)
		}
	}
	text := Instructions()
	if flatListApp(caller.AppName) {
		text = flatListInstructions()
	}
	return instanceText(s.hostInstructions(text))
}

// hostInstructions fits the Simple Host instructions to this server: custom
// connector steps off simple-host.app.
func (s *Server) hostInstructions(text string) string {
	if s.cfg.SelfHosted {
		text = strings.Replace(text, claudeReconnectHosted, claudeReconnectSelf, 1)
	}
	return text
}

func (s *Server) productInstructions(text string) string {
	if s.cfg.Contribution != nil && s.cfg.Contribution.Rewrite != nil {
		rewrite := s.cfg.Contribution.Rewrite
		return rewrite(text, s.cfg.APIHost)
	}
	return text
}

// productInstanceTools applies a product's text rewrite once at construction.
func productInstanceTools(tools []Tool, apiHost string, rewrite func(string, string) string) []Tool {
	if apiHost == "" || rewrite == nil {
		return tools
	}
	out := make([]Tool, len(tools))
	for i, t := range tools {
		out[i] = productInstanceTool(t, apiHost, rewrite)
	}
	return out
}

func productInstanceToolMap(m map[string]Tool, apiHost string, rewrite func(string, string) string) map[string]Tool {
	if apiHost == "" || rewrite == nil {
		return m
	}
	out := make(map[string]Tool, len(m))
	for name, t := range m {
		out[name] = productInstanceTool(t, apiHost, rewrite)
	}
	return out
}

func productInstanceTool(t Tool, apiHost string, rewrite func(string, string) string) Tool {
	t.Title = rewrite(t.Title, apiHost)
	t.Description = rewrite(t.Description, apiHost)
	t.InputSchema = mapSchema(t.InputSchema, func(s string) string { return rewrite(s, apiHost) })
	t.OutputSchema = mapSchema(t.OutputSchema, func(s string) string { return rewrite(s, apiHost) })
	return t
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
	req, status, refusal := s.readRequest(w, r)
	if status == http.StatusAccepted {
		w.WriteHeader(status)
		return
	}
	if refusal != nil {
		writeRPC(w, status, *refusal)
		return
	}
	// A notification is acknowledged with 202 and no body, whatever it names.
	if req.isNotification() {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	caller, _ := callerFrom(r.Context())
	resp := s.dispatch(r, req, caller)
	status = http.StatusOK
	if resp.Error != nil && resp.Error.Code == codeMethodNotFound && (s.isModern(r, req) || modernInitialize(r, req)) {
		// The stateless revision distinguishes an unimplemented method from a
		// server with no MCP endpoint by a 404 carrying a JSON-RPC body. An
		// initialize-era client reads 404 as "session gone", so it gets 200.
		status = http.StatusNotFound
	}
	writeRPC(w, status, resp)
}

// readRequest reads, parses and checks one message. A refusal comes back
// with its status; 202 with no refusal acknowledges a client's response.
func (s *Server) readRequest(w http.ResponseWriter, r *http.Request) (request, int, *response) {
	refuse := func(status int, id json.RawMessage, code int, message string) (request, int, *response) {
		reply := failure(id, code, message, nil)
		return request{}, status, &reply
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, s.cfg.MaxBodyBytes))
	if err != nil {
		return refuse(http.StatusRequestEntityTooLarge, nil, codeParseError, "request body could not be read (too large?)")
	}
	var raw json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		return refuse(http.StatusBadRequest, nil, codeParseError, "invalid JSON")
	}
	if trimmed := bytes.TrimSpace(raw); len(trimmed) > 0 && trimmed[0] == '[' {
		return refuse(http.StatusBadRequest, nil, codeInvalidRequest, "batched messages are not supported; send one JSON-RPC message per request")
	}
	var req request
	if err := json.Unmarshal(body, &req); err != nil {
		return refuse(http.StatusBadRequest, nil, codeInvalidRequest, "not a JSON-RPC 2.0 request object")
	}
	if !req.wellFormed() {
		// A JSON-RPC response (a client answering a server request) is valid
		// traffic on this transport; this server never sends requests, so it
		// is acknowledged and ignored.
		if req.Method == "" && len(req.ID) > 0 && bytes.Contains(body, []byte(`"result"`)) {
			return request{}, http.StatusAccepted, nil
		}
		return refuse(http.StatusBadRequest, req.ID, codeInvalidRequest, "a JSON-RPC 2.0 request needs jsonrpc:\"2.0\" and a method")
	}
	if req.hasNullID() {
		return refuse(http.StatusBadRequest, nil, codeInvalidRequest, "id must not be null")
	}
	if _, ok := callerFrom(r.Context()); !ok && !isEventMethod(req.Method) {
		// The wrapper authenticates before calling in; reaching here without
		// a caller is a wiring bug, and it must fail closed.
		return refuse(http.StatusUnauthorized, nil, codeInvalidRequest, "not authenticated")
	}
	if status, rpcErr := s.validate(r, req); rpcErr != nil {
		id := req.ID
		if req.isNotification() {
			id = nil
		}
		reply := failure(id, rpcErr.Code, rpcErr.Message, rpcErr.Data)
		return request{}, status, &reply
	}
	return req, http.StatusOK, nil
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
	requested, err := checkVersion(r, req)
	if err == nil {
		// Only a client declaring the stateless revision is held to its
		// header and _meta rules. An initialize-era client predates both.
		modern := requested == protocolVersion && req.Method != "initialize"
		err = s.checkHeaders(r, req, modern)
	}
	if err != nil {
		return http.StatusBadRequest, err
	}
	return http.StatusOK, nil
}

// checkVersion returns the revision the request declares, by header or
// _meta, refusing a disagreement or (outside initialize) one not spoken here.
func checkVersion(r *http.Request, req request) (string, *rpcError) {
	declared := strings.TrimSpace(r.Header.Get("MCP-Protocol-Version"))
	inBody := metaProtocolVersion(req.Params)
	if declared != "" && inBody != "" && declared != inBody && req.Method != "initialize" {
		return "", &rpcError{Code: codeHeaderMismatch, Message: fmt.Sprintf("MCP-Protocol-Version header %q does not match the body's %q", declared, inBody)}
	}
	requested := cmp.Or(declared, inBody)
	// initialize negotiates, so an unknown version there is answered with one
	// this server speaks rather than refused.
	if requested != "" && !supported(requested) && req.Method != "initialize" {
		return "", &rpcError{Code: codeUnsupportedVersion, Message: "Unsupported protocol version",
			Data: map[string]any{"supported": supportedVersions, "requested": requested}}
	}
	return requested, nil
}

// checkHeaders holds the request to its Mcp-Method and Mcp-Name headers and,
// under the stateless revision, to declaring its client capabilities.
func (s *Server) checkHeaders(r *http.Request, req request, modern bool) *rpcError {
	method := strings.TrimSpace(r.Header.Get("Mcp-Method"))
	if method == "" && modern {
		return &rpcError{Code: codeHeaderMismatch, Message: "Mcp-Method header is required"}
	}
	if method != "" && method != req.Method {
		return &rpcError{Code: codeHeaderMismatch, Message: fmt.Sprintf("Mcp-Method header %q does not match the body's %q", method, req.Method)}
	}
	if err := s.checkNameHeader(r, req, modern); err != nil {
		return err
	}
	if modern && !req.isNotification() && !hasClientCapabilities(req.Params) {
		return &rpcError{Code: codeInvalidParams, Message: "params._meta must carry io.modelcontextprotocol/clientCapabilities"}
	}
	return nil
}

// checkNameHeader matches Mcp-Name to the tool called or the resource read.
func (s *Server) checkNameHeader(r *http.Request, req request, modern bool) *rpcError {
	var target string
	var mismatch func(header string) string
	switch req.Method {
	case "tools/call":
		params, err := s.parseCallParams(req.Params)
		if err != nil {
			return &rpcError{Code: codeInvalidParams, Message: "params could not be read"}
		}
		target = params.Name
		mismatch = func(header string) string {
			return fmt.Sprintf("Mcp-Name header %q does not match the tool being called, %q", header, target)
		}
	case "resources/read":
		var params struct {
			URI string `json:"uri"`
		}
		if json.Unmarshal(req.Params, &params) != nil {
			return &rpcError{Code: codeInvalidParams, Message: "params could not be read"}
		}
		target = params.URI
		mismatch = func(string) string { return "Mcp-Name header does not match the resource URI" }
	default:
		return nil
	}
	name := strings.TrimSpace(r.Header.Get("Mcp-Name"))
	if name == "" && modern {
		return &rpcError{Code: codeHeaderMismatch, Message: "Mcp-Name header is required on " + req.Method}
	}
	if header := decodeHeaderValue(name); name != "" && header != target {
		return &rpcError{Code: codeHeaderMismatch, Message: mismatch(header)}
	}
	return nil
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

// rpcCall is one request on its way to its method.
type rpcCall struct {
	r      *http.Request
	req    request
	caller Caller
	modern bool
}

// rpcMethods answers each method this server implements.
var rpcMethods = map[string]func(*Server, rpcCall) response{
	"initialize":               (*Server).rpcInitialize,
	"ping":                     (*Server).rpcPing,
	"server/discover":          (*Server).rpcDiscover,
	"tools/list":               (*Server).rpcToolsList,
	"tools/call":               func(s *Server, c rpcCall) response { return s.callTool(c.r, c.req, c.caller, c.modern) },
	"events/list":              (*Server).rpcEvents,
	"events/subscribe":         (*Server).rpcEvents,
	"events/unsubscribe":       (*Server).rpcEvents,
	"resources/list":           (*Server).rpcResourcesList,
	"resources/read":           (*Server).rpcResourcesRead,
	"resources/templates/list": (*Server).rpcResourceTemplates,
	// Disabled prompts return an empty list. Some clients list these
	// regardless of the declared capabilities; an empty list is the true
	// answer and keeps them from treating the connector as broken.
	"prompts/list": func(_ *Server, c rpcCall) response { return result(c.req.ID, map[string]any{"prompts": []any{}}) },
}

func (s *Server) dispatch(r *http.Request, req request, caller Caller) response {
	method, ok := rpcMethods[req.Method]
	if !ok {
		return failure(req.ID, codeMethodNotFound, "unknown method: "+req.Method, nil)
	}
	return method(s, rpcCall{r: r, req: req, caller: caller, modern: s.isModern(r, req)})
}

func (s *Server) rpcInitialize(c rpcCall) response {
	if modernInitialize(c.r, c.req) {
		return failure(c.req.ID, codeMethodNotFound, "initialize is not part of "+protocolVersion+"; send requests directly", nil)
	}
	s.clientUI.remember(c.caller, initializeDeclaresUI(c.req.Params), time.Now())
	return result(c.req.ID, map[string]any{
		"protocolVersion": negotiateInitialize(metaProtocolVersion(c.req.Params)),
		"capabilities":    s.capabilities(c.caller),
		"serverInfo":      s.serverInfo(),
		"instructions":    s.instructionsFor(c.caller),
	})
}

// ping is a keepalive in the initialize-era revisions. The stateless
// revision removed it along with the connection it kept alive.
func (s *Server) rpcPing(c rpcCall) response {
	if c.modern {
		return failure(c.req.ID, codeMethodNotFound, "ping is not part of "+protocolVersion, nil)
	}
	return result(c.req.ID, map[string]any{})
}

func (s *Server) rpcDiscover(c rpcCall) response {
	return result(c.req.ID, s.withMeta(map[string]any{
		"resultType":        "complete",
		"supportedVersions": supportedVersions,
		"capabilities":      s.capabilities(c.caller),
		"serverInfo":        s.serverInfo(),
		"instructions":      s.instructionsFor(c.caller),
		"ttlMs":             cacheTTLMillis,
		"cacheScope":        "private",
	}))
}

func (s *Server) rpcToolsList(c rpcCall) response {
	full, bare, _ := s.forCaller(c.caller)
	showAllowed := s.showToolsAllowed(c.caller, s.uiDeclaredForListing(c.caller, c.req.Params, c.modern))
	full, bare = filterShowTools(full, showAllowed), filterShowTools(bare, showAllowed)
	if c.modern {
		return result(c.req.ID, s.withMeta(map[string]any{
			"resultType": "complete",
			"tools":      full,
			"ttlMs":      cacheTTLMillis,
			"cacheScope": "private",
		}))
	}
	if requestedVersion(c.r, c.req) == "2025-03-26" {
		return result(c.req.ID, map[string]any{"tools": bare})
	}
	return result(c.req.ID, map[string]any{"tools": full})
}

func (s *Server) rpcEvents(c rpcCall) response {
	return s.dispatchEvents(c.r, c.req, c.caller, c.modern)
}

// productResourcesAllowed: this call may see the product's screens.
func (s *Server) productResourcesAllowed(c rpcCall) bool {
	return s.productAppsGate(c.caller, s.uiDeclaredForListing(c.caller, c.req.Params, c.modern))
}

// cacheResourceListing marks a modern resource listing cacheable when any
// screen is on offer.
func (s *Server) cacheResourceListing(c rpcCall, productAllowed bool, payload map[string]any) {
	if c.modern && (len(s.enabledScreens(c.caller)) > 0 || (productAllowed && len(s.productScreens(c.caller)) > 0)) {
		markCached(payload)
	}
}

// Disabled resources return empty lists, for the clients that list them
// regardless of the declared capabilities.
func (s *Server) rpcResourcesList(c rpcCall) response {
	allowed := s.productResourcesAllowed(c)
	payload := map[string]any{"resources": append(s.appResources(c.caller), s.productAppResources(c.caller, allowed)...)}
	s.cacheResourceListing(c, allowed, payload)
	return result(c.req.ID, payload)
}

func (s *Server) rpcResourceTemplates(c rpcCall) response {
	payload := map[string]any{"resourceTemplates": []any{}}
	s.cacheResourceListing(c, s.productResourcesAllowed(c), payload)
	return result(c.req.ID, payload)
}

func (s *Server) rpcResourcesRead(c rpcCall) response {
	reply := s.readApp(c.req, c.caller)
	if reply.Error != nil && s.apps.Read != nil {
		if productReply, handled := s.apps.Read(c.req, c.caller, s.productResourcesAllowed(c)); handled {
			reply = productReply
		}
	}
	if c.modern && reply.Error == nil {
		markCached(reply.Result.(map[string]any))
	}
	return reply
}

// cacheTTLMillis is how long a client may reuse a discovery or tool listing.
const cacheTTLMillis = 300000

// markCached marks a modern answer complete and privately reusable.
func markCached(payload map[string]any) {
	payload["resultType"], payload["ttlMs"], payload["cacheScope"] = "complete", cacheTTLMillis, "private"
}

func (s *Server) withMeta(payload map[string]any) map[string]any {
	payload["_meta"] = map[string]any{"io.modelcontextprotocol/serverInfo": s.serverInfo()}
	return payload
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
