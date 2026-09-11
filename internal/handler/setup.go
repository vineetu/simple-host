package handler

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/vsriram/simple-host/internal/eventdns"
)

// SetupHandler serves first-boot setup. Register it on the setup-mode mux only.
type SetupHandler struct {
	db                         *sql.DB
	publicAPI, password, token string
	dataDir                    string
	limiter                    *rateLimiter
	client                     *http.Client
	ipMu                       sync.Mutex
	ip                         string

	// done is closed once setup succeeds, so the process can leave setup mode.
	// Settings chosen here are read at startup — the hostnames, and the
	// per-site cap — and nothing in a running setup-mode process re-reads them,
	// because the serving routes were never registered on this mux.
	done     chan struct{}
	doneOnce sync.Once
}

// Done is closed once setup has been completed successfully. The caller stops
// the setup server and exits, and the supervisor starts the process again —
// this time reading the answers out of the database. Without it an organiser
// sees "Done", reloads, and gets the setup page back.
func (h *SetupHandler) Done() <-chan struct{} { return h.done }

func NewSetupHandler(db *sql.DB, publicAPI string, password string, dataDir string) *SetupHandler {
	return &SetupHandler{db: db, publicAPI: strings.TrimRight(publicAPI, "/"), password: password, dataDir: dataDir,
		token: rand.Text(), limiter: newRateLimiter(5, 1.0/60), client: &http.Client{Timeout: 30 * time.Second},
		done: make(chan struct{})}
}

func (h *SetupHandler) Register(mux *http.ServeMux) {
	mux.Handle("/", serveStaticPage("setup.html"))
	mux.HandleFunc("GET /v1/setup/state", h.state)
	mux.HandleFunc("POST /v1/setup/verify", h.verify)
	mux.Handle("POST /v1/setup/own-domain", h.authorize(h.ownDomain))
	mux.Handle("GET /v1/setup/dns-check", h.authorize(h.dnsCheck))
	mux.Handle("POST /v1/setup/free-name", h.authorize(h.freeName))
	mux.Handle("POST /v1/setup/finish", h.authorize(h.finish))
}

// InstanceConfigured reads persisted hosts; absent rows mean setup is unfinished.
// Apply db/migrations/instance-config.sql before calling this on older databases.
func InstanceConfigured(ctx context.Context, db *sql.DB) (siteDomain, contentHost string, err error) {
	rows, err := db.QueryContext(ctx, `SELECT key, value FROM instance_config WHERE key IN ('site_domain', 'content_host')`)
	if err != nil {
		return "", "", err
	}
	defer rows.Close()
	for rows.Next() {
		var key, value string
		if err = rows.Scan(&key, &value); err != nil {
			return "", "", err
		}
		if key == "site_domain" {
			siteDomain = value
		} else {
			contentHost = value
		}
	}
	return siteDomain, contentHost, rows.Err()
}

func setupError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, errorResponse{Error: message})
}

func setupDecode(w http.ResponseWriter, r *http.Request, value any) bool {
	if err := json.NewDecoder(r.Body).Decode(value); err != nil {
		setupError(w, 400, "Check what you entered and try again.")
		return false
	}
	return true
}

func (h *SetupHandler) state(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	host, _, err := InstanceConfigured(r.Context(), h.db)
	if err != nil {
		setupError(w, 500, "Cannot check setup. Try again.")
		return
	}
	writeJSON(w, 200, map[string]bool{"configured": host != "", "needs_password": h.password != ""})
}

func (h *SetupHandler) verify(w http.ResponseWriter, r *http.Request) {
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		ip = r.RemoteAddr
	}
	if !h.limiter.allow(ip) {
		setupError(w, 429, "Wait one minute, then try your password again.")
		return
	}
	var req struct {
		Password string `json:"password"`
	}
	if !setupDecode(w, r, &req) {
		return
	}
	if h.password != "" && subtle.ConstantTimeCompare([]byte(req.Password), []byte(h.password)) != 1 {
		setupError(w, 401, "Check your setup password and try again.")
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "sh_setup", Value: h.token, Path: "/v1/setup", HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: r.TLS != nil})
	w.WriteHeader(http.StatusNoContent)
}

func (h *SetupHandler) authorize(next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if h.password != "" {
			cookie, err := r.Cookie("sh_setup")
			if err != nil || subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(h.token)) != 1 {
				setupError(w, 401, "Reload this page and enter your setup password.")
				return
			}
		}
		next(w, r)
	})
}

func setupDomain(raw string) (string, error) {
	domain := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(raw), "."))
	if len(domain) > 247 || !strings.Contains(domain, ".") || net.ParseIP(domain) != nil {
		return "", errors.New("Enter a domain like hack.example.com, without https:// or a path.")
	}
	for _, label := range strings.Split(domain, ".") {
		if !labelRE.MatchString(label) || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return "", errors.New("Enter a domain like hack.example.com, using letters, numbers and hyphens.")
		}
	}
	return domain, nil
}

func (h *SetupHandler) ownDomain(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Domain string `json:"domain"`
	}
	if !setupDecode(w, r, &req) {
		return
	}
	host, err := setupDomain(req.Domain)
	if err != nil {
		setupError(w, 400, err.Error())
		return
	}
	ip, err := h.publicIPv4(r.Context())
	if err != nil {
		setupError(w, 502, err.Error())
		return
	}
	records := []map[string]string{}
	for _, name := range []string{host, "sites." + host} {
		records = append(records, map[string]string{"name": name, "type": "A", "value": ip})
	}
	writeJSON(w, 200, map[string]any{"host": host, "content_host": "sites." + host, "records": records})
}

func (h *SetupHandler) dnsCheck(w http.ResponseWriter, r *http.Request) {
	host, err := setupDomain(r.URL.Query().Get("domain"))
	if err != nil {
		setupError(w, 400, err.Error())
		return
	}
	ip, err := h.publicIPv4(r.Context())
	if err != nil {
		setupError(w, 502, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	matches := func(name string) bool {
		addresses, err := net.DefaultResolver.LookupIP(ctx, "ip4", name)
		if err != nil {
			return false
		}
		for _, address := range addresses {
			if address.String() == ip {
				return true
			}
		}
		return false
	}
	writeJSON(w, 200, map[string]any{"host_ok": matches(host), "content_ok": matches("sites." + host), "ip": ip})
}

func (h *SetupHandler) freeName(w http.ResponseWriter, r *http.Request) {
	var req struct {
		APIKey string `json:"api_key"`
		Name   string `json:"name"`
	}
	if !setupDecode(w, r, &req) {
		return
	}
	ip, err := h.publicIPv4(r.Context())
	if err != nil {
		setupError(w, 502, err.Error())
		return
	}
	body, _ := json.Marshal(map[string]string{"name": req.Name, "ip": ip})
	upstream, err := http.NewRequestWithContext(r.Context(), "POST", h.publicAPI+"/v1/events", bytes.NewReader(body))
	if err != nil {
		setupError(w, 502, "Cannot claim a name. Try again.")
		return
	}
	upstream.Header.Set("X-API-Key", req.APIKey)
	upstream.Header.Set("Content-Type", "application/json")
	resp, err := h.client.Do(upstream)
	if err != nil {
		setupError(w, 502, "Cannot claim a name. Try again.")
		return
	}
	defer resp.Body.Close()
	// Preserve upstream error text and status, including name-taken errors.
	w.Header().Set("Content-Type", resp.Header.Get("Content-Type"))
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}

func (h *SetupHandler) finish(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Host        string `json:"host"`
		ContentHost string `json:"content_host"`
	}
	if !setupDecode(w, r, &req) {
		return
	}
	host, err := setupDomain(req.Host)
	if err != nil {
		setupError(w, 400, err.Error())
		return
	}
	if req.ContentHost != "sites."+host {
		setupError(w, 400, "Use the two hostnames from the previous step.")
		return
	}
	key := os.Getenv("ADMIN_API_KEY")
	if key == "" {
		setupError(w, 500, "Set ADMIN_API_KEY on this server, then retry.")
		return
	}
	tx, err := h.db.BeginTx(r.Context(), nil)
	if err != nil {
		setupError(w, 500, "Cannot save setup. Try again.")
		return
	}
	defer tx.Rollback()
	// Establish a lockable row even on a fresh database. A concurrent insert
	// waits for this transaction, then FOR UPDATE sees its committed value.
	_, err = tx.ExecContext(r.Context(), `INSERT INTO instance_config (key, value) VALUES ('site_domain', '') ON CONFLICT (key) DO NOTHING`)
	if err != nil {
		setupError(w, 500, "Cannot save setup. Try again.")
		return
	}
	var current string
	err = tx.QueryRowContext(r.Context(), `SELECT value FROM instance_config WHERE key='site_domain' FOR UPDATE`).Scan(&current)
	if err != nil {
		setupError(w, 500, "Cannot save setup. Try again.")
		return
	}
	if current != "" {
		setupError(w, 409, "Setup is already complete. Open your instance.")
		return
	}
	_, err = tx.ExecContext(r.Context(), `INSERT INTO instance_config (key, value) VALUES ('site_domain', $1), ('content_host', $2) ON CONFLICT (key) DO UPDATE SET value=EXCLUDED.value, updated_at=now()`, host, req.ContentHost)
	if err != nil {
		setupError(w, 500, "Cannot save setup. Try again.")
		return
	}
	if err = tx.Commit(); err != nil {
		setupError(w, 500, "Cannot confirm setup. Reload to check its status.")
		return
	}
	writeJSON(w, 200, map[string]string{"admin_api_key": key})
	// Only after the key has been written. It is shown exactly once, and an
	// organiser who loses it is not the administrator of their own instance.
	h.doneOnce.Do(func() { close(h.done) })
}

func (h *SetupHandler) publicIPv4(ctx context.Context) (string, error) {
	h.ipMu.Lock()
	defer h.ipMu.Unlock()
	if h.ip != "" {
		return h.ip, nil
	}
	// Metadata describes the assigned address even behind NAT; an echo service
	// may instead report a shared outbound gateway. Try provider metadata first.
	client := &http.Client{Timeout: 2 * time.Second, Transport: &http.Transport{Proxy: nil}}
	defer client.CloseIdleConnections()
	get := func(method, url string, headers map[string]string) string {
		req, err := http.NewRequestWithContext(ctx, method, url, nil)
		if err != nil {
			return ""
		}
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		resp, err := client.Do(req)
		if err != nil {
			return ""
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			return ""
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
		if err != nil {
			return ""
		}
		return strings.TrimSpace(string(body))
	}
	sources := []struct {
		url     string
		headers map[string]string
	}{
		{"http://169.254.169.254/metadata/v1/interfaces/public/0/ipv4/address", nil}, // DigitalOcean
		{"http://169.254.169.254/latest/meta-data/public-ipv4", nil},                 // Vultr / IMDSv1
		{"http://169.254.169.254/computeMetadata/v1/instance/network-interfaces/0/access-configs/0/external-ip", map[string]string{"Metadata-Flavor": "Google"}},
		{"http://169.254.169.254/metadata/instance/network/interface/0/ipv4/ipAddress/0/publicIpAddress?api-version=2021-02-01&format=text", map[string]string{"Metadata": "true"}},
	}
	token := get("PUT", "http://169.254.169.254/latest/api/token", map[string]string{"X-aws-ec2-metadata-token-ttl-seconds": "60"})
	if token != "" {
		sources = append(sources, struct {
			url     string
			headers map[string]string
		}{"http://169.254.169.254/latest/meta-data/public-ipv4", map[string]string{"X-aws-ec2-metadata-token": token}})
	}
	sources = append(sources, struct {
		url     string
		headers map[string]string
	}{"https://api.ipify.org", nil})
	for _, source := range sources {
		raw := get("GET", source.url, source.headers)
		ip := net.ParseIP(raw)
		if ip == nil || ip.To4() == nil {
			continue
		}
		if canonical, err := eventdns.ValidatePublicIP(raw); err == nil {
			h.ip = canonical
			return h.ip, nil
		}
	}
	return "", errors.New("Cannot find this server's address. Check its internet connection, then retry.")
}
