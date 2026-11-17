package analytics

import (
	"testing"
	"time"
)

// A verbatim line from a running Caddy 2 container, not a hand-written fixture.
const realCaddyLine = `{"level":"info","ts":1788931538.3623993,"logger":"http.log.access.log0","msg":"handled request","request":{"remote_ip":"172.18.0.1","remote_port":"53016","client_ip":"172.18.0.1","proto":"HTTP/1.1","method":"GET","host":"sites.localhost","uri":"/admin-2/e2e-test/","headers":{"User-Agent":["curl/8.5.0"],"Accept":["*/*"]}},"bytes_read":0,"user_id":"","duration":0.000185081,"size":24,"status":200,"resp_headers":{"Content-Length":["24"]}}`

func TestParseCaddyJSONRealLine(t *testing.T) {
	l, ok := parseCaddyJSON(realCaddyLine)
	if !ok {
		t.Fatal("failed to parse a real Caddy access line")
	}
	if l.host != "sites.localhost" || l.method != "GET" || l.status != "200" {
		t.Errorf("host/method/status wrong: %+v", l)
	}
	if l.uri != "/admin-2/e2e-test/" {
		t.Errorf("uri = %q", l.uri)
	}
	if l.remoteAddr != "172.18.0.1" {
		t.Errorf("remoteAddr = %q", l.remoteAddr)
	}
	if l.ua != "curl/8.5.0" {
		t.Errorf("ua = %q", l.ua)
	}
	// Caddy writes a float unix timestamp; losing the date would bucket every
	// view into the wrong hour, which is how analytics silently go wrong.
	if got := l.ts.UTC().Format(time.RFC3339); got != "2026-09-09T05:25:38Z" {
		t.Errorf("ts = %s", got)
	}
}

func TestParseCaddyJSONSkipsNonAccessRecords(t *testing.T) {
	// Caddy logs its own startup and errors in the same JSON stream shape.
	// Counting those as page views would invent traffic.
	for _, line := range []string{
		`{"level":"info","ts":1788931538.1,"msg":"serving initial configuration"}`,
		`{"level":"error","ts":1788931538.1,"msg":"failed to load certificate","request":{"host":"x"}}`,
		`{"level":"info","ts":1,"msg":"handled request","request":{"method":"GET"}}`,
		`not json at all`,
		``,
	} {
		if _, ok := parseCaddyJSON(line); ok {
			t.Errorf("accepted a non-access record: %s", line)
		}
	}
}

func TestParseCaddyJSONPrefersClientIP(t *testing.T) {
	// Behind a trusted proxy, remote_ip is the proxy and client_ip is the
	// person. Attributing every visit to the proxy would collapse unique
	// visitors to one.
	line := `{"msg":"handled request","ts":1788931538,"status":200,"request":{"method":"GET","host":"h","uri":"/a/b/","remote_ip":"10.0.0.1","client_ip":"203.0.113.9","headers":{"User-Agent":["x"]}}}`
	l, ok := parseCaddyJSON(line)
	if !ok || l.remoteAddr != "203.0.113.9" {
		t.Errorf("got %q, want the client ip", l.remoteAddr)
	}
}

func TestParseTSVUnchanged(t *testing.T) {
	// The nginx format is what simple-host.app writes and what 400 days of
	// retained log holds. A rebuild replays all of it, so this must not drift.
	line := "2026-09-09T05:25:38+00:00\tsites.simple-host.app\t200\tGET\t/vineetu/demo/\t203.0.113.4\tMozilla/5.0"
	l, ok := parseTSV(line)
	if !ok {
		t.Fatal("TSV line no longer parses")
	}
	if l.host != "sites.simple-host.app" || l.status != "200" || l.uri != "/vineetu/demo/" || l.remoteAddr != "203.0.113.4" || l.ua != "Mozilla/5.0" {
		t.Errorf("TSV parse drifted: %+v", l)
	}
	// The six-field form predates the user-agent column and must still parse.
	if _, ok := parseTSV("2026-09-09T05:25:38+00:00\th\t200\tGET\t/a/b/\t1.2.3.4"); !ok {
		t.Error("six-field TSV line rejected")
	}
}
