package notify_test

import (
	"bufio"
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"strings"
	"testing"
	"time"

	"simplehost/api"
	"simplehost/core/testkit"
	"simplehost/data/rows"
	datarules "simplehost/data/rules"
	datasql "simplehost/data/sql"
	"simplehost/ops/notify"
)

// device registers a browser for push for who, at the fake push service.
func device(t *testing.T, k *testkit.Kit, who testkit.Who) {
	key, _ := ecdh.P256().GenerateKey(rand.Reader)
	secret := make([]byte, 16)
	rand.Read(secret)
	b64 := base64.RawURLEncoding.EncodeToString
	r := testkit.Call(t, k.Browser(), "POST", "https://simple-host.app/v1/me/push", map[string]any{"endpoint": k.Push.URL + who.Handle,
		"name": "phone", "keys": map[string]string{"p256dh": b64(key.PublicKey().Bytes()), "auth": b64(secret)}}, "X-API-Key", who.Key)
	if r.Status != 200 {
		t.Fatalf("push device: %d %s", r.Status, r.Body)
	}
}

// SPEC test 24 (F17): a data change in a watched table reaches the owner's
// push and MCP events. A visitor gets nothing.
func TestDataChangeTellsTheOwner(t *testing.T) {
	k := testkit.Start(t, notify.Mount, api.Mount, rows.Mount, datasql.Mount, datarules.Mount)
	nina, vera := k.Person(t, "nina"), k.Person(t, "vera")
	k.Publish(t, nina, "cafe", map[string]string{"index.html": "cafe"})
	owner := func(method, path string, body any) {
		if r := testkit.Call(t, k.Browser(), method, "https://simple-host.app/v1/sites/cafe"+path, body, "X-API-Key", nina.Key); r.Status != 200 {
			t.Fatalf("%s %s: %d %s", method, path, r.Status, r.Body)
		}
	}
	owner("POST", "/storage/sqlite/book/execute", map[string]any{"sql": "CREATE TABLE bookings (id INTEGER PRIMARY KEY, name TEXT, visitor_id TEXT)"})
	owner("PUT", "/storage/book/rules", `{"rules": {"bookings": {"version": 1, "policies": [{"name": "add", "command": "add", "check": "auth.signed_in"}]}}}`)
	owner("PATCH", "", map[string]any{"notifications": []map[string]string{{"kind": "data", "namespace": "book", "target": "bookings"}}})
	device(t, k, nina)
	device(t, k, vera)

	// A visitor books on the page.
	site := "https://cafe--nina.simple-host.site/"
	page := k.Browser()
	k.SignIn(t, page, vera, site)
	if r := testkit.Call(t, page, "POST", site+"v1/sites/cafe/storage/sqlite/book/tables/bookings/rows", map[string]string{"name": "Vera"}, "X-SH-CSRF", "1"); r.Status != 200 {
		t.Fatalf("booking: %d %s", r.Status, r.Body)
	}

	// The AI app's event stream, from its start (Last-Event-ID 0).
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", "https://simple-host.app/mcp", nil)
	req.Header.Set("X-API-Key", nina.Key)
	req.Header.Set("Last-Event-ID", "0")
	stream, err := k.Browser().Do(req)
	if err != nil || stream.StatusCode != 200 {
		t.Fatalf("event stream: %v %v", stream, err)
	}
	defer stream.Body.Close()

	lines := bufio.NewScanner(stream.Body)
	for lines.Scan() && !strings.HasPrefix(lines.Text(), "data:") {
	}
	if got := lines.Text(); !strings.Contains(got, "notifications/message") || !strings.Contains(got, "bookings") {
		t.Fatalf("event: %q %v", got, lines.Err())
	}
	if err := notify.Deliver(context.Background(), k.App); err != nil {
		t.Fatal(err)
	}
	if sent := k.Push.Sent(); len(sent) != 1 || sent[0] != "/push/nina" {
		t.Fatalf("pushes: %v, want only nina's device", sent)
	}
	if events, err := notify.Feed(context.Background(), k.App, vera.ID, 0); err != nil || len(events) != 0 {
		t.Fatalf("the visitor's feed: %v %v", events, err)
	}
}
