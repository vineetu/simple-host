package call

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"errors"
	"log"
	"net"
	"net/http"
	"time"

	"github.com/vsriram/simple-host/host/data"
	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/outbound"
	"github.com/vsriram/simple-host/host/secrets"
)

// timeout is the most one call may take, answer included.
const timeout = 120 * time.Second

// Register mounts the call route, for the methods a page may send, behind Access's storage caller (caller), the
// same site and visitor resolution as the saved-data routes.
func Register(mux *http.ServeMux, h *data.Handler, caller func(http.Handler) http.Handler, store *secrets.Store) {
	route{h: h, store: store, port: "443"}.register(mux, caller)
}

// route holds what a call needs. dialer, port and roots are the outbound
// guard's settings: tests point them at a local TLS server.
type route struct {
	h      *data.Handler
	store  *secrets.Store
	dialer outbound.Dialer
	port   string
	roots  *x509.CertPool
}

func (rt route) register(mux *http.ServeMux, caller func(http.Handler) http.Handler) {
	serve := caller(http.HandlerFunc(rt.serve))
	mux.Handle("GET /v1/sites/{sitename}/secrets/{name}/call/{path...}", serve)
	mux.Handle("POST /v1/sites/{sitename}/secrets/{name}/call/{path...}", serve)
	mux.Handle("PUT /v1/sites/{sitename}/secrets/{name}/call/{path...}", serve)
	mux.Handle("PATCH /v1/sites/{sitename}/secrets/{name}/call/{path...}", serve)
	mux.Handle("DELETE /v1/sites/{sitename}/secrets/{name}/call/{path...}", serve)
}

func (rt route) serve(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodHead {
		httpx.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "a call is GET, POST, PUT, PATCH or DELETE")
		return
	}
	c := data.CallFrom(r.Context())
	name := r.PathValue("name")
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()
	// One read: the value and the settings the caller is admitted against.
	value, v, err := rt.store.Open(ctx, c.SiteID, name)
	if errors.Is(err, sql.ErrNoRows) {
		httpx.WriteError(w, http.StatusNotFound, "secret_not_found", "this site has no key of that name")
		return
	}
	if err != nil {
		rt.fail(w, c, name, "open", category("open", err))
		return
	}
	if err := admit(ctx, rt.h.StorageSvc(), c, v.Who); err != nil {
		data.WriteStorageError(w, err, http.StatusForbidden)
		return
	}
	if !c.Owner && (!rt.h.StorageIPLimiter.Allow(c.SiteID+"/"+httpx.ClientIP(r)) ||
		c.VisitorID != "" && !rt.h.StorageVisitorLimiter.Allow(c.SiteID+"/"+c.VisitorID)) {
		httpx.TooManyRequests(w)
		return
	}
	out, err := target(ctx, r, r.PathValue("path"), value, v)
	if err != nil {
		data.WriteStorageError(w, err, http.StatusBadRequest)
		return
	}
	if err := rt.store.Used(ctx, c.SiteID, name); err != nil {
		rt.fail(w, c, name, "count", "connection_failed")
		return
	}
	res, err := rt.client(ctx, v.Destination).Do(out)
	if err != nil {
		rt.fail(w, c, name, "send", category("send", err))
		return
	}
	defer res.Body.Close()
	// A redirect is never handed to the browser: it could carry the key on
	// to another address.
	if res.StatusCode >= 300 && res.StatusCode < 400 {
		rt.fail(w, c, name, "send", "redirect_refused")
		return
	}
	relay(w, res)
}

// client sends to destination only, over TLS 1.2+, through the outbound
// guard with this server's own addresses refused too, and never follows a
// redirect.
func (rt route) client(ctx context.Context, destination string) *http.Client {
	d := rt.dialer
	d.Refuse = rt.h.Hosts.ServerAddrs(ctx)
	transport := &http.Transport{Proxy: nil, DisableKeepAlives: true, ForceAttemptHTTP2: true,
		TLSClientConfig: &tls.Config{RootCAs: rt.roots, MinVersion: tls.VersionTLS12},
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(address)
			if err != nil || host != destination || port != "443" {
				return nil, outbound.Failure("refused_address")
			}
			return d.Dial(ctx, network, net.JoinHostPort(host, rt.port))
		}}
	return &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

// relay streams the answer back as it arrives (server-sent events and
// chunked answers included), without cookies or hop-by-hop headers, up to
// MaxResponseBytes.
func relay(w http.ResponseWriter, res *http.Response) {
	for name, values := range res.Header {
		if kept(name) && w.Header().Get(name) == "" {
			w.Header()[name] = values
		}
	}
	w.Header().Set("X-Accel-Buffering", "no")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(res.StatusCode)
	flush := http.NewResponseController(w)
	buf := make([]byte, 32<<10)
	for left := MaxResponseBytes; left > 0; {
		n, err := res.Body.Read(buf[:min(len(buf), left)])
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				return
			}
			_ = flush.Flush()
			left -= n
		}
		if err != nil {
			return
		}
	}
}

// category names why a call could not be made, without the error itself:
// it may hold the request's URL, and with it a key in the query.
func category(step string, err error) string {
	var failure outbound.Failure
	var ne net.Error
	switch {
	case errors.As(err, &failure):
		return string(failure)
	case errors.As(err, &ne) && ne.Timeout(), errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case errors.As(err, new(*tls.CertificateVerificationError)), errors.As(err, new(tls.RecordHeaderError)):
		return "tls_error"
	case step == "open":
		return "key_unreadable"
	}
	return "connection_failed"
}

// fail answers a call that could not be made with its category only.
func (rt route) fail(w http.ResponseWriter, c data.Call, name, step, category string) {
	log.Printf("secrets: call %s/%s: %s failed: %s", c.SiteID, name, step, category)
	httpx.WriteError(w, http.StatusBadGateway, "secret_call_failed", "the request to the key's destination could not be made ("+category+")")
}
