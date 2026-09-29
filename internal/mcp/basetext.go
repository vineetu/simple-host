package mcp

import (
	"strings"
	"sync"
)

// The tool descriptions, output schemas, hints and instructions name a
// person's or site's address under simple-host.site (the base domain people's
// addresses move to; the app stays on simple-host.app). Until the hosted
// service hands those addresses out, and on every other install, they are
// sent with simple-host.site swapped back to simple-host.app: exactly the
// text sent before the move existed.

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
