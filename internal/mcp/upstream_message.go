package mcp

import (
	"encoding/json"
	"net/http"
	"strings"
)

func upstreamMessage(u upstreamResult) (string, string) {
	var payload struct {
		Error string `json:"error"`
		Code  string `json:"code"`
	}
	_ = json.Unmarshal(u.body, &payload)
	msg := payload.Error
	if msg == "" {
		msg = strings.TrimSpace(string(u.body))
		if len(msg) > 300 {
			msg = msg[:300]
		}
	}
	if msg == "" {
		msg = http.StatusText(u.status)
	}
	return msg, payload.Code
}
