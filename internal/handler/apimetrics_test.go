package handler

import (
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
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
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "apimetrics.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, imp := range f.Imports {
		p, _ := strconv.Unquote(imp.Path.Value)
		if p == "net/http/httputil" || p == "net/rpc" || strings.HasPrefix(p, "golang.org/x/net") {
			t.Errorf("apimetrics.go imports %s", p)
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
					t.Errorf("%s: external URL literal %s in apimetrics.go", fset.Position(x.Pos()), x.Value)
				}
			}
		}
		return true
	})
}
