package handler

import (
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/vsriram/simple-host/internal/geoip"
	"github.com/vsriram/simple-host/internal/geoip/geoiptest"
)

func TestLocateLocalDB(t *testing.T) {
	dir := t.TempDir()
	if err := geoiptest.Write(filepath.Join(dir, geoip.CityFile), "DBIP-City-Lite", map[string]geoiptest.Record{
		"8.8.8.0/24": geoiptest.CityRecord("Mountain View", "United States"),
	}); err != nil {
		t.Fatal(err)
	}
	if err := geoiptest.Write(filepath.Join(dir, geoip.ASNFile), "DBIP-ASN-Lite", map[string]geoiptest.Record{
		"8.8.8.0/24": geoiptest.ASNRecord(15169, "Google LLC"),
	}); err != nil {
		t.Fatal(err)
	}
	geo := geoip.Open(dir)
	defer geo.Close()
	m := &APIMetrics{geo: geo}

	cases := []struct{ ip, where, org string }{
		{"8.8.8.8", "Mountain View, United States", "Google LLC"},
		{"127.0.0.1", "this box", "local"},
		{"10.1.2.3", "this box", "local"},
		{"::1", "this box", "local"},
		{"fe80::1", "this box", "local"},
		{"1.1.1.1", "", ""},
	}
	for _, c := range cases {
		where, org := m.locate(c.ip)
		if where != c.where || org != c.org {
			t.Errorf("locate(%q) = %q, %q; want %q, %q", c.ip, where, org, c.where, c.org)
		}
	}
}

// With no database files, public IPs come back blank — and nothing tries the
// network to fill the gap. Any HTTP request made while resolving fails the test.
func TestLocateMissingDBNoNetwork(t *testing.T) {
	var calls atomic.Int32
	orig := http.DefaultTransport
	http.DefaultTransport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		return nil, errors.New("network disabled in test: " + r.URL.String())
	})
	defer func() { http.DefaultTransport = orig }()

	geo := geoip.Open(filepath.Join(t.TempDir(), "missing"))
	defer geo.Close()
	for _, m := range []*APIMetrics{{geo: geo}, {geo: nil}} {
		for _, ip := range []string{"8.8.8.8", "1.1.1.1", "2606:4700:4700::1111"} {
			if where, org := m.locate(ip); where != "" || org != "" {
				t.Errorf("locate(%q) with no DB = %q, %q; want blank", ip, where, org)
			}
		}
		if where, _ := m.locate("127.0.0.1"); where != "this box" {
			t.Errorf("private IP with no DB = %q, want \"this box\"", where)
		}
	}
	if n := calls.Load(); n != 0 {
		t.Fatalf("%d HTTP request(s) made while resolving caller IPs", n)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// Caller IPs must never be sent to a third party. This fails if apimetrics.go
// grows any outbound HTTP/network call or any absolute URL again (the old
// ip-api.com lookup was exactly that).
func TestAPIMetricsMakesNoOutboundCalls(t *testing.T) {
	for _, file := range []string{"apimetrics.go", "apigrowth.go"} {
		checkNoOutboundCalls(t, file)
	}
}

func checkNoOutboundCalls(t *testing.T, file string) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, file, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, imp := range f.Imports {
		p, _ := strconv.Unquote(imp.Path.Value)
		if p == "net/http/httputil" || p == "net/rpc" || strings.HasPrefix(p, "golang.org/x/net") {
			t.Errorf("%s imports %s", file, p)
		}
	}
	banned := map[string]bool{
		// net/http client surface. (http.Handler, ResponseWriter, Request as a
		// server-side parameter etc. are fine.)
		"Client": true, "DefaultClient": true, "NewRequest": true, "NewRequestWithContext": true,
		"Get": true, "Post": true, "PostForm": true, "Head": true, "Transport": true, "DefaultTransport": true,
		// net dialing.
		"Dial": true, "DialTimeout": true, "DialTCP": true, "DialUDP": true, "Dialer": true,
		"LookupHost": true, "LookupIP": true, "LookupAddr": true,
	}
	ast.Inspect(f, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.SelectorExpr:
			if id, ok := x.X.(*ast.Ident); ok && (id.Name == "http" || id.Name == "net") && banned[x.Sel.Name] {
				t.Errorf("%s: %s.%s — apimetrics must not make outbound calls", fset.Position(x.Pos()), id.Name, x.Sel.Name)
			}
		case *ast.BasicLit:
			if x.Kind == token.STRING {
				s := strings.ToLower(x.Value)
				if strings.Contains(s, "http://") || strings.Contains(s, "https://") || strings.Contains(s, "ip-api") {
					t.Errorf("%s: external URL literal %s in %s", fset.Position(x.Pos()), x.Value, file)
				}
			}
		}
		return true
	})
}

func TestTruncateIP(t *testing.T) {
	for in, want := range map[string]string{
		"203.0.113.77":         "203.0.113.0",
		"2001:db8:1:2:3:4:5:6": "2001:db8:1::",
		"::ffff:198.51.100.9":  "198.51.100.0",
		"127.0.0.1":            "127.0.0.1",
		"::1":                  "::1",
		"10.1.2.3":             "10.1.2.0",
		"not-an-ip":            "not-an-ip",
	} {
		if got := truncateIP(in); got != want {
			t.Errorf("truncateIP(%q) = %q, want %q", in, got, want)
		}
	}
}

// flushLoop must prune before waiting on its 6-hour ticker. Restarts more
// often than that would otherwise starve the prune and keep shortened IPs past
// the 30 days the privacy page promises (seen on 2026-09-26: rows 31 days old).
func TestAPIMetricsPrunesAtStartup(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "apimetrics.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range f.Decls {
		fn, ok := d.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "flushLoop" {
			continue
		}
		for _, st := range fn.Body.List {
			if _, isFor := st.(*ast.ForStmt); isFor {
				break
			}
			if es, ok := st.(*ast.ExprStmt); ok {
				if call, ok := es.X.(*ast.CallExpr); ok {
					if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "pruneOld" {
						return
					}
				}
			}
		}
		t.Fatal("flushLoop does not call pruneOld before its loop")
	}
	t.Fatal("flushLoop not found")
}

func TestIsAPIRoute(t *testing.T) {
	for p, want := range map[string]bool{
		"GET /v1/sites/{sitename}/versions": true,
		"/v1/sites":                         false, // no method: a redirect's literal path
		"POST /mcp":                         true,
		"/mcp":                              false,
		"GET example.com/v1/sites":          true,
		"GET /":                             false, // the site catch-all a bot's /v1/<junk> lands on
		"/":                                 false,
		"":                                  false, // nothing matched, or not for this method
		"GET /features":                     false,
		"GET /mcpx":                         false,
	} {
		if got := isAPIRoute(p); got != want {
			t.Errorf("isAPIRoute(%q) = %v, want %v", p, got, want)
		}
	}
}

// Requests that match no API route are not API calls; calls from this server
// (loopback, its own public address) are counted apart, a private address is
// a real caller; a request a
// middleware cloned before the mux still gets its real route.
func TestAPIMetricsWrapCountsOnlyOutsideAPICalls(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/sites/{sitename}/versions", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNotFound) })
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) })
	m := &APIMetrics{routes: map[routeKey]int64{}, self: map[routeKey]int64{}, ips: map[string]*ipAgg{}}
	m.SetRouting(mux, "147.224.49.228")
	// A middleware that clones the request (as the connector's bearer
	// exchange does), so the outer request never sees the pattern.
	cloning := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { mux.ServeHTTP(w, r.Clone(r.Context())) })
	h := m.Wrap(cloning)
	call := func(method, path, from string) {
		r := httptest.NewRequest(method, path, nil)
		r.RemoteAddr = "127.0.0.1:1234"
		r.Header.Set("X-Forwarded-For", from)
		h.ServeHTTP(httptest.NewRecorder(), r)
	}
	call("GET", "/v1/wp-login.php", "8.8.8.8")         // catch-all 404
	call("POST", "/v1/sites/blog/versions", "8.8.8.8") // 405: no route for POST
	call("GET", "/v1/sites/blog/versions", "8.8.8.8")
	call("GET", "/v1/sites/blog/versions", "147.224.49.228")
	call("GET", "/v1/sites/blog/versions", "127.0.0.1")
	call("GET", "/v1/sites/blog/versions", "::1")
	call("GET", "/v1/sites/blog/versions", "10.0.0.5")

	want := routeKey{"GET /v1/sites/{sitename}/versions", 404}
	if len(m.routes) != 1 || m.routes[want] != 2 {
		t.Errorf("routes = %v, want only %v twice", m.routes, want)
	}
	if len(m.self) != 1 || m.self[want] != 3 {
		t.Errorf("self = %v, want %v three times", m.self, want)
	}
	if len(m.ips) != 2 || m.ips["8.8.8.0"] == nil || m.ips["10.0.0.0"] == nil {
		t.Errorf("ips = %v, want the two outside callers", m.ips)
	}
	if m.growth[growthKey{growthDimSource, growthKeySelf}] != 3 || m.growth[growthKey{growthDimGroup, "deploy"}] != 2 || m.growth[growthKey{growthDimCountry, unknownCountry}] != 2 {
		t.Errorf("growth = %v", m.growth)
	}
}
