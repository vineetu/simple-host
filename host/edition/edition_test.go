package edition

import (
	"github.com/vsriram/simple-host/host/config"
	"testing"
)

func TestDefaultAndPolicies(t *testing.T) {
	var nilEd *Edition
	def := Default(config.DefaultLimits())
	if nilEd.Routes() != def.Routes() || nilEd.Identity() != def.Identity() || nilEd.Storage() != def.Storage() {
		t.Fatal("nil edition differs from platform default")
	}
	limits := config.DefaultLimits()
	limits.MaxFilesPerSite = 123
	got := FromPolicies(RoutePolicy{HostedEvents: true}, PresentationPolicy{ProductName: "Test product"}, IdentityPolicy{PaperMetrics: true}, StoragePolicy{CombinedPool: true}, NotificationPolicy{EventScoped: true}, limits)
	if !got.Routes().HostedEvents || got.Presentation().ProductName != "Test product" || !got.Identity().PaperMetrics || !got.Storage().CombinedPool || !got.Notifications().EventScoped || got.Limits().MaxFilesPerSite != 123 {
		t.Fatal("policies or limits lost")
	}
}
