package handler

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"database/sql"

	"github.com/vsriram/simple-host/internal/auth"
	"github.com/vsriram/simple-host/internal/config"
	db "github.com/vsriram/simple-host/internal/db"
	"github.com/vsriram/simple-host/internal/email"
	"github.com/vsriram/simple-host/internal/storage"
	"net/http/httptest"
)

// Private collections and claimed <name>.<SITE_DOMAIN> addresses, end to end
// over HTTP against the real router (BoundSubdomains + LegacyHostRedirect in
// front, as cmd/server/main.go wires it). Needs DB_DSN (db/schema.sql applied).

const (
	pcSiteDomain  = "simple-host.test"
	pcContentHost = "sites.simple-host.test"
)

type privateApp struct {
	*connectorApp
	sites *SiteHandler
}

func newPrivateApp(t *testing.T) *privateApp {
	t.Helper()
	dsn := os.Getenv("DB_DSN")
	if dsn == "" {
		t.Skip("DB_DSN unset")
	}
	database, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	if err := database.Ping(); err != nil {
		t.Skipf("postgres not reachable: %v", err)
	}
	if err := db.VerifySchema(context.Background(), database); err != nil {
		t.Fatalf("test database is behind the schema: %v", err)
	}
	disk, err := storage.NewDiskStorage(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	adminKey, _ := auth.GenerateAPIKey()
	adminID, err := db.EnsureAdminUser(context.Background(), database)
	if err != nil {
		t.Fatal(err)
	}
	a := &privateApp{connectorApp: &connectorApp{database: database, admin: adminKey}}
	mux := http.NewServeMux()
	authMW := auth.Middleware(adminKey, adminID, database)
	mailer := email.NewResendSender("", "test@example.com")
	var root http.Handler
	a.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { root.ServeHTTP(w, r) }))
	t.Cleanup(a.srv.Close)
	users := NewUserHandler(database, mailer, a.srv.URL)
	users.Register(mux, authMW, NoticeMiddleware("1.0.0"))
	a.sites = NewSiteHandler(database, disk, pcSiteDomain, pcContentHost, "cname."+pcSiteDomain, "", "", adminKey, nil, 0, "on", adminID, mailer, users.EmailLimiter())
	a.sites.Register(mux, authMW, NoticeMiddleware("1.0.0"))
	users.SetPublicPage(a.sites.PersonPageURL)
	a.conn = NewConnectorHandler(database, a.srv.URL, adminKey, pcSiteDomain, pcContentHost, "1.0.0", mux)
	a.conn.Register(mux, authMW)
	app := SecurityHeaders(CORS(a.conn.BearerAuth(mux)))
	root = a.sites.BoundSubdomains(app, a.sites.PersonHosts(app, LegacyHostRedirect(pcSiteDomain, pcContentHost, database, app)))
	return a
}

// at sends one request as if it arrived at host over HTTPS (behind nginx).
func (a *privateApp) at(t *testing.T, method, host, path string, body any, headers map[string]string) resp {
	t.Helper()
	var rd io.Reader
	if body != nil {
		if s, ok := body.(string); ok {
			rd = strings.NewReader(s)
		} else {
			rd = jsonBody(body)
		}
	}
	req, err := http.NewRequest(method, a.srv.URL+path, rd)
	if err != nil {
		t.Fatal(err)
	}
	req.Host = host
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
	return resp{res.StatusCode, res.Header, b}
}

func (a *privateApp) deploy(t *testing.T, p person, site string) {
	t.Helper()
	r := a.at(t, "POST", "simple-host.test", "/v1/sites/"+site+"/files",
		map[string]any{"files": map[string]string{"index.html": "<h1>" + site + "</h1>", "sub/index.html": "sub"}},
		map[string]string{"X-API-Key": p.key})
	if r.status != http.StatusCreated {
		t.Fatalf("deploy %s: %d %s", site, r.status, r.body)
	}
}

func (a *privateApp) userID(t *testing.T, p person) (id, handle string) {
	t.Helper()
	if err := a.database.QueryRow(`SELECT id, handle FROM users WHERE username = $1`, p.email).Scan(&id, &handle); err != nil {
		t.Fatal(err)
	}
	return
}

func (a *privateApp) siteID(t *testing.T, p person, site string) string {
	t.Helper()
	uid, _ := a.userID(t, p)
	var id string
	if err := a.database.QueryRow(`SELECT id FROM sites WHERE user_id = $1 AND name = $2`, uid, site).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

// session signs p in as a visitor of site on host, returning the cookie value.
func (a *privateApp) session(t *testing.T, p person, siteID, host string) string {
	t.Helper()
	uid, _ := a.userID(t, p)
	id := make([]byte, 32)
	copy(id, []byte(strings.Repeat("x", 8)+time.Now().Format(time.RFC3339Nano)))
	for i := range id {
		id[i] ^= byte(i * 7)
	}
	raw, _ := auth.GenerateAPIKey() // 64 hex chars = 32 random bytes
	id, _ = hex.DecodeString(raw)
	now := time.Now()
	if err := db.InsertVisitorSession(context.Background(), a.database, id, uid, siteID, host, now.Add(time.Hour), now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	return raw
}

func browser(host, cookie string, extra ...string) map[string]string {
	h := map[string]string{"Origin": "https://" + host, "Sec-Fetch-Site": "same-origin", "X-SH-CSRF": "1"}
	if cookie != "" {
		h["Cookie"] = visitorCookieHost + "=" + cookie
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

func itemsOf(t *testing.T, r resp) []map[string]any {
	t.Helper()
	var body struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(r.body, &body); err != nil {
		t.Fatalf("not a list (%d): %s", r.status, r.body)
	}
	return body.Items
}

func TestPrivateCollectionsEndToEnd(t *testing.T) {
	a := newPrivateApp(t)
	olive, vic, oscar := a.newPerson(t, "olive"), a.newPerson(t, "vic"), a.newPerson(t, "oscar")
	// Oscar's "shop" is older: the legacy name lookup would pick it.
	a.deploy(t, oscar, "shop")
	a.deploy(t, olive, "shop")
	a.deploy(t, olive, "plain")
	_, oliveHandle := a.userID(t, olive)
	_, oscarHandle := a.userID(t, oscar)
	shopID := a.siteID(t, olive, "shop")
	const dom = "olive-shop.simple-host.test"
	const apex = "simple-host.test"
	okey := map[string]string{"X-API-Key": olive.key}

	// ---- setting private needs an own domain ---------------------------------
	r := a.at(t, "PUT", apex, "/v1/sites/plain/collections/orders/privacy", map[string]bool{"private": true}, okey)
	if r.status != http.StatusConflict || r.json(t)["code"] != "custom_domain_required" || !strings.Contains(r.json(t)["error"].(string), "private lists need the site on its own domain") {
		t.Fatalf("private without domain: %d %s", r.status, r.body)
	}
	// A custom domain still pending DNS does not count.
	if r := a.at(t, "POST", apex, "/v1/sites/plain/domain", map[string]string{"domain": "plain.example.test"}, okey); r.status != 200 {
		t.Fatalf("bind pending: %d %s", r.status, r.body)
	}
	if r := a.at(t, "PUT", apex, "/v1/sites/plain/collections/orders/privacy", map[string]bool{"private": true}, okey); r.status != http.StatusConflict {
		t.Fatalf("private with pending domain: %d %s", r.status, r.body)
	}

	// ---- claiming a free address ----------------------------------------------
	for _, tc := range []struct{ domain, code string }{
		{"www.simple-host.test", "name_reserved"},
		{"sites.simple-host.test", "name_reserved"},
		{"sf-fog.simple-host.test", "name_reserved"},
		{"a.b.simple-host.test", "invalid_name"},
		{"-bad.simple-host.test", "invalid_name"},
	} {
		if r := a.at(t, "POST", apex, "/v1/sites/shop/domain", map[string]string{"domain": tc.domain}, okey); r.status != 400 || r.json(t)["code"] != tc.code {
			t.Errorf("claim %s: %d %s", tc.domain, r.status, r.body)
		}
	}
	r = a.at(t, "POST", apex, "/v1/sites/shop/domain", map[string]string{"domain": "https://Olive-Shop.simple-host.test/"}, okey)
	if r.status != 200 || r.json(t)["status"] != "active" || r.json(t)["domain"] != dom || r.json(t)["dns"] != nil {
		t.Fatalf("claim: %d %s", r.status, r.body)
	}
	if r := a.at(t, "POST", apex, "/v1/sites/shop/domain", map[string]string{"domain": dom}, map[string]string{"X-API-Key": oscar.key}); r.status != http.StatusConflict || r.json(t)["code"] != "domain_taken" {
		t.Fatalf("second claim: %d %s", r.status, r.body)
	}
	if r := a.at(t, "GET", apex, "/v1/sites/shop/domain", nil, okey); r.json(t)["status"] != "active" || r.json(t)["dns"] != nil {
		t.Fatalf("domain status: %s", r.body)
	}
	// Oscar claims his own address for his "shop" (a different site, same name).
	if r := a.at(t, "POST", apex, "/v1/sites/shop/domain", map[string]string{"domain": "oscar.simple-host.test"}, map[string]string{"X-API-Key": oscar.key}); r.status != 200 {
		t.Fatalf("oscar claim: %d %s", r.status, r.body)
	}

	// ---- the claimed host serves the site like a custom domain -----------------
	if r := a.at(t, "GET", dom, "/", nil, nil); r.status != 200 || string(r.body) != "<h1>shop</h1>" {
		t.Fatalf("serve root: %d %s", r.status, r.body)
	}
	if r := a.at(t, "GET", dom, "/sub", nil, nil); r.status != http.StatusMovedPermanently {
		t.Fatalf("dir without slash: %d", r.status)
	}
	if r := a.at(t, "GET", dom, "/sub/", nil, nil); r.status != 200 || string(r.body) != "sub" {
		t.Fatalf("dir index: %d %s", r.status, r.body)
	}
	for _, p := range []string{"/../../etc/passwd", "/internal/tls-ask?domain=x.com", "/mcp", "/nope.html"} {
		if r := a.at(t, "GET", dom, p, nil, nil); r.status != 404 {
			t.Errorf("GET %s on claimed host: %d %s", p, r.status, r.body)
		}
	}
	if r := a.at(t, "POST", dom, "/", nil, nil); r.status != http.StatusMethodNotAllowed {
		t.Errorf("POST page: %d", r.status)
	}
	if r := a.at(t, "GET", "nobody-here.simple-host.test", "/x", nil, nil); r.status != http.StatusMovedPermanently {
		t.Fatalf("unclaimed name keeps legacy redirect: %d", r.status)
	}
	// The shared-host URL now points at the address (marker written on claim).
	if _, err := os.Stat(a.sites.disk.SiteDir(a.siteIDOwner(t, olive), "shop") + "/domain-redirect"); err != nil {
		t.Errorf("domain-redirect marker: %v", err)
	}

	// ---- owner makes "orders" private ------------------------------------------
	r = a.at(t, "PUT", apex, "/v1/sites/shop/collections/orders/privacy", map[string]bool{"private": true}, okey)
	if r.status != 200 || r.json(t)["private"] != true || r.json(t)["domain"] != dom {
		t.Fatalf("set private: %d %s", r.status, r.body)
	}
	// Other people cannot change it.
	if r := a.at(t, "PUT", apex, "/v1/u/"+oliveHandle+"/sites/shop/collections/orders/privacy", map[string]bool{"private": false}, map[string]string{"X-API-Key": vic.key}); r.status == 200 {
		t.Fatalf("stranger flipped privacy: %s", r.body)
	}
	if p, _ := db.IsCollectionPrivate(context.Background(), a.database, shopID, "orders"); !p {
		t.Fatal("privacy changed by a stranger")
	}

	orders := "/v1/sites/shop/collections/orders"
	vicCookie := a.session(t, vic, shopID, dom)
	ownerCookie := a.session(t, olive, shopID, dom)

	// ---- submitting ---------------------------------------------------------------
	if r := a.at(t, "POST", dom, orders, map[string]string{"name": "anon"}, browser(dom, "")); r.status != 401 || r.json(t)["code"] != "visitor_auth_required" {
		t.Fatalf("anonymous submit: %d %s", r.status, r.body)
	}
	if r := a.at(t, "POST", dom, orders, map[string]string{"name": "x"}, browser(dom, vicCookie, "X-SH-CSRF", "")); r.status != 403 || r.json(t)["code"] != "csrf_required" {
		t.Fatalf("no csrf: %d %s", r.status, r.body)
	}
	// A page on a sibling host (same-site to the browser) cannot ride the cookie.
	for name, h := range map[string]map[string]string{
		"shared-host page":    browser(dom, vicCookie, "Origin", "https://"+pcContentHost, "Sec-Fetch-Site", "same-site"),
		"other subdomain":     browser(dom, vicCookie, "Origin", "https://oscar.simple-host.test", "Sec-Fetch-Site", "same-site"),
		"no origin/referer":   browser(dom, vicCookie, "Origin", "", "Sec-Fetch-Site", ""),
		"tossed plain cookie": {"Origin": "https://" + dom, "X-SH-CSRF": "1", "Cookie": visitorCookieHTTP + "=" + vicCookie},
	} {
		// 403 = the Origin gate refused first; 401 = no usable session.
		if r := a.at(t, "POST", dom, orders, map[string]string{"name": "x"}, h); r.status != 401 && r.status != 403 {
			t.Errorf("submit via %s: %d %s", name, r.status, r.body)
		}
	}
	if r := a.at(t, "POST", dom, orders, map[string]string{"name": "k"}, map[string]string{"X-API-Key": olive.key, "Origin": "https://" + dom}); r.status != 403 || r.json(t)["code"] != "private_visitor_only" {
		t.Fatalf("key submit on domain: %d %s", r.status, r.body)
	}
	if r := a.at(t, "POST", apex, "/v1/u/"+oliveHandle+"/sites/shop/collections/orders", map[string]string{"name": "k"}, map[string]string{"X-API-Key": olive.key, "Origin": "https://" + pcContentHost}); r.status != 403 || r.json(t)["code"] != "private_visitor_only" {
		t.Fatalf("key submit on apex: %d %s", r.status, r.body)
	}
	if r := a.at(t, "POST", pcContentHost, "/v1/u/"+oliveHandle+"/sites/shop/collections/orders", map[string]string{"name": "s"}, map[string]string{"Origin": "https://" + pcContentHost}); r.status != 401 || r.json(t)["code"] != "use_custom_domain" {
		t.Fatalf("shared-host submit: %d %s", r.status, r.body)
	}
	if r := a.at(t, "POST", dom, orders, `[1,2]`, browser(dom, vicCookie)); r.status != 400 {
		t.Fatalf("array item: %d %s", r.status, r.body)
	}
	// The stamps cannot be forged.
	r = a.at(t, "POST", dom, orders, map[string]string{"name": "Vic", "phone": "555", "_submitted_by": "ceo@example.com", "_submitted_at": "1999-01-01T00:00:00Z"}, browser(dom, vicCookie))
	if r.status != 201 {
		t.Fatalf("visitor submit: %d %s", r.status, r.body)
	}
	data := r.json(t)["data"].(map[string]any)
	if data["_submitted_by"] != vic.email || data["_submitted_at"] == "1999-01-01T00:00:00Z" || data["name"] != "Vic" {
		t.Fatalf("stamps: %v", data)
	}
	vicItem := int64(r.json(t)["id"].(float64))
	var submitter string
	_ = a.database.QueryRow(`SELECT submitted_by FROM collection_items WHERE id = $1`, vicItem).Scan(&submitter)
	if vid, _ := a.userID(t, vic); submitter != vid {
		t.Fatalf("submitted_by column: %q", submitter)
	}
	// Referer alone (a same-origin GET-style page) also counts as same origin.
	if r := a.at(t, "POST", dom, orders, map[string]string{"name": "Spam"}, browser(dom, vicCookie, "Origin", "", "Referer", "https://"+dom+"/order.html")); r.status != 201 {
		t.Fatalf("referer submit: %d %s", r.status, r.body)
	}

	// ---- reading: everyone but the owner gets 404 -----------------------------
	for name, tc := range map[string]struct {
		host, path string
		h          map[string]string
	}{
		"anonymous on domain":       {dom, orders, browser(dom, "")},
		"submitter on domain":       {dom, orders, browser(dom, vicCookie)},
		"forged origin script":      {dom, orders, map[string]string{"Origin": "https://" + dom, "Referer": "https://" + dom + "/admin.html"}},
		"owner cookie cross-origin": {dom, orders, browser(dom, ownerCookie, "Origin", "https://"+pcContentHost, "Sec-Fetch-Site", "same-site")},
		"owner cookie plain name":   {dom, orders, map[string]string{"Origin": "https://" + dom, "Cookie": visitorCookieHTTP + "=" + ownerCookie}},
		"shared host anonymous":     {pcContentHost, "/v1/u/" + oliveHandle + "/sites/shop/collections/orders", map[string]string{"Origin": "https://" + pcContentHost}},
		"shared host owner key":     {pcContentHost, "/v1/u/" + oliveHandle + "/sites/shop/collections/orders", map[string]string{"X-API-Key": olive.key}},
		"other site's host":         {"oscar.simple-host.test", "/v1/u/" + oliveHandle + "/sites/shop/collections/orders", browser("oscar.simple-host.test", a.session(t, oscar, a.siteID(t, oscar, "shop"), "oscar.simple-host.test"))},
		"other user's session here": {dom, orders, browser(dom, a.session(t, oscar, shopID, dom))},
		"apex with owner cookie":    {apex, "/v1/u/" + oliveHandle + "/sites/shop/collections/orders", browser(apex, ownerCookie)},
		"invalid key":               {apex, "/v1/u/" + oliveHandle + "/sites/shop/collections/orders", map[string]string{"X-API-Key": "nope", "Origin": "https://" + dom}},
	} {
		r := a.at(t, "GET", tc.host, tc.path, nil, tc.h)
		if r.status != 404 && r.status != 403 {
			t.Errorf("%s: %d %s", name, r.status, r.body)
		}
		if strings.Contains(string(r.body), "555") || strings.Contains(string(r.body), vic.email) {
			t.Errorf("%s leaked data: %s", name, r.body)
		}
	}
	// Another account's key reads its OWN same-named site, never Olive's.
	for _, p := range []string{orders, "/v1/u/" + oliveHandle + "/sites/shop/collections/orders"} {
		r := a.at(t, "GET", apex, p, nil, map[string]string{"X-API-Key": oscar.key})
		if strings.Contains(string(r.body), "555") {
			t.Errorf("other user's key via %s leaked: %s", p, r.body)
		}
	}
	// The owner: key, admin key, whole-server connector token, CSV, own-domain session.
	whole := a.connectResource(t, olive, a.srv.URL)
	for name, tc := range map[string]struct {
		host string
		h    map[string]string
	}{
		"owner key":            {apex, okey},
		"admin key":            {apex, map[string]string{"X-API-Key": a.admin}},
		"connector bearer":     {apex, map[string]string{"Authorization": "Bearer " + whole}},
		"owner session domain": {dom, browser(dom, ownerCookie)},
	} {
		path := orders
		if name == "admin key" {
			path = "/v1/u/" + oliveHandle + "/sites/shop/collections/orders"
		}
		r := a.at(t, "GET", tc.host, path, nil, tc.h)
		items := itemsOf(t, r)
		if r.status != 200 || len(items) != 2 || r.json(t)["private"] != true || r.header.Get("Cache-Control") != "private, no-store" {
			t.Errorf("%s: %d %s", name, r.status, r.body)
		}
	}
	r = a.at(t, "GET", apex, orders+"/export.csv", nil, okey)
	if r.status != 200 || !strings.Contains(string(r.body), "_submitted_by") || !strings.Contains(string(r.body), vic.email) {
		t.Fatalf("csv: %d %s", r.status, r.body)
	}
	if r := a.at(t, "GET", pcContentHost, orders+"/export.csv", nil, okey); r.status != 404 {
		t.Fatalf("csv via shared host: %d", r.status)
	}
	r = a.at(t, "GET", apex, "/v1/sites/shop/collections", nil, okey)
	if !strings.Contains(string(r.body), `"name":"orders"`) || !strings.Contains(string(r.body), `"private":true`) {
		t.Fatalf("summaries: %s", r.body)
	}

	// ---- edit and delete (private only; owner and admin only) -----------------
	item := orders + "/items/" + itoa(vicItem)
	for name, tc := range map[string]struct {
		host string
		h    map[string]string
	}{
		"submitter session": {dom, browser(dom, vicCookie)},
		"anonymous":         {dom, browser(dom, "")},
		"forged origin":     {dom, map[string]string{"Origin": "https://" + dom, "X-SH-CSRF": "1"}},
		"other user key":    {apex, map[string]string{"X-API-Key": oscar.key}},
		"visitor key":       {apex, map[string]string{"X-API-Key": vic.key}},
		"shared host owner": {pcContentHost, okey},
	} {
		path := item
		if name == "visitor key" {
			path = "/v1/u/" + oliveHandle + item[len("/v1"):]
		}
		for _, m := range []string{"PATCH", "DELETE"} {
			r := a.at(t, m, tc.host, path, map[string]string{"status": "hacked"}, tc.h)
			if r.status != 404 && !(name == "other user key" && r.status == 409) {
				t.Errorf("%s %s: %d %s", name, m, r.status, r.body)
			}
		}
	}
	// Oscar through Olive's handle route: 404.
	if r := a.at(t, "PATCH", apex, "/v1/u/"+oliveHandle+"/sites/shop/collections/orders/items/"+itoa(vicItem), map[string]string{"status": "x"}, map[string]string{"X-API-Key": oscar.key}); r.status != 404 {
		t.Errorf("other user via handle route: %d %s", r.status, r.body)
	}
	r = a.at(t, "PATCH", apex, item, map[string]any{"status": "done", "phone": nil, "_submitted_by": "x@y", "_submitted_at": "2000"}, okey)
	if r.status != 200 {
		t.Fatalf("owner patch: %d %s", r.status, r.body)
	}
	data = r.json(t)["data"].(map[string]any)
	if data["status"] != "done" || data["_submitted_by"] != vic.email || data["_submitted_at"] == "2000" || data["phone"] != nil {
		t.Fatalf("patched: %v", data)
	}
	if r := a.at(t, "PATCH", apex, "/v1/u/"+oliveHandle+"/sites/shop/collections/orders/items/"+itoa(vicItem), map[string]string{"note": "admin"}, map[string]string{"X-API-Key": a.admin}); r.status != 200 {
		t.Fatalf("admin patch: %d %s", r.status, r.body)
	}
	if r := a.at(t, "PATCH", dom, item, map[string]string{"note": "from my admin page"}, browser(dom, ownerCookie)); r.status != 200 {
		t.Fatalf("owner session patch: %d %s", r.status, r.body)
	}
	if r := a.at(t, "PATCH", dom, item, map[string]string{"note": "x"}, browser(dom, ownerCookie, "X-SH-CSRF", "")); r.status != 403 {
		t.Fatalf("owner session patch without csrf: %d %s", r.status, r.body)
	}
	if r := a.at(t, "PATCH", apex, item, `"str"`, okey); r.status != 400 {
		t.Fatalf("non-object patch: %d", r.status)
	}
	if r := a.at(t, "PATCH", apex, orders+"/items/999999999", map[string]string{"a": "b"}, okey); r.status != 404 {
		t.Fatalf("missing item: %d", r.status)
	}
	// Delete the spam item as the owner (via the connector token); gone for everyone.
	spam := itemsOf(t, a.at(t, "GET", apex, orders, nil, okey))[0]
	spamID := int64(spam["id"].(float64))
	if r := a.at(t, "DELETE", apex, orders+"/items/"+itoa(spamID), nil, map[string]string{"Authorization": "Bearer " + whole}); r.status != 204 {
		t.Fatalf("delete: %d %s", r.status, r.body)
	}
	if n := len(itemsOf(t, a.at(t, "GET", apex, orders, nil, okey))); n != 1 {
		t.Fatalf("after delete: %d items", n)
	}
	var left int
	_ = a.database.QueryRow(`SELECT count(*) FROM collection_items WHERE id = $1`, spamID).Scan(&left)
	if left != 0 {
		t.Fatal("deleted row still stored")
	}

	// ---- public collections are unchanged ---------------------------------------
	gb := "/v1/sites/shop/collections/guestbook"
	r = a.at(t, "POST", dom, gb, map[string]string{"msg": "hi", "_submitted_by": "whoever"}, browser(dom, vicCookie))
	if r.status != 201 || r.json(t)["data"].(map[string]any)["_submitted_by"] != "whoever" {
		t.Fatalf("public submit: %d %s", r.status, r.body)
	}
	gbID := int64(r.json(t)["id"].(float64))
	if r := a.at(t, "GET", dom, gb, nil, map[string]string{"Origin": "https://" + dom}); r.status != 200 || len(itemsOf(t, r)) != 1 || r.json(t)["private"] != nil {
		t.Fatalf("public read: %d %s", r.status, r.body)
	}
	if r := a.at(t, "POST", dom, gb, map[string]string{"msg": "anon"}, browser(dom, "")); r.status != 401 {
		t.Fatalf("public anonymous write on domain (unchanged rule): %d", r.status)
	}
	if r := a.at(t, "POST", apex, "/v1/u/"+oliveHandle+"/sites/shop/collections/guestbook", map[string]string{"msg": "agent"}, map[string]string{"X-API-Key": olive.key, "Origin": "https://" + dom}); r.status != 201 {
		t.Fatalf("public key write (unchanged): %d %s", r.status, r.body)
	}
	if r := a.at(t, "PATCH", apex, gb+"/items/"+itoa(gbID), map[string]string{"msg": "edited"}, okey); r.status != 409 || r.json(t)["code"] != "append_only" {
		t.Fatalf("public edit: %d %s", r.status, r.body)
	}
	if r := a.at(t, "DELETE", apex, gb+"/items/"+itoa(gbID), nil, map[string]string{"X-API-Key": a.admin}); r.status != 409 {
		t.Fatalf("public delete by admin: %d %s", r.status, r.body)
	}
	// A site with no domain on the shared host: open writes, open reads (unchanged).
	sharedPlain := "/v1/u/" + oliveHandle + "/sites/plain/collections/notes"
	if r := a.at(t, "DELETE", apex, "/v1/sites/plain/domain", nil, okey); r.status != 204 {
		t.Fatalf("unbind: %d", r.status)
	}
	if r := a.at(t, "POST", pcContentHost, sharedPlain, map[string]string{"n": "1"}, map[string]string{"Origin": "https://" + pcContentHost}); r.status != 201 {
		t.Fatalf("shared host public write: %d %s", r.status, r.body)
	}
	if r := a.at(t, "GET", pcContentHost, sharedPlain, nil, map[string]string{"Origin": "https://" + pcContentHost}); r.status != 200 || len(itemsOf(t, r)) != 1 {
		t.Fatalf("shared host public read: %d %s", r.status, r.body)
	}

	// ---- the connector (MCP tools/call) ----------------------------------------
	clientID := a.registerClient(t, testRedirect)
	mcpTok := a.connect(t, olive, clientID, testRedirect)["access_token"].(string)
	call := func(name string, args map[string]any) (string, map[string]any, bool) {
		return toolResultOf(t, a.rpc(t, mcpTok, "tools/call", map[string]any{"name": name, "arguments": args}))
	}
	text, s, isErr := call("list_collections", map[string]any{"site": "shop"})
	if isErr || !strings.Contains(text, `"private": true`) {
		t.Fatalf("list_collections: %s", text)
	}
	text, s, isErr = call("read_collection", map[string]any{"site": "shop", "collection": "orders"})
	if isErr || s["private"] != true || !strings.Contains(text, vic.email) || !strings.Contains(text, `"id"`) {
		t.Fatalf("read_collection: %s", text)
	}
	text, _, isErr = call("read_collection", map[string]any{"site": "shop", "collection": "guestbook"})
	if isErr || strings.Contains(text, `"id"`) {
		t.Fatalf("public read_collection shows ids: %s", text)
	}
	if text, _, isErr = call("add_to_collection", map[string]any{"site": "shop", "collection": "orders", "item": map[string]any{"a": 1}}); !isErr || !strings.Contains(text, "private") {
		t.Fatalf("add_to_collection private: %s", text)
	}
	if text, _, isErr = call("set_collection_privacy", map[string]any{"site": "plain", "collection": "x", "private": true}); !isErr || !strings.Contains(text, "own domain") {
		t.Fatalf("set_collection_privacy without domain: %s", text)
	}
	if text, s, isErr = call("set_collection_privacy", map[string]any{"site": "shop", "collection": "rsvps", "private": true}); isErr || s["private"] != true {
		t.Fatalf("set_collection_privacy: %s", text)
	}
	if text, s, isErr = call("update_collection_item", map[string]any{"site": "shop", "collection": "orders", "id": itoa(vicItem), "fields": map[string]any{"status": "shipped", "_submitted_by": "nope"}}); isErr || s["data"].(map[string]any)["_submitted_by"] != vic.email || s["data"].(map[string]any)["status"] != "shipped" {
		t.Fatalf("update_collection_item: %s", text)
	}
	if text, _, isErr = call("delete_collection_item", map[string]any{"site": "shop", "collection": "orders", "id": itoa(vicItem), "confirm_id": itoa(vicItem + 1000)}); !isErr {
		t.Fatalf("delete without matching confirm: %s", text)
	}
	if text, _, isErr = call("delete_collection_item", map[string]any{"site": "shop", "collection": "orders", "id": itoa(vicItem), "confirm_id": itoa(vicItem)}); isErr {
		t.Fatalf("delete_collection_item: %s", text)
	}
	if text, s, isErr = call("connect_domain", map[string]any{"site": "plain", "domain": "olive-plain.simple-host.test"}); isErr || s["status"] != "active" || s["url"] != "https://olive-plain.simple-host.test/" {
		t.Fatalf("connect_domain free address: %s", text)
	}
	_ = oscarHandle
}

// Sessions and cookies on a claimed address: host-only __Host- cookie, never a
// Domain= attribute; /me from a sibling host reports signed out; sign-in by
// emailed code must come from the host itself.
func TestClaimedSubdomainSessionIsolation(t *testing.T) {
	a := newPrivateApp(t)
	olive, vic := a.newPerson(t, "olive"), a.newPerson(t, "vic")
	a.deploy(t, olive, "clay")
	const dom = "clay-and-kiln.simple-host.test"
	if r := a.at(t, "POST", "simple-host.test", "/v1/sites/clay/domain", map[string]string{"domain": dom}, map[string]string{"X-API-Key": olive.key}); r.status != 200 {
		t.Fatalf("claim: %d %s", r.status, r.body)
	}
	siteID := a.siteID(t, olive, "clay")
	// Establish hand-off on the claimed host sets a host-only __Host- cookie.
	uid, _ := a.userID(t, vic)
	sid, _ := hex.DecodeString(strings.Repeat("ab", 32))
	now := time.Now()
	if err := db.InsertVisitorSession(context.Background(), a.database, sid, uid, siteID, dom, now.Add(time.Hour), now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.DeleteVisitorSession(context.Background(), a.database, sid) })
	if err := db.InsertEstablishToken(context.Background(), a.database, "once-"+uid, sid, dom, "https://"+dom+"/", now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	r := a.at(t, "GET", dom, "/v1/visitor/establish?once=once-"+uid, nil, nil)
	sc := r.header.Values("Set-Cookie")
	if r.status != http.StatusFound || len(sc) != 1 || !strings.HasPrefix(sc[0], visitorCookieHost+"=") ||
		strings.Contains(strings.ToLower(sc[0]), "domain=") || !strings.Contains(sc[0], "Secure") || !strings.Contains(sc[0], "HttpOnly") {
		t.Fatalf("establish cookie: %d %v", r.status, sc)
	}
	cookie := strings.Repeat("ab", 32)
	// /me: same origin sees the visitor; a sibling host's page does not.
	me := "/v1/sites/clay/me"
	if r := a.at(t, "GET", dom, me, nil, browser(dom, cookie)); r.json(t)["signed_in"] != true || r.json(t)["email"] != vic.email {
		t.Fatalf("/me same origin: %s", r.body)
	}
	for _, o := range []string{"https://" + pcContentHost, "https://evil.simple-host.test"} {
		r := a.at(t, "GET", dom, me, nil, browser(dom, cookie, "Origin", o, "Sec-Fetch-Site", "same-site"))
		if r.json(t)["signed_in"] == true || strings.Contains(string(r.body), vic.email) {
			t.Errorf("/me from %s: %s", o, r.body)
		}
	}
	// Public state writes with the cookie from a sibling page are not the visitor.
	if r := a.at(t, "PATCH", dom, "/v1/sites/clay/state", map[string]any{"ops": []any{map[string]any{"op": "inc", "path": "n", "by": 1}}}, browser(dom, cookie, "Origin", "https://"+pcContentHost, "Sec-Fetch-Site", "same-site")); r.status != 401 {
		t.Errorf("sibling state write: %d %s", r.status, r.body)
	}
	if r := a.at(t, "PATCH", dom, "/v1/sites/clay/state", map[string]any{"ops": []any{map[string]any{"op": "inc", "path": "n", "by": 1}}}, browser(dom, cookie)); r.status != 200 {
		t.Errorf("same-origin state write: %d %s", r.status, r.body)
	}
	// Email sign-in on the claimed host must be same-origin.
	if r := a.at(t, "POST", dom, "/v1/sites/clay/visitor/auth/verify", map[string]string{"email": vic.email, "code": "123456"}, map[string]string{"Origin": "https://" + pcContentHost, "X-SH-CSRF": "1"}); r.status != 403 {
		t.Errorf("sibling sign-in: %d %s", r.status, r.body)
	}
	// CORS: the claimed host's Origin is its own; another site's API does not trust it.
	a.deploy(t, vic, "other")
	_, vicHandle := a.userID(t, vic)
	if r := a.at(t, "GET", pcContentHost, "/v1/u/"+vicHandle+"/sites/other/state", nil, map[string]string{"Origin": "https://" + dom}); r.status != 403 {
		t.Errorf("claimed origin trusted by another site: %d", r.status)
	}
	// Apex auth is unaffected: the dashboard API takes keys, never this cookie.
	if r := a.at(t, "GET", "simple-host.test", "/v1/me", nil, map[string]string{"Cookie": visitorCookieHost + "=" + cookie}); r.status != 401 {
		t.Errorf("cookie accepted as apex credential: %d", r.status)
	}
	// Sign-in may return to the claimed host (no DNS proof needed); not to an unclaimed one.
	oh := &OAuthHandler{database: a.database, cfg: config.Config{PublicBaseURL: "https://simple-host.test", SiteDomain: pcSiteDomain, ContentHost: pcContentHost, CNAMETarget: "cname." + pcSiteDomain}}
	if _, site, _, purpose, err := oh.sanitizeReturnTo(context.Background(), "https://"+dom+"/rsvp.html"); err != nil || purpose != "site" || site.String != siteID {
		t.Errorf("return_to claimed host: %v %q", err, purpose)
	}
	for _, bad := range []string{"https://unclaimed.simple-host.test/", "https://www.simple-host.test/", "https://" + pcContentHost + "/x/y/"} {
		if _, _, _, _, err := oh.sanitizeReturnTo(context.Background(), bad); err == nil {
			t.Errorf("return_to %s accepted", bad)
		}
	}
	// Releasing the address frees the name and restores the legacy redirect.
	if r := a.at(t, "DELETE", "simple-host.test", "/v1/sites/clay/domain", nil, map[string]string{"X-API-Key": olive.key}); r.status != 204 {
		t.Fatalf("release: %d", r.status)
	}
	if r := a.at(t, "GET", dom, "/", nil, nil); r.status != http.StatusMovedPermanently {
		t.Fatalf("released host: %d", r.status)
	}
}

func (a *privateApp) siteIDOwner(t *testing.T, p person) string {
	id, _ := a.userID(t, p)
	return id
}

// connectResource runs the connector flow for a whole-server token (a /v1
// credential), not an /mcp-only one.
func (a *privateApp) connectResource(t *testing.T, p person, resource string) string {
	t.Helper()
	clientID := a.registerClient(t, testRedirect)
	verifier, challenge := newVerifier()
	q := authorizeQuery(clientID, testRedirect, challenge)
	q.Set("resource", resource)
	code := codeFrom(t, a.consent(t, q, p.key, "allow"))
	r := a.form(t, "/oauth/token", url.Values{
		"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {testRedirect},
		"client_id": {clientID}, "code_verifier": {verifier}, "resource": {resource},
	}, nil)
	if r.status != http.StatusOK {
		t.Fatalf("token: %d %s", r.status, r.body)
	}
	return r.json(t)["access_token"].(string)
}

func itoa(n int64) string {
	b, _ := json.Marshal(n)
	return string(b)
}

// A visitor-supplied value must never reach the owner's spreadsheet as a
// formula: text starting with = + - @ tab or CR is neutralised, numbers are not.
func TestCSVSafeNeutralisesFormulas(t *testing.T) {
	cases := map[string]string{
		`"=cmd|'/C calc'!A1"`:        `'=cmd|'/C calc'!A1`,
		`"+HYPERLINK(\"http://x\")"`: `'+HYPERLINK("http://x")`,
		`"-2+3"`:                     `'-2+3`,
		`"@SUM(A1)"`:                 `'@SUM(A1)`,
		`"\t=1+1"`:                   "'\t=1+1",
		`"hello"`:                    `hello`,
		`-5`:                         `-5`,
		`""`:                         ``,
	}
	for in, want := range cases {
		if got := jsonCSVCell([]byte(in)); got != want {
			t.Errorf("jsonCSVCell(%s) = %q, want %q", in, got, want)
		}
	}
	if got := csvSafe("=bad-header"); got != "'=bad-header" {
		t.Errorf("csvSafe header = %q", got)
	}
}

// An establish code presented on the wrong host is burned: it cannot then be
// replayed on the host it was issued for. The normal hand-off still works once.
func TestEstablishTokenWrongHostBurns(t *testing.T) {
	a := newPrivateApp(t)
	olive, vic := a.newPerson(t, "olive"), a.newPerson(t, "vic")
	a.deploy(t, olive, "clay")
	a.deploy(t, olive, "pots")
	const dom, other = "burn-clay.simple-host.test", "burn-pots.simple-host.test"
	for site, d := range map[string]string{"clay": dom, "pots": other} {
		if r := a.at(t, "POST", "simple-host.test", "/v1/sites/"+site+"/domain", map[string]string{"domain": d}, map[string]string{"X-API-Key": olive.key}); r.status != 200 {
			t.Fatalf("claim %s: %d %s", d, r.status, r.body)
		}
	}
	siteID := a.siteID(t, olive, "clay")
	uid, _ := a.userID(t, vic)
	ctx := context.Background()
	now := time.Now()
	issue := func(sidByte, once string) {
		sid, _ := hex.DecodeString(strings.Repeat(sidByte, 32))
		if err := db.InsertVisitorSession(ctx, a.database, sid, uid, siteID, dom, now.Add(time.Hour), now.Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = db.DeleteVisitorSession(ctx, a.database, sid) })
		if err := db.InsertEstablishToken(ctx, a.database, once, sid, dom, "https://"+dom+"/", now.Add(time.Minute)); err != nil {
			t.Fatal(err)
		}
	}
	establish := func(host, once string) resp {
		return a.at(t, "GET", host, "/v1/visitor/establish?once="+once, nil, nil)
	}

	// Wrong host: rejected, then the right host is rejected too (burned).
	burn := "burn-" + uid
	issue("cd", burn)
	if r := establish(other, burn); r.status != http.StatusBadRequest || len(r.header.Values("Set-Cookie")) != 0 {
		t.Fatalf("wrong host accepted: %d %v", r.status, r.header.Values("Set-Cookie"))
	}
	if r := establish(dom, burn); r.status != http.StatusBadRequest || len(r.header.Values("Set-Cookie")) != 0 {
		t.Fatalf("burned code replayed on right host: %d %v", r.status, r.header.Values("Set-Cookie"))
	}

	// Normal flow: right host works once, then the code is spent.
	good := "good-" + uid
	issue("ef", good)
	if r := establish(strings.ToUpper(dom), good); r.status != http.StatusFound || len(r.header.Values("Set-Cookie")) != 1 {
		t.Fatalf("normal establish: %d %v", r.status, r.header.Values("Set-Cookie"))
	}
	if r := establish(dom, good); r.status != http.StatusBadRequest {
		t.Fatalf("code reused: %d", r.status)
	}
}
