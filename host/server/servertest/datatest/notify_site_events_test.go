package datatest

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/vsriram/simple-host/host/access"
	"github.com/vsriram/simple-host/host/data/datahttp"
	notifyapp "github.com/vsriram/simple-host/host/data/notify"
	"github.com/vsriram/simple-host/host/httpx"
)

// Site events, engine side. The hooks that emit them (deploys, domain
// checks, sign-ins) belong to Sites and Access.

func siteEventCount(t *testing.T, s Shop, name string) int {
	t.Helper()
	return countRows(t, s, `SELECT count(*) FROM site_events WHERE site_id=$1 AND name=$2`, s.Sid, name)
}

func siteEventDeliveries(x *notificationReceiver, name string) []map[string]any {
	var out []map[string]any
	for _, r := range x.snapshot() {
		if r.body["name"] == name {
			out = append(out, r.body)
		}
	}
	return out
}

func TestNotifySiteEventsRESTAndDelivery(t *testing.T) {
	s, x := notificationFixture(t)
	all := s.A.Data.Notifier().EventNamesForTest()
	if len(all) != 7 {
		t.Fatalf("event names: %v", all)
	}
	created := notificationRequest(t, s, "POST", "", map[string]any{"url": x.server.URL, "events": all, "secret": notifyTestSecret}, 201).JSON(t)["notification"].(map[string]any)
	notificationRequest(t, s, "DELETE", "/"+created["id"].(string), nil, 200)
	for _, events := range [][]string{{"record.added", "site.nope"}, {"site.deployed", "site.deployed"}} {
		if r := notificationRequest(t, s, "POST", "", map[string]any{"url": x.server.URL, "events": events, "secret": notifyTestSecret}, 400); r.Code() != "invalid_events" {
			t.Fatalf("%v: code %v", events, r.Code())
		}
	}
	// A storage filter narrows record events only.
	s.Must(t, "PUT", "/resources/orders", StorageRules("sqlite", RulesPersonal), s.Key, 201)
	notificationRequest(t, s, "POST", "", map[string]any{"url": x.server.URL, "events": []string{"record.added", "site.deployed", "viewer.access_requested", "visitor.signed_up"}, "storage": "orders", "secret": notifyTestSecret}, 201)
	s.A.Data.EmitSiteNotification(s.Sid, "site.deployed", map[string]any{"version": 2, "previous_version": 1, "via": "deploy"}, 0)
	drainNotifications(t, s)
	got := siteEventDeliveries(x, "site.deployed")
	if len(got) != 1 {
		t.Fatalf("want one site.deployed delivery, got %d", len(got))
	}
	data := got[0]["data"].(map[string]any)
	if data["v"] != float64(1) || data["site"] != "shop" || !strings.HasPrefix(data["site_url"].(string), "https://shop.") || data["version"] != float64(2) || data["via"] != "deploy" || len(data) != 6 {
		t.Fatalf("site.deployed data: %v", data)
	}
	// The owner on their own site is not an event; one per person per
	// dedupe window; another person is their own event.
	s.A.Data.EmitSiteNotification(s.Sid, "visitor.signed_up", map[string]any{"visitor": s.OwnerID}, 0)
	for _, who := range []string{s.AliceID, s.AliceID, s.Ids["bob"]} {
		s.A.Data.EmitSiteNotification(s.Sid, "viewer.access_requested", map[string]any{"visitor": who}, 24*time.Hour)
		waitNotificationEmits(t, s)
	}
	if siteEventCount(t, s, "visitor.signed_up") != 0 || siteEventCount(t, s, "viewer.access_requested") != 2 {
		t.Fatal("owner event stored, or dedupe wrong")
	}
	drainNotifications(t, s)
	requested := siteEventDeliveries(x, "viewer.access_requested")
	if len(requested) != 2 || requested[0]["data"].(map[string]any)["visitor"] != s.AliceID {
		t.Fatalf("access requests: %v", requested)
	}
}

// A connector subscription's site event names the visitor by an opaque
// reference, never the account id.
func TestNotifySiteEventsMCPVisitorReference(t *testing.T) {
	s, x := notificationFixture(t)
	req := notifyapp.EventSubscribeRequest{Name: "visitor.signed_up", Site: "shop", CanonicalArgs: `{"site":"shop"}`, URL: x.server.URL, Secret: notifyTestSecret}
	result := mustMCPSubscribe(t, s, notifyapp.Subscriber{Owner: s.OwnerID, AppName: "Test app"}, req)
	if n := getNotification(t, s, result.ID); n.Storage != "" || n.Source != "mcp" {
		t.Fatalf("stored MCP site subscription: %+v", n)
	}
	req.Storage = "orders"
	if _, err := mcpSubscribe(s, notifyapp.Subscriber{Owner: s.OwnerID}, req); err == nil {
		t.Fatal("storage on a site event accepted")
	}
	s.A.Data.EmitSiteNotification(s.Sid, "visitor.signed_up", map[string]any{"visitor": s.AliceID}, 0)
	drainNotifications(t, s)
	got := siteEventDeliveries(x, "visitor.signed_up")
	if len(got) != 1 {
		t.Fatalf("want one visitor.signed_up delivery, got %d", len(got))
	}
	expected, err := access.NewVisitorReferences(s.A.Data.PasscodeKeys.VisitorRef(s.A.Data.ExportKey)).Encode(s.Sid, s.AliceID)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(got[0])
	if got[0]["data"].(map[string]any)["visitor"] != expected || bytes.Contains(raw, []byte(s.AliceID)) {
		t.Fatalf("MCP delivery leaks the account id: %s", raw)
	}
}

// An event no subscription names stores nothing and spends no budget; a
// site event over the body limit is dropped.
func TestNotifyUnsubscribedFreeAndBodyTooLarge(t *testing.T) {
	s, x := notificationFixture(t)
	notificationRequest(t, s, "POST", "", map[string]any{"url": x.server.URL, "events": []string{"site.deployed"}, "secret": notifyTestSecret}, 201)
	minute := httpx.NewRateLimiter(1, 0)
	s.A.Data.Notifier().EventCapsForTest(minute, httpx.NewRateLimiter(1, 0))
	s.A.Data.StoreSiteNotification(s.Sid, "visitor.signed_up", map[string]any{"visitor": s.AliceID}, 0)
	s.A.Data.StoreNotification(datahttp.StorageCall{SiteID: s.Sid}, "record.added", map[string]any{"visitor": s.AliceID})
	if siteEventCount(t, s, "visitor.signed_up")+siteEventCount(t, s, "record.added") != 0 || !minute.Allow(s.Sid) {
		t.Fatal("unsubscribed event stored or spent the budget")
	}
	s.A.Data.Notifier().EventCapsForTest(httpx.NewRateLimiter(100, 0), httpx.NewRateLimiter(100, 0))
	big := strings.Repeat("x", s.A.Data.Limits.NotifyBodyMaxBytes+1)
	s.A.Data.StoreSiteNotification(s.Sid, "site.deployed", map[string]any{"version": 2, "previous_version": 1, "via": big}, 0)
	if siteEventCount(t, s, "site.deployed") != 0 {
		t.Fatal("oversized site event stored")
	}
	s.A.Data.StoreSiteNotification(s.Sid, "site.deployed", map[string]any{"version": 2, "previous_version": 1, "via": "deploy"}, 0)
	if siteEventCount(t, s, "site.deployed") != 1 {
		t.Fatal("normal site event not stored")
	}
}
