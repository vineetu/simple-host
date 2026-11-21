package web

import (
	"errors"
	"log"
	"net/http"
	"strings"

	"github.com/vsriram/simple-host/host/access/auth"
	"github.com/vsriram/simple-host/host/httpx"
	operations "github.com/vsriram/simple-host/host/ops"
)

// APIMetrics adapts the injected operations recorder to HTTP.
type APIMetrics struct {
	*operations.APIMetrics
	mux *http.ServeMux
}

func NewAPIMetrics(metrics *operations.APIMetrics) *APIMetrics {
	return &APIMetrics{APIMetrics: metrics}
}
func (m *APIMetrics) SetRouting(mux *http.ServeMux, selfIPs ...string) {
	m.mux = mux
	m.SetSelfIPs(selfIPs...)
}

// Wrap counts /v1/* and /mcp traffic around the mux. Static pages, health probes, and
// hosted-site content are deliberately not counted — this is API analytics,
// not visitor analytics (that already exists per site).
func (m *APIMetrics) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !(strings.HasPrefix(r.URL.Path, "/v1/") || r.URL.Path == "/mcp") {
			next.ServeHTTP(w, r)
			return
		}
		rec := &StatusCapture{ResponseWriter: w, Status: http.StatusOK}
		next.ServeHTTP(rec, r)

		route := r.Pattern // set by ServeMux on match (Go ≥1.23)
		if route == "" && m.mux != nil {
			_, route = m.mux.Handler(r)
		}
		if !isAPIRoute(route) {
			return // matched no API route: not an API call
		}
		raw := httpx.ClientIP(r)
		m.Observe(route, rec.Status, raw)
	})
}

// isAPIRoute reports whether a mux pattern ("METHOD [host]/path") is an API
// route: under /v1/, or the connector at /mcp. Every API route is registered
// with a method, so a pattern without one is not an API route: the mux's
// trailing-slash redirect reports the literal path as its pattern, which
// would mint a row per site name. The site catch-all ("GET /") and an empty
// pattern (no route matched, or not for this method) are not API routes either.
func isAPIRoute(pattern string) bool {
	i := strings.IndexByte(pattern, ' ')
	if i < 0 {
		return false
	}
	p := pattern[i+1:]
	if i := strings.IndexByte(p, '/'); i > 0 {
		p = p[i:] // drop a host
	}
	return strings.HasPrefix(p, "/v1/") || p == "/mcp" || strings.HasPrefix(p, "/mcp/")
}

type StatusCapture struct {
	http.ResponseWriter
	Status int
}

func (s *StatusCapture) Unwrap() http.ResponseWriter { return s.ResponseWriter }

func (s *StatusCapture) WriteHeader(code int) {
	s.Status = code
	s.ResponseWriter.WriteHeader(code)
}

// Flush forwards to the wrapped writer when it supports it (an SSE/MCP
// stream under /v1/ or /mcp needs this to actually reach the client as it
// writes, not only once the handler returns). http.ResponseController
// already finds this through Unwrap without it, but a direct
// w.(http.Flusher) assertion -- as a caller might still reasonably write --
// would otherwise silently see "no, this writer can't flush", since an
// embedded http.ResponseWriter field never promotes the concrete writer's
// own Flush method.
func (s *StatusCapture) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (m *APIMetrics) AdminSummary(w http.ResponseWriter, r *http.Request) {
	if !auth.Admin(w, r) {
		return
	}
	out, err := m.Summary(r.Context())
	if err != nil {
		httpx.WriteJSON(w, 500, httpx.ErrorResponse{Error: "analytics query failed"})
		return
	}
	httpx.WriteJSON(w, 200, out)
}
func (m *APIMetrics) AdminGrowth(w http.ResponseWriter, r *http.Request) {
	if !auth.Admin(w, r) {
		return
	}
	body, err := m.Growth(r.Context(), r.URL.Query().Get("range"))
	if errors.Is(err, operations.ErrGrowthRange) {
		httpx.WriteJSON(w, 400, httpx.ErrorResponse{Error: err.Error()})
		return
	}
	if err != nil {
		log.Printf("api growth: %v", err)
		httpx.WriteJSON(w, 500, httpx.ErrorResponse{Error: "growth query failed"})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Write(body)
}
