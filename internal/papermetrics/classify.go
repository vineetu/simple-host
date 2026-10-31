package papermetrics

import (
	"net/http"
	"strings"
)

// Route classes. The names are stored, so never rename one; add new ones.
const (
	classNone      = iota // not timed: health probes, CORS preflight
	classSiteFile         // any request for a hosted site's files (incl. 304, 404)
	classDataRead         // per-site saved data, GET/HEAD
	classDataWrite        // per-site saved data, POST/PUT/PATCH/DELETE
	classAuth             // sign-in, OAuth, SSO, visitor sessions
	classMCP              // POST /mcp: one connector tool call or handshake
	classMCPStream        // GET /mcp: a long-lived stream, so its time is not latency
	classAPIRead          // every other /v1 GET
	classAPIWrite         // every other /v1 write (deploys included)
	classPlatform         // platform pages on the apex: home, dashboard, docs
	numClasses
)

var classNames = [numClasses]string{"", "site_file", "data_read", "data_write", "auth", "mcp", "mcp_stream", "api_read", "api_write", "platform_page"}

// ClassNames lists the stored class names, for the export and the docs.
func ClassNames() []string { return append([]string(nil), classNames[1:]...) }

func (c *Collector) classify(r *http.Request) int {
	p := r.URL.Path
	if r.Method == http.MethodOptions || p == "/healthz" || p == "/readyz" {
		return classNone
	}
	read := r.Method == http.MethodGet || r.Method == http.MethodHead
	switch {
	case p == "/mcp" || strings.HasPrefix(p, "/mcp/") || p == "/brain/mcp":
		if r.Method == http.MethodGet && p == "/mcp" {
			return classMCPStream
		}
		return classMCP
	case strings.HasPrefix(p, "/oauth/") || strings.HasPrefix(p, "/.well-known/oauth") ||
		strings.HasPrefix(p, "/v1/auth/") || strings.HasPrefix(p, "/v1/sso/") ||
		strings.HasPrefix(p, "/v1/visitor/") || strings.HasPrefix(p, "/v1/me/sessions") ||
		siteSub(p) == "me":
		return classAuth
	case strings.HasPrefix(p, "/v1/"):
		if isDataPath(p) {
			if read {
				return classDataRead
			}
			return classDataWrite
		}
		if read {
			return classAPIRead
		}
		return classAPIWrite
	}
	host := strings.ToLower(r.Host)
	if i := strings.LastIndexByte(host, ':'); i >= 0 && !strings.Contains(host[i:], "]") {
		host = host[:i]
	}
	if c.apexHosts[host] || host == "" || host == "localhost" || isIPLiteral(host) {
		return classPlatform
	}
	return classSiteFile
}

// Histogram upper bounds in microseconds; the last bucket is everything
// slower. Log-spaced, so p50 to p99.9 of anything from a cached file to a
// slow deploy lands in a bucket with useful resolution.
var bucketUS = [...]int64{50, 100, 250, 500, 1_000, 2_500, 5_000, 10_000, 25_000, 50_000, 100_000, 250_000, 500_000, 1_000_000, 2_500_000, 10_000_000}

const numBuckets = len(bucketUS) + 1

// BucketBounds returns the upper bounds; -1 marks the overflow bucket.
func BucketBounds() []int64 { return append(append([]int64(nil), bucketUS[:]...), -1) }

func bucketFor(us int64) int {
	for i, b := range bucketUS {
		if us <= b {
			return i
		}
	}
	return numBuckets - 1
}

// siteSub is the segment after the site name in /v1/sites/{sitename}/<sub>/...
func siteSub(p string) string {
	rest, ok := strings.CutPrefix(p, "/v1/sites/")
	if !ok {
		return ""
	}
	parts := strings.SplitN(rest, "/", 3)
	if len(parts) < 2 {
		return ""
	}
	return parts[1]
}

// isDataPath: a site's saved data (storage presets, legacy collections, state, data).
func isDataPath(p string) bool {
	if strings.HasPrefix(p, "/v1/storage-download") {
		return true
	}
	switch siteSub(p) {
	case "storage", "collections", "state", "data":
		return true
	}
	return false
}

// isIPLiteral: a bare address (a probe or a health check), not a site.
func isIPLiteral(h string) bool {
	if strings.HasPrefix(h, "[") {
		return true
	}
	for _, r := range h {
		if (r < '0' || r > '9') && r != '.' {
			return false
		}
	}
	return true
}
