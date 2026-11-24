package testkit

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"

	"github.com/letsencrypt/pebble/v2/ca"
	"github.com/letsencrypt/pebble/v2/db"
	"github.com/letsencrypt/pebble/v2/va"
	"github.com/letsencrypt/pebble/v2/wfe"
)

// Message is one email the service sent.
type Message struct{ To, Subject, Text string }

// Mail is the fake Resend.
type Mail struct {
	mu   sync.Mutex
	sent []Message
}

func (m *Mail) serve(w http.ResponseWriter, r *http.Request) {
	var in struct {
		To            []string
		Subject, Text string
	}
	json.NewDecoder(r.Body).Decode(&in)
	m.mu.Lock()
	for _, to := range in.To {
		m.sent = append(m.sent, Message{to, in.Subject, in.Text})
	}
	m.mu.Unlock()
	w.Write([]byte(`{"id":"test"}`))
}

// To is every email sent to an address, oldest first.
func (m *Mail) To(addr string) []Message {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Message
	for _, x := range m.sent {
		if x.To == addr {
			out = append(out, x)
		}
	}
	return out
}

// Push is the fake browser push service. A test registers a device with an
// endpoint under URL; every push sent there is recorded.
type Push struct {
	URL  string
	mu   sync.Mutex
	sent []string // endpoint paths
}

func (p *Push) serve(w http.ResponseWriter, r *http.Request) {
	io.Copy(io.Discard, r.Body)
	p.mu.Lock()
	p.sent = append(p.sent, r.URL.Path)
	p.mu.Unlock()
	w.WriteHeader(http.StatusCreated)
}

// Sent is the endpoint paths pushes were sent to, oldest first.
func (p *Push) Sent() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.sent...)
}

// DNS is the fake DNS-over-HTTPS resolver, answering as dns.google/resolve
// does (?name=&type=, JSON with an Answer list).
type DNS struct {
	mu      sync.Mutex
	records map[string][]string
}

// Set makes name answer type (A, AAAA, CNAME or TXT) with values.
func (d *DNS) Set(name, typ string, values ...string) {
	d.mu.Lock()
	d.records[strings.ToLower(strings.TrimSuffix(name, "."))+" "+typ] = values
	d.mu.Unlock()
}

var dnsTypes = map[string]int{"A": 1, "CNAME": 5, "TXT": 16, "AAAA": 28}

func (d *DNS) serve(w http.ResponseWriter, r *http.Request) {
	name := strings.ToLower(strings.TrimSuffix(r.URL.Query().Get("name"), "."))
	typ := strings.ToUpper(r.URL.Query().Get("type"))
	d.mu.Lock()
	values := d.records[name+" "+typ]
	d.mu.Unlock()
	type answer struct {
		Name string `json:"name"`
		Type int    `json:"type"`
		Data string `json:"data"`
	}
	out := struct {
		Status int      `json:"Status"`
		Answer []answer `json:"Answer,omitempty"`
	}{}
	for _, v := range values {
		out.Answer = append(out.Answer, answer{name + ".", dnsTypes[typ], v})
	}
	if len(values) == 0 {
		out.Status = 3 // NXDOMAIN
	}
	json.NewEncoder(w).Encode(out)
}

// Google is the fake Google sign-in. Set Email before the browser visits
// /auth; the code it hands back proves that email.
type Google struct {
	mu    sync.Mutex
	Email string
}

func (g *Google) serve(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	email := g.Email
	g.mu.Unlock()
	switch r.URL.Path {
	case "/google/auth":
		q := r.URL.Query()
		http.Redirect(w, r, q.Get("redirect_uri")+"?state="+q.Get("state")+"&code="+email, http.StatusFound)
	case "/google/token":
		json.NewEncoder(w).Encode(map[string]any{"access_token": r.FormValue("code"), "token_type": "Bearer", "expires_in": 3600})
	case "/google/userinfo":
		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		json.NewEncoder(w).Encode(map[string]any{"email": token, "email_verified": true})
	default:
		http.NotFound(w, r)
	}
}

// certificateAuthority is Pebble, Let's Encrypt's test ACME server, in
// process, made on first use. Its directory is /dir. It treats every challenge as passed: the
// service's own DNS check (against the fake DNS) is what tests exercise.
func certificateAuthority() http.HandlerFunc {
	var once sync.Once
	var h http.Handler
	return func(w http.ResponseWriter, r *http.Request) {
		once.Do(func() {
			os.Setenv("PEBBLE_VA_ALWAYS_VALID", "1")
			os.Setenv("PEBBLE_VA_NOSLEEP", "1")
			os.Setenv("PEBBLE_WFE_NONCEREJECT", "0")
			logger := log.New(io.Discard, "", 0)
			store := db.NewMemoryStore()
			authority := ca.New(logger, store, "", "ecdsa", 0, 1, map[string]ca.Profile{"default": {Description: "test"}})
			fe := wfe.New(logger, store, va.New(logger, 80, 443, false, "", store), authority, nil, false, false, 0, 0)
			h = fe.Handler()
		})
		// Pebble answers a finalize without the order's Location, which
		// golang.org/x/crypto/acme needs to wait for the certificate.
		if id, ok := strings.CutPrefix(r.URL.Path, "/finalize-order/"); ok {
			w.Header().Set("Location", "https://"+r.Host+"/my-order/"+id)
		}
		h.ServeHTTP(w, r)
	}
}
