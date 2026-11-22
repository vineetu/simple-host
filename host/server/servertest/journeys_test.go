package servertest

// Cross-module wiring journeys through the composed router (the host routing
// chain, Access's gate, Sites' pages and deploys, Data's routes and the
// connector). Each is a short path proving the modules are wired together;
// the rules themselves are proven in accesstest, datatest and sitestest.

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/vsriram/simple-host/host/access/gate"
)

// (a) Access's passcode gate stands in front of Sites' pages and Data's
// routes: a visitor gets neither until they unlock, then both; the owner's
// key always reads the data.
func TestJourneyPasscodeGatesPagesAndData(t *testing.T) {
	p := NewPasscodeApp(t)
	const code = "journey-pass-1"
	if r := p.Lock(t, code); r.Status != http.StatusOK {
		t.Fatalf("lock: %d %s", r.Status, r.Body)
	}
	noLeak := func(what string, r Resp, status int) {
		t.Helper()
		if r.Status != status || strings.Contains(string(r.Body), PasscodeCanary) {
			t.Fatalf("%s while locked: %d %s", what, r.Status, r.Body)
		}
	}
	noLeak("page", p.At(t, "GET", p.Host, "/", nil, nil), http.StatusUnauthorized)
	noLeak("data", p.At(t, "GET", p.Host, PasscodeData, nil, map[string]string{"Origin": "https://" + p.Host}), http.StatusForbidden)
	if r := p.At(t, "GET", PcSiteDomain, PasscodeData, nil, p.Okey); r.Status != http.StatusOK || !strings.Contains(string(r.Body), PasscodeCanary) {
		t.Fatalf("owner key data read: %d %s", r.Status, r.Body)
	}

	r := p.Unlock(t, p.Host, code, "/", nil)
	if r.Status != http.StatusSeeOther {
		t.Fatalf("unlock: %d %s", r.Status, r.Body)
	}
	cookie := CookieHeader(t, r)
	if r := p.At(t, "GET", p.Host, "/", nil, map[string]string{"Cookie": cookie}); r.Status != http.StatusOK || !strings.Contains(string(r.Body), PasscodeCanary) {
		t.Fatalf("page after unlock: %d %s", r.Status, r.Body)
	}
	if r := p.At(t, "GET", p.Host, PasscodeData, nil, map[string]string{"Cookie": cookie, "Origin": "https://" + p.Host}); r.Status != http.StatusOK || !strings.Contains(string(r.Body), PasscodeCanary) {
		t.Fatalf("data after unlock: %d %s", r.Status, r.Body)
	}
}

// (b) The connector: OAuth connect, then MCP tools deploy and read a site
// through Sites' routes, and the deployed page is served on its own host.
func TestJourneyConnectorDeploysAndReadsSite(t *testing.T) {
	a, dir := NewSiteApp(t, "canonical")
	olive := a.NewPerson(t, "olive")
	_, handle := a.UserID(t, olive)
	MarkReady(t, dir, handle)
	if r := a.Rpc(t, "not-a-token", "tools/list", map[string]any{}); r.Status != http.StatusUnauthorized {
		t.Fatalf("tools/list without a token: %d %s", r.Status, r.Body)
	}
	token := a.Connect(t, olive, a.RegisterClient(t, TestRedirect), TestRedirect)["access_token"].(string)
	call := func(tok, name string, args map[string]any) (string, map[string]any, bool) {
		t.Helper()
		return ToolResultOf(t, a.Rpc(t, tok, "tools/call", map[string]any{"name": name, "arguments": args}))
	}
	if text, _, bad := call(token, "create_site", map[string]any{"site": "shop", "files": map[string]any{"index.html": "<h1>via connector v1</h1>"}}); bad {
		t.Fatalf("create_site: %s", text)
	}
	host := "shop." + handle + "." + PcSiteDomain
	if r := a.At(t, "GET", host, "/", nil, nil); r.Status != http.StatusOK || !strings.Contains(string(r.Body), "via connector v1") {
		t.Fatalf("deployed page: %d %s", r.Status, r.Body)
	}
	if text, _, bad := call(token, "update_site", map[string]any{"site": "shop", "files": map[string]any{"index.html": "<h1>via connector v2</h1>"}}); bad {
		t.Fatalf("update_site: %s", text)
	}
	if text, out, bad := call(token, "read_site_file", map[string]any{"site": "shop", "path": "index.html"}); bad || !strings.Contains(text+toJSON(out), "via connector v2") {
		t.Fatalf("read_site_file: %v %s", bad, text)
	}
	// Another person's connection does not reach olive's site.
	vic := a.NewPerson(t, "vic")
	vicToken := a.Connect(t, vic, a.RegisterClient(t, TestRedirect), TestRedirect)["access_token"].(string)
	if text, _, bad := call(vicToken, "read_site_file", map[string]any{"site": "shop", "path": "index.html"}); !bad || strings.Contains(text, "via connector") {
		t.Fatalf("another account read olive's site: %v %s", bad, text)
	}
}

// (c) The visitor tools (Access's identity, Data's rules): a connected
// visitor adds an entry to someone else's site and reads back only their own;
// the owner sees every entry.
func TestJourneyVisitorToolsOnSiteWithData(t *testing.T) {
	f := NewVisitorFixture(t)
	f.Must(t, "PUT", "/resources/notes", StorageRules("sqlite", RulesPersonal), f.Key, http.StatusCreated)
	f.Must(t, "POST", "/sqlite/notes/schema", map[string]any{"sql": "CREATE TABLE entries(id INTEGER PRIMARY KEY, text TEXT)"}, f.Key, http.StatusOK)
	f.Must(t, "POST", "/sqlite/notes/tables/entries/rows", map[string]any{"text": "alice-only"}, f.Callers["alice"], http.StatusOK)

	if out := f.Call(t, "visitor_read_site", map[string]any{"address": "https://" + f.Host + "/"}, ""); out == nil {
		t.Fatal("visitor_read_site gave nothing")
	}
	args := f.Args("sqlite", "notes")
	args["table"] = "entries"
	args["value"] = map[string]any{"text": "bob-via-ai"}
	f.Call(t, "visitor_add_entry", args, "")
	read := f.Args("sqlite", "notes")
	read["table"] = "entries"
	got := toJSON(f.Call(t, "visitor_read_site", read, ""))
	if !strings.Contains(got, "bob-via-ai") || strings.Contains(got, "alice-only") {
		t.Fatalf("visitor read: %s", got)
	}
	owner := string(f.Must(t, "GET", "/sqlite/notes/tables/entries/rows", nil, f.Key, http.StatusOK).Body)
	if !strings.Contains(owner, "bob-via-ai") || !strings.Contains(owner, "alice-only") {
		t.Fatalf("owner read: %s", owner)
	}
}

// (d) An operator take-down holds everywhere: pages, data routes, visitor
// sign-in, deploys and the owner's delete are all refused; a site taken down
// while in Recently deleted comes back from Restore still down.
func TestJourneyTakeDownRefusesPagesDataAndDeploys(t *testing.T) {
	a, dir := NewSiteApp(t, "canonical")
	olive := a.NewPerson(t, "olive")
	a.Deploy(t, olive, "shop")
	a.Deploy(t, olive, "blog")
	uid, oh := a.UserID(t, olive)
	MarkReady(t, dir, oh)
	okey := map[string]string{"X-API-Key": olive.Key}
	admin := map[string]string{"X-API-Key": a.Admin}
	host := "shop." + oh + "." + PcSiteDomain
	data := "/v1/sites/shop/storage/kv/notes/keys/k"
	if r := a.At(t, "PUT", PcSiteDomain, "/v1/sites/shop/storage/resources/notes", StorageRules("kv", RulesPublic), okey); r.Status/100 != 2 {
		t.Fatalf("resource: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "PUT", PcSiteDomain, data, map[string]any{"value": "v"}, okey); r.Status != http.StatusOK {
		t.Fatalf("value: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "GET", host, data, nil, Browser(host, "")); r.Status != http.StatusOK {
		t.Fatalf("data before take-down: %d %s", r.Status, r.Body)
	}

	shopID := a.SiteID(t, olive, "shop")
	if r := a.At(t, "POST", PcSiteDomain, "/v1/admin/sites/"+shopID+"/suspend", map[string]string{"reason": "report"}, admin); r.Status != http.StatusOK {
		t.Fatalf("suspend: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "GET", host, "/", nil, nil); r.Status != http.StatusGone {
		t.Fatalf("taken-down page: %d", r.Status)
	}
	suspended := func(what string, r Resp) {
		t.Helper()
		if r.Status != http.StatusForbidden || r.JSON(t)["code"] != "site_suspended" {
			t.Fatalf("%s on a taken-down site: %d %s", what, r.Status, r.Body)
		}
	}
	suspended("visitor data read", a.At(t, "GET", host, data, nil, Browser(host, "")))
	suspended("owner data write", a.At(t, "PUT", PcSiteDomain, data, map[string]any{"value": "w"}, okey))
	suspended("visitor sign-in", a.At(t, "POST", host, "/v1/sites/shop/visitor/auth", map[string]string{"email": "ann@example.com"}, Browser(host, "")))
	suspended("owner delete", a.At(t, "DELETE", PcSiteDomain, "/v1/sites/shop", nil, okey))
	if r := a.At(t, "PUT", PcSiteDomain, "/v1/sites/shop/files", map[string]any{"files": map[string]string{"index.html": "x"}}, okey); r.Status != http.StatusForbidden {
		t.Fatalf("deploy to a taken-down site: %d %s", r.Status, r.Body)
	}
	if _, err := os.Stat(a.Sites.Disk.SiteDir(uid, "shop")); err != nil {
		t.Fatalf("taken-down site's files moved: %v", err)
	}
	if r := a.At(t, "POST", PcSiteDomain, "/v1/admin/sites/"+shopID+"/restore", nil, admin); r.Status != http.StatusOK {
		t.Fatalf("lift: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "GET", host, "/", nil, nil); r.Status != http.StatusOK {
		t.Fatalf("page after the take-down is lifted: %d", r.Status)
	}

	// Deleted, then taken down: Restore brings it back still down.
	blogID := a.SiteID(t, olive, "blog")
	if r := a.At(t, "DELETE", PcSiteDomain, "/v1/sites/blog", nil, okey); r.Status != http.StatusNoContent {
		t.Fatalf("delete: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "POST", PcSiteDomain, "/v1/admin/sites/"+blogID+"/suspend", map[string]string{"reason": "notice"}, admin); r.Status != http.StatusOK {
		t.Fatalf("suspend a deleted site: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "POST", PcSiteDomain, "/v1/sites/blog/restore", nil, okey); r.Status != http.StatusOK || r.JSON(t)["suspended"] != true {
		t.Fatalf("restore: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "GET", "blog."+oh+"."+PcSiteDomain, "/", nil, nil); r.Status != http.StatusGone {
		t.Fatalf("restored taken-down site serves: %d", r.Status)
	}
}

// (e) Download my data and Delete my account cover the account's sites, its
// Recently deleted ones and their saved data (Access's account routes over
// Sites' files and Data's storage).
func TestJourneyAccountExportAndErasure(t *testing.T) {
	s := NewStorageTestSite(t)
	a := s.A
	olive := s.Owner
	_, oh := a.UserID(t, olive)
	s.Must(t, "PUT", "/resources/wall", StorageRules("sqlite", RulesWall), s.Key, http.StatusCreated)
	s.Must(t, "POST", "/sqlite/wall/schema", map[string]any{"sql": "CREATE TABLE entries(id INTEGER PRIMARY KEY, text TEXT)"}, s.Key, http.StatusOK)
	s.Must(t, "POST", "/sqlite/wall/tables/entries/rows", map[string]any{"text": "from-a-visitor"}, s.Callers["alice"], http.StatusOK)
	// Deleting a site and restoring it keeps its saved data (Sites' trash
	// moves the folder Data's storage lives in).
	if r := a.At(t, "DELETE", PcSiteDomain, "/v1/sites/shop", nil, s.Key); r.Status != http.StatusNoContent {
		t.Fatalf("delete shop: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "POST", PcSiteDomain, "/v1/sites/shop/restore", nil, s.Key); r.Status != http.StatusOK {
		t.Fatalf("restore shop: %d %s", r.Status, r.Body)
	}
	if r := s.Must(t, "GET", "/sqlite/wall/tables/entries/rows", nil, s.Key, http.StatusOK); !strings.Contains(string(r.Body), "from-a-visitor") {
		t.Fatalf("restored site lost its saved data: %s", r.Body)
	}
	a.Deploy(t, olive, "old")
	if r := a.At(t, "DELETE", PcSiteDomain, "/v1/sites/old", nil, s.Key); r.Status != http.StatusNoContent {
		t.Fatalf("delete old: %d %s", r.Status, r.Body)
	}

	// The owner's export holds the live site with its saved data, and the
	// site in Recently deleted.
	r := a.At(t, "GET", PcSiteDomain, "/v1/me/export.zip", nil, s.Key)
	if r.Status != http.StatusOK {
		t.Fatalf("export: %d %s", r.Status, r.Body)
	}
	files := ZipFiles(t, r.Body)
	FileUnder(t, files, "/sites/shop/files/index.html")
	FileUnder(t, files, "/recently-deleted/old/files/index.html")
	hasData := false
	for name := range files {
		hasData = hasData || strings.Contains(name, "/sites/shop/storage/runtime/sqlite/wall")
	}
	if !hasData {
		t.Fatal("export has no saved data for shop")
	}

	// Erasure: sites, Recently deleted and saved data all go.
	uid, _ := a.UserID(t, olive)
	oldID := a.SiteIDAny(t, uid, "old")
	if r := a.At(t, "DELETE", PcSiteDomain, "/v1/me", map[string]string{"confirm": oh}, s.Key); r.Status != http.StatusNoContent {
		t.Fatalf("olive delete: %d %s", r.Status, r.Body)
	}
	var n int
	if err := a.Database.QueryRow(`SELECT count(*) FROM sites WHERE user_id = $1`, uid).Scan(&n); err != nil || n != 0 {
		t.Fatalf("erasure left %d site rows (%v)", n, err)
	}
	// The saved data lives under the site folder (runtime/).
	if _, err := os.Stat(a.Sites.Disk.SiteDir(uid, "shop")); !os.IsNotExist(err) {
		t.Fatalf("erasure left the site folder and its saved data: %v", err)
	}
	if _, err := os.Stat(a.Sites.Disk.TrashDir(uid, oldID)); !os.IsNotExist(err) {
		t.Fatalf("erasure left the Recently deleted site: %v", err)
	}
	if r := a.At(t, "GET", s.Host, "/", nil, nil); r.Status == http.StatusOK {
		t.Fatalf("erased site still serves: %d", r.Status)
	}
	if r := a.At(t, "GET", PcSiteDomain, "/v1/sites", nil, s.Key); r.Status != http.StatusUnauthorized {
		t.Fatalf("erased account's key: %d", r.Status)
	}
}

// A claimed <name>.<SITE_DOMAIN> address (Sites' host routing) carries
// Access's visitor sign-in: the hand-off sets a host-only cookie there, the
// site's /me sees the visitor, the apex never takes the cookie, and releasing
// the address restores the old redirect.
func TestJourneyClaimedAddressCarriesVisitorSignIn(t *testing.T) {
	a := NewPrivateApp(t)
	olive, vic := a.NewPerson(t, "olive"), a.NewPerson(t, "vic")
	a.Deploy(t, olive, "clay")
	dom := Uniq("clay") + "." + PcSiteDomain
	okey := map[string]string{"X-API-Key": olive.Key}
	if r := a.At(t, "POST", PcSiteDomain, "/v1/sites/clay/domain", map[string]string{"domain": dom}, okey); r.Status != http.StatusOK {
		t.Fatalf("claim: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "GET", dom, "/", nil, nil); r.Status != http.StatusOK || !strings.Contains(string(r.Body), "clay") {
		t.Fatalf("claimed address page: %d %s", r.Status, r.Body)
	}
	cookie := a.Session(t, vic, a.SiteID(t, olive, "clay"), dom)
	if r := a.At(t, "GET", dom, "/v1/sites/clay/me", nil, Browser(dom, cookie)); r.JSON(t)["signed_in"] != true || r.JSON(t)["email"] != vic.Email {
		t.Fatalf("/me on the claimed address: %s", r.Body)
	}
	if r := a.At(t, "GET", PcSiteDomain, "/v1/me", nil, map[string]string{"Cookie": gate.VisitorCookieHost + "=" + cookie}); r.Status != http.StatusUnauthorized {
		t.Fatalf("visitor cookie accepted as an apex credential: %d", r.Status)
	}
	if r := a.At(t, "DELETE", PcSiteDomain, "/v1/sites/clay/domain", nil, okey); r.Status != http.StatusNoContent {
		t.Fatalf("release: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "GET", dom, "/", nil, nil); r.Status != http.StatusMovedPermanently {
		t.Fatalf("released address: %d", r.Status)
	}
}

func toJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
