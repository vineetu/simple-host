package mcp

import (
	"encoding/json"
	"strings"
	"testing"
)

// The connector's text names people's addresses under simple-host.site in
// source; it is sent as simple-host.app (exactly today's text) until the
// hosted service hands the base out.
func TestAddressBase(t *testing.T) {
	defer SetAddressBase("", "")
	send := func() string {
		s := NewServer(Config{})
		b, _ := json.Marshal(s.tools)
		return string(b) + addressText(Instructions()) + addressText(codeHint("custom_domain_required"))
	}
	SetAddressBase("simple-host.app", "simple-host.app")
	today := send()
	if strings.Contains(today, "simple-host.site") || !strings.Contains(today, "<site>.<handle>.simple-host.app") {
		t.Fatal("default text names the base")
	}
	SetAddressBase("hack.example.com", "simple-host.site")
	if send() != today {
		t.Fatal("another install's text changed")
	}
	SetAddressBase("simple-host.app", "simple-host.site")
	moved := send()
	if !strings.Contains(moved, "<site>.<handle>.simple-host.site") || !strings.Contains(moved, "https://simple-host.app/") {
		t.Fatal("moved text")
	}
	if strings.ReplaceAll(moved, "simple-host.site", "simple-host.app") != today {
		t.Fatal("the move changed more than the base")
	}
}
