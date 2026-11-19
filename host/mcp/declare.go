package mcp

import (
	"encoding/json"
	"io/fs"
	"net/http"
)

// Declaration types expose tool adapters without exposing the registry.
type Call = call
type Output = output
type UpstreamResult = upstreamResult
type AppScreen = appScreen
type Request = request
type Response = response

// Policy is an opaque declaration option. Products use the option functions
// below; the underlying policy and its fields remain private.
type Policy func(*toolPolicy)

func WithPolicy(tool Tool, options ...Policy) Tool {
	policy := toolPolicy{}
	for _, option := range options {
		option(&policy)
	}
	return withToolPolicy(tool, policy)
}
func MemberResult(kind string) Policy { return func(p *toolPolicy) { p.memberResult = kind } }
func OrganiserResult(kind string) Policy {
	return func(p *toolPolicy) { p.organiserResult = kind }
}
func JudgeParameter() Policy  { return func(p *toolPolicy) { p.judgeParameter = true } }
func IncludeEmails() Policy   { return func(p *toolPolicy) { p.includeEmails = true } }
func RecordsPins() Policy     { return func(p *toolPolicy) { p.recordsPins = true } }
func Screenshot() Policy      { return func(p *toolPolicy) { p.screenshot = true } }
func OptionalBody() Policy    { return func(p *toolPolicy) { p.optionalBody = true } }
func ConnectorHidden() Policy { return func(p *toolPolicy) { p.connectorHidden = true } }
func Screen(id string) Policy { return func(p *toolPolicy) { p.screen = id } }
func ShowAllowed(allowed func(Caller, bool) bool) Policy {
	return func(p *toolPolicy) { p.showAllowed = allowed }
}

// AnalyticsOption exposes only the telemetry declaration choices products use.
type AnalyticsOption func(*analyticsPolicy)

func Analytics(options ...AnalyticsOption) Policy {
	return func(p *toolPolicy) {
		for _, option := range options {
			option(&p.analytics)
		}
	}
}
func SecretText() AnalyticsOption    { return func(p *analyticsPolicy) { p.secretText = true } }
func SecretKey() AnalyticsOption     { return func(p *analyticsPolicy) { p.secretKey = true } }
func SecretURL() AnalyticsOption     { return func(p *analyticsPolicy) { p.secretURL = true } }
func FileResult() AnalyticsOption    { return func(p *analyticsPolicy) { p.fileResult = true } }
func FileArguments() AnalyticsOption { return func(p *analyticsPolicy) { p.fileArguments = true } }
func ApplicationAnswers() AnalyticsOption {
	return func(p *analyticsPolicy) { p.applicationAnswers = true }
}

func Object(properties map[string]any, required ...string) map[string]any {
	return object(properties, required...)
}
func Str(description string) map[string]any { return str(description) }
func NoArgs() map[string]any                { return noArgs() }
func StringMap(args map[string]any, key string) (map[string]string, error) {
	return stringMap(args, key)
}
func AnyJSON(description string) map[string]any { return anyJSON(description) }
func OutObject(properties map[string]any, required ...string) map[string]any {
	return outObject(properties, required...)
}
func OutArray(description string, items map[string]any) map[string]any {
	return outArray(description, items)
}
func OutString(description string) map[string]any  { return outString(description) }
func OutBool(description string) map[string]any    { return outBool(description) }
func OutInteger(description string) map[string]any { return outInteger(description) }
func WithDescription(schema map[string]any, description string) map[string]any {
	return withDescription(schema, description)
}
func FilesBase64Schema() map[string]any { return filesBase64Schema() }
func WholeNumber(args map[string]any, key string, required bool, min, max int) (int, bool, error) {
	return wholeNumber(args, key, required, min, max)
}
func StringArg(args map[string]any, key string) (string, error) { return stringArg(args, key) }
func OptionalString(args map[string]any, key string) (string, error) {
	return optionalString(args, key)
}
func ReadOnly() map[string]any { return readOnly() }
func Writes(destructive, idempotent, public bool) map[string]any {
	return writes(destructive, idempotent, public)
}
func WithMeta(tool Tool, meta map[string]any) Tool { tool.Meta = meta; return tool }
func WithRun(tool Tool, run func(*Call, map[string]any) (Output, error)) Tool {
	tool.run = run
	return tool
}

// CallTool runs an existing declaration with its own result policy, as app-only twins do.
func CallTool(tool Tool, c *Call, args map[string]any) (Output, error) {
	own := *c
	own.policy = tool.policy
	return tool.run(&own, args)
}
func RestError(tool string, u UpstreamResult) error     { return restError(tool, u) }
func UpstreamMessage(u UpstreamResult) (string, string) { return upstreamMessage(u) }
func JSONText(value any) string                         { return jsonText(value) }
func (c *call) Do(method, path string, body []byte, extra map[string]string) UpstreamResult {
	return c.do(method, path, body, extra)
}
func (c *call) Caller() Caller         { return c.caller }
func (c *call) Request() *http.Request { return c.orig }
func (c *call) UICapable() bool        { return c.uiClient }
func (c *call) MaxBodyBytes() int64    { return c.server.cfg.MaxBodyBytes }
func (c *call) APIHost() string        { return c.server.cfg.APIHost }
func (c *call) SelectTarget(team string) error {
	return c.server.cfg.SelectTarget(c.orig.Context(), c.caller, team)
}
func (c *call) CanSelectTarget() bool               { return c.server.cfg.SelectTarget != nil }
func (c *call) MemberResult() string                { return c.policy.memberResult }
func (c *call) OrganiserResult() string             { return c.policy.organiserResult }
func (u upstreamResult) OK() bool                   { return u.ok() }
func (u upstreamResult) Status() int                { return u.status }
func (u upstreamResult) Header() http.Header        { return u.header }
func (u upstreamResult) Body() []byte               { return u.body }
func Result(id json.RawMessage, value any) Response { return result(id, value) }
func Failure(id json.RawMessage, code int, message string, data any) Response {
	return failure(id, code, message, data)
}

const CodeInvalidParams = codeInvalidParams
const CodeInternalError = codeInternalError
const AppMIME = appMIME

func AppFiles() fs.ReadFileFS           { return appFiles }
func AppFlag(value, handle string) bool { return appFlag(value, handle) }
func FlatListApp(name string) bool      { return flatListApp(name) }
func EventStorageTools(wording func(string) string) []Tool {
	return storageToolsFor(true, wording)
}

const DeployPhotoTip = deployPhotoTip
const ArchiveUploadDescription = archiveUploadDescription

func ReadRecipe(topic string) ([]byte, error) { return readRecipe(topic) }

const StorageRulesInstructions = storageRulesInstructions

func NotificationTools() []Tool             { return notificationTools() }
func RequiresNotificationID(tool Tool) bool { return tool.policy.notificationID }
func PageRecipeTool() Tool                  { return pageRecipeTool() }
func RecipeTopics() []string                { return append([]string(nil), recipeTopics...) }

// NewCall is the context one tool call runs in; the server builds the same
// value for each tools/call. Product tests run a declaration with it.
func NewCall(server *Server, r *http.Request, caller Caller) *Call {
	return &call{server: server, orig: r, caller: caller}
}

// WithCaller copies this replay context with the supplied credential identity.
func (c *call) WithCaller(caller Caller) *Call { clone := *c; clone.caller = caller; return &clone }

// WithUICapable copies this context as a call from a client that did, or did
// not, declare MCP Apps support. The server sets the same fact on each
// tools/call; product tests run a screen tool with it.
func (c *call) WithUICapable(ui bool) *Call {
	clone := *c
	clone.uiClient, clone.textOnlyClient = ui, !ui
	return &clone
}
