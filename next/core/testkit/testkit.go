// Package testkit starts the service against a throwaway Postgres database,
// with every outside service replaced by a local fake chosen by config URL:
// mail, browser push, DNS, Turnstile, Google and a certificate authority.
package testkit

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	_ "github.com/lib/pq"

	access "simplehost/access/core"
	"simplehost/app"
	sites "simplehost/sites/core"
)

// Kit is one started service and its fakes.
type Kit struct {
	App        *app.App
	Mail       *Mail
	Push       *Push
	DNS        *DNS
	Google     *Google
	Cloudflare *Cloudflare
	Turnstile  string // the fake Turnstile passes exactly this token
	server     *httptest.Server
	fakes      http.RoundTripper
}

// Start runs the service on a fresh database, mounts the given features
// (pass their Mount functions), and stops it all when the test ends.
func Start(t *testing.T, mounts ...func(*app.App)) *Kit {
	t.Helper()
	k := &Kit{Mail: &Mail{}, Push: &Push{}, DNS: &DNS{records: map[string][]string{}}, Google: &Google{}, Cloudflare: &Cloudflare{}, Turnstile: "pass"}
	mux := http.NewServeMux()
	mux.HandleFunc("/mail", k.Mail.serve)
	mux.HandleFunc("/resolve", k.DNS.serve)
	mux.HandleFunc("/google/", k.Google.serve)
	mux.HandleFunc("/turnstile", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]bool{"success": r.FormValue("response") == k.Turnstile})
	})
	mux.HandleFunc("/push/", k.Push.serve)
	mux.HandleFunc("/cloudflare/", k.Cloudflare.serve)
	mux.HandleFunc("/", certificateAuthority()) // Pebble's own paths: /dir, /nonce-plz, ...
	fakes := httptest.NewTLSServer(mux)
	t.Cleanup(fakes.Close)
	k.Push.URL = fakes.URL + "/push/"
	k.Cloudflare.URL = fakes.URL + "/cloudflare"
	key := make([]byte, 32)
	rand.Read(key)
	a, err := app.New(context.Background(), app.Config{
		DatabaseURL: freshDatabase(t), DataDir: t.TempDir(), AppDomain: "simple-host.app", SiteDomain: "simple-host.site",
		PasscodeKey: base64.StdEncoding.EncodeToString(key), MailKey: "test", MailFrom: "Simple Host <hello@simple-host.app>",
		MailURL: fakes.URL + "/mail", TurnstileURL: fakes.URL + "/turnstile", TurnstileSiteKey: "site-key", TurnstileSecret: "secret", DNSURL: fakes.URL + "/resolve",
		GoogleClientID: "test", GoogleClientSecret: "test", GoogleAuthURL: fakes.URL + "/google/auth",
		GoogleTokenURL: fakes.URL + "/google/token", GoogleUserURL: fakes.URL + "/google/userinfo",
		ACMEDirectories: []string{fakes.URL + "/dir"}, LoopbackOutbound: true, VAPIDKey: base64.RawURLEncoding.EncodeToString(key),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	k.fakes = fakes.Client().Transport
	a.HTTP.Transport = k.fakes // trust the fakes' certificate
	for _, m := range mounts {
		m(a)
	}
	k.App, k.server = a, httptest.NewServer(a)
	t.Cleanup(k.server.Close)
	return k
}

// BehindCloudflare puts the fake Cloudflare in front, as CF_ZONE_ID and
// CF_API_TOKEN do.
func (k *Kit) BehindCloudflare() {
	k.App.Sites.Cloudflare = &sites.Cloudflare{URL: k.Cloudflare.URL, Zone: "zone", Token: "token", Client: k.App.Guarded}
}

// Browser is a new browser with its own cookies. Every address, whatever
// its host, reaches this kit's service (the fakes' 127.0.0.1 addresses reach
// the fakes); https:// addresses arrive as nginx sends them
// (X-Forwarded-Proto: https). Like a browser, it sends Origin (its own
// address unless the call names another) with anything but a read.
// Redirects are not followed.
func (k *Kit) Browser() *http.Client {
	jar, _ := cookiejar.New(nil)
	return &http.Client{Jar: jar, Transport: toServer{k.server.URL, k.fakes},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

type toServer struct {
	base  string
	fakes http.RoundTripper
}

func (s toServer) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Hostname() == "127.0.0.1" {
		return s.fakes.RoundTrip(r)
	}
	r = r.Clone(r.Context())
	if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Header.Get("Origin") == "" {
		r.Header.Set("Origin", r.URL.Scheme+"://"+r.URL.Host) // as a browser sends it from the same address
	}
	if r.URL.Scheme == "https" {
		r.Header.Set("X-Forwarded-Proto", "https")
	}
	r.Host = r.URL.Host
	r.URL.Scheme, r.URL.Host = "http", s.base[len("http://"):]
	return http.DefaultTransport.RoundTrip(r)
}

// Res is an answer.
type Res struct {
	Status int
	Body   string
	Header http.Header
}

// Call sends one request through a browser. body is sent as JSON unless it
// is nil, a string or []byte. header is name, value pairs.
func Call(t *testing.T, c *http.Client, method, address string, body any, header ...string) Res {
	t.Helper()
	var rd io.Reader
	switch b := body.(type) {
	case nil:
	case string:
		rd = bytes.NewBufferString(b)
	case []byte:
		rd = bytes.NewReader(b)
	default:
		j, _ := json.Marshal(b)
		rd = bytes.NewReader(j)
	}
	req, err := http.NewRequest(method, address, rd)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i+1 < len(header); i += 2 {
		req.Header.Set(header[i], header[i+1])
	}
	res, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return Res{res.StatusCode, string(b), res.Header}
}

// Who is a person made for a test, with a key to act as them.
type Who struct{ ID, Email, Handle, Key string }

// Person makes a new-account person with a handle and a key.
func (k *Kit) Person(t *testing.T, handle string) Who {
	t.Helper()
	ctx := context.Background()
	p, err := k.App.People.Ensure(ctx, handle+"@example.com")
	if err == nil {
		err = access.ClaimName(ctx, k.App.DB, handle, "handle", p.ID, "")
	}
	var key string
	if err == nil {
		_, key, err = k.App.People.NewKey(ctx, p.ID, "test")
	}
	if err != nil {
		t.Fatal(err)
	}
	return Who{p.ID, p.Email, handle, key}
}

// Exec runs SQL for setup that no feature in the test offers.
func (k *Kit) Exec(t *testing.T, query string, args ...any) {
	t.Helper()
	if _, err := k.App.DB.Exec(query, args...); err != nil {
		t.Fatal(err)
	}
	k.App.Sites.Forget("")
}

// Publish puts files live as a new version of who's site, making the site
// if needed, through sites/core (for tests of features that need a site).
func (k *Kit) Publish(t *testing.T, who Who, site string, files map[string]string) {
	t.Helper()
	ctx := context.Background()
	s, err := k.App.Sites.Get(ctx, who.ID, site)
	if err != nil {
		s, err = k.App.Sites.Create(ctx, who.ID, site)
	}
	if err != nil {
		t.Fatal(err)
	}
	dir, err := k.App.Sites.Stage(s)
	for name, body := range files {
		if err == nil {
			os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o755)
			err = os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644)
		}
	}
	if err == nil {
		_, err = k.App.Sites.Commit(ctx, s, dir, who.ID)
	}
	if err != nil {
		t.Fatal(err)
	}
}

// The throwaway Postgres: SH_TEST_POSTGRES (an admin URL) when set, else a
// docker container kept on 127.0.0.1:54329 with its data in memory. Each
// Start gets its own database in it, dropped at the end.
var server struct {
	once sync.Once
	url  string
	err  error
}

func freshDatabase(t *testing.T) string {
	t.Helper()
	server.once.Do(func() {
		server.url = os.Getenv("SH_TEST_POSTGRES")
		if server.url == "" {
			server.url = "postgres://postgres:test@127.0.0.1:54329/postgres?sslmode=disable"
			if !answers(server.url) {
				exec.Command("docker", "run", "-d", "--rm", "--name", "sh-next-test", "-p", "127.0.0.1:54329:5432",
					"-e", "POSTGRES_PASSWORD=test", "--tmpfs", "/var/lib/postgresql/data", "postgres:16-alpine",
					"-c", "fsync=off", "-c", "synchronous_commit=off", "-c", "full_page_writes=off").Run()
			}
		}
		for i := 0; i < 60 && !answers(server.url); i++ {
			time.Sleep(500 * time.Millisecond)
		}
		if !answers(server.url) {
			server.err = io.ErrUnexpectedEOF
		}
	})
	if server.err != nil {
		t.Fatalf("no test Postgres at %s (start docker, or set SH_TEST_POSTGRES)", server.url)
	}
	admin, _ := sql.Open("postgres", server.url)
	defer admin.Close()
	b := make([]byte, 6)
	rand.Read(b)
	name := "t_" + hex.EncodeToString(b)
	if _, err := admin.Exec(`CREATE DATABASE ` + name); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		a, _ := sql.Open("postgres", server.url)
		defer a.Close()
		a.Exec(`DROP DATABASE ` + name + ` WITH (FORCE)`)
	})
	return replaceDB(server.url, name)
}

// Database is a fresh empty database on the throwaway Postgres, dropped when
// the test ends (for the copy tool, which needs an old and a new one).
func Database(t *testing.T) string { return freshDatabase(t) }

func answers(dsn string) bool {
	c, err := sql.Open("postgres", dsn)
	if err != nil {
		return false
	}
	defer c.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	return c.PingContext(ctx) == nil
}

func replaceDB(dsn, name string) string {
	u, _ := url.Parse(dsn)
	u.Path = "/" + name
	return u.String()
}

// SignIn signs who in on the site at siteURL in browser b, as visitor
// sign-in does when it ends (for tests of features that need a visitor).
func (k *Kit) SignIn(t *testing.T, b *http.Client, who Who, siteURL string) {
	t.Helper()
	u, _ := url.Parse(siteURL)
	p, err := k.App.Sites.Lookup(context.Background(), u.Hostname())
	if err != nil || p.Site == nil {
		t.Fatalf("no site at %s: %v", siteURL, err)
	}
	id := make([]byte, 32)
	rand.Read(id)
	k.Exec(t, `INSERT INTO visitor_sessions (id, person_id, site_id, host, expires_at) VALUES ($1, $2, $3, $4, now() + interval '1 day')`,
		access.SiteSessionID(hex.EncodeToString(id)), who.ID, p.Site.ID, u.Hostname())
	b.Jar.SetCookies(u, []*http.Cookie{{Name: "__Host-sh_vsess", Value: hex.EncodeToString(id), Path: "/", Secure: true}})
}
