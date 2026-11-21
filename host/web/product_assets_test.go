package web

import (
	"strings"
	"testing"

	"github.com/vsriram/simple-host/host/config"

	"github.com/vsriram/simple-host/host/edition"
)

func TestAssetServicesInstanceRewriting(t *testing.T) {
	old := InstanceHosts
	InstanceHosts = newHostRewriter("assets.example", "sites.assets.example", "")
	t.Cleanup(func() { InstanceHosts = old })
	p := NewAssetPipeline(edition.Default(config.DefaultLimits()), "")
	body, ok := p.Static("llms.txt")
	if !ok || !strings.Contains(string(body), "assets.example") {
		t.Fatal("Static omitted the instance host rewrite")
	}
	if got := p.InstanceText([]byte("https://simple-host.app/mcp")); string(got) != "https://assets.example/mcp" {
		t.Fatalf("InstanceText: %s", got)
	}
}
