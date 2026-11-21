package web

import (
	"strings"
	"testing"

	_ "github.com/lib/pq"

	httpx "github.com/vsriram/simple-host/host/httpx"
)

func TestCleanClientName(t *testing.T) {
	if got := httpx.CleanClientName("  Claude‮\n  Desktop  "); got != "Claude Desktop" {
		t.Errorf("got %q", got)
	}
	if got := httpx.CleanClientName(""); got != "An app" {
		t.Errorf("got %q", got)
	}
	if got := httpx.CleanClientName(strings.Repeat("x", 200)); len([]rune(got)) != 60 {
		t.Errorf("not truncated: %d", len(got))
	}
}
