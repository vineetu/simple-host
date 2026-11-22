package call

import (
	"bufio"
	"bytes"
	"context"
	"crypto/x509"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/vsriram/simple-host/host/outbound"
	"github.com/vsriram/simple-host/host/secrets"
	"github.com/vsriram/simple-host/host/server/servertest/datatest"
	rt "github.com/vsriram/simple-host/host/server/servertest/routetest"
	"github.com/vsriram/simple-host/host/testdb"
)

func TestMain(m *testing.M) { testdb.Main(m) }

const key = "AIzaSy-call-test-0123456789abcdefXYZ"

// upstream is the key's destination: a local TLS server answering for
// example.com, recording what reached it.
type upstream struct {
	*httptest.Server
	mu   sync.Mutex
	got  []*http.Request
	next chan struct{} // the stream's second event waits for this
}

func newUpstream(t *testing.T) *upstream {
	u := &upstream{next: make(chan struct{})}
	u.Server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u.mu.Lock()
		u.got = append(u.got, r.Clone(context.Background()))
		u.mu.Unlock()
		switch r.URL.Path {
		case "/stream":
			w.Header().Set("Content-Type", "text/event-stream")
			io.WriteString(w, "data: one\n\n")
			w.(http.Flusher).Flush()
			<-u.next
			io.WriteString(w, "data: two\n\n")
		case "/moved":
			http.Redirect(w, r, "https://elsewhere.example.net/", http.StatusFound)
		default:
			w.Header().Set("Set-Cookie", "upstream=1")
			w.Header().Set("Access-Control-Allow-Origin", "*")
			w.Header().Set("X-Upstream", "yes")
			w.Header().Set("X-Accel-Redirect", "/internal/secret-place")
			w.Header().Set("X-Accel-Limit-Rate", "1")
			w.Header().Set("X-Accel-Buffering", "yes")
			body, _ := io.ReadAll(r.Body)
			w.Write(append([]byte("echo:"), body...))
		}
	}))
	t.Cleanup(u.Close)
	return u
}

func (u *upstream) last(t *testing.T) *http.Request {
	t.Helper()
	u.mu.Lock()
	defer u.mu.Unlock()
	if len(u.got) == 0 {
		t.Fatal("nothing reached the destination")
	}
	return u.got[len(u.got)-1]
}

// fixture is the call route on Data's route fixture (the shop and its
// callers), with the key's destination resolved to the local upstream.
type fixture struct {
	datatest.Shop
	up    *upstream
	store *secrets.Store
	logs  *bytes.Buffer
}

func newFixture(t *testing.T, resolveTo string, loopback bool) fixture {
	t.Helper()
	up := newUpstream(t)
	_, port, _ := net.SplitHostPort(up.Listener.Addr().String())
	roots := x509.NewCertPool()
	roots.AddCert(up.Certificate())
	var store *secrets.Store
	shop := datatest.NewShop(t, func(a *datatest.App) {
		store = secrets.New(a.Database, filepath.Join(t.TempDir(), "secrets.key"))
		// A new master file is made only while no secret is saved anywhere.
		t.Cleanup(func() { a.Database.Exec(`DELETE FROM site_secrets`) })
		route{h: a.Data, store: store, port: port, roots: roots, dialer: outbound.Dialer{AllowLoopback: loopback,
			Resolver: func(context.Context, string) ([]net.IPAddr, error) {
				return []net.IPAddr{{IP: net.ParseIP(resolveTo)}}, nil
			}},
		}.register(a.Mux, a.Caller)
	})
	logs := &bytes.Buffer{}
	log.SetOutput(logs)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	f := fixture{Shop: shop, up: up, store: store, logs: logs}
	for name, who := range map[string]string{"own": "owner", "member": "signed-in", "open": "anyone"} {
		f.save(t, name, who, "header", "x-goog-api-key", "")
	}
	f.save(t, "bearer", "anyone", "header", "Authorization", "Bearer ")
	f.save(t, "inquery", "anyone", "query", "key", "")
	return f
}

func (f fixture) save(t *testing.T, name, who, attach, attachName, prefix string) {
	t.Helper()
	if err := f.store.Save(context.Background(), f.Sid, key, secrets.Secret{Name: name, Destination: "example.com", Who: who, Attach: attach, AttachName: attachName, AttachPrefix: prefix}); err != nil {
		t.Fatal(err)
	}
}

// call asks for path through the secret name as caller.
func (f fixture) call(t *testing.T, caller, method, name, path string, body any, extra ...string) rt.Resp {
	t.Helper()
	headers, host := map[string]string{}, f.Host
	if caller == "key" {
		headers, host = f.Key, rt.SiteDomain
	} else {
		headers = rt.Browser(f.Host, "", extra...)
		for k, v := range f.Callers[caller] {
			headers[k] = v
		}
		for i := 0; i+1 < len(extra); i += 2 {
			headers[extra[i]] = extra[i+1]
		}
	}
	return f.A.At(t, method, host, "/v1/sites/shop/secrets/"+name+"/call/"+path, body, headers)
}

func noKey(t *testing.T, what string, b []byte) {
	t.Helper()
	if bytes.Contains(b, []byte(key)) || bytes.Contains(b, []byte(key[:14])) || bytes.Contains(b, []byte(key[len(key)-14:])) {
		t.Fatalf("%s shows the key: %s", what, b)
	}
}

// Who may use a key is its who, through Data's one gate: owner is the
// owner's key or the owner signed in on the site; signed-in any visitor
// signed in there; anyone everyone the gate lets in. A browser call needs
// X-SH-CSRF, and a passcode covers every key.
func TestWhoMayCall(t *testing.T) {
	f := newFixture(t, "127.0.0.1", true)
	for _, c := range []struct {
		caller, name string
		status       int
		code         string
	}{
		{"anon", "own", 401, "sign_in_required"},
		{"anon", "member", 401, "sign_in_required"},
		{"anon", "open", 200, ""},
		{"alice", "own", 403, "forbidden"},
		{"alice", "member", 200, ""},
		{"owner-site", "own", 200, ""},
		{"key", "own", 200, ""},
		{"anon", "missing", 404, "secret_not_found"},
	} {
		if r := f.call(t, c.caller, "POST", c.name, "v1/models", map[string]string{"q": "hi"}); r.Status != c.status || r.Code() != c.code {
			t.Errorf("%s on %s: %d %s", c.caller, c.name, r.Status, r.Body)
		}
	}
	if r := f.call(t, "anon", "POST", "open", "v1", nil, "X-SH-CSRF", ""); r.Status != 403 || r.Code() != "csrf_required" {
		t.Errorf("no CSRF header: %d %s", r.Status, r.Body)
	}
	rt.SetPasscode(t, f.A.Database, f.Sid)
	if r := f.call(t, "anon", "POST", "open", "v1", nil); r.Status != 403 || r.Code() != "site_locked" {
		t.Errorf("locked site: %d %s", r.Status, r.Body)
	}
	if r := f.call(t, "anon", "POST", "open", "v1", nil, datatest.Unlocked, "1"); r.Status != 200 {
		t.Errorf("unlocked: %d %s", r.Status, r.Body)
	}
	var count int
	if err := f.A.Database.QueryRow(`SELECT use_count FROM site_secrets WHERE site_id = $1 AND name = 'open'`, f.Sid).Scan(&count); err != nil || count != 2 {
		t.Errorf("uses counted: %d %v", count, err)
	}
}

// The request goes to the destination and nowhere else, whatever the path:
// the host is a field of the outgoing URL, and the dial is pinned to it.
func TestDestinationIsPinned(t *testing.T) {
	f := newFixture(t, "127.0.0.1", true)
	for _, path := range []string{"v1/x", "%2F%2Fevil.example.net/x", "@evil.example.net/x", "https:%2F%2Fevil.example.net/", "..%2F..%2Fx", "x%2F..%2F..%2Fy", "evil.example.net:443/x"} {
		r := f.call(t, "anon", "GET", "open", path, nil)
		if r.Status != 200 {
			t.Errorf("%s: %d %s", path, r.Status, r.Body)
			continue
		}
		if got := f.up.last(t); got.Host != "example.com" {
			t.Errorf("%s reached host %q", path, got.Host)
		}
	}
	// An encoded spelling of the key's name still sends the path given.
	if r := f.call(t, "anon", "GET", "%6Fpen", "v1/spelled", nil); r.Status != 200 || f.up.last(t).URL.Path != "/v1/spelled" {
		t.Errorf("encoded name: %d path %q", r.Status, f.up.last(t).URL.Path)
	}
	// A redirect is neither followed nor handed to the browser.
	if r := f.call(t, "anon", "GET", "open", "moved", nil); r.Status != http.StatusBadGateway || !strings.Contains(string(r.Body), "redirect_refused") || r.Header.Get("Location") != "" {
		t.Errorf("redirect: %d %v %s", r.Status, r.Header, r.Body)
	}
	if n := len(f.up.got); n != 9 {
		t.Errorf("%d requests reached the destination, want 9", n)
	}
}

// A destination resolving to a private address, or to loopback without the
// tests' allowance, is refused at connect time.
func TestPrivateAddressesRefused(t *testing.T) {
	for _, ip := range []string{"10.0.0.1", "127.0.0.1", "169.254.169.254", "100.64.0.1", "::1", "fd00::1"} {
		t.Run(ip, func(t *testing.T) {
			f := newFixture(t, ip, false)
			r := f.call(t, "anon", "GET", "inquery", "v1", nil)
			if r.Status != http.StatusBadGateway || !strings.Contains(string(r.Body), "refused_address") {
				t.Errorf("%s: %d %s", ip, r.Status, r.Body)
			}
			noKey(t, "refusal", r.Body)
			noKey(t, "logs", f.logs.Bytes())
		})
	}
	if d := (outbound.Dialer{Refuse: map[string]bool{"8.8.8.8": true}}); d.Public("8.8.8.8") || !d.Public("8.8.4.4") {
		t.Error("this server's own address is not refused")
	}
}

// The key goes out as stored; the page's cookies, this server's
// credentials and forwarding headers never do; the answer comes back
// without cookies or headers nginx would act on, and this server's own CORS
// headers stand.
func TestHeadersBothWays(t *testing.T) {
	f := newFixture(t, "127.0.0.1", true)
	r := f.call(t, "alice", "POST", "bearer", "v1/chat?stream=1", map[string]string{"q": "hi"},
		"Authorization", "Bearer page-guess", "X-Forwarded-For", "1.2.3.4", "anthropic-version", "2023-06-01", "X-Custom-Thing", "kept", "X-SH-Visitor-Pass", "p")
	if r.Status != 200 || !strings.HasPrefix(string(r.Body), `echo:{"q":"hi"}`) {
		t.Fatalf("call: %d %s", r.Status, r.Body)
	}
	got := f.up.last(t)
	if got.Header.Get("Authorization") != "Bearer "+key || got.URL.RawQuery != "stream=1" || got.Header.Get("Content-Type") != "application/json" {
		t.Errorf("sent: %v %q", got.Header, got.URL.RawQuery)
	}
	for _, h := range []string{"Cookie", "X-Forwarded-For", "X-Sh-Csrf", "X-Sh-Visitor-Pass", "Origin"} {
		if got.Header.Get(h) != "" {
			t.Errorf("%s was forwarded", h)
		}
	}
	if got.Header.Get("Anthropic-Version") != "2023-06-01" || got.Header.Get("X-Custom-Thing") != "kept" {
		t.Errorf("service headers dropped: %v", got.Header)
	}
	if r.Header.Get("Set-Cookie") != "" || len(r.Header["Access-Control-Allow-Origin"]) > 1 || r.Header.Get("X-Upstream") != "yes" ||
		r.Header.Get("X-Accel-Redirect") != "" || r.Header.Get("X-Accel-Limit-Rate") != "" || r.Header.Get("X-Accel-Buffering") != "no" {
		t.Errorf("answer headers: %v", r.Header)
	}
	noKey(t, "answer", r.Body)
	// In the query: added once, the page's own value replaced.
	f.call(t, "anon", "GET", "inquery", "v1/models?key=page&alt=sse", nil)
	if q := f.up.last(t).URL.Query(); q.Get("key") != key || len(q["key"]) != 1 || q.Get("alt") != "sse" {
		t.Errorf("query: %v", q)
	}
	noKey(t, "logs", f.logs.Bytes())
}

// A streamed answer reaches the page event by event, as it is sent.
func TestStreamPassesThrough(t *testing.T) {
	f := newFixture(t, "127.0.0.1", true)
	req, _ := http.NewRequest("POST", f.A.Srv.URL+"/v1/sites/shop/secrets/open/call/stream", strings.NewReader("{}"))
	req.Host = f.Host
	for k, v := range rt.Browser(f.Host, "") {
		req.Header.Set(k, v)
	}
	req.Header.Set("X-Forwarded-Proto", "https")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	lines := bufio.NewReader(res.Body)
	if line, err := lines.ReadString('\n'); err != nil || line != "data: one\n" {
		t.Fatalf("first event before the second was sent: %q %v", line, err)
	}
	close(f.up.next)
	rest, _ := io.ReadAll(lines)
	if !strings.Contains(string(rest), "data: two") || res.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("rest: %q %v", rest, res.Header)
	}
}

// HEAD is refused (Go would otherwise serve it as GET), and a refused call
// spends nothing: the use count moves only for a call that was let through.
func TestRefusedCallsAreNotCounted(t *testing.T) {
	f := newFixture(t, "127.0.0.1", true)
	if r := f.call(t, "anon", "HEAD", "open", "v1", nil); r.Status != http.StatusMethodNotAllowed {
		t.Errorf("HEAD: %d", r.Status)
	}
	f.call(t, "anon", "POST", "own", "v1", nil)
	f.call(t, "alice", "POST", "own", "v1", nil)
	f.call(t, "owner-site", "POST", "own", "v1", nil)
	var count int
	if err := f.A.Database.QueryRow(`SELECT use_count FROM site_secrets WHERE site_id = $1 AND name = 'own'`, f.Sid).Scan(&count); err != nil || count != 1 {
		t.Errorf("uses counted: %d %v", count, err)
	}
	if n := len(f.up.got); n != 1 {
		t.Errorf("%d requests reached the destination, want 1", n)
	}
}
