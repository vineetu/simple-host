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
	rt "github.com/vsriram/simple-host/host/server/servertest/routetest"
)

func TestAccountExport(t *testing.T) {
	a := NewApp(t)
	olive, vic := a.NewPerson(t, "olive"), a.NewPerson(t, "vic")
	files := map[string]string{"index.html": "<h1>shop</h1>", "_redirects": "/before /after"}
	shopID := rt.Deploy(t, a.Database, a.Disk, olive, "shop", files)
	rt.Deploy(t, a.Database, a.Disk, olive, "old", files)
	a.acctSoftDelete(t, olive.ID, "old")
	claimed := olive.Handle + "-store." + rt.SiteDomain
	if _, err := a.Database.Exec(`UPDATE sites SET custom_domain = $2, domain_status = 'active' WHERE id = $1`, shopID, claimed); err != nil {
		t.Fatal(err)
	}
	a.Session(t, vic, shopID, olive.SiteHost("shop"))
	if _, err := a.Database.Exec(`INSERT INTO oauth_identities (user_id, provider, provider_user_id, email, email_verified) VALUES ($1, 'google', $2, $3, true)`, olive.ID, "g-"+olive.ID, olive.Email); err != nil {
		t.Fatal(err)
	}

	// The owner: sites (live and recently deleted), account, keys, never a secret.
	r := a.At(t, "GET", rt.SiteDomain, "/v1/me/export.zip", nil, olive.Header())
	if r.Status != 200 || r.Header.Get("Content-Type") != "application/zip" {
		t.Fatalf("export: %d %v", r.Status, r.Header)
	}
	got := acctZipFiles(t, r.Body)
	for _, want := range []string{"/account.json", "/keys.json", "/sites/shop/files/index.html", "/sites/shop/files/_redirects",
		"/recently-deleted/old/files/_redirects", "/recently-deleted/old/deleted.json"} {
		acctFileUnder(t, got, want)
	}
	for name := range got {
		if strings.Contains(name, "/sites/old/") {
			t.Errorf("a deleted site is filed with the live ones: %s", name)
		}
	}
	var account map[string]any
	if err := json.Unmarshal([]byte(acctFileUnder(t, got, "/account.json")), &account); err != nil {
		t.Fatal(err)
	}
	if account["email"] != olive.Email || account["handle"] != olive.Handle {
		t.Errorf("account.json: %v", account)
	}
	if text := acctFileUnder(t, got, "/account.json"); !strings.Contains(text, claimed) || !strings.Contains(text, `"google"`) {
		t.Errorf("account.json lacks the claimed name or sign-in: %s", text)
	}
	var keys []map[string]any
	if err := json.Unmarshal([]byte(acctFileUnder(t, got, "/keys.json")), &keys); err != nil || len(keys) == 0 {
		t.Fatalf("keys.json: %v", err)
	}
	for k := range keys[0] {
		if k != "name" && k != "last4" && k != "scope" && k != "created_at" && k != "last_used_at" && k != "expires_at" {
			t.Errorf("keys.json carries %q", k)
		}
	}
	whole := string(r.Body)
	for name, body := range got {
		whole += name + body
	}
	if strings.Contains(whole, olive.Key) || strings.Contains(whole, accountsrepo.HashAPIKey(olive.Key)) || strings.Contains(whole, strings.TrimPrefix(olive.Key, "shk_")) {
		t.Error("the export holds the API key or its hash")
	}
	// A connected app acting for Olive cannot pull the whole account.
	ik, revoke, err := accountsrepo.IssueInternalKey(olive.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer revoke()
	if r := a.At(t, "GET", rt.SiteDomain, "/v1/me/export.zip", nil, map[string]string{"X-API-Key": ik}); r.Status != 400 || r.Code() != "not_an_account_key" {
		t.Errorf("connected app export: %d %s", r.Status, r.Body)
	}

	// The visitor: where they are signed in on someone else's site, and no one else's sites.
	r = a.At(t, "GET", rt.SiteDomain, "/v1/me/export.zip", nil, vic.Header())
	if r.Status != 200 {
		t.Fatalf("visitor export: %d %s", r.Status, r.Body)
	}
	got = acctZipFiles(t, r.Body)
	var visitor struct {
		SignedInTo []map[string]any `json:"signed_in_to"`
	}
	if err := json.Unmarshal([]byte(acctFileUnder(t, got, "/visitor.json")), &visitor); err != nil {
		t.Fatal(err)
	}
	if len(visitor.SignedInTo) != 1 || !strings.Contains(visitor.SignedInTo[0]["site"].(string), "shop") {
		t.Errorf("visitor sign-ins: %v", visitor.SignedInTo)
	}
	for name := range got {
		if strings.Contains(name, "/sites/") || strings.Contains(name, "/recently-deleted/") {
			t.Errorf("a visitor's export holds someone else's site: %s", name)
		}
	}

	if r := a.At(t, "GET", rt.SiteDomain, "/v1/me/export.zip", nil, nil); r.Status != http.StatusUnauthorized {
		t.Errorf("export without a key: %d", r.Status)
	}
	if _, err := a.Database.Exec(`UPDATE users SET suspended_at = now() WHERE id = $1`, vic.ID); err != nil {
		t.Fatal(err)
	}
	if r := a.At(t, "GET", rt.SiteDomain, "/v1/me/export.zip", nil, vic.Header()); r.Status != 403 {
		t.Errorf("suspended export: %d %s", r.Status, r.Body)
	}
}

// Erasing an account retires its handle, but a retired-name row another
// account holds under the same name (possible with namespace checks off)
// stays that account's.
func TestEraseLeavesOthersAlias(t *testing.T) {
	a := NewApp(t)
	p, other := a.NewPerson(t, "leaver"), a.NewPerson(t, "keeper")
	if _, err := a.Database.Exec(`INSERT INTO handle_aliases (handle, user_id) VALUES ($1, $2)`, p.Handle, other.ID); err != nil {
		t.Fatal(err)
	}
	if r := a.At(t, "DELETE", rt.SiteDomain, "/v1/me", map[string]string{"confirm": p.Handle}, p.Header()); r.Status != http.StatusNoContent {
		t.Fatalf("delete: %d %s", r.Status, r.Body)
	}
	var holder sql.NullString
	if err := a.Database.QueryRow(`SELECT user_id FROM handle_aliases WHERE handle = $1`, p.Handle).Scan(&holder); err != nil || holder.String != other.ID {
		t.Errorf("another account's alias changed: %v %v", holder, err)
	}
}

func TestDeleteMyAccount(t *testing.T) {
	a := NewApp(t)
	olive, vic, other := a.NewPerson(t, "olive"), a.NewPerson(t, "vic"), a.NewPerson(t, "other")
	shopID, blogID, boardID := a.Deploy(t, olive, "shop"), a.Deploy(t, olive, "blog"), a.Deploy(t, vic, "board")
	a.Deploy(t, other, "spare")
	oid, firstHandle := olive.ID, olive.Handle
	const apex = rt.SiteDomain
	okey := olive.Header()

	// A handle change leaves the old handle as an alias.
	handle := firstHandle + "-x"
	if r := a.At(t, "PATCH", apex, "/v1/me", map[string]string{"handle": handle}, okey); r.Status != 200 {
		t.Fatalf("rename: %d %s", r.Status, r.Body)
	}
	a.acctSoftDelete(t, oid, "blog")
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
	mustExec(`INSERT INTO collection_items (site_id, collection, data, submitted_by) VALUES ($1, 'rsvps', '{"who":"vic"}', $2)`, boardID, vic.ID)
	a.Session(t, olive, boardID, "board."+rt.SiteDomain)
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
	if r := a.At(t, "DELETE", apex, "/v1/me", nil, okey); r.Status != 400 || r.Code() != "confirm_required" {
		t.Errorf("no body: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "DELETE", apex, "/v1/me", map[string]string{"confirm": firstHandle}, okey); r.Status != 400 || r.Code() != "confirm_mismatch" {
		t.Errorf("old handle as confirm: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "DELETE", apex, "/v1/me", map[string]string{"confirm": handle}, map[string]string{"X-API-Key": a.Admin}); r.Status != 403 || r.Code() != "admin_account" {
		t.Errorf("admin: %d %s", r.Status, r.Body)
	}
	// A connected app acting for her (an internal key) cannot delete the account.
	ik, revoke, err := accountsrepo.IssueInternalKey(oid)
	if err != nil {
		t.Fatal(err)
	}
	if r := a.At(t, "DELETE", apex, "/v1/me", map[string]string{"confirm": handle}, map[string]string{"X-API-Key": ik}); r.Status != 400 || r.Code() != "not_an_account_key" {
		t.Errorf("connected app: %d %s", r.Status, r.Body)
	}
	revoke()
	mustExec(`INSERT INTO event_domains (name, domain, user_id, ip, expires_at) VALUES ($1, 'event.test', $2, '192.0.2.1', now() + interval '1 day')`, "ev-"+handle, oid)
	if r := a.At(t, "DELETE", apex, "/v1/me", map[string]string{"confirm": handle}, okey); r.Status != 409 || r.Code() != "event_hostnames" {
		t.Errorf("event hostnames: %d %s", r.Status, r.Body)
	}
	mustExec(`DELETE FROM event_domains WHERE user_id = $1`, oid)
	admin := map[string]string{"X-API-Key": a.Admin}
	if r := a.At(t, "POST", apex, "/v1/admin/users/"+oid+"/suspend", map[string]string{"reason": "check"}, admin); r.Status != 200 {
		t.Fatalf("suspend: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "DELETE", apex, "/v1/me", map[string]string{"confirm": handle}, okey); r.Status != 403 || r.Code() != "account_suspended" {
		t.Errorf("suspended: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "POST", apex, "/v1/admin/users/"+oid+"/enable", nil, admin); r.Status != 200 {
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
	data := a.Disk.DataDir()
	for _, p := range []string{
		filepath.Join(data, "by-id", oid),
		filepath.Join(data, "deleted", oid),
		filepath.Join(data, "handles", handle),
		filepath.Join(data, "handles", firstHandle),
	} {
		if _, err := os.Lstat(p); !os.IsNotExist(err) {
			t.Errorf("%s left behind: %v", p, err)
		}
	}
	// Names stay retired: nobody else takes the handle or the old handle.
	okey2 := other.Header()
	for _, h := range []string{handle, firstHandle} {
		if r := a.At(t, "PATCH", apex, "/v1/me", map[string]string{"handle": h}, okey2); r.Status != http.StatusConflict {
			t.Errorf("someone else took the retired handle %s: %d %s", h, r.Status, r.Body)
		}
	}
}

// An account without a handle confirms with its email.
func TestDeleteMyAccountWithoutHandle(t *testing.T) {
	a := NewApp(t)
	p := a.NewPerson(t, "nohandle")
	if _, err := a.Database.Exec(`UPDATE users SET handle = NULL WHERE id = $1`, p.ID); err != nil {
		t.Fatal(err)
	}
	key := p.Header()
	r := a.At(t, "GET", rt.SiteDomain, "/v1/me/export.zip", nil, key)
	if r.Status != 200 || !strings.Contains(acctFileUnder(t, acctZipFiles(t, r.Body), "/account.json"), p.Email) {
		t.Fatalf("export without a handle: %d", r.Status)
	}
	if r := a.At(t, "DELETE", rt.SiteDomain, "/v1/me", map[string]string{"confirm": "nohandle"}, key); r.Status != 400 || r.Code() != "confirm_mismatch" {
		t.Errorf("wrong confirm: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "DELETE", rt.SiteDomain, "/v1/me", map[string]string{"confirm": " " + p.Email + " "}, key); r.Status != http.StatusNoContent {
		t.Fatalf("delete with email: %d %s", r.Status, r.Body)
	}
	var n int
	if err := a.Database.QueryRow(`SELECT count(*) FROM users WHERE id = $1`, p.ID).Scan(&n); err != nil || n != 0 {
		t.Errorf("account row left: %d %v", n, err)
	}
}

// Erasing an account lets go of the earlier address a site still answers at
// (previous_domain) too, unbinds and withdraws a domain only while it still
// points at this account's site, and removes take-down markers with the
// folders. The person cannot delete an account holding a taken-down site; the
// operator can delete a suspended one.
func TestEraseAccountDomainsAndTakeDowns(t *testing.T) {
	a := NewApp(t)
	certs := t.TempDir()
	a.Hosts.SetDomainCertDir(certs)
	olive, other := a.NewPerson(t, "olive"), a.NewPerson(t, "other")
	shopID, blogID, spareID := a.Deploy(t, olive, "shop"), a.Deploy(t, olive, "blog"), a.Deploy(t, other, "spare")
	oid, xid := olive.ID, other.ID
	const apex = rt.SiteDomain
	okey := olive.Header()
	admin := map[string]string{"X-API-Key": a.Admin}
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
	data := a.Disk.DataDir()
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
	if !a.Disk.IsSuspended(oid, "shop") {
		t.Fatal("no take-down marker")
	}
	if r := a.At(t, "DELETE", apex, "/v1/me", map[string]string{"confirm": olive.Handle}, okey); r.Status != 403 || r.Code() != "site_suspended" {
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
