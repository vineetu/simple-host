package mcp

import (
	"encoding/json"
	"time"
)

// uiDeclaredForListing reports whether this connection has declared MCP
// Apps UI support, for gating a screen tool's presence in tools/list
// (never its behaviour -- that stays c.uiClient, set at call time from
// the same two sources). A modern (stateless) request carries its
// capabilities on every call, including tools/list, since every modern
// request's params._meta must carry io.modelcontextprotocol/
// clientCapabilities (server.go's validateHeaders); an initialize-era
// client only ever declares it once, in initialize, so clientUIMemory is
// the only record of it by the time tools/list is asked.
func (s *Server) uiDeclaredForListing(caller Caller, params json.RawMessage, modern bool) bool {
	if modern {
		var envelope struct {
			Meta map[string]json.RawMessage `json:"_meta"`
		}
		if json.Unmarshal(params, &envelope) != nil {
			return false
		}
		raw, present := envelope.Meta["io.modelcontextprotocol/clientCapabilities"]
		if !present {
			return false
		}
		var caps struct {
			Extensions map[string]json.RawMessage `json:"extensions"`
		}
		if json.Unmarshal(raw, &caps) != nil {
			return false
		}
		_, ui := caps.Extensions["io.modelcontextprotocol/ui"]
		return ui
	}
	ui, known := s.clientUI.lookup(caller, time.Now())
	return known && ui
}
