package handler

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vsriram/simple-host/internal/db"
)

// Download my data (GET /v1/me/export.tar.gz) and Delete my account
// (DELETE /v1/me), end to end. Needs DB_DSN (db/schema.sql applied).

func tarFiles(t *testing.T, b []byte) map[string]string {
	t.Helper()
	gz, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		t.Fatalf("not a gzip (%d bytes): %v", len(b), err)
	}
	tr := tar.NewReader(gz)
	out := map[string]string{}
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(tr)
		out[hdr.Name] = string(body)
	}
	return out
}

// fileUnder returns the archive entry whose name ends in suffix.
func fileUnder(t *testing.T, files map[string]string, suffix string) string {
	t.Helper()
	for name, body := range files {
		if strings.HasSuffix(name, suffix) {
			return body
		}
	}
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	t.Fatalf("archive has no %s: %v", suffix, names)
	return ""
}

func TestAccountExport(t *testing.T) {
	a, dir := newSiteApp(t, "canonical")
	olive, vic := a.newPerson(t, "olive"), a.newPerson(t, "vic")
	a.deploy(t, olive, "shop")
	oid, oh := a.userID(t, olive)
	vid, _ := a.userID(t, vic)
	markReady(t, dir, oh)
	const apex = pcSiteDomain
	okey := map[string]string{"X-API-Key": olive.key}
	vkey := map[string]string{"X-API-Key": vic.key}
	shopID := a.siteID(t, olive, "shop")
	claimed := oh + "-store." + pcSiteDomain
	if r := a.at(t, "POST", apex, "/v1/sites/shop/domain", map[string]string{"domain": claimed}, okey); r.status != 200 {
		t.Fatalf("claim: %d %s", r.status, r.body)
	}
	if _, err := a.database.Exec(`UPDATE sites SET state = '{"votes":3}' WHERE id = $1`, shopID); err != nil {
		t.Fatal(err)
	}
	// A public entry, and one Vic sent while signed in on Olive's site.
	var pubID, vicItem int64
	if err := a.database.QueryRow(`INSERT INTO collection_items (site_id, collection, data) VALUES ($1, 'guestbook', '{"msg":"hi"}') RETURNING id`, shopID).Scan(&pubID); err != nil {
		t.Fatal(err)
	}
	if err := a.database.QueryRow(`INSERT INTO collection_items (site_id, collection, data, submitted_by) VALUES ($1, 'orders', '{"dish":"idly","_submitted_by":"vic"}', $2) RETURNING id`, shopID, vid).Scan(&vicItem); err != nil {
		t.Fatal(err)
	}
	a.session(t, vic, shopID, claimed)
	if _, err := a.database.Exec(`INSERT INTO oauth_identities (user_id, provider, provider_user_id, email, email_verified) VALUES ($1, 'google', $2, $3, true)`, oid, "g-"+oid, olive.email); err != nil {
		t.Fatal(err)
	}

	// ---- the owner: sites, account, keys, never a secret ----
	r := a.at(t, "GET", apex, "/v1/me/export.tar.gz", nil, okey)
	if r.status != 200 || !strings.Contains(r.header.Get("Content-Disposition"), "simple-host-"+oh) {
		t.Fatalf("export: %d %v %s", r.status, r.header, r.body)
	}
	files := tarFiles(t, r.body)
	for _, want := range []string{"/README.txt", "/account.json", "/keys.json", "/connected_apps.json", "/visitor.json",
		"/sites/shop/state.json", "/sites/shop/collections.json", "/sites/shop/files/index.html"} {
		fileUnder(t, files, want)
	}
	if !strings.Contains(fileUnder(t, files, "/README.txt"), "salted") {
		t.Error("README does not explain why analytics are not included")
	}
	var account map[string]any
	if err := json.Unmarshal([]byte(fileUnder(t, files, "/account.json")), &account); err != nil {
		t.Fatal(err)
	}
	if account["email"] != olive.email || account["handle"] != oh || account["created_at"] == nil {
		t.Errorf("account.json: %v", account)
	}
	if !strings.Contains(fileUnder(t, files, "/account.json"), claimed) || !strings.Contains(fileUnder(t, files, "/account.json"), `"google"`) {
		t.Errorf("account.json lacks the claimed name or sign-in: %s", fileUnder(t, files, "/account.json"))
	}
	var colls map[string][]map[string]any
	if err := json.Unmarshal([]byte(fileUnder(t, files, "/sites/shop/collections.json")), &colls); err != nil {
		t.Fatal(err)
	}
	if len(colls["guestbook"]) != 1 || colls["guestbook"][0]["id"] != float64(pubID) || colls["guestbook"][0]["created_at"] == nil ||
		colls["guestbook"][0]["data"].(map[string]any)["msg"] != "hi" {
		t.Errorf("guestbook export: %v", colls["guestbook"])
	}
	if len(colls["orders"]) != 1 || colls["orders"][0]["submitted_by"] != vic.email {
		t.Errorf("orders export lacks its submitter: %v", colls["orders"])
	}
	var keys []map[string]any
	if err := json.Unmarshal([]byte(fileUnder(t, files, "/keys.json")), &keys); err != nil || len(keys) == 0 {
		t.Fatalf("keys.json: %v %s", err, fileUnder(t, files, "/keys.json"))
	}
	for k := range keys[0] {
		if k != "name" && k != "last4" && k != "created_at" && k != "last_used_at" {
			t.Errorf("keys.json carries %q", k)
		}
	}
	whole := string(r.body)
	for name, body := range files {
		whole += name + body
	}
	if strings.Contains(whole, olive.key) || strings.Contains(whole, db.HashAPIKey(olive.key)) || strings.Contains(whole, strings.TrimPrefix(olive.key, "shk_")) {
		t.Error("the export holds the API key or its hash")
	}

	// ---- the visitor: what they sent to someone else's site ----
	r = a.at(t, "GET", apex, "/v1/me/export.tar.gz", nil, vkey)
	if r.status != 200 {
		t.Fatalf("visitor export: %d %s", r.status, r.body)
	}
	files = tarFiles(t, r.body)
	var visitor struct {
		SignedInTo []map[string]any `json:"signed_in_to"`
		Submitted  []map[string]any `json:"submitted"`
	}
	if err := json.Unmarshal([]byte(fileUnder(t, files, "/visitor.json")), &visitor); err != nil {
		t.Fatal(err)
	}
	if len(visitor.Submitted) != 1 || visitor.Submitted[0]["list"] != "orders" || visitor.Submitted[0]["id"] != float64(vicItem) ||
		visitor.Submitted[0]["site"] != "https://"+claimed+"/" || visitor.Submitted[0]["item"].(map[string]any)["dish"] != "idly" {
		t.Errorf("visitor submissions: %v", visitor.Submitted)
	}
	if len(visitor.SignedInTo) != 1 || visitor.SignedInTo[0]["site"] != "https://"+claimed+"/" || visitor.SignedInTo[0]["first_sign_in"] == nil {
		t.Errorf("visitor sign-ins: %v", visitor.SignedInTo)
	}
	for name := range files {
		if strings.Contains(name, "/sites/") {
			t.Errorf("a visitor's export holds someone else's site: %s", name)
		}
	}

	if r := a.at(t, "GET", apex, "/v1/me/export.tar.gz", nil, nil); r.status != http.StatusUnauthorized {
		t.Errorf("export without a key: %d", r.status)
	}
}

func TestDeleteMyAccount(t *testing.T) {
	a, dir := newSiteApp(t, "canonical")
	olive, vic, other := a.newPerson(t, "olive"), a.newPerson(t, "vic"), a.newPerson(t, "other")
	a.deploy(t, olive, "shop")
	a.deploy(t, olive, "blog")
	a.deploy(t, vic, "board")
	a.deploy(t, other, "spare")
	oid, firstHandle := a.userID(t, olive)
	vid, _ := a.userID(t, vic)
	markReady(t, dir, firstHandle)
	const apex = pcSiteDomain
	okey := map[string]string{"X-API-Key": olive.key}
	shopID, blogID, boardID := a.siteID(t, olive, "shop"), a.siteID(t, olive, "blog"), a.siteID(t, vic, "board")

	claimed := firstHandle + "-store." + pcSiteDomain
	if r := a.at(t, "POST", apex, "/v1/sites/shop/domain", map[string]string{"domain": claimed}, okey); r.status != 200 {
		t.Fatalf("claim: %d %s", r.status, r.body)
	}
	if _, err := os.Lstat(filepath.Join(a.sites.disk.DataDir(), "domains", claimed)); err != nil {
		t.Fatalf("claimed name not bound on disk: %v", err)
	}
	// A handle change leaves the old handle as an alias.
	handle := firstHandle + "-x"
	if r := a.at(t, "PATCH", apex, "/v1/me", map[string]string{"handle": handle}, okey); r.status != 200 {
		t.Fatalf("rename: %d %s", r.status, r.body)
	}
	if r := a.at(t, "DELETE", apex, "/v1/sites/blog", nil, okey); r.status != http.StatusNoContent {
		t.Fatalf("soft delete: %d %s", r.status, r.body)
	}
	// Olive's data everywhere: an item on her site, one she sent to Vic's
	// list, a visitor session there, a connected app, a Google sign-in and a
	// pending sign-in code.
	mustExec := func(q string, args ...any) {
		t.Helper()
		if _, err := a.database.Exec(q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	mustExec(`INSERT INTO collection_items (site_id, collection, data) VALUES ($1, 'guestbook', '{"msg":"mine"}')`, shopID)
	mustExec(`INSERT INTO collection_items (site_id, collection, data, submitted_by) VALUES ($1, 'rsvps', '{"who":"olive"}', $2)`, boardID, oid)
	mustExec(`INSERT INTO collection_items (site_id, collection, data, submitted_by) VALUES ($1, 'rsvps', '{"who":"vic"}', $2)`, boardID, vid)
	a.session(t, olive, boardID, "board."+pcSiteDomain)
	clientID := "gdpr-test-" + oid
	mustExec(`INSERT INTO oauth_clients (client_id, client_name, redirect_uris) VALUES ($1, 'Test Chat', ARRAY['https://example.test/cb'])`, clientID)
	t.Cleanup(func() { _, _ = a.database.Exec(`DELETE FROM oauth_clients WHERE client_id = $1`, clientID) })
	var grantID string
	if err := a.database.QueryRow(`INSERT INTO oauth_grants (user_id, client_id, scope, resource) VALUES ($1, $2, 'sites', 'x') RETURNING id`, oid, clientID).Scan(&grantID); err != nil {
		t.Fatal(err)
	}
	mustExec(`INSERT INTO oauth_tokens (token_hash, grant_id, kind, expires_at) VALUES ($1, $2, 'access', now() + interval '1 hour')`, "tok-"+oid, grantID)
	mustExec(`INSERT INTO oauth_identities (user_id, provider, provider_user_id, email) VALUES ($1, 'github', $2, $3)`, oid, "gh-"+oid, olive.email)
	mustExec(`INSERT INTO auth_tokens (email, code, link_token, expires_at) VALUES ($1, '123456', $2, now() + interval '15 minutes')`, olive.email, "lt-"+oid)

	// ---- refusals ----
	if r := a.at(t, "DELETE", apex, "/v1/me", nil, okey); r.status != 400 || r.json(t)["code"] != "confirm_required" {
		t.Errorf("no body: %d %s", r.status, r.body)
	}
	if r := a.at(t, "DELETE", apex, "/v1/me", map[string]string{"confirm": firstHandle}, okey); r.status != 400 || r.json(t)["code"] != "confirm_mismatch" {
		t.Errorf("old handle as confirm: %d %s", r.status, r.body)
	}
	if r := a.at(t, "DELETE", apex, "/v1/me", map[string]string{"confirm": handle}, map[string]string{"X-API-Key": a.admin}); r.status != 403 || r.json(t)["code"] != "admin_account" {
		t.Errorf("admin: %d %s", r.status, r.body)
	}
	// A connected app acting for her (an internal key) cannot delete the account.
	ik, revoke, ierr := db.IssueInternalKey(oid)
	if ierr != nil {
		t.Fatal(ierr)
	}
	if r := a.at(t, "DELETE", apex, "/v1/me", map[string]string{"confirm": handle}, map[string]string{"X-API-Key": ik}); r.status != 400 || r.json(t)["code"] != "not_an_account_key" {
		t.Errorf("connected app: %d %s", r.status, r.body)
	}
	revoke()
	mustExec(`INSERT INTO event_domains (name, domain, user_id, ip, expires_at) VALUES ($1, 'event.test', $2, '192.0.2.1', now() + interval '1 day')`, "ev-"+handle, oid)
	if r := a.at(t, "DELETE", apex, "/v1/me", map[string]string{"confirm": handle}, okey); r.status != 409 || r.json(t)["code"] != "event_hostnames" {
		t.Errorf("event hostnames: %d %s", r.status, r.body)
	}
	mustExec(`DELETE FROM event_domains WHERE user_id = $1`, oid)
	if r := a.at(t, "POST", apex, "/v1/admin/users/"+oid+"/suspend", map[string]string{"reason": "check"}, map[string]string{"X-API-Key": a.admin}); r.status != 200 {
		t.Fatalf("suspend: %d %s", r.status, r.body)
	}
	if r := a.at(t, "DELETE", apex, "/v1/me", map[string]string{"confirm": handle}, okey); r.status != 403 || r.json(t)["code"] != "account_suspended" {
		t.Errorf("suspended: %d %s", r.status, r.body)
	}
	if r := a.at(t, "POST", apex, "/v1/admin/users/"+oid+"/enable", nil, map[string]string{"X-API-Key": a.admin}); r.status != 200 {
		t.Fatalf("enable: %d %s", r.status, r.body)
	}
	var n int
	if err := a.database.QueryRow(`SELECT count(*) FROM sites WHERE user_id = $1`, oid).Scan(&n); err != nil || n != 2 {
		t.Fatalf("a refused delete removed sites: %d %v", n, err)
	}

	// ---- delete ----
	if r := a.at(t, "DELETE", apex, "/v1/me", map[string]string{"confirm": strings.ToUpper(handle)}, okey); r.status != http.StatusNoContent {
		t.Fatalf("delete: %d %s", r.status, r.body)
	}
	if r := a.at(t, "GET", apex, "/v1/me", nil, okey); r.status != http.StatusUnauthorized {
		t.Errorf("the key still works after delete: %d", r.status)
	}

	// No row anywhere refers to the account or its sites.
	for _, ref := range []struct{ table, id string }{{"users", oid}, {"sites", shopID}, {"sites", blogID}} {
		rows, err := a.database.Query(`
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
			if err := a.database.QueryRow(`SELECT count(*) FROM `+c[0]+` WHERE `+c[1]+`::text = $1`, ref.id).Scan(&n); err != nil {
				t.Fatal(err)
			}
			if n != 0 {
				t.Errorf("%s.%s still holds %d row(s) for the deleted %s %s", c[0], c[1], n, ref.table, ref.id)
			}
		}
	}
	for _, q := range []string{`SELECT count(*) FROM users WHERE id = $1`, `SELECT count(*) FROM sites WHERE user_id = $1`} {
		if err := a.database.QueryRow(q, oid).Scan(&n); err != nil || n != 0 {
			t.Errorf("%s: %d %v", q, n, err)
		}
	}
	if err := a.database.QueryRow(`SELECT count(*) FROM auth_tokens WHERE email = $1`, olive.email).Scan(&n); err != nil || n != 0 {
		t.Errorf("sign-in codes left: %d %v", n, err)
	}
	// Her entry on Vic's list is gone; Vic's own stays.
	var left []string
	rows, err := a.database.Query(`SELECT data->>'who' FROM collection_items WHERE site_id = $1`, boardID)
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
	data := a.sites.disk.DataDir()
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
	if err := a.database.QueryRow(`SELECT site_id IS NULL, user_id IS NULL FROM legacy_hostnames WHERE hostname = $1`, claimed).Scan(&siteNull, &userNull); err != nil || !siteNull || !userNull {
		t.Errorf("claimed name not retired: %v %v %v", siteNull, userNull, err)
	}
	okey2 := map[string]string{"X-API-Key": other.key}
	if r := a.at(t, "POST", apex, "/v1/sites/spare/domain", map[string]string{"domain": claimed}, okey2); r.status == 200 {
		t.Errorf("someone else took the retired name: %s", r.body)
	}
	for _, h := range []string{handle, firstHandle} {
		if r := a.at(t, "PATCH", apex, "/v1/me", map[string]string{"handle": h}, okey2); r.status != http.StatusConflict {
			t.Errorf("someone else took the retired handle %s: %d %s", h, r.status, r.body)
		}
	}
	if r := a.at(t, "GET", claimed, "/", nil, nil); r.status == 200 && strings.Contains(string(r.body), "<h1>shop</h1>") {
		t.Error("the deleted site still serves at its claimed name")
	}
}

// An account without a handle confirms with its email.
func TestDeleteMyAccountWithoutHandle(t *testing.T) {
	a, _ := newSiteApp(t, "canonical")
	p := a.newPerson(t, "nohandle")
	uid, _ := a.userID(t, p)
	if _, err := a.database.Exec(`UPDATE users SET handle = NULL WHERE id = $1`, uid); err != nil {
		t.Fatal(err)
	}
	key := map[string]string{"X-API-Key": p.key}
	r := a.at(t, "GET", pcSiteDomain, "/v1/me/export.tar.gz", nil, key)
	if r.status != 200 || !strings.Contains(fileUnder(t, tarFiles(t, r.body), "/account.json"), p.email) {
		t.Fatalf("export without a handle: %d", r.status)
	}
	if r := a.at(t, "DELETE", pcSiteDomain, "/v1/me", map[string]string{"confirm": "nohandle"}, key); r.status != 400 || r.json(t)["code"] != "confirm_mismatch" {
		t.Errorf("wrong confirm: %d %s", r.status, r.body)
	}
	if r := a.at(t, "DELETE", pcSiteDomain, "/v1/me", map[string]string{"confirm": " " + p.email + " "}, key); r.status != http.StatusNoContent {
		t.Fatalf("delete with email: %d %s", r.status, r.body)
	}
	var n int
	if err := a.database.QueryRow(`SELECT count(*) FROM users WHERE id = $1`, uid).Scan(&n); err != nil || n != 0 {
		t.Errorf("account row left: %d %v", n, err)
	}
}

// Erasing an account lets go of the earlier address a site still answers at
// (previous_domain) too, unbinds and withdraws a domain only while it still
// points at this account's site, and removes take-down markers with the
// folders. The person cannot delete an account holding a taken-down site or a
// suspended account; the operator can.
func TestEraseAccountDomainsAndTakeDowns(t *testing.T) {
	a, _ := newSiteApp(t, "canonical")
	certs := t.TempDir()
	a.sites.domainCertDir = certs
	olive, other := a.newPerson(t, "olive"), a.newPerson(t, "other")
	a.deploy(t, olive, "shop")
	a.deploy(t, olive, "blog")
	a.deploy(t, other, "spare")
	oid, handle := a.userID(t, olive)
	xid, _ := a.userID(t, other)
	const apex = pcSiteDomain
	okey := map[string]string{"X-API-Key": olive.key}
	admin := map[string]string{"X-API-Key": a.admin}
	shopID, blogID, spareID := a.siteID(t, olive, "shop"), a.siteID(t, olive, "blog"), a.siteID(t, other, "spare")
	tag := oid[:8]
	// shop: a pending new domain (cur) and the earlier address it still
	// answers at (prev, linked to shop). The other account's site has since
	// taken cur up as its own earlier address, so cur's request stays.
	// blog: its domain's link was taken over on disk by the other account.
	cur, prev, taken := "new-"+tag+".example.test", "old-"+tag+".example.test", "taken-"+tag+".example.test"
	mustExec := func(q string, args ...any) {
		t.Helper()
		if _, err := a.database.Exec(q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	mustExec(`UPDATE sites SET custom_domain = $2, domain_status = 'pending', previous_domain = $3 WHERE id = $1`, shopID, cur, prev)
	mustExec(`UPDATE sites SET custom_domain = $2, domain_status = 'active' WHERE id = $1`, blogID, taken)
	mustExec(`UPDATE sites SET previous_domain = $2 WHERE id = $1`, spareID, cur)
	data := a.sites.disk.DataDir()
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
	if r := a.at(t, "POST", apex, "/v1/admin/sites/"+shopID+"/suspend", map[string]string{"reason": "report"}, admin); r.status != 200 {
		t.Fatalf("take down: %d %s", r.status, r.body)
	}
	if !a.sites.disk.IsSuspended(oid, "shop") {
		t.Fatal("no take-down marker")
	}
	if r := a.at(t, "DELETE", apex, "/v1/me", map[string]string{"confirm": handle}, okey); r.status != 403 || r.json(t)["code"] != "site_suspended" {
		t.Errorf("self-delete with a taken-down site: %d %s", r.status, r.body)
	}
	// The account suspended too: the operator still deletes it.
	if r := a.at(t, "POST", apex, "/v1/admin/users/"+oid+"/suspend", map[string]string{"reason": "check"}, admin); r.status != 200 {
		t.Fatalf("suspend: %d %s", r.status, r.body)
	}
	if r := a.at(t, "DELETE", apex, "/v1/admin/users/"+strings.ToUpper(oid), nil, admin); r.status != http.StatusNoContent {
		t.Fatalf("admin delete of a suspended account: %d %s", r.status, r.body)
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
	if err := a.database.QueryRow(`SELECT count(*) FROM users WHERE id = $1`, oid).Scan(&n); err != nil || n != 0 {
		t.Errorf("account row left: %d %v", n, err)
	}
}
