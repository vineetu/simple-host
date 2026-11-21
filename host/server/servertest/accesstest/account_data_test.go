package accesstest

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	accountsrepo "github.com/vsriram/simple-host/host/access/repo"
	"github.com/vsriram/simple-host/host/server/servertest"
)

func TestAccountExport(t *testing.T) {
	a, dir := servertest.NewSiteApp(t, "canonical")
	olive, vic := a.NewPerson(t, "olive"), a.NewPerson(t, "vic")
	a.Deploy(t, olive, "shop")
	a.Deploy(t, olive, "old")
	oid, oh := a.UserID(t, olive)
	vid, _ := a.UserID(t, vic)
	servertest.MarkReady(t, dir, oh)
	const apex = servertest.PcSiteDomain
	okey := map[string]string{"X-API-Key": olive.Key}
	for _, site := range []string{"shop", "old"} {
		if r := a.At(t, "PUT", apex, "/v1/sites/"+site+"/files?keep=1", map[string]any{"files": map[string]string{"_redirects": "/before /after"}}, okey); r.Status != 200 {
			t.Fatal(r.Status, string(r.Body))
		}
	}
	// A site in Recently deleted comes along, in its own folder.
	if r := a.At(t, "DELETE", apex, "/v1/sites/old", nil, okey); r.Status != http.StatusNoContent {
		t.Fatalf("delete old: %d %s", r.Status, r.Body)
	}
	vkey := map[string]string{"X-API-Key": vic.Key}
	shopID := a.SiteID(t, olive, "shop")
	claimed := oh + "-store." + servertest.PcSiteDomain
	if r := a.At(t, "POST", apex, "/v1/sites/shop/domain", map[string]string{"domain": claimed}, okey); r.Status != 200 {
		t.Fatalf("claim: %d %s", r.Status, r.Body)
	}
	a.Session(t, vic, shopID, claimed)
	if _, err := a.Database.Exec(`INSERT INTO oauth_identities (user_id, provider, provider_user_id, email, email_verified) VALUES ($1, 'google', $2, $3, true)`, oid, "g-"+oid, olive.Email); err != nil {
		t.Fatal(err)
	}

	// ---- the owner: sites, account, keys, never a secret ----
	r := a.At(t, "GET", apex, "/v1/me/export.zip", nil, okey)
	if r.Status != 200 || !strings.Contains(r.Header.Get("Content-Disposition"), "simple-host-"+oh) ||
		!strings.Contains(r.Header.Get("Content-Disposition"), `.zip"`) || r.Header.Get("Content-Type") != "application/zip" {
		t.Fatalf("export: %d %v %s", r.Status, r.Header, r.Body)
	}
	files := servertest.ZipFiles(t, r.Body)
	for _, want := range []string{"/README.txt", "/account.json", "/keys.json", "/connected_apps.json", "/visitor.json",
		"/sites/shop/files/index.html", "/sites/shop/files/_redirects", "/recently-deleted/old/files/_redirects",
		"/recently-deleted/old/deleted.json", "/recently-deleted/old/files/index.html"} {
		servertest.FileUnder(t, files, want)
	}
	var del map[string]any
	if err := json.Unmarshal([]byte(servertest.FileUnder(t, files, "/recently-deleted/old/deleted.json")), &del); err != nil || del["removed_for_good_at"] == nil || del["deleted_at"] == nil {
		t.Errorf("deleted.json: %v %v", del, err)
	}
	for name := range files {
		if strings.Contains(name, "/sites/old/") {
			t.Errorf("a deleted site is filed with the live ones: %s", name)
		}
	}
	if !strings.Contains(servertest.FileUnder(t, files, "/README.txt"), "recently-deleted/") {
		t.Error("README does not explain the recently-deleted folder")
	}
	// The older .tar.gz address serves the same zip, so agents that used it
	// keep working.
	if old := a.At(t, "GET", apex, "/v1/me/export.tar.gz", nil, okey); old.Status != 200 || old.Header.Get("Content-Type") != "application/zip" {
		t.Errorf("old export address: %d %v", old.Status, old.Header)
	} else {
		servertest.FileUnder(t, servertest.ZipFiles(t, old.Body), "/sites/shop/files/index.html")
	}
	if !strings.Contains(servertest.FileUnder(t, files, "/README.txt"), "salted") {
		t.Error("README does not explain why analytics are not included")
	}
	var account map[string]any
	if err := json.Unmarshal([]byte(servertest.FileUnder(t, files, "/account.json")), &account); err != nil {
		t.Fatal(err)
	}
	if account["email"] != olive.Email || account["handle"] != oh || account["created_at"] == nil {
		t.Errorf("account.json: %v", account)
	}
	if !strings.Contains(servertest.FileUnder(t, files, "/account.json"), claimed) || !strings.Contains(servertest.FileUnder(t, files, "/account.json"), `"google"`) {
		t.Errorf("account.json lacks the claimed name or sign-in: %s", servertest.FileUnder(t, files, "/account.json"))
	}
	// A connected app acting for Olive cannot pull the whole account.
	ik, revoke, ierr := accountsrepo.IssueInternalKey(oid)
	if ierr != nil {
		t.Fatal(ierr)
	}
	if r := a.At(t, "GET", apex, "/v1/me/export.tar.gz", nil, map[string]string{"X-API-Key": ik}); r.Status != 400 || r.JSON(t)["code"] != "not_an_account_key" {
		t.Errorf("connected app export: %d %s", r.Status, r.Body)
	}
	revoke()
	var keys []map[string]any
	if err := json.Unmarshal([]byte(servertest.FileUnder(t, files, "/keys.json")), &keys); err != nil || len(keys) == 0 {
		t.Fatalf("keys.json: %v %s", err, servertest.FileUnder(t, files, "/keys.json"))
	}
	for k := range keys[0] {
		if k != "name" && k != "last4" && k != "scope" && k != "created_at" && k != "last_used_at" && k != "expires_at" {
			t.Errorf("keys.json carries %q", k)
		}
	}
	whole := string(r.Body)
	for name, body := range files {
		whole += name + body
	}
	if strings.Contains(whole, olive.Key) || strings.Contains(whole, accountsrepo.HashAPIKey(olive.Key)) || strings.Contains(whole, strings.TrimPrefix(olive.Key, "shk_")) {
		t.Error("the export holds the API key or its hash")
	}

	// ---- the visitor: where they are signed in on someone else's site ----
	r = a.At(t, "GET", apex, "/v1/me/export.zip", nil, vkey)
	if r.Status != 200 {
		t.Fatalf("visitor export: %d %s", r.Status, r.Body)
	}
	files = servertest.ZipFiles(t, r.Body)
	var visitor struct {
		SignedInTo []map[string]any `json:"signed_in_to"`
	}
	if err := json.Unmarshal([]byte(servertest.FileUnder(t, files, "/visitor.json")), &visitor); err != nil {
		t.Fatal(err)
	}
	if len(visitor.SignedInTo) != 1 || visitor.SignedInTo[0]["site"] != "https://"+claimed+"/" || visitor.SignedInTo[0]["first_sign_in"] == nil {
		t.Errorf("visitor sign-ins: %v", visitor.SignedInTo)
	}
	for name := range files {
		if strings.Contains(name, "/sites/") || strings.Contains(name, "/recently-deleted/") {
			t.Errorf("a visitor's export holds someone else's site: %s", name)
		}
	}

	if r := a.At(t, "GET", apex, "/v1/me/export.tar.gz", nil, nil); r.Status != http.StatusUnauthorized {
		t.Errorf("export without a key: %d", r.Status)
	}
	// Suspended: refused, and told where to ask for a copy.
	if _, err := a.Database.Exec(`UPDATE users SET suspended_at = now() WHERE id = $1`, vid); err != nil {
		t.Fatal(err)
	}
	if r := a.At(t, "GET", apex, "/v1/me/export.zip", nil, vkey); r.Status != 403 || !strings.Contains(r.JSON(t)["error"].(string), "support@simple-host.app") {
		t.Errorf("suspended export: %d %s", r.Status, r.Body)
	}
}

// Erasing an account retires its handle, but a retired-name row another
// account holds under the same name (possible with namespace checks off)
// stays that account's.
func TestEraseLeavesOthersAlias(t *testing.T) {
	a, _ := servertest.NewSiteApp(t, "canonical")
	p, other := a.NewPerson(t, "leaver"), a.NewPerson(t, "keeper")
	_, handle := a.UserID(t, p)
	kid, _ := a.UserID(t, other)
	if _, err := a.Database.Exec(`INSERT INTO handle_aliases (handle, user_id) VALUES ($1, $2)`, handle, kid); err != nil {
		t.Fatal(err)
	}
	if r := a.At(t, "DELETE", servertest.PcSiteDomain, "/v1/me", map[string]string{"confirm": handle}, map[string]string{"X-API-Key": p.Key}); r.Status != http.StatusNoContent {
		t.Fatalf("delete: %d %s", r.Status, r.Body)
	}
	var holder sql.NullString
	if err := a.Database.QueryRow(`SELECT user_id FROM handle_aliases WHERE handle = $1`, handle).Scan(&holder); err != nil || holder.String != kid {
		t.Errorf("another account's alias changed: %v %v", holder, err)
	}
}

func TestDeleteMyAccount(t *testing.T) {
	a, dir := servertest.NewSiteApp(t, "canonical")
	olive, vic, other := a.NewPerson(t, "olive"), a.NewPerson(t, "vic"), a.NewPerson(t, "other")
	a.Deploy(t, olive, "shop")
	a.Deploy(t, olive, "blog")
	a.Deploy(t, vic, "board")
	a.Deploy(t, other, "spare")
	oid, firstHandle := a.UserID(t, olive)
	vid, _ := a.UserID(t, vic)
	servertest.MarkReady(t, dir, firstHandle)
	const apex = servertest.PcSiteDomain
	okey := map[string]string{"X-API-Key": olive.Key}
	shopID, blogID, boardID := a.SiteID(t, olive, "shop"), a.SiteID(t, olive, "blog"), a.SiteID(t, vic, "board")

	claimed := firstHandle + "-store." + servertest.PcSiteDomain
	if r := a.At(t, "POST", apex, "/v1/sites/shop/domain", map[string]string{"domain": claimed}, okey); r.Status != 200 {
		t.Fatalf("claim: %d %s", r.Status, r.Body)
	}
	if _, err := os.Lstat(filepath.Join(a.Sites.Disk.DataDir(), "domains", claimed)); err != nil {
		t.Fatalf("claimed name not bound on disk: %v", err)
	}
	// A handle change leaves the old handle as an alias.
	handle := firstHandle + "-x"
	if r := a.At(t, "PATCH", apex, "/v1/me", map[string]string{"handle": handle}, okey); r.Status != 200 {
		t.Fatalf("rename: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "DELETE", apex, "/v1/sites/blog", nil, okey); r.Status != http.StatusNoContent {
		t.Fatalf("soft delete: %d %s", r.Status, r.Body)
	}
	// Olive's data everywhere: an item on her site, one she sent to Vic's
	// list, a visitor session there, a connected app, a Google sign-in and a
	// pending sign-in code.
	mustExec := func(q string, args ...any) {
		t.Helper()
		if _, err := a.Database.Exec(q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	mustExec(`INSERT INTO collection_items (site_id, collection, data) VALUES ($1, 'guestbook', '{"msg":"mine"}')`, shopID)
	mustExec(`INSERT INTO collection_items (site_id, collection, data, submitted_by) VALUES ($1, 'rsvps', '{"who":"olive"}', $2)`, boardID, oid)
	mustExec(`INSERT INTO collection_items (site_id, collection, data, submitted_by) VALUES ($1, 'rsvps', '{"who":"vic"}', $2)`, boardID, vid)
	a.Session(t, olive, boardID, "board."+servertest.PcSiteDomain)
	clientID := "gdpr-test-" + oid
	mustExec(`INSERT INTO oauth_clients (client_id, client_name, redirect_uris) VALUES ($1, 'Test Chat', ARRAY['https://example.test/cb'])`, clientID)
	t.Cleanup(func() { _, _ = a.Database.Exec(`DELETE FROM oauth_clients WHERE client_id = $1`, clientID) })
	var grantID string
	if err := a.Database.QueryRow(`INSERT INTO oauth_grants (user_id, client_id, scope, resource) VALUES ($1, $2, 'sites', 'x') RETURNING id`, oid, clientID).Scan(&grantID); err != nil {
		t.Fatal(err)
	}
	mustExec(`INSERT INTO oauth_tokens (token_hash, grant_id, kind, expires_at) VALUES ($1, $2, 'access', now() + interval '1 hour')`, "tok-"+oid, grantID)
	mustExec(`INSERT INTO oauth_identities (user_id, provider, provider_user_id, email) VALUES ($1, 'github', $2, $3)`, oid, "gh-"+oid, olive.Email)
	mustExec(`INSERT INTO auth_tokens (email, code, link_token, expires_at) VALUES ($1, '123456', $2, now() + interval '15 minutes')`, olive.Email, "lt-"+oid)

	// ---- refusals ----
	if r := a.At(t, "DELETE", apex, "/v1/me", nil, okey); r.Status != 400 || r.JSON(t)["code"] != "confirm_required" {
		t.Errorf("no body: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "DELETE", apex, "/v1/me", map[string]string{"confirm": firstHandle}, okey); r.Status != 400 || r.JSON(t)["code"] != "confirm_mismatch" {
		t.Errorf("old handle as confirm: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "DELETE", apex, "/v1/me", map[string]string{"confirm": handle}, map[string]string{"X-API-Key": a.Admin}); r.Status != 403 || r.JSON(t)["code"] != "admin_account" {
		t.Errorf("admin: %d %s", r.Status, r.Body)
	}
	// A connected app acting for her (an internal key) cannot delete the account.
	ik, revoke, ierr := accountsrepo.IssueInternalKey(oid)
	if ierr != nil {
		t.Fatal(ierr)
	}
	if r := a.At(t, "DELETE", apex, "/v1/me", map[string]string{"confirm": handle}, map[string]string{"X-API-Key": ik}); r.Status != 400 || r.JSON(t)["code"] != "not_an_account_key" {
		t.Errorf("connected app: %d %s", r.Status, r.Body)
	}
	revoke()
	mustExec(`INSERT INTO event_domains (name, domain, user_id, ip, expires_at) VALUES ($1, 'event.test', $2, '192.0.2.1', now() + interval '1 day')`, "ev-"+handle, oid)
	if r := a.At(t, "DELETE", apex, "/v1/me", map[string]string{"confirm": handle}, okey); r.Status != 409 || r.JSON(t)["code"] != "event_hostnames" {
		t.Errorf("event hostnames: %d %s", r.Status, r.Body)
	}
	mustExec(`DELETE FROM event_domains WHERE user_id = $1`, oid)
	if r := a.At(t, "POST", apex, "/v1/admin/users/"+oid+"/suspend", map[string]string{"reason": "check"}, map[string]string{"X-API-Key": a.Admin}); r.Status != 200 {
		t.Fatalf("suspend: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "DELETE", apex, "/v1/me", map[string]string{"confirm": handle}, okey); r.Status != 403 || r.JSON(t)["code"] != "account_suspended" {
		t.Errorf("suspended: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "POST", apex, "/v1/admin/users/"+oid+"/enable", nil, map[string]string{"X-API-Key": a.Admin}); r.Status != 200 {
		t.Fatalf("enable: %d %s", r.Status, r.Body)
	}
	var n int
	if err := a.Database.QueryRow(`SELECT count(*) FROM sites WHERE user_id = $1`, oid).Scan(&n); err != nil || n != 2 {
		t.Fatalf("a refused delete removed sites: %d %v", n, err)
	}

	// ---- delete ----
	if r := a.At(t, "DELETE", apex, "/v1/me", map[string]string{"confirm": strings.ToUpper(handle)}, okey); r.Status != http.StatusNoContent {
		t.Fatalf("delete: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "GET", apex, "/v1/me", nil, okey); r.Status != http.StatusUnauthorized {
		t.Errorf("the key still works after delete: %d", r.Status)
	}

	// No row anywhere refers to the account or its sites.
	for _, ref := range []struct{ table, id string }{{"users", oid}, {"sites", shopID}, {"sites", blogID}} {
		rows, err := a.Database.Query(`
			SELECT c.conrelid::regclass::text, a.attname
			  FROM pg_constraint c JOIN pg_attribute a ON a.attrelid = c.conrelid AND a.attnum = ANY (c.conkey)
			 WHERE c.contype = 'f' AND c.confrelid = $1::regclass`, ref.table)
		if err != nil {
			t.Fatal(err)
		}
		var cols [][2]string
		for rows.Next() {
			var tbl, col string
			_ = rows.Scan(&tbl, &col)
			cols = append(cols, [2]string{tbl, col})
		}
		rows.Close()
		if len(cols) == 0 {
			t.Fatalf("no foreign keys found to %s", ref.table)
		}
		for _, c := range cols {
			if err := a.Database.QueryRow(`SELECT count(*) FROM `+c[0]+` WHERE `+c[1]+`::text = $1`, ref.id).Scan(&n); err != nil {
				t.Fatal(err)
			}
			if n != 0 {
				t.Errorf("%s.%s still holds %d row(s) for the deleted %s %s", c[0], c[1], n, ref.table, ref.id)
			}
		}
	}
	for _, q := range []string{`SELECT count(*) FROM users WHERE id = $1`, `SELECT count(*) FROM sites WHERE user_id = $1`} {
		if err := a.Database.QueryRow(q, oid).Scan(&n); err != nil || n != 0 {
			t.Errorf("%s: %d %v", q, n, err)
		}
	}
	if err := a.Database.QueryRow(`SELECT count(*) FROM auth_tokens WHERE email = $1`, olive.Email).Scan(&n); err != nil || n != 0 {
		t.Errorf("sign-in codes left: %d %v", n, err)
	}
	// Her entry on Vic's list is gone; Vic's own stays.
	var left []string
	rows, err := a.Database.Query(`SELECT data->>'who' FROM collection_items WHERE site_id = $1`, boardID)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var w string
		_ = rows.Scan(&w)
		left = append(left, w)
	}
	rows.Close()
	if len(left) != 1 || left[0] != "vic" {
		t.Errorf("entries left on Vic's list: %v", left)
	}

	// Files and links are gone.
	data := a.Sites.Disk.DataDir()
	for _, p := range []string{
		filepath.Join(data, "by-id", oid),
		filepath.Join(data, "deleted", oid),
		filepath.Join(data, "handles", handle),
		filepath.Join(data, "handles", firstHandle),
		filepath.Join(data, "domains", claimed),
	} {
		if _, err := os.Lstat(p); !os.IsNotExist(err) {
			t.Errorf("%s left behind: %v", p, err)
		}
	}

	// Names stay retired: nobody else takes the claimed name, the handle or
	// the old handle.
	var siteNull, userNull bool
	if err := a.Database.QueryRow(`SELECT site_id IS NULL, user_id IS NULL FROM legacy_hostnames WHERE hostname = $1`, claimed).Scan(&siteNull, &userNull); err != nil || !siteNull || !userNull {
		t.Errorf("claimed name not retired: %v %v %v", siteNull, userNull, err)
	}
	okey2 := map[string]string{"X-API-Key": other.Key}
	if r := a.At(t, "POST", apex, "/v1/sites/spare/domain", map[string]string{"domain": claimed}, okey2); r.Status == 200 {
		t.Errorf("someone else took the retired name: %s", r.Body)
	}
	for _, h := range []string{handle, firstHandle} {
		if r := a.At(t, "PATCH", apex, "/v1/me", map[string]string{"handle": h}, okey2); r.Status != http.StatusConflict {
			t.Errorf("someone else took the retired handle %s: %d %s", h, r.Status, r.Body)
		}
	}
	if r := a.At(t, "GET", claimed, "/", nil, nil); r.Status == 200 && strings.Contains(string(r.Body), "<h1>shop</h1>") {
		t.Error("the deleted site still serves at its claimed name")
	}
}

// An account without a handle confirms with its email.
func TestDeleteMyAccountWithoutHandle(t *testing.T) {
	a, _ := servertest.NewSiteApp(t, "canonical")
	p := a.NewPerson(t, "nohandle")
	uid, _ := a.UserID(t, p)
	if _, err := a.Database.Exec(`UPDATE users SET handle = NULL WHERE id = $1`, uid); err != nil {
		t.Fatal(err)
	}
	key := map[string]string{"X-API-Key": p.Key}
	r := a.At(t, "GET", servertest.PcSiteDomain, "/v1/me/export.zip", nil, key)
	if r.Status != 200 || !strings.Contains(servertest.FileUnder(t, servertest.ZipFiles(t, r.Body), "/account.json"), p.Email) {
		t.Fatalf("export without a handle: %d", r.Status)
	}
	if r := a.At(t, "DELETE", servertest.PcSiteDomain, "/v1/me", map[string]string{"confirm": "nohandle"}, key); r.Status != 400 || r.JSON(t)["code"] != "confirm_mismatch" {
		t.Errorf("wrong confirm: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "DELETE", servertest.PcSiteDomain, "/v1/me", map[string]string{"confirm": " " + p.Email + " "}, key); r.Status != http.StatusNoContent {
		t.Fatalf("delete with email: %d %s", r.Status, r.Body)
	}
	var n int
	if err := a.Database.QueryRow(`SELECT count(*) FROM users WHERE id = $1`, uid).Scan(&n); err != nil || n != 0 {
		t.Errorf("account row left: %d %v", n, err)
	}
}

// Erasing an account lets go of the earlier address a site still answers at
// (previous_domain) too, unbinds and withdraws a domain only while it still
// points at this account's site, and removes take-down markers with the
// folders. The person cannot delete an account holding a taken-down site or a
// suspended account; the operator can.
func TestEraseAccountDomainsAndTakeDowns(t *testing.T) {
	a, _ := servertest.NewSiteApp(t, "canonical")
	certs := t.TempDir()
	a.Sites.Hosts.SetDomainCertDir(certs)
	olive, other := a.NewPerson(t, "olive"), a.NewPerson(t, "other")
	a.Deploy(t, olive, "shop")
	a.Deploy(t, olive, "blog")
	a.Deploy(t, other, "spare")
	oid, handle := a.UserID(t, olive)
	xid, _ := a.UserID(t, other)
	const apex = servertest.PcSiteDomain
	okey := map[string]string{"X-API-Key": olive.Key}
	admin := map[string]string{"X-API-Key": a.Admin}
	shopID, blogID, spareID := a.SiteID(t, olive, "shop"), a.SiteID(t, olive, "blog"), a.SiteID(t, other, "spare")
	tag := oid[:8]
	// shop: a pending new domain (cur) and the earlier address it still
	// answers at (prev, linked to shop). The other account's site has since
	// taken cur up as its own earlier address, so cur's request stays.
	// blog: its domain's link was taken over on disk by the other account.
	cur, prev, taken := "new-"+tag+".example.test", "old-"+tag+".example.test", "taken-"+tag+".example.test"
	mustExec := func(q string, args ...any) {
		t.Helper()
		if _, err := a.Database.Exec(q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	mustExec(`UPDATE sites SET custom_domain = $2, domain_status = 'pending', previous_domain = $3 WHERE id = $1`, shopID, cur, prev)
	mustExec(`UPDATE sites SET custom_domain = $2, domain_status = 'active' WHERE id = $1`, blogID, taken)
	mustExec(`UPDATE sites SET previous_domain = $2 WHERE id = $1`, spareID, cur)
	data := a.Sites.Disk.DataDir()
	_ = os.MkdirAll(filepath.Join(data, "domains"), 0o755)
	for d, target := range map[string]string{prev: filepath.Join("..", "by-id", oid, "shop"), taken: filepath.Join("..", "by-id", xid, "spare")} {
		if err := os.Symlink(target, filepath.Join(data, "domains", d)); err != nil {
			t.Fatal(err)
		}
	}
	_ = os.MkdirAll(filepath.Join(certs, "requests"), 0o755)
	for _, d := range []string{cur, prev, taken} {
		if err := os.WriteFile(filepath.Join(certs, "requests", d), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// Taken down: the person cannot delete the account; the marker is on disk.
	if r := a.At(t, "POST", apex, "/v1/admin/sites/"+shopID+"/suspend", map[string]string{"reason": "report"}, admin); r.Status != 200 {
		t.Fatalf("take down: %d %s", r.Status, r.Body)
	}
	if !a.Sites.Disk.IsSuspended(oid, "shop") {
		t.Fatal("no take-down marker")
	}
	if r := a.At(t, "DELETE", apex, "/v1/me", map[string]string{"confirm": handle}, okey); r.Status != 403 || r.JSON(t)["code"] != "site_suspended" {
		t.Errorf("self-delete with a taken-down site: %d %s", r.Status, r.Body)
	}
	// The account suspended too: the operator still deletes it.
	if r := a.At(t, "POST", apex, "/v1/admin/users/"+oid+"/suspend", map[string]string{"reason": "check"}, admin); r.Status != 200 {
		t.Fatalf("suspend: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "DELETE", apex, "/v1/admin/users/"+strings.ToUpper(oid), nil, admin); r.Status != http.StatusNoContent {
		t.Fatalf("admin delete of a suspended account: %d %s", r.Status, r.Body)
	}
	for _, p := range []string{filepath.Join(data, "domains", prev), filepath.Join(certs, "requests", prev), filepath.Join(data, "by-id", oid)} {
		if _, err := os.Lstat(p); !os.IsNotExist(err) {
			t.Errorf("%s left behind: %v", p, err)
		}
	}
	for _, p := range []string{filepath.Join(certs, "requests", cur), filepath.Join(data, "domains", taken), filepath.Join(certs, "requests", taken)} {
		if _, err := os.Lstat(p); err != nil {
			t.Errorf("another account's %s was removed: %v", p, err)
		}
	}
	var n int
	if err := a.Database.QueryRow(`SELECT count(*) FROM users WHERE id = $1`, oid).Scan(&n); err != nil || n != 0 {
		t.Errorf("account row left: %d %v", n, err)
	}
}
