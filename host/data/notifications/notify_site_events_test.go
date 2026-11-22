package notifications

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/vsriram/simple-host/host/server/servertest/datatest"

	"github.com/vsriram/simple-host/host/access"

	appstorage "github.com/vsriram/simple-host/host/data"
	notifyapp "github.com/vsriram/simple-host/host/data/notify"
	"github.com/vsriram/simple-host/host/httpx"
)

// Site events, engine side. The hooks that emit them (deploys, domain
// checks, sign-ins) belong to Sites and Access.

func siteEventCount(t *testing.T, s datatest.Shop, name string) int {
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
	s.A.Data.StoreNotification(appstorage.Call{SiteID: s.Sid}, "record.added", map[string]any{"visitor": s.AliceID})
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
