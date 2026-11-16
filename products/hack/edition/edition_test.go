package edition

import (
	"github.com/vsriram/simple-host/internal/config"
	"testing"
)

func TestProductPolicies(t *testing.T) {
	host, product := ForKind(Host), ForKind(Hack)
	if host.Identity().TeamKeys || !host.Identity().PersonalSites || host.Notifications().EventScoped {
		t.Fatal("platform policy changed")
	}
	if !product.Routes().HostedEvents || !product.Identity().TeamKeys || !product.Identity().HoldingAccounts || !product.Storage().EventOwners || !product.Storage().CombinedPool || !product.Notifications().EventScoped || product.Presentation().ProductName != "Simple Hack" {
		t.Fatal("product policy changed")
	}
	limits := config.DefaultLimits()
	limits.MaxFilesPerSite = 123
	if ForKindWithLimits(Hack, limits).Limits() != limits {
		t.Fatal("limits lost")
	}
}
