package mcp

import (
	"encoding/json"
	"strings"
	"testing"
)

// Connector descriptions, schemas and instructions all use simple-host.app.
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
	if send() != today {
		t.Fatal("the paused move advertised another base")
	}
}
