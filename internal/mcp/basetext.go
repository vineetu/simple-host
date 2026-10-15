package mcp

import (
	"strings"
	"sync"
)

// Public copy uses simple-host.app. The old base-domain text rewriter stays
// available for compatibility; no public copy advertises the paused base.

const (
	appDomain  = "simple-host.app"
	baseDomain = "simple-host.site"
)

var (
	addressMu sync.RWMutex
	// addressTo is what baseDomain is sent as ("" = as written).
	addressTo = appDomain
)

// SetAddressBase says where addresses are handed out: siteDomain is the app's
// domain and handoutBase the domain of people's addresses. Only the hosted
// service handing out simple-host.site addresses sends the text as written.
// Call before NewServer.
func SetAddressBase(siteDomain, handoutBase string) {
	to := appDomain
	if strings.EqualFold(siteDomain, appDomain) && strings.EqualFold(handoutBase, baseDomain) {
		to = ""
	}
	addressMu.Lock()
	addressTo = to
	addressMu.Unlock()
}

// addressText applies the swap to s.
func addressText(s string) string {
	addressMu.RLock()
	to := addressTo
	addressMu.RUnlock()
	if to == "" || !strings.Contains(s, baseDomain) {
		return s
	}
	return strings.ReplaceAll(s, baseDomain, to)
}

// addressTools applies the swap to every text in tools.
func addressTools(tools []Tool) []Tool {
	for i := range tools {
		tools[i].Title = addressText(tools[i].Title)
		tools[i].Description = addressText(tools[i].Description)
		tools[i].InputSchema = addressSchema(tools[i].InputSchema)
		tools[i].OutputSchema = addressSchema(tools[i].OutputSchema)
	}
	return tools
}

// addressSchema applies the swap to every string in a JSON schema, in place.
func addressSchema(m map[string]any) map[string]any {
	for k, v := range m {
		m[k] = addressValue(v)
	}
	return m
}

func addressValue(v any) any {
	switch x := v.(type) {
	case string:
		return addressText(x)
	case map[string]any:
		return addressSchema(x)
	case []any:
		for i := range x {
			x[i] = addressValue(x[i])
		}
		return x
	case []string:
		for i := range x {
			x[i] = addressText(x[i])
		}
		return x
	}
	return v
}

// instanceRewrite names this server in the connector's own text on an
// install that is not simple-host.app (Config.Rewrite: the same host
// rewriter llms.txt and Get started use). Nil on simple-host.app.
var instanceRewrite func(string) string

// instanceText is s for this server: the base swap, then, on another
// install, its own hostnames. The support address is the operator's, never
// rewritten into a mailbox that does not exist.
func instanceText(s string) string {
	s = addressText(s)
	if instanceRewrite == nil || !strings.Contains(s, appDomain) {
		return s
	}
	const mark = "\x00support\x00"
	s = strings.ReplaceAll(s, "support@"+appDomain, mark)
	s = instanceRewrite(s)
	contact := supportContact
	if contact == "" {
		contact = "support@" + appDomain
	}
	return strings.ReplaceAll(s, mark, contact)
}

// instanceTools applies instanceText to every text in tools.
func instanceTools(tools []Tool) []Tool {
	if instanceRewrite == nil {
		return tools
	}
	for i := range tools {
		tools[i].Title = instanceText(tools[i].Title)
		tools[i].Description = instanceText(tools[i].Description)
		tools[i].InputSchema = mapSchema(tools[i].InputSchema, instanceText)
		tools[i].OutputSchema = mapSchema(tools[i].OutputSchema, instanceText)
	}
	return tools
}

func mapSchema(m map[string]any, f func(string) string) map[string]any {
	for k, v := range m {
		m[k] = mapValue(v, f)
	}
	return m
}

func mapValue(v any, f func(string) string) any {
	switch x := v.(type) {
	case string:
		return f(x)
	case map[string]any:
		return mapSchema(x, f)
	case []any:
		for i := range x {
			x[i] = mapValue(x[i], f)
		}
		return x
	case []string:
		for i := range x {
			x[i] = f(x[i])
		}
		return x
	}
	return v
}
