package datatest

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	appstorage "github.com/vsriram/simple-host/host/data"
	"github.com/vsriram/simple-host/host/data/datahttp"
	"github.com/vsriram/simple-host/host/mcp"
	"github.com/vsriram/simple-host/host/server/servertest"
)

// Save through the service: R3-S2b needs R2-S2, not the HTTP rules decoder
// or visitor enforcement built by the parallel R3-S1 slices.
func saveNotificationRules(t *testing.T, s servertest.StorageTestSite, read string) datahttp.StorageResource {
	t.Helper()
	h := s.A.Data
	resource, err := h.LoadStorageResource(context.Background(), s.Sid, "orders")
	if err != nil {
		t.Fatal(err)
	}
	c := datahttp.StorageCall{SiteID: s.Sid, OwnerID: s.OwnerID, SiteName: "shop", ResourceName: "orders", Resource: resource, Owner: true}
	raw, err := json.Marshal(map[string]any{"version": 1, "policies": []any{
		map[string]any{"name": "read", "command": "read", "using": read},
	}})
	if err != nil {
		t.Fatal(err)
	}
	rules := json.RawMessage(raw)
	req := httptest.NewRequest("PUT", "/", nil)
	result, err := h.StorageFor(c).PutStorageResource(req.Context(), appstorage.ResourceInput{
		Call: c, Name: "orders", Kind: "sqlite", Rules: &rules,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Resource.RulesStatus() != "rules" {
		t.Fatalf("saved resource status = %s", result.Resource.RulesStatus())
	}
	return result.Resource
}

func TestNotifyRulesEventPayloads(t *testing.T) {
	for _, rules := range []bool{false, true} {
		t.Run(fmt.Sprintf("saved=%t", rules), func(t *testing.T) {
			s := webPushFixture(t)
			x := newNotificationReceiver(t)
			s.A.Data.Notifier().UseTestClient(x.roots(), true)
			subscribeNotification(t, s, x)
			backend := s.A.Data.EventsBackend()
			for _, name := range []string{"record.added", "record.changed"} {
				_, err := backend.Subscribe(context.Background(), mcp.Caller{Owner: s.OwnerID, AppName: "Test app"}, mcp.EventSubscribeRequest{
					Name: name, Site: "shop", CanonicalArgs: `{"site":"shop"}`, URL: x.server.URL, Secret: notifyTestSecret,
				})
				if err != nil {
					t.Fatal(err)
				}
			}
			resource, err := s.A.Data.LoadStorageResource(context.Background(), s.Sid, "orders")
			if err != nil {
				t.Fatal(err)
			}
			if rules {
				resource = saveNotificationRules(t, s, "true")
			}
			server := mcp.NewServer(mcp.Config{Events: backend})
			listed := siteEventRPC(t, s, server, "events/list", nil)["result"].(map[string]any)["events"].([]any)
			schemas := map[string]any{}
			for _, raw := range listed {
				event := raw.(map[string]any)
				schemas[event["name"].(string)] = event["payloadSchema"]
			}
			c := datahttp.StorageCall{SiteID: s.Sid, OwnerID: s.OwnerID, SiteName: "shop", ResourceName: "orders", Resource: resource, VisitorID: s.AliceID}
			for _, name := range []string{"record.added", "record.changed"} {
				data := map[string]any{"table": "orders", "row_id": 1, "fields": json.RawMessage(`{"item":"event snapshot"}`), "row_visitor_id": s.AliceID}
				if name == "record.changed" {
					data["change"] = "edited"
				}
				s.A.Data.StoreNotification(c, name, data)
			}
			drainNotifications(t, s)
			seen := map[string]int{}
			for _, received := range x.snapshot() {
				if received.body["type"] == "verification" {
					continue
				}
				name := received.body["name"].(string)
				data := received.body["data"].(map[string]any)
				if err := mcp.ValidateOutput(schemas[name], data, nil); err != nil {
					t.Fatalf("%s schema: %v", name, err)
				}
				if data["fields"].(map[string]any)["item"] != "event snapshot" {
					t.Fatal("owner subscription lost event fields")
				}
				kind := "mcp-event"
				if received.header.Get("Authorization") != "" {
					kind = "webhook"
				}
				seen[kind+"/"+name]++
			}
			want := map[string]int{"webhook/record.added": 1, "webhook/record.changed": 1, "mcp-event/record.added": 1, "mcp-event/record.changed": 1}
			if !reflect.DeepEqual(seen, want) {
				t.Fatalf("deliveries = %v, want %v", seen, want)
			}
		})
	}
}

func TestWebPushRulesQueuedDelivery(t *testing.T) {
	for _, policy := range []struct{ name, read string }{
		{"public", "true"},
		{"own-or-owner", `visitor_id == auth.id || auth.role == "owner"`},
		{"owner-only", `auth.role == "owner"`},
	} {
		for _, after := range []string{"unchanged", "delete-row", "change-row", "change-rules", "none", "lookup-failure"} {
			t.Run(policy.name+"/"+after, func(t *testing.T) {
				s := webPushFixture(t)
				row := s.Must(t, "POST", "/sqlite/orders/tables/orders/rows", map[string]string{"item": "original"}, s.Callers["alice"], 200).JSON(t)["last_insert_id"]
				waitNotificationEmits(t, s)
				saveNotificationRules(t, s, policy.read)
				srv, hits := pushTestServer(t)
				defer srv.Close()
				wirePushTestClient(t, s, srv)
				devices := map[string]string{}
				for _, caller := range []string{"alice", "bob", "owner-site"} {
					r := s.A.At(t, "POST", s.Host, "/v1/sites/shop/push/subscribe", servertest.PushSubscribeBody(t, srv.URL+"/"+caller, "orders"), s.Callers[caller])
					if r.Status != 201 {
						t.Fatalf("subscribe %s: %d %s", caller, r.Status, r.Body)
					}
					devices[caller] = r.JSON(t)["id"].(string)
				}
				setPushRule := func(recipients, column string, status int) {
					t.Helper()
					r := s.A.At(t, "PUT", servertest.PcSiteDomain, "/v1/sites/shop/push/rules", map[string]any{"resource": "orders", "recipients": recipients, "body_column": column}, s.Key)
					if r.Status != status {
						t.Fatalf("push rule: %d %s, want %d", r.Status, r.Body, status)
					}
				}
				// Exercise Inspect on a rules resource, including absent columns.
				setPushRule("both", "no_such_column", 400)
				setPushRule("both", "visitor_id", 400)
				setPushRule("both", "item", 200)
				path := "/sqlite/orders/tables/orders/rows/" + fmt.Sprint(row)
				s.Must(t, "PATCH", path, map[string]string{"item": "owner's reply"}, s.Key, 200)
				if got := pendingPushSubscriptions(t, s); !reflect.DeepEqual(got, []string{devices["alice"]}) {
					t.Fatalf("push recipients = %v, want only row author %s", got, devices["alice"])
				}
				switch after {
				case "delete-row", "change-row":
					// Owner SQL changes the stored row without emitting a second event.
					sql := "DELETE FROM orders WHERE id=" + fmt.Sprint(row)
					if after == "change-row" {
						sql = "UPDATE orders SET item='later value' WHERE id=" + fmt.Sprint(row)
					}
					s.Must(t, "POST", "/sqlite/orders/execute", map[string]string{"sql": sql}, s.Key, 200)
				case "change-rules":
					saveNotificationRules(t, s, "false")
				case "none":
					setPushRule("none", "item", 200)
				}
				h := s.A.Data
				ctx := context.Background()
				d, err := notificationsSvc(h).ClaimNotificationDelivery(ctx, time.Minute, 5, 7, time.Hour)
				if err != nil {
					t.Fatal(err)
				}
				n, err := notificationsSvc(h).GetNotification(ctx, d.SubscriptionID)
				if err != nil {
					t.Fatal(err)
				}
				if after == "lookup-failure" {
					// The claim is left to expire and be retried, never
					// delivered as handled.
					failed, cancel := context.WithCancel(ctx)
					cancel()
					if got := h.Notifier().PushPayloadForTest(failed, n, d); got != nil {
						t.Fatalf("failed push-rule lookup must defer, got %s", got)
					}
				} else {
					got := h.Notifier().PushPayloadForTest(ctx, n, d)
					if after == "none" {
						if string(got) != "null" {
							t.Fatalf("revoked push = %s, want skip", got)
						}
					} else {
						var payload map[string]any
						if err := json.Unmarshal(got, &payload); err != nil || payload["body"] != "owner's reply" {
							t.Fatalf("push must use queued owner-chosen column: %s (%v)", got, err)
						}
					}
					h.Notifier().Process(ctx, h.NotifyEnv(), d)
				}
				wantHits := int32(1)
				if after == "none" || after == "lookup-failure" {
					wantHits = 0
				}
				if got := atomic.LoadInt32(hits); got != wantHits {
					t.Fatalf("push requests = %d, want %d", got, wantHits)
				}
				if after == "lookup-failure" {
					if got := pendingPushSubscriptions(t, s); !reflect.DeepEqual(got, []string{devices["alice"]}) {
						t.Fatalf("failed lookup lost queued push: %v", got)
					}
					notificationSQL(t, s, `UPDATE site_event_deliveries SET lease_until=now()-interval '1 second' WHERE subscription_id=$1`, devices["alice"])
					drainNotifications(t, s)
					if atomic.LoadInt32(hits) != 1 {
						t.Fatal("deferred push was not retried")
					}
				}
				if got := pendingPushSubscriptions(t, s); len(got) != 0 {
					t.Fatalf("unfinished deliveries: %v", got)
				}
			})
		}
	}
}
