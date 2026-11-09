package handler

import (
	"errors"
	"github.com/vsriram/simple-host/internal/app/operations"
	"github.com/vsriram/simple-host/internal/auth"
	"log"
	"net/http"
	"strings"
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
		rec := &statusCapture{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)

		route := r.Pattern // set by ServeMux on match (Go ≥1.23)
		if route == "" && m.mux != nil {
			_, route = m.mux.Handler(r)
		}
		if !isAPIRoute(route) {
			return // matched no API route: not an API call
		}
		raw := clientIP(r)
		m.Observe(route, rec.status, raw)
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

type statusCapture struct {
	http.ResponseWriter
	status int
}

func (s *statusCapture) Unwrap() http.ResponseWriter { return s.ResponseWriter }

func (s *statusCapture) WriteHeader(code int) {
	s.status = code
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
func (s *statusCapture) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func metricsAdmin(w http.ResponseWriter, r *http.Request) bool {
	user := auth.GetUser(r.Context())
	if user == nil {
		writeJSON(w, 401, errorResponse{Error: "unauthorized"})
		return false
	}
	if !user.IsAdmin {
		writeJSON(w, 404, errorResponse{Error: "not found"})
		return false
	}
	return true
}
func (m *APIMetrics) AdminSummary(w http.ResponseWriter, r *http.Request) {
	if !metricsAdmin(w, r) {
		return
	}
	out, err := m.Summary(r.Context())
	if err != nil {
		writeJSON(w, 500, errorResponse{Error: "analytics query failed"})
		return
	}
	writeJSON(w, 200, out)
}
func (m *APIMetrics) AdminGrowth(w http.ResponseWriter, r *http.Request) {
	if !metricsAdmin(w, r) {
		return
	}
	body, err := m.Growth(r.Context(), r.URL.Query().Get("range"))
	if errors.Is(err, operations.ErrGrowthRange) {
		writeJSON(w, 400, errorResponse{Error: err.Error()})
		return
	}
	if err != nil {
		log.Printf("api growth: %v", err)
		writeJSON(w, 500, errorResponse{Error: "growth query failed"})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Write(body)
}
