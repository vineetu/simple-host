package notify

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/vsriram/simple-host/host/access"
	"github.com/vsriram/simple-host/host/model"
)

// An MCP subscriber sees a visitor as the connector's site-scoped reference
// (never the account id), a webhook the raw id; the numbers in the fields
// keep their exact digits and the stored event is not changed. An
// event-scoped edition keeps the id for MCP too.
func TestRDWebhookBodyVisitorReferences(t *testing.T) {
	const visitorID = "1b4e28ba-2fa1-11d2-883f-0016d3cca427"
	const site = "2b4e28ba-2fa1-11d2-883f-0016d3cca427"
	env := Env{VisitorRef: []byte("rd-visitor-ref-key-0123456789abc")}
	want, err := access.NewVisitorReferences(env.VisitorRef).Encode(site, visitorID)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"record.added", "record.changed", "file.uploaded"} {
		for _, visitor := range []any{visitorID, nil} {
			raw, _ := json.Marshal(map[string]any{"visitor": visitor, "site": "blog", "fields": map[string]any{"item": "cake", "number": json.Number("9007199254740993")}})
			for _, source := range []string{"mcp", "webhook"} {
				d := model.NotificationDelivery{EventID: "evt_test", Name: name, Data: raw}
				body := WebhookBody(env, false, model.Notification{Source: source, SiteID: site}, d, 1, 1<<20)
				var out struct {
					Data struct {
						Visitor any `json:"visitor"`
						Fields  struct {
							Number json.Number `json:"number"`
						} `json:"fields"`
					} `json:"data"`
				}
				if err := json.Unmarshal(body, &out); err != nil {
					t.Fatalf("%s %s: %v", name, source, err)
				}
				expect := visitor
				if source == "mcp" && visitor != nil {
					expect = want
				}
				if out.Data.Visitor != expect {
					t.Errorf("%s %s: visitor = %v, want %v", name, source, out.Data.Visitor, expect)
				}
				if source == "mcp" && bytes.Contains(body, []byte(visitorID)) {
					t.Errorf("%s mcp: payload leaks the account id", name)
				}
				if out.Data.Fields.Number.String() != "9007199254740993" {
					t.Errorf("%s %s: number became %s", name, source, out.Data.Fields.Number)
				}
				if !bytes.Equal(d.Data, raw) {
					t.Errorf("%s %s: stored event mutated", name, source)
				}
			}
		}
	}
	raw, _ := json.Marshal(map[string]any{"visitor": visitorID})
	body := WebhookBody(env, true, model.Notification{Source: "mcp", SiteID: site}, model.NotificationDelivery{Data: raw}, 1, 1<<20)
	if !bytes.Contains(body, []byte(visitorID)) {
		t.Error("an event-scoped edition's MCP subscriber lost the visitor id")
	}
}
