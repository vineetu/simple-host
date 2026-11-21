package mcp

// tools/call: argument checks, run_tool unwrapping, the tool run and its result.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
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
	a := &callAnalytics{s: s, caller: caller, event: params.Name, started: time.Now(),
		secrets: analyticsSecretValues(s.policy(params.Name), params.Arguments, s.policy)}
	a.intent, _ = takeAnalyticsContext(byName[params.Name], params.Arguments)
	defer func() { a.send(params.Arguments, reply) }()
	refuse := func(err error, text string) response {
		a.record(err)
		return toolResult(req.ID, text, nil, true, modern)
	}
	// Unwrap before lookup so direct and wrapped calls share the same checks.
	_, runTool := byName["run_tool"]
	wrapped := runTool && byName[params.Name].policy.unwrap
	if wrapped {
		name, args, text, err := s.unwrapRunTool(byName, params.Arguments, a)
		if err != nil {
			return refuse(err, text)
		}
		params.Name, params.Arguments = name, args
	}
	tool, known := byName[params.Name]
	if inner, present := takeAnalyticsContext(tool, params.Arguments); present {
		a.intent = inner
	}
	if !known {
		return s.unknownTool(req.ID, params.Name, caller, wrapped, modern, a.record)
	}
	if params.Arguments == nil {
		params.Arguments = map[string]any{}
	}
	c, text, err := s.prepareCall(r, tool, params, caller, wrapped, modern)
	if err != nil {
		return refuse(err, text)
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
// arguments. The refusal comes with the text the caller reads.
func (s *Server) unwrapRunTool(byName map[string]Tool, outer map[string]any, a *callAnalytics) (name string, args map[string]any, text string, err error) {
	name, nameErr := stringArg(outer, "name")
	inner, objectArgs := outer["arguments"].(map[string]any)
	if intent, present := takeAnalyticsContext(byName[name], inner); present {
		a.intent = intent
	}
	target, known := byName[name]
	validTarget := known && !target.policy.discovery
	if validTarget {
		a.event = name
	}
	_, present := outer["arguments"]
	if s.analytics != nil {
		a.parameters = map[string]any{"arguments": "[removed]"}
		if validTarget && (objectArgs || !present) {
			a.parameters = analyticsParametersForPolicy(s.policy(name), inner)
		}
	}
	if err := runToolRefusal(byName, outer, nameErr, s.policy(name).screenOnly, target.policy.discovery); err != nil {
		return "", nil, err.Error(), err
	}
	if present && !objectArgs {
		err := errors.New("arguments must be an object")
		return "", nil, argumentMessage(err, target), err
	}
	return name, inner, "", nil
}

// runToolRefusal is why run_tool may not run this call, or nil.
func runToolRefusal(byName map[string]Tool, outer map[string]any, nameErr error, screenOnly, discovery bool) error {
	switch err := unexpectedArgument(byName["run_tool"], outer); {
	case err != nil:
		return err
	case nameErr != nil:
		return nameErr
	case screenOnly:
		return errors.New("passcode_screen is only for the passcode screen")
	case discovery:
		return errors.New("Nested discovery cannot call another discovery tool. Choose the required operation from the tool list.")
	}
	return nil
}

// prepareCall checks the arguments and builds the call the tool runs in.
func (s *Server) prepareCall(r *http.Request, tool Tool, params callParams, caller Caller, wrapped, modern bool) (*call, string, error) {
	if argErr := checkArguments(tool, params.Arguments, wrapped); argErr != nil {
		// Some apps (ChatGPT custom servers) keep the tool list from when the
		// person connected, so a stale schema is common: give the current one.
		message := argumentMessage(argErr, tool)
		if !wrapped {
			message += "\n" + staleToolsHint
		}
		return nil, message, codedError("invalid_argument", argErr.Error())
	}
	c := &call{server: s, orig: r, caller: caller, policy: tool.policy}
	if err := c.takeTeamArguments(params.Arguments); err != nil {
		return nil, argumentMessage(err, tool), codedError("invalid_argument", err.Error())
	}
	s.setClientUI(c, params.Meta, caller, modern)
	// A host draws a screen for the tool it listed, never for run_tool.
	if wrapped {
		c.wrapped, c.textOnlyClient = true, true
	}
	return c, "", nil
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
	a.s.analytics.enqueue(a.caller, a.event, a.completed.Sub(a.started), a.completed, a.err,
		redactAnalyticsSecrets(parameters, a.secrets), redactAnalyticsSecrets(analyticsResponseForPolicy(policy, reply.Result), a.secrets),
		capAnalyticsIntent(redactAnalyticsSecrets(intent, a.secrets).(string)))
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
// family's word for the same concept: storage_* tools call the saved-data resource "name", while visitor_read_site/
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
	case strings.HasPrefix(name, "storage_"):
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
