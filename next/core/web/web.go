// Package web is how every feature answers HTTP: three route tables, JSON in
// and out, refusals as {code, fix}, and the caller carried on the request.
package web

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
)

// Routes are the three places a feature mounts routes. Platform answers on
// the service's own host (simple-host.app). Site answers on every site
// address (its own origin). Page answers on a person's or team's public page
// (<handle>.simple-host.app or <handle>.simple-host.site).
type Routes struct {
	Platform *http.ServeMux
	Site     *http.ServeMux
	Page     *http.ServeMux
}

func NewRoutes() *Routes {
	return &Routes{Platform: http.NewServeMux(), Site: http.NewServeMux(), Page: http.NewServeMux()}
}

// Refusal is an error a person can act on: an HTTP status, a stable code and
// a one-line fix in plain English.
type Refusal struct {
	Status int    `json:"-"`
	Code   string `json:"code"`
	Fix    string `json:"fix"`
	// RetryAfter is seconds, sent as Retry-After when set.
	RetryAfter int `json:"-"`
}

func (r *Refusal) Error() string { return r.Code + ": " + r.Fix }

// Refuse makes a refusal.
func Refuse(status int, code, fix string) *Refusal {
	return &Refusal{Status: status, Code: code, Fix: fix}
}

// NotFound is the one answer for anything that does not exist or is hidden.
var NotFound = Refuse(http.StatusNotFound, "not_found", "Check the address. Nothing is here.")

// Func turns a handler that returns an error into an http.Handler. A
// *Refusal is written as {code, fix} with its status; any other error is
// logged and answered as a plain server error.
func Func(fn func(w http.ResponseWriter, r *http.Request) error) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := fn(w, r); err != nil {
			Fail(w, r, err)
		}
	})
}

// Fail writes err as a refusal.
func Fail(w http.ResponseWriter, r *http.Request, err error) {
	var ref *Refusal
	if !errors.As(err, &ref) {
		log.Printf("error %s %s: %v", r.Method, r.URL.Path, err)
		ref = Refuse(http.StatusInternalServerError, "server_error", "Something broke on our side. Try again in a minute.")
	}
	if ref.RetryAfter > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(ref.RetryAfter))
	}
	JSON(w, ref.Status, ref)
}

// JSON writes v as JSON with status.
func JSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// Read decodes a JSON body of at most 1 MB into v.
func Read(r *http.Request, v any) error {
	d := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	if err := d.Decode(v); err != nil {
		return Refuse(http.StatusBadRequest, "bad_json", "Send a JSON body that matches the documented fields.")
	}
	return nil
}

// Caller is who is making a request, whichever way they came in. ID "" is
// nobody. Via says how: "key", "app", "session" (the dashboard) or "site" (a
// visitor's sign-in on one site, then SiteID names that site).
type Caller struct {
	ID       string
	Email    string
	Operator bool
	Via      string
	SiteID   string
}

// Visitor is a person signed in on one site only.
func (c Caller) Visitor() bool { return c.Via == "site" }

type callerKey struct{}

// WithCaller returns r carrying c.
func WithCaller(r *http.Request, c Caller) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), callerKey{}, c))
}

// CallerOf is the request's caller (the zero Caller when nobody).
func CallerOf(r *http.Request) Caller {
	c, _ := r.Context().Value(callerKey{}).(Caller)
	return c
}

// ClientIP is the visitor's address: nginx's X-Real-IP when the request came
// through it on this machine, else the connection's address.
func ClientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		if real := strings.TrimSpace(r.Header.Get("X-Real-IP")); real != "" {
			return real
		}
	}
	return host
}

// HTTPS reports whether the visitor's browser is on HTTPS (nginx says so in
// X-Forwarded-Proto).
func HTTPS(r *http.Request) bool {
	return r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"
}

// Host is the request's host name, lower case, without a port.
func Host(r *http.Request) string {
	h := r.Host
	if i := strings.LastIndexByte(h, ':'); i > 0 && !strings.Contains(h[i:], "]") {
		h = h[:i]
	}
	return strings.TrimSuffix(strings.ToLower(h), ".")
}
