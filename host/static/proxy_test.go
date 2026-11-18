package static

import (
	"bufio"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/vsriram/simple-host/host/httpx"
)

func TestProxyClientAddressAndLimits(t *testing.T) {
	_, trusted, _ := net.ParseCIDR("127.0.0.0/8")
	httpx.SetTrustedProxies([]*net.IPNet{trusted})
	t.Cleanup(func() { httpx.SetTrustedProxies(nil) })
	limiter := httpx.NewRateLimiter(1, 0.00001)
	api := httptest.NewServer(httpx.RateLimitByIP(limiter, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Client", httpx.ClientIP(r))
		w.Header().Set("Host-Seen", r.Host)
		w.Header().Set("Proto-Seen", r.Header.Get("X-Forwarded-Proto"))
		w.Header().Set("Forwarded-Host-Seen", r.Header.Get("X-Forwarded-Host"))
	})))
	defer api.Close()
	target, _ := url.Parse(api.URL)
	proxy := NewProxy(target, []*net.IPNet{trusted})
	for _, tc := range []struct {
		peer, xff, client, proto string
		status                   int
	}{
		{"127.0.0.1:1234", "198.51.100.99, 203.0.113.1", "203.0.113.1", "https", 200},
		{"127.0.0.1:1234", "198.51.100.98, 203.0.113.1", "", "", 429},
		{"203.0.113.2:1234", "198.51.100.99", "203.0.113.2", "http", 200},
		{"203.0.113.2:1234", "198.51.100.98", "", "", 429},
		{"127.0.0.1:1234", "203.0.113.3", "203.0.113.3", "https", 200},
	} {
		r := httptest.NewRequest("GET", "http://site.owner.example/v1/test", nil)
		r.RemoteAddr = tc.peer
		r.Header.Set("X-Forwarded-For", tc.xff)
		r.Header.Set("X-Forwarded-Proto", "https")
		r.Header.Set("X-Forwarded-Host", "forged.example")
		w := httptest.NewRecorder()
		proxy.ServeHTTP(w, r)
		if w.Code != tc.status {
			t.Fatalf("status %d want %d", w.Code, tc.status)
		}
		if tc.status == 200 && (w.Header().Get("Client") != tc.client || w.Header().Get("Host-Seen") != "site.owner.example" || w.Header().Get("Proto-Seen") != tc.proto || w.Header().Get("Forwarded-Host-Seen") != "") {
			t.Fatalf("forwarding: %v", w.Header())
		}
	}
}

func TestProxyStreamsLargeImportAndResponse(t *testing.T) {
	testProxyStreams(t, "POST", "/v1/import", false)
}

func TestPageHandoffStreamsRequestAndResponse(t *testing.T) {
	testProxyStreams(t, "GET", "/page", true)
}

func testProxyStreams(t *testing.T, method, path string, handoff bool) {
	t.Helper()
	const remaining = 8 << 20
	readFirst := make(chan struct{})
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := http.NewResponseController(w).EnableFullDuplex(); err != nil {
			t.Error(err)
			return
		}
		b := make([]byte, 5)
		if _, err := io.ReadFull(r.Body, b); err != nil {
			t.Error(err)
			return
		}
		if string(b) != "start" || r.URL.Path != path || r.Method != method {
			t.Error("wrong import prefix or path")
		}
		close(readFirst)
		_, _ = io.WriteString(w, "accepted\n")
		w.(http.Flusher).Flush()
		n, err := io.Copy(io.Discard, r.Body)
		if err != nil || n != remaining {
			t.Errorf("import length %d: %v", n, err)
		}
		_, _ = io.WriteString(w, "complete\n")
	}))
	defer api.Close()
	target, _ := url.Parse(api.URL)
	proxy := NewProxy(target, nil)
	if handoff {
		proxy = pageHandoff(proxy)
	}
	front := httptest.NewServer(proxy)
	defer front.Close()
	reader, writer := io.Pipe()
	defer reader.Close()
	sendRest := make(chan struct{})
	writerDone := make(chan error, 1)
	go func() {
		_, err := io.WriteString(writer, "start")
		if err == nil {
			<-sendRest
			_, err = io.CopyN(writer, strings.NewReader(strings.Repeat("x", remaining)), remaining)
		}
		_ = writer.CloseWithError(err)
		writerDone <- err
	}()
	defer close(sendRest)
	client := &http.Client{Timeout: 5 * time.Second}
	req, err := http.NewRequest(method, front.URL+path, reader)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	response, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	select {
	case <-readFirst:
	default:
		t.Fatal("response before import reached API")
	}
	body := bufio.NewReader(response.Body)
	line, err := body.ReadString('\n')
	if err != nil || line != "accepted\n" {
		t.Fatalf("first streamed response %q: %v", line, err)
	}
	// Release the request body only after the response prefix reached the client.
	sendRest <- struct{}{}
	line, err = body.ReadString('\n')
	if err != nil || line != "complete\n" {
		t.Fatalf("final response %q: %v", line, err)
	}
	if err = <-writerDone; err != nil {
		t.Fatal(err)
	}
}

func TestProxyQueryHandling(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, r.URL.RawQuery)
	}))
	defer api.Close()
	target, _ := url.Parse(api.URL)
	proxy := NewProxy(target, nil)
	const raw = "q=one%2Ftwo&return=x;y"
	for _, tc := range []struct {
		path, want string
		handler    http.Handler
	}{
		{"/protected", raw, pageHandoff(proxy)},
		{"/v1/test", "q=one%2Ftwo", proxy},
		{"/mcp", "q=one%2Ftwo", proxy},
	} {
		w := httptest.NewRecorder()
		tc.handler.ServeHTTP(w, httptest.NewRequest("GET", tc.path+"?"+raw, nil))
		if got := w.Body.String(); got != tc.want {
			t.Errorf("%s query = %q, want %q", tc.path, got, tc.want)
		}
	}
}
