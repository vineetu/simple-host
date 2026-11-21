// Package routetest is what one module's route suite (accesstest, datatest,
// sitestest) needs to drive that module's real routes on a disposable
// database without building the other modules' routes: a test server, the
// response helpers, and people, sites and visitor sessions made through the
// modules' repositories. It imports no route package, so a change to one
// module's routes reruns only that module's suite (scripts/layering/rules.txt
// keeps it so). The whole composed router is host/server/servertest's.
package routetest

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	_ "github.com/lib/pq"

	"github.com/vsriram/simple-host/host/access/auth"
	accountsrepo "github.com/vsriram/simple-host/host/access/repo"
	operationsrepo "github.com/vsriram/simple-host/host/ops/repo"
	sitesrepo "github.com/vsriram/simple-host/host/sites/repo"
	"github.com/vsriram/simple-host/host/storage"
	"github.com/vsriram/simple-host/host/testdb"
)

// Hosts of the fixtures: hosted pages live on <site>.<handle>.SiteDomain.
const (
	SiteDomain  = "simple-host.test"
	ContentHost = "sites.simple-host.test"
)

// VisitorCookie is Access's visitor session cookie (accesshttp.VisitorCookieHost).
const VisitorCookie = "__Host-sh_vsess"

var schemaErr struct {
	once sync.Once
	err  error
}

// DB is the test's pool (testdb.DB), its schema checked once per process.
func DB(t *testing.T) *sql.DB {
	t.Helper()
	database := testdb.DB(t)
	schemaErr.once.Do(func() { schemaErr.err = operationsrepo.VerifySchema(context.Background(), database) })
	if schemaErr.err != nil {
		t.Fatalf("test database is behind the schema: %v", schemaErr.err)
	}
	return database
}

// Disk is a fresh site store under the test's temporary directory.
func Disk(t *testing.T) *storage.DiskStorage {
	t.Helper()
	disk, err := storage.NewDiskStorage(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return disk
}

// Resp is one response, read in full.
type Resp struct {
	Status int
	Header http.Header
	Body   []byte
}

// JSON decodes the body as an object.
func (r Resp) JSON(t *testing.T) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(r.Body, &out); err != nil {
		t.Fatalf("not JSON (%d): %s", r.Status, r.Body)
	}
	return out
}

// Code is the body's error code ("" when it has none).
func (r Resp) Code() string {
	var out struct {
		Code string `json:"code"`
	}
	_ = json.Unmarshal(r.Body, &out)
	return out.Code
}

// JSONBody is v as a JSON request body.
func JSONBody(v any) io.Reader {
	b, _ := json.Marshal(v)
	return strings.NewReader(string(b))
}

// Server is a test server in front of h.
type Server struct {
	Srv *httptest.Server
}

// NewServer serves h until the test ends.
func NewServer(t *testing.T, h http.Handler) *Server {
	t.Helper()
	s := &Server{Srv: httptest.NewServer(h)}
	t.Cleanup(s.Srv.Close)
	return s
}

// At sends one request as if it arrived at host over HTTPS (behind nginx).
// body is a string or a value sent as JSON; redirects are not followed.
func (s *Server) At(t *testing.T, method, host, path string, body any, headers map[string]string) Resp {
	t.Helper()
	var rd io.Reader
	if body != nil {
		if str, ok := body.(string); ok {
			rd = strings.NewReader(str)
		} else if r, ok := body.(io.Reader); ok {
			rd = r
		} else {
			rd = JSONBody(body)
		}
	}
	req, err := http.NewRequest(method, s.Srv.URL+path, rd)
	if err != nil {
		t.Fatal(err)
	}
	if host != "" {
		req.Host = host
	}
	req.Header.Set("X-Forwarded-Proto", "https")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return Resp{res.StatusCode, res.Header, b}
}

// Person is an account and its key.
type Person struct{ Email, Key, ID, Handle string }

// NewPerson creates an account with a handle and a key, removed after the test.
func NewPerson(t *testing.T, database *sql.DB, label string) Person {
	t.Helper()
	addr := label + "-" + strconv.FormatInt(time.Now().UnixNano(), 36) + "@example.com"
	key, _ := auth.GenerateAPIKey()
	u, err := accountsrepo.CreateUser(context.Background(), database, addr, key, false)
	if err != nil {
		t.Fatal(err)
	}
	handle := strings.ReplaceAll(strings.Split(addr, "@")[0], ".", "-")
	if _, err := database.Exec(`UPDATE users SET handle = $1 WHERE id = $2`, handle, u.ID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = database.Exec(`DELETE FROM users WHERE id = $1`, u.ID) })
	return Person{Email: addr, Key: key, ID: u.ID, Handle: handle}
}

// Key is p's key as a request header.
func (p Person) Header() map[string]string { return map[string]string{"X-API-Key": p.Key} }

// SiteHost is p's site's own host.
func (p Person) SiteHost(site string) string { return site + "." + p.Handle + "." + SiteDomain }

// Deploy publishes files as p's site name (version 1, live), the way a
// first deploy leaves the database and the disk, and returns the site's id.
// nil files deploy a small two-page site.
func Deploy(t *testing.T, database *sql.DB, disk *storage.DiskStorage, p Person, name string, files map[string]string) string {
	t.Helper()
	if files == nil {
		files = map[string]string{"index.html": "<h1>" + name + "</h1>", "sub/index.html": "sub"}
	}
	ctx := context.Background()
	site, err := sitesrepo.CreateSite(ctx, database, p.ID, name, "https://"+ContentHost+"/"+p.Handle+"/"+name+"/", nil)
	if err != nil {
		t.Fatal(err)
	}
	v, err := sitesrepo.CreateVersion(ctx, database, site.ID, 1, "by-id/"+p.ID+"/"+name+"/v1/", "", "")
	if err != nil {
		t.Fatal(err)
	}
	raw := map[string][]byte{}
	for k, s := range files {
		raw[k] = []byte(s)
	}
	if err := disk.WriteFiles(ctx, p.ID, name, 1, raw); err != nil {
		t.Fatal(err)
	}
	if err := sitesrepo.ActivateVersion(ctx, database, v.ID); err != nil {
		t.Fatal(err)
	}
	if err := sitesrepo.UpdateSiteActiveVersion(ctx, database, site.ID, 1); err != nil {
		t.Fatal(err)
	}
	if err := disk.UpdateCurrent(p.ID, name, 1); err != nil {
		t.Fatal(err)
	}
	if err := disk.EnsureHandleLink(p.Handle, p.ID); err != nil {
		t.Fatal(err)
	}
	return site.ID
}

// Session signs userID in as a visitor of siteID on host and returns the
// cookie value (Browser sends it).
func Session(t *testing.T, database *sql.DB, userID, siteID, host string) string {
	t.Helper()
	key, _ := auth.GenerateAPIKey() // shk_ + 64 hex chars = 32 random bytes
	raw := strings.TrimPrefix(key, auth.APIKeyPrefix)
	id, _ := hex.DecodeString(raw)
	now := time.Now()
	if _, err := accountsrepo.InsertVisitorSession(context.Background(), database, id, userID, siteID, host, now.Add(time.Hour), now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	return raw
}

// Browser is the headers a page on host sends to its own data routes, with
// the visitor cookie when one is given; extra are header/value pairs ("" deletes).
func Browser(host, cookie string, extra ...string) map[string]string {
	h := map[string]string{"Origin": "https://" + host, "Sec-Fetch-Site": "same-origin", "X-SH-CSRF": "1"}
	if cookie != "" {
		h["Cookie"] = VisitorCookie + "=" + cookie
	}
	for i := 0; i+1 < len(extra); i += 2 {
		if extra[i+1] == "" {
			delete(h, extra[i])
		} else {
			h[extra[i]] = extra[i+1]
		}
	}
	return h
}

// SetPasscode protects siteID with a passcode row the way Access stores one
// (an encrypted value is enough: these suites never unlock it).
func SetPasscode(t *testing.T, database *sql.DB, siteID string) {
	t.Helper()
	if _, err := accountsrepo.SetSitePasscode(context.Background(), database, siteID, []byte("locked"), time.Now()); err != nil {
		t.Fatal(err)
	}
}
