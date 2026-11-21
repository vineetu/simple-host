package mcp

// tools/call: argument checks, run_tool unwrapping, the tool run and its result.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"reflect"
	"slices"
	"sort"
	"strings"
	"time"
)

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
	_, runTool := byName["run_tool"]
	wrapped := runTool && byName[params.Name].policy.unwrap
	a := s.startCallAnalytics(caller, params, byName, wrapped)
	a.protocol = requestedVersion(r, req)
	defer func() { a.send(params.Arguments, reply) }()
	refuse := func(err error, text string) response {
		a.record(err)
		return toolResult(req.ID, text, nil, true, modern)
	}
	// Unwrap before lookup so direct and wrapped calls share the same checks.
	if wrapped {
		name, args, err := s.unwrapRunTool(byName, params.Arguments)
		if err != nil {
			return refuse(err, err.Error())
		}
		params.Name, params.Arguments = name, args
	}
	tool, known := byName[params.Name]
	if !known {
		return s.unknownTool(req.ID, params.Name, caller, wrapped, modern, a.record)
	}
	if params.Arguments == nil {
		params.Arguments = map[string]any{}
	}
	if err := checkArguments(tool, params.Arguments, wrapped); err != nil {
		// Some apps (ChatGPT custom servers) keep the tool list from when the
		// person connected, so a stale schema is common: give the current one.
		text := argumentMessage(err, tool)
		if !wrapped {
			text += "\n" + staleToolsHint
		}
		return refuse(codedError("invalid_argument", err.Error()), text)
	}
	c := &call{server: s, orig: r, caller: caller, policy: tool.policy}
	if err := c.takeTeamArguments(params.Arguments); err != nil {
		return refuse(codedError("invalid_argument", err.Error()), argumentMessage(err, tool))
	}
	a.owner = c.owner
	s.setClientUI(c, params.Meta, caller, modern)
	// A host draws a screen for the tool it listed, never for run_tool.
	if wrapped {
		c.wrapped, c.textOnlyClient = true, true
	}
	if s.analytics != nil && a.parameters == nil {
		a.parameters = analyticsParametersForPolicy(s.policy(a.event), params.Arguments)
	}
	a.started = time.Now()
	out, err := tool.run(c, params.Arguments)
	a.record(err)
	if err != nil {
		// A bad argument or a refused REST call is the model's to correct, so
		// it comes back as a failed tool result it can read, not a protocol
		// error that ends the turn.
		return toolResult(req.ID, s.toolErrorText(err, caller, tool, wrapped), nil, true, modern)
	}
	reply = toolResult(req.ID, out.Text, out.Structured, false, modern)
	if out.Meta != nil {
		reply.Result.(map[string]any)["_meta"] = out.Meta
	}
	return reply
}

// unwrapRunTool reads run_tool's own arguments: the tool to run and its
// arguments. A refusal's text is what the caller reads.
func (s *Server) unwrapRunTool(byName map[string]Tool, outer map[string]any) (string, map[string]any, error) {
	name, nameErr := stringArg(outer, "name")
	target := byName[name]
	switch err := unexpectedArgument(byName["run_tool"], outer); {
	case err != nil:
		return "", nil, err
	case nameErr != nil:
		return "", nil, nameErr
	case s.policy(name).screenOnly:
		return "", nil, errors.New("passcode_screen is only for the passcode screen")
	case target.policy.discovery:
		return "", nil, errors.New("Nested discovery cannot call another discovery tool. Choose the required operation from the tool list.")
	}
	inner, objectArgs := outer["arguments"].(map[string]any)
	if _, present := outer["arguments"]; present && !objectArgs {
		return "", nil, errors.New(argumentMessage(errors.New("arguments must be an object"), target))
	}
	return name, inner, nil
}

// callAnalytics is what one tools/call reports to usage analytics.
type callAnalytics struct {
	s                  *Server
	caller             Caller
	event, intent      string
	parameters         any
	secrets            []string
	started, completed time.Time
	recorded           bool
	err                error
	protocol           string
	owner              string
}

// startCallAnalytics takes the analytics context out of the arguments. A call
// through run_tool reports as the tool it names when that is a real,
// non-discovery tool, and never reports arguments that are not an object.
func (s *Server) startCallAnalytics(caller Caller, params callParams, byName map[string]Tool, wrapped bool) *callAnalytics {
	outer := params.Arguments
	a := &callAnalytics{s: s, caller: caller, event: params.Name, started: time.Now(),
		secrets: analyticsSecretValues(s.policy(params.Name), outer, s.policy)}
	a.intent, _ = takeAnalyticsContext(byName[params.Name], outer)
	if !wrapped {
		return a
	}
	name, _ := stringArg(outer, "name")
	inner, objectArgs := outer["arguments"].(map[string]any)
	_, present := outer["arguments"]
	if intent, ok := takeAnalyticsContext(byName[name], inner); ok {
		a.intent = intent
	}
	target, known := byName[name]
	validTarget := known && !target.policy.discovery
	if validTarget {
		a.event = name
	}
	if s.analytics != nil {
		a.parameters = map[string]any{"arguments": "[removed]"}
		if validTarget && (objectArgs || !present) {
			a.parameters = analyticsParametersForPolicy(s.policy(name), inner)
		}
	}
	return a
}

func (a *callAnalytics) record(err error) {
	a.recorded, a.err, a.completed = true, err, time.Now()
}

// send enqueues the event for a recorded call; args are its final arguments.
func (a *callAnalytics) send(args map[string]any, reply response) {
	if a.s.analytics == nil || !a.recorded {
		return
	}
	policy := a.s.policy(a.event)
	intent := a.intent
	if policy.analytics.redactAll {
		intent = "[removed]"
	}
	parameters := a.parameters
	if parameters == nil {
		parameters = analyticsParametersForPolicy(policy, args)
	}
	result := analyticsResponseForPolicy(policy, reply.Result)
	if a.err != nil && !policy.analytics.redactAll {
		result = analyticsRefusalResponse(a.err)
	}
	a.s.analytics.enqueue(a.caller, a.event, a.completed.Sub(a.started), a.completed, a.err,
		redactAnalyticsSecrets(parameters, a.secrets), redactAnalyticsSecrets(result, a.secrets),
		capAnalyticsIntent(redactAnalyticsSecrets(intent, a.secrets).(string)),
		analyticsCall{protocol: a.protocol, lateTags: a.productTags(args)})
}

// productTags defers the product's tags for this call to the worker, with a
// copy of its arguments.
func (a *callAnalytics) productTags(args map[string]any) func(context.Context) map[string]string {
	product := a.s.cfg.Contribution
	if product == nil || product.AnalyticsTags == nil {
		return nil
	}
	caller, tool, args := a.caller, a.event, maps.Clone(args)
	if a.owner != "" {
		// takeTeamArguments moved owner onto the call; the product's tags
		// name the account (a Hack event) the call was about.
		if args == nil {
			args = map[string]any{}
		}
		args["owner"] = a.owner
	}
	return func(ctx context.Context) map[string]string { return product.AnalyticsTags(ctx, caller, tool, args) }
}

// unknownTool answers a call naming no tool of this connection: a retired
// product mutation's own message, or through run_tool the closest names.
func (s *Server) unknownTool(id json.RawMessage, name string, caller Caller, wrapped, modern bool, record func(error)) response {
	if s.cfg.Contribution != nil {
		if retired, ok := s.cfg.Contribution.MutationNames[name]; ok {
			if wrapped {
				record(codedError(retired.Code, retired.Message))
			}
			return toolResult(id, retired.Message, nil, true, modern)
		}
	}
	if !wrapped {
		return failure(id, codeInvalidParams, "unknown tool: "+name, nil)
	}
	message := "Unknown tool: " + name + ". Call find_tools with the job."
	if flatListApp(caller.AppName) {
		message = "Unknown tool: " + name + ". Choose a tool from the available tool list."
	}
	if matches := s.searchFor(caller).rank(name, 3); len(matches) > 0 {
		names := make([]string, 0, len(matches))
		for _, match := range matches {
			names = append(names, match.tool.Name)
		}
		message += " Closest tools: " + strings.Join(names, ", ") + "."
	}
	message += " [unknown_tool]"
	record(codedError("unknown_tool", message))
	return toolResult(id, message, nil, true, modern)
}

// checkArguments renames aliases and refuses undeclared arguments; a call
// through run_tool is also checked for the required ones.
func checkArguments(tool Tool, args map[string]any, wrapped bool) error {
	if err := resolveArgAlias(tool, args); err != nil {
		return err
	}
	if err := unexpectedArgument(tool, args); err != nil || !wrapped {
		return err
	}
	required, _ := tool.InputSchema["required"].([]string)
	for _, name := range required {
		if _, present := args[name]; !present {
			return fmt.Errorf("%s is required", name)
		}
	}
	return nil
}

// setClientUI records whether the client draws MCP Apps screens: declared on
// the call under the stateless revision, else remembered from initialize.
func (s *Server) setClientUI(c *call, meta map[string]json.RawMessage, caller Caller, modern bool) {
	if raw, present := meta["io.modelcontextprotocol/clientCapabilities"]; modern && present {
		var caps struct {
			Extensions map[string]json.RawMessage `json:"extensions"`
		}
		if json.Unmarshal(raw, &caps) == nil {
			_, ui := caps.Extensions["io.modelcontextprotocol/ui"]
			c.textOnlyClient, c.uiClient = !ui, ui
		}
	} else if ui, known := s.clientUI.lookup(caller, time.Now()); !modern && known {
		c.textOnlyClient, c.uiClient = !ui, ui
	}
}

// toolErrorText is a failed run's message: product wording for a product
// connection, reconnect steps for a dead key, the schema for a run_tool slip.
func (s *Server) toolErrorText(err error, caller Caller, tool Tool, wrapped bool) string {
	message := err.Error()
	var refusal *refusalError
	isRefusal := errors.As(err, &refusal)
	if caller.Mode == CallerModeEvents {
		if s.cfg.Contribution != nil && s.cfg.Contribution.StorageError != nil {
			message = s.cfg.Contribution.StorageError(message)
		}
	} else if isRefusal && (refusal.code == "key_expired" || refusal.code == "key_expired_idle" || refusal.code == "invalid_api_key" || refusal.status == http.StatusUnauthorized) {
		message, _, _ = strings.Cut(message, "\n")
		message += "\n" + instanceText(s.hostInstructions(reconnectText))
	}
	if wrapped && !isRefusal {
		message = argumentMessage(err, tool)
	}
	return message
}

const staleToolsHint = "Call again with only these arguments. If your copy of this tool shows other arguments, the app's tool list is out of date: ask the person to refresh Simple Host in the app's connector settings."

func argumentMessage(err error, tool Tool) string {
	if tool.InputSchema == nil {
		return err.Error()
	}
	schema, _ := json.Marshal(tool.InputSchema)
	return err.Error() + "\nInput schema for " + tool.Name + ": " + string(schema)
}

// unexpectedArgument refuses an argument the tool's schema does not declare.
// Every schema is closed (additionalProperties: false), and ignoring a stray
// argument would let a call quietly mean something other than what the model
// asked (e.g. a mode it thought it was choosing).
func unexpectedArgument(tool Tool, args map[string]any) error {
	props, _ := tool.InputSchema["properties"].(map[string]any)
	for key := range args {
		if _, ok := props[key]; !ok && !slices.Contains(tool.policy.unlistedArgs, key) {
			// Name the fields the schema has, so a near-miss guess is told
			// what to use.
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

// argAliasFor names the word a model borrows from a sibling tool family for
// the same argument (storage_* say "name", visitor_* say "resource").
func argAliasFor(name string) (canonical, alias string) {
	switch {
	case name == "find_tools":
		return "query", "task"
	case name == "get_page_recipe" || name == "hack_get_page_recipe":
		return "topic", "kind"
	case name == "visitor_read_site" || name == "visitor_add_entry" || name == "visitor_change_entry":
		return "resource", "name"
	case strings.HasPrefix(name, "storage_"):
		return "name", "resource"
	}
	return "", ""
}

// resolveArgAlias renames args[alias] to args[canonical] before the schema
// check. The alias is never added to a schema, which would change the tool
// list some clients freeze. It applies only when the schema declares
// canonical and not alias.
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
