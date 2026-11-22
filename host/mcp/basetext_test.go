package mcp

import (
	"encoding/json"
	"strings"
	"testing"
)

// Connector descriptions, schemas and instructions use simple-host.app, and
// name simple-host.site only on the hosted service with the flat base on.
func TestAddressBase(t *testing.T) {
	defer SetAddressBase("", "")
	send := func() string {
		s := NewServer(testConfig(Config{}))
		b, _ := json.Marshal(s.tools)
		return string(b) + addressText(Instructions()) + addressText(resourceCodeHint("custom_domain_required", ""))
	}
	SetAddressBase("simple-host.app", "simple-host.app")
	today := send()
	if strings.Contains(today, "simple-host.site") || !strings.Contains(today, "simple-host.app") {
		t.Fatal("default text names the base")
	}
	SetAddressBase("hack.example.com", "simple-host.site")
	if send() != today {
		t.Fatal("another install's text changed")
	}
	SetAddressBase("simple-host.app", "simple-host.site")
	if flat := send(); !strings.Contains(flat, "simple-host.site") || strings.Replace(flat, "simple-host.site", "simple-host.app", -1) != today {
		t.Fatal("the flat base: text names only the base, and only in place of simple-host.app")
	}
}
