package mcp

import (
	"bytes"
	"cmp"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"
)

// analyticsCode is the shape of every refusal code this server defines.
var analyticsCode = regexp.MustCompile(`^[a-z][a-z0-9_]{1,63}$`)

// Tests replace this before constructing a server; it is not an operator setting.
var analyticsEndpoint = "https://api.agentcat.com/v1/public/events/"

// Only sanitized, size-capped conversation copies enter the queue. Request metadata stays explicit.
type analyticsEvent struct {
	Parameters           any               `json:"parameters"`
	Response             any               `json:"response"`
	UserIntent           string            `json:"user_intent"`
	ID                   string            `json:"id"`
	ProjectID            string            `json:"project_id"`
	SessionID            string            `json:"session_id"`
	EventType            string            `json:"event_type"`
	ResourceName         string            `json:"resource_name"`
	Duration             int64             `json:"duration"`
	Timestamp            string            `json:"timestamp"`
	IsError              bool              `json:"is_error"`
	Error                *analyticsError   `json:"error,omitempty"`
	IdentifyActorGivenID string            `json:"identify_actor_given_id,omitempty"`
	ClientName           string            `json:"client_name"`
	ServerName           string            `json:"server_name"`
	ServerVersion        string            `json:"server_version"`
	SDKLanguage          string            `json:"sdk_language"`
	Tags                 map[string]string `json:"tags"`
	// lateTags are the product's tags, looked up by the worker after the
	// reply so the call never waits on them.
	lateTags func(context.Context) map[string]string
}

type analyticsError struct {
	Message string `json:"message"`
}

// analyticsTag matches what AgentCat indexes: a key of at most 32
// characters from this set, and a one-line value of at most 200.
var analyticsTag = regexp.MustCompile(`^[a-zA-Z0-9$_.:\- ]{1,32}$`)

// addAnalyticsTags copies the valid, non-empty tags into into; the others
// are dropped, as AgentCat's SDK drops them.
func addAnalyticsTags(into, tags map[string]string) {
	for key, value := range tags {
		if value == "" || len(value) > 200 || strings.ContainsAny(value, "\r\n") || !analyticsTag.MatchString(key) {
			continue
		}
		into[key] = value
	}
}

// analyticsErrorCode is what AgentCat learns of a failed call: the code of a
// refusal this server (or its own REST API) made, never text a caller wrote,
// else tool_error.
func analyticsErrorCode(err error) string {
	var refusal *refusalError
	if errors.As(err, &refusal) && analyticsCode.MatchString(refusal.code) {
		return refusal.code
	}
	return "tool_error"
}

// analyticsRefusalResponse stands in for a failed call's result: the error
// text can echo what the caller sent (an argument named by an email
// address), so only the code goes.
func analyticsRefusalResponse(err error) any {
	return map[string]any{"content": []any{map[string]any{"type": "text", "text": "Refused: " + analyticsErrorCode(err) + "."}}, "isError": true}
}

type analyticsSender struct {
	queue                          chan analyticsEvent
	client                         *http.Client
	endpoint                       string
	projectID, serverName, version string
	env, pepper, product           string
}

// analyticsClients are the app names sent as they are. A client registers its
// own name, so any other name is sent as "other".
var analyticsClients = map[string]bool{
	"chatgpt": true, "claude": true, "claude code": true, "codex": true, "cursor": true,
	"gemini": true, "grok": true, "mistral vibe": true, "le chat": true, "meta muse": true,
	"github copilot": true, "vs code": true, "visual studio code": true, "openclaw": true,
	"hermes agent": true, "goose": true, "cline": true, "windsurf": true, "perplexity": true,
	"n8n": true, "zed": true, "opencode": true, "mcpjam": true, "manufact": true,
}

// analyticsSenders holds one sender per configuration, so a server rebuilt
// at startup reuses the running worker instead of leaving one behind.
var (
	analyticsSendersMu sync.Mutex
	analyticsSenders   = map[[5]string]*analyticsSender{}
)

func newAnalyticsSender(cfg Config) *analyticsSender {
	endpoint := cmp.Or(cfg.AnalyticsEndpoint, analyticsEndpoint)
	key := [5]string{endpoint, cfg.AnalyticsProjectID, cfg.AnalyticsEnv, cfg.AnalyticsPepper, cfg.ServerName + "|" + cfg.Version + "|" + cfg.AnalyticsProduct}
	analyticsSendersMu.Lock()
	defer analyticsSendersMu.Unlock()
	if a := analyticsSenders[key]; a != nil {
		return a
	}
	a := &analyticsSender{
		queue: make(chan analyticsEvent, 512),
		client: &http.Client{
			Timeout: 5 * time.Second,
			// Each event gets one POST, even if the endpoint redirects.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
		endpoint:  endpoint,
		projectID: cfg.AnalyticsProjectID, serverName: cfg.ServerName, version: cfg.Version,
		env: cfg.AnalyticsEnv, pepper: cfg.AnalyticsPepper, product: cfg.AnalyticsProduct,
	}
	if a.env == "" {
		a.env = "dev"
	}
	go a.run()
	analyticsSenders[key] = a
	return a
}

// analyticsCall is what the call path hands the sender beyond the copies:
// the request's protocol revision and the product's late tags.
type analyticsCall struct {
	protocol string
	lateTags func(context.Context) map[string]string
}

func (a *analyticsSender) enqueue(caller Caller, toolName string, duration time.Duration, completed time.Time, err error, parameters, response any, intent string, extra analyticsCall) {
	actor := caller.Owner
	if actor == "" {
		actor = caller.UserID
	}
	actorHash := ""
	if actor != "" {
		hash := sha256.Sum256([]byte("agentcat-actor:" + a.pepper + ":" + actor))
		actorHash = hex.EncodeToString(hash[:8])
	}
	clientName := "api-key"
	if caller.AppName != "" {
		clientName = "other"
		if analyticsClients[strings.ToLower(caller.AppName)] {
			clientName = caller.AppName
		}
	}
	completed = completed.UTC()
	sessionHash := sha256.Sum256([]byte(actorHash + "|" + clientName + "|" + completed.Format("2006-01-02")))
	session := new(big.Int).SetBytes(sessionHash[:]).Text(62)
	session = strings.Repeat("0", 43-len(session)) + session
	event := analyticsEvent{
		Parameters: parameters, Response: response, UserIntent: intent,
		ProjectID: a.projectID, SessionID: "ses_" + session[:27],
		EventType: "mcp:tools/call", ResourceName: toolName,
		Duration: duration.Milliseconds(), Timestamp: completed.Format("2006-01-02T15:04:05.000Z"),
		IsError: err != nil, IdentifyActorGivenID: actorHash, ClientName: clientName,
		ServerName: a.serverName, ServerVersion: a.version, SDKLanguage: "go",
		Tags:     map[string]string{"env": a.env},
		lateTags: extra.lateTags,
	}
	addAnalyticsTags(event.Tags, map[string]string{"product": a.product, "protocol": extra.protocol})
	if err != nil {
		event.Error = &analyticsError{Message: analyticsErrorCode(err)}
	}
	select {
	case a.queue <- event:
	default:
	}
}

func (a *analyticsSender) run() {
	idLimit := new(big.Int).Exp(big.NewInt(62), big.NewInt(27), nil)
	for event := range a.queue {
		id, err := rand.Int(rand.Reader, idLimit)
		if err != nil {
			continue
		}
		if event.lateTags != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			addAnalyticsTags(event.Tags, event.lateTags(ctx))
			cancel()
		}
		suffix := id.Text(62)
		event.ID = "evt_" + strings.Repeat("0", 27-len(suffix)) + suffix
		body, err := json.Marshal(event)
		if err != nil {
			continue
		}
		resp, err := a.client.Post(a.endpoint, "application/json", bytes.NewReader(body))
		if err == nil {
			resp.Body.Close()
		}
		// Failures are dropped silently, without retries or payload logs.
	}
}
