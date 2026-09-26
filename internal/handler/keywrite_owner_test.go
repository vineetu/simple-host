package handler

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

// A key (or a connector token, or the MCP server acting for a person) writes a
// site's saved data only when its account owns the site, or is the platform
// admin. Any other account's key gets exactly the 404 of a missing site and
// changes nothing. Signed-in visitors on the site's own address are
// unaffected. Needs DB_DSN (db/schema.sql applied).
func TestKeyWritesNeedSiteOwner(t *testing.T) {
	a := newPersonApp(t, "serve")
	olive, oscar, boss, vic := a.newPerson(t, "olive"), a.newPerson(t, "oscar"), a.newPerson(t, "boss"), a.newPerson(t, "vic")
	// Oscar's "shop" is older: a bare-name lookup on the apex would pick it.
	a.deploy(t, oscar, "shop")
	a.deploy(t, olive, "shop")
	a.deploy(t, olive, "solo")
	_, oh := a.userID(t, olive)
	bossID, _ := a.userID(t, boss)
	if _, err := a.database.Exec(`UPDATE users SET is_admin = true WHERE id = $1`, bossID); err != nil {
		t.Fatal(err)
	}
	const apex = pcSiteDomain
	// Agents send the shared content host as Origin (the state routes are Origin-gated).
	agentOrigin := "https://" + pcContentHost
	key := func(k string) map[string]string { return map[string]string{"X-API-Key": k, "Origin": agentOrigin} }
	bearer := func(tok string) map[string]string {
		return map[string]string{"Authorization": "Bearer " + tok, "Origin": agentOrigin}
	}

	stateOf := func(site string, p person) string {
		t.Helper()
		var s []byte
		if err := a.database.QueryRow(`SELECT state FROM sites WHERE id = $1`, a.siteID(t, p, site)).Scan(&s); err != nil {
			t.Fatal(err)
		}
		return string(s)
	}
	countOf := func(site string, p person) int {
		t.Helper()
		var n int
		if err := a.database.QueryRow(`SELECT count(*) FROM collection_items WHERE site_id = $1`, a.siteID(t, p, site)).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	type write struct {
		label, method, path string
		body                any
		ok                  int
	}
	writes := func(site string) []write {
		var out []write
		for _, base := range []string{"/v1/sites/" + site, "/v1/u/" + oh + "/sites/" + site} {
			out = append(out,
				write{"PUT state " + base, "PUT", base + "/state", map[string]any{"who": "x"}, http.StatusOK},
				write{"PATCH state " + base, "PATCH", base + "/state", map[string]any{"ops": []any{map[string]any{"op": "inc", "path": "n", "by": 1}}}, http.StatusOK},
				write{"POST collection " + base, "POST", base + "/collections/guestbook", map[string]any{"msg": "hi"}, http.StatusCreated},
			)
		}
		return out
	}

	// ---- another account's key and connector token: 404, nothing written ----
	// The same answer the write routes give a site that does not resolve.
	missing := resp{body: []byte(`{"error":"site not found"}` + "\n")}
	oscarWhole := a.connectResource(t, oscar, a.srv.URL)
	beforeState, beforeCount := stateOf("solo", olive), countOf("solo", olive)
	for _, cred := range []struct {
		label string
		h     map[string]string
	}{{"other key", key(oscar.key)}, {"other connector bearer", bearer(oscarWhole)}} {
		for _, w := range writes("solo") {
			r := a.at(t, w.method, apex, w.path, w.body, cred.h)
			if r.status != http.StatusNotFound || !bytes.Equal(bytes.TrimSpace(r.body), bytes.TrimSpace(missing.body)) {
				t.Errorf("%s %s: %d %s, want the missing-site 404", cred.label, w.label, r.status, r.body)
			}
		}
	}
	if stateOf("solo", olive) != beforeState || countOf("solo", olive) != beforeCount {
		t.Fatalf("refused writes changed data: %s", stateOf("solo", olive))
	}

	// ---- the MCP server acting for another account: refused ----
	oscarMCP := a.connect(t, oscar, a.registerClient(t, testRedirect), testRedirect)["access_token"].(string)
	// oscar's own "shop" is his; "solo" is olive's and oscar has none.
	if _, _, isErr := toolResultOf(t, a.rpc(t, oscarMCP, "tools/call", map[string]any{"name": "update_state", "arguments": map[string]any{
		"site": "solo", "ops": []any{map[string]any{"op": "set", "path": "pwned", "value": true}},
	}})); !isErr {
		t.Errorf("MCP update_state on another account's site succeeded")
	}
	if _, _, isErr := toolResultOf(t, a.rpc(t, oscarMCP, "tools/call", map[string]any{"name": "add_to_collection", "arguments": map[string]any{
		"site": "solo", "collection": "guestbook", "item": map[string]any{"msg": "pwned"},
	}})); !isErr {
		t.Errorf("MCP add_to_collection on another account's site succeeded")
	}
	if strings.Contains(stateOf("solo", olive), "pwned") || countOf("solo", olive) != beforeCount {
		t.Fatalf("MCP refused writes changed data")
	}

	// ---- owner key, owner connector token, admin key, is_admin account: allowed ----
	oliveWhole := a.connectResource(t, olive, a.srv.URL)
	for _, cred := range []struct {
		label string
		h     map[string]string
	}{
		{"owner key", key(olive.key)},
		{"owner connector bearer", bearer(oliveWhole)},
		{"admin key", key(a.admin)},
		{"is_admin account", key(boss.key)},
	} {
		for _, w := range writes("solo") {
			if r := a.at(t, w.method, apex, w.path, w.body, cred.h); r.status != w.ok {
				t.Errorf("%s %s: %d %s, want %d", cred.label, w.label, r.status, r.body, w.ok)
			}
		}
	}
	oliveMCP := a.connect(t, olive, a.registerClient(t, testRedirect), testRedirect)["access_token"].(string)
	if text, _, isErr := toolResultOf(t, a.rpc(t, oliveMCP, "tools/call", map[string]any{"name": "update_state", "arguments": map[string]any{
		"site": "solo", "ops": []any{map[string]any{"op": "set", "path": "mcp", "value": true}},
	}})); isErr {
		t.Errorf("owner MCP update_state: %s", text)
	}

	// ---- a same-named older site: the owner's key writes the owner's site ----
	oscarShop := stateOf("shop", oscar)
	if r := a.at(t, "PATCH", apex, "/v1/sites/shop/state", map[string]any{"ops": []any{map[string]any{"op": "set", "path": "mine", "value": "olive"}}}, key(olive.key)); r.status != http.StatusOK {
		t.Fatalf("owner key, same-named site: %d %s", r.status, r.body)
	}
	var st map[string]any
	_ = json.Unmarshal([]byte(stateOf("shop", olive)), &st)
	if st["mine"] != "olive" || stateOf("shop", oscar) != oscarShop {
		t.Fatalf("same-named write went to the wrong site: olive=%s oscar=%s", stateOf("shop", olive), stateOf("shop", oscar))
	}
	// ...and oscar's key on the bare name still writes oscar's own shop.
	if r := a.at(t, "PATCH", apex, "/v1/sites/shop/state", map[string]any{"ops": []any{map[string]any{"op": "set", "path": "mine", "value": "oscar"}}}, key(oscar.key)); r.status != http.StatusOK {
		t.Fatalf("oscar key, own shop: %d %s", r.status, r.body)
	}
	if !strings.Contains(stateOf("shop", oscar), "oscar") || strings.Contains(stateOf("shop", olive), `"oscar"`) {
		t.Fatalf("oscar's write: olive=%s oscar=%s", stateOf("shop", olive), stateOf("shop", oscar))
	}
	// Oscar naming olive's shop by handle: refused.
	if r := a.at(t, "PATCH", apex, "/v1/u/"+oh+"/sites/shop/state", map[string]any{"ops": []any{}}, key(oscar.key)); r.status != http.StatusNotFound {
		t.Fatalf("other key via handle: %d %s", r.status, r.body)
	}

	// ---- a signed-in visitor on the site's own address: unchanged ----
	host := oh + "." + pcSiteDomain
	cookie := a.session(t, vic, a.siteID(t, olive, "solo"), host)
	if r := a.at(t, "PATCH", host, "/v1/sites/solo/state", map[string]any{"ops": []any{map[string]any{"op": "inc", "path": "v", "by": 1}}}, browser(host, cookie)); r.status != http.StatusOK {
		t.Fatalf("visitor save: %d %s", r.status, r.body)
	}
	if r := a.at(t, "POST", host, "/v1/sites/solo/collections/guestbook", map[string]any{"msg": "visitor"}, browser(host, cookie)); r.status != http.StatusCreated {
		t.Fatalf("visitor append: %d %s", r.status, r.body)
	}

	// ---- reads stay open ----
	if r := a.at(t, "GET", apex, "/v1/sites/solo/state", nil, key(oscar.key)); r.status != http.StatusOK {
		t.Fatalf("read with another key: %d %s", r.status, r.body)
	}
}

// A site's export carries that site's own saved state, never a same-named site
// of another account (the export used to look state up by bare name).
func TestExportStateIsTheOwnersSite(t *testing.T) {
	a := newPrivateApp(t)
	olive, oscar := a.newPerson(t, "olive"), a.newPerson(t, "oscar")
	a.deploy(t, oscar, "shop") // older: a bare-name lookup picks it
	a.deploy(t, olive, "shop")
	if _, err := a.database.Exec(`UPDATE sites SET state = '{"secret":"oscar-only"}' WHERE id = $1`, a.siteID(t, oscar, "shop")); err != nil {
		t.Fatal(err)
	}
	if _, err := a.database.Exec(`UPDATE sites SET state = '{"mine":"olive"}' WHERE id = $1`, a.siteID(t, olive, "shop")); err != nil {
		t.Fatal(err)
	}
	r := a.at(t, "GET", pcSiteDomain, "/v1/sites/shop/export.tar.gz", nil, map[string]string{"X-API-Key": olive.key})
	if r.status != http.StatusOK {
		t.Fatalf("export: %d %s", r.status, r.body)
	}
	gz, err := gzip.NewReader(bytes.NewReader(r.body))
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)
	var state string
	for {
		hdr, err := tr.Next()
		if err != nil {
			break
		}
		if hdr.Name == "shop/state.json" {
			b, _ := io.ReadAll(tr)
			state = string(b)
		}
	}
	if !strings.Contains(state, "olive") || strings.Contains(state, "oscar-only") {
		t.Fatalf("export state = %q, want olive's own", state)
	}
}

// The admin's allow-anonymous-writes toggle with ?owner= sets that person's
// site, not the oldest of the name.
func TestAllowAnonymousWritesOwnerParam(t *testing.T) {
	a := newPrivateApp(t)
	olive, oscar := a.newPerson(t, "olive"), a.newPerson(t, "oscar")
	a.deploy(t, oscar, "shop")
	a.deploy(t, olive, "shop")
	_, oh := a.userID(t, olive)
	adm := map[string]string{"X-API-Key": a.admin}
	if r := a.at(t, "PUT", pcSiteDomain, "/v1/sites/shop/allow-anonymous-writes?owner="+oh, map[string]bool{"allow": true}, adm); r.status != 200 {
		t.Fatalf("toggle: %d %s", r.status, r.body)
	}
	flag := func(p person) (b bool) {
		if err := a.database.QueryRow(`SELECT allow_anonymous_writes FROM sites WHERE id = $1`, a.siteID(t, p, "shop")).Scan(&b); err != nil {
			t.Fatal(err)
		}
		return
	}
	if !flag(olive) || flag(oscar) {
		t.Fatalf("owner param: olive=%t oscar=%t", flag(olive), flag(oscar))
	}
	if r := a.at(t, "PUT", pcSiteDomain, "/v1/sites/shop/allow-anonymous-writes?owner=no-such-handle-xyz", map[string]bool{"allow": true}, adm); r.status != 404 {
		t.Fatalf("unknown owner: %d %s", r.status, r.body)
	}
	if r := a.at(t, "PUT", pcSiteDomain, "/v1/sites/shop/allow-anonymous-writes?owner="+oh, map[string]bool{"allow": true}, map[string]string{"X-API-Key": oscar.key}); r.status == 200 {
		t.Fatalf("non-admin toggle: %d", r.status)
	}
}
