package notify

import (
	"context"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/vsriram/simple-host/host/outbound"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vsriram/simple-host/host/config"
	"github.com/vsriram/simple-host/host/model"
)

const testSecret = "whsec_MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY="

// receiver is a local TLS webhook receiver recording each request.
type receiver struct {
	server  *httptest.Server
	mu      sync.Mutex
	headers []http.Header
	status  int
}

func newReceiver(t *testing.T) *receiver {
	t.Helper()
	x := &receiver{status: 200}
	x.server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		x.mu.Lock()
		x.headers = append(x.headers, r.Header.Clone())
		status := x.status
		x.mu.Unlock()
		if status == 302 {
			w.Header().Set("Location", x.server.URL+"/redirected")
		}
		w.WriteHeader(status)
	}))
	t.Cleanup(x.server.Close)
	return x
}

func trusting(srv *httptest.Server) client {
	roots := x509.NewCertPool()
	roots.AddCert(srv.Certificate())
	return client{Dialer: outbound.Dialer{AllowLoopback: true}, roots: roots}
}

func TestClientRefusesAddresses(t *testing.T) {
	c := client{}
	for _, raw := range []string{"https://127.0.0.1/", "https://169.254.169.254/", "https://10.0.0.1/", "https://[::1]/", "https://[::ffff:127.0.0.1]/", "https://100.64.0.1/", "https://[fc00::1]/"} {
		if _, err := c.validate(context.Background(), raw); !errors.Is(err, Failure("refused_address")) {
			t.Errorf("%s: %v", raw, err)
		}
	}
	for _, raw := range []string{"http://example.com/", "https://user:pass@example.com/", "https://example.com/#fragment", "https://example.com/#", "https://example.com:0/", strings.Repeat("a", 2049)} {
		if _, err := c.validate(context.Background(), raw); !errors.Is(err, Failure("invalid_url")) {
			t.Errorf("invalid URL accepted: %s", raw)
		}
	}
	c.Resolver = func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("8.8.8.8")}, {IP: net.ParseIP("10.0.0.1")}}, nil
	}
	if _, err := c.validate(context.Background(), "https://mixed.example/"); !errors.Is(err, Failure("refused_address")) {
		t.Fatal("mixed answer accepted")
	}
	calls := 0
	c.Resolver = func(context.Context, string) ([]net.IPAddr, error) {
		calls++
		ip := "8.8.8.8"
		if calls > 1 {
			ip = "127.0.0.1"
		}
		return []net.IPAddr{{IP: net.ParseIP(ip)}}, nil
	}
	result := c.send(context.Background(), Env{}, model.Notification{URL: "https://rebind.example/", Secret: testSecret}, "evt_test", []byte(`{}`))
	if result.Result != "refused_address" || calls != 2 {
		t.Fatalf("rebinding: %s, lookups %d", result.Result, calls)
	}
	c.Resolver = func(context.Context, string) ([]net.IPAddr, error) { return nil, errors.New("lookup failed") }
	if _, err := c.validate(context.Background(), "https://dns.example/"); !errors.Is(err, Failure("dns_error")) {
		t.Fatal(err)
	}
}

func TestClientRedirectAndHeaders(t *testing.T) {
	x := newReceiver(t)
	c := trusting(x.server)
	x.status = 302
	n := model.Notification{ID: "sub_test", URL: x.server.URL, Secret: testSecret, Source: "owner", AuthHeaderName: "Authorization", AuthHeaderValue: "Bearer receiver"}
	result := c.send(context.Background(), Env{}, n, "evt_redirect", []byte(`{}`))
	if result.Result != "redirect" || result.Status != 302 {
		t.Fatal(result.Result)
	}
	if len(x.headers) != 1 {
		t.Fatal("followed redirect")
	}
	h := x.headers[0]
	if h.Get("svix-signature") != h.Get("webhook-signature") || h.Get("Authorization") != "Bearer receiver" || h.Get("Content-Type") != "application/json" || h.Get("User-Agent") != "SimpleHost-Webhooks/1" || h.Get("X-MCP-Subscription-Id") != "sub_test" {
		t.Fatal("REST headers missing")
	}
	want, _ := Signature(testSecret, "evt_redirect", h.Get("webhook-timestamp"), []byte(`{}`))
	if h.Get("webhook-signature") != want {
		t.Fatal("signature")
	}
	n.Source = "mcp"
	x.status = 200
	if result = c.send(context.Background(), Env{}, n, "evt_mcp", []byte(`{}`)); !result.Delivered {
		t.Fatal(result.Result)
	}
	if x.headers[1].Get("svix-id") != "" || x.headers[1].Get("Authorization") != "" {
		t.Fatal("owner headers sent on MCP")
	}
	c.roots = nil
	if result = c.send(context.Background(), Env{}, n, "evt_tls", []byte(`{}`)); result.Result != "tls_error" {
		t.Fatalf("untrusted TLS: %s", result.Result)
	}
}

func TestClientPreviousSecretSigns(t *testing.T) {
	x := newReceiver(t)
	c := trusting(x.server)
	until := time.Now().Add(time.Hour)
	second, _ := NewSecret()
	n := model.Notification{ID: "sub_test", URL: x.server.URL, Secret: second, PrevSecret: testSecret, PrevSecretUntil: &until}
	if result := c.send(context.Background(), Env{}, n, "evt_rotate", []byte(`{}`)); !result.Delivered {
		t.Fatal(result.Result)
	}
	h := x.headers[0]
	now, _ := Signature(second, "evt_rotate", h.Get("webhook-timestamp"), []byte(`{}`))
	old, _ := Signature(testSecret, "evt_rotate", h.Get("webhook-timestamp"), []byte(`{}`))
	if h.Get("webhook-signature") != now+" "+old {
		t.Fatal("rotation signatures")
	}
	n.Secret = "enc1:garbage"
	if result := c.send(context.Background(), Env{}, n, "evt_bad", []byte(`{}`)); result.Result != "secret_unreadable" {
		t.Fatal(result.Result)
	}
}

func TestClientTimeoutAndResponseLimit(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/slow" {
			time.Sleep(100 * time.Millisecond)
		}
		_, _ = w.Write([]byte(strings.Repeat("x", 8192)))
	}))
	defer srv.Close()
	// Body truncation is independent of the short deadline exercised below.
	c := trusting(srv)
	n := model.Notification{Secret: testSecret, URL: srv.URL}
	result := c.send(context.Background(), Env{}, n, "msg_verification_size", []byte(`{}`))
	if !result.Delivered || string(result.body) != strings.Repeat("x", 4096) {
		t.Fatalf("response limit: %s, %d", result.Result, len(result.body))
	}
	n.URL += "/slow"
	c.timeout = 30 * time.Millisecond
	if result = c.send(context.Background(), Env{}, n, "evt_timeout", []byte(`{}`)); result.Result != "timeout" {
		t.Fatalf("timeout category: %s", result.Result)
	}
}

func TestClientAcknowledgmentIgnoresBrokenBody(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "100")
		w.WriteHeader(200)
		_, _ = w.Write([]byte("{"))
	}))
	defer srv.Close()
	c := trusting(srv)
	n := model.Notification{URL: srv.URL, Secret: testSecret}
	if got := c.send(context.Background(), Env{}, n, "evt_test", []byte(`{}`)); !got.Delivered {
		t.Fatal("2xx acknowledgment failed on body read")
	}
	if got := c.send(context.Background(), Env{}, n, "msg_verification_test", []byte(`{}`)); got.Delivered {
		t.Fatal("verification accepted a broken body")
	}
}

func TestHeaderAndSecretRules(t *testing.T) {
	for _, name := range []string{"Host", "Cookie", "Svix-Signature", "bad_name", "X-Forwarded-For", "x-FoRwArDeD-Host", "X-" + strings.Repeat("a", 63), "Proxy-Authorization"} {
		if validHeader(name, "x") {
			t.Errorf("accepted %s", name)
		}
	}
	for _, name := range []string{"Authorization", "authorization", "X-API-Key", "x-token", "X-" + strings.Repeat("a", 62)} {
		if !validHeader(name, "test") {
			t.Errorf("rejected permitted header %s", name)
		}
	}
	if validHeader("Authorization", "x\r\ny") || validHeader("Authorization", strings.Repeat("x", 1025)) || !validHeader("", "") || validHeader("", "x") {
		t.Fatal("header values")
	}
	for _, secret := range []string{"no-prefix", "whsec_?", "whsec_" + base64.StdEncoding.EncodeToString(make([]byte, 23)), "whsec_" + base64.StdEncoding.EncodeToString(make([]byte, 65))} {
		if _, err := SecretKey(secret); err == nil {
			t.Fatal("accepted invalid secret")
		}
	}
	generated, err := NewSecret()
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(generated, "whsec_"))
	if err != nil || len(decoded) != 32 {
		t.Fatal("generated key length")
	}
}

func TestSpecialAddressesAndExactPayloads(t *testing.T) {
	for _, ip := range []string{"64:ff9b:1::a00:1", "2001::1", "2001:1ff::1", "2002:a00:1::1", "100::1", "2001:db8::1", "fec0::1", "::7f00:1", "::a9fe:a9fe", "::8.8.8.8"} {
		if outbound.PublicIP(ip) {
			t.Errorf("accepted non-public address %s", ip)
		}
	}
	for _, raw := range []string{`9007199254740993`, `1e400`, `{"large":1e400,"n":9007199254740993}`} {
		body, err := json.Marshal(Fields(json.RawMessage(raw)))
		if err != nil || !strings.Contains(string(body), raw) {
			t.Errorf("number changed: %s", body)
		}
	}
	if Fields(json.RawMessage("not JSON"))["value"] != "not JSON" {
		t.Fatal("undecodable value was discarded")
	}
	for _, key := range []string{"path", "key"} {
		data, _ := json.Marshal(map[string]any{"v": 1, "site": "shop", key: strings.Repeat("é\n", 5000), "fields": map[string]any{"n": json.Number("9007199254740993")}})
		body := Body(model.NotificationDelivery{EventID: "evt_test", Name: "record.added", Data: data}, 1, 4096)
		var payload struct {
			Data map[string]any `json:"data"`
		}
		if len(body) > 4096 || json.Unmarshal(body, &payload) != nil {
			t.Fatal("body exceeded final limit or was invalid")
		}
		if len(payload.Data[key].(string)) > 256 || !strings.Contains(string(body), `"truncated":["fields","`+key+`"]`) {
			t.Fatal("long visitor key was not listed as truncated")
		}
	}
}

func TestTruncateListsCutPaths(t *testing.T) {
	data := map[string]any{"fields": map[string]any{"a": strings.Repeat("é", 10), "b": map[string]any{"c": []any{"short", strings.Repeat("x", 20)}}}}
	Truncate(data, 5)
	fields := data["fields"].(map[string]any)
	if fields["a"] != "éé" || fields["b"].(map[string]any)["c"].([]any)[1] != "xxxxx" {
		t.Fatalf("cut values: %v", fields)
	}
	if fmt.Sprint(data["truncated"]) != "[a b.c.1]" {
		t.Fatalf("truncated: %v", data["truncated"])
	}
}

func TestChallengeBudgets(t *testing.T) {
	ctx := context.Background()
	e := NewEngine(nil, nil, config.DefaultLimits(), "")
	e.client.Resolver = func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("8.8.8.8")}, {IP: net.ParseIP("8.8.4.4")}}, nil
	}
	for i := 0; i < 10; i++ {
		if _, err := e.challengeClient(ctx, fmt.Sprint(i), fmt.Sprintf("https://host%d.example/", i)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := e.challengeClient(ctx, "another", "https://alias.example/"); !errors.Is(err, Failure("rate_limited")) {
		t.Fatal("aliases bypassed IP budget")
	}
	e = NewEngine(nil, nil, config.DefaultLimits(), "")
	e.client.Resolver = func(_ context.Context, host string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP(host)}}, nil
	}
	for i := 1; i <= 10; i++ {
		if _, err := e.challengeClient(ctx, "owner", fmt.Sprintf("https://8.8.8.%d/", i)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := e.challengeClient(ctx, "owner", "https://8.8.4.4/"); !errors.Is(err, Failure("rate_limited")) {
		t.Fatal("destinations bypassed owner budget")
	}
}

func TestURLMinuteBudget(t *testing.T) {
	l := config.DefaultLimits()
	l.NotifyPerURLPerMinute = 120
	e := NewEngine(nil, nil, l, "")
	now := time.Now()
	budget := e.URLBudgetForTest(func() time.Time { return now })
	for minute := 0; minute < 2; minute++ {
		attempts := 0
		for tick := 0; tick < 240; tick++ {
			for worker := 0; worker < 20; worker++ {
				if budget.Allow("https://receiver.example/events") {
					attempts++
				}
			}
			now = now.Add(250 * time.Millisecond)
		}
		if attempts != 120 {
			t.Fatalf("minute %d: got %d attempts, want 120", minute, attempts)
		}
	}
}

func TestEngineStopBeforeStart(t *testing.T) {
	e := NewEngine(nil, nil, config.DefaultLimits(), "")
	e.Stop()
	e.Stop()
	e.Start(context.Background(), func() Env { return Env{} })
	e.Wait()
	for _, status := range e.Status() {
		if status.Running || status.LastAttempt != nil {
			t.Fatalf("stopped engine started: %+v", status)
		}
	}
	e.Emit(func(context.Context) { t.Error("emit admitted after stop") })
	e.Wait()
	if e.PendingForTest() != 0 {
		t.Fatal("stopped engine admitted an emit")
	}
}
