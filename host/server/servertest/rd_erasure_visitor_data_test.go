package servertest

import (
	"net/http"
	"strings"
	"testing"
)

// What account erasure does to what a person saved as a signed-in visitor on
// OTHER people's sites. This pins the behaviour as it is today, not as the
// deletion notice might be read:
//
//   - KV entries, SQLite rows and files the visitor wrote stay in the site
//     owner's storage, still carrying the visitor's id (kv/sqlite visitor_id,
//     files writer_id). They are not deleted and not rewritten.
//   - Only entries in Access-managed lists (collection_items.submitted_by)
//     are deleted; data_history.actor_id is set null by the account row's
//     delete and actor_email cleared (the HTTP storage routes write neither).
//
// The notice (host/access/accesshttp/account_data.go emailAccountDeleted)
// says: "Changes you made to other people's page data stay with their
// sites, with your address taken off them."
func TestRDErasureKeepsVisitorsStorageOnOtherSites(t *testing.T) {
	a, cert := NewSiteApp(t, "canonical")
	owner, vic := a.NewPerson(t, "rd-owner"), a.NewPerson(t, "rd-vic")
	a.Deploy(t, owner, "shop")
	_, ohandle := a.UserID(t, owner)
	vid, vhandle := a.UserID(t, vic)
	MarkReady(t, cert, ohandle)
	host := "shop." + ohandle + "." + PcSiteDomain
	sid := a.SiteID(t, owner, "shop")
	okey := map[string]string{"X-API-Key": owner.Key}
	vis := Browser(host, a.Session(t, vic, sid, host))
	do := func(method, path string, body any, h map[string]string) Resp {
		hostFor := host
		if h["X-API-Key"] != "" {
			hostFor = PcSiteDomain
		}
		return a.At(t, method, hostFor, "/v1/sites/shop/storage"+path, body, h)
	}
	must := func(method, path string, body any, h map[string]string, status int) Resp {
		t.Helper()
		r := do(method, path, body, h)
		if r.Status != status {
			t.Fatalf("%s %s: want %d got %d %s", method, path, status, r.Status, r.Body)
		}
		return r
	}
	must("PUT", "/resources/notes", StorageRules("kv", RulesSignedInAdd), okey, http.StatusCreated)
	must("PUT", "/resources/wall", StorageRules("sqlite", RulesWall), okey, http.StatusCreated)
	must("PUT", "/resources/assets", StorageRules("files", RulesSignedInAdd), okey, http.StatusCreated)
	must("POST", "/sqlite/wall/schema", map[string]any{"sql": "CREATE TABLE entries(id INTEGER PRIMARY KEY, text TEXT)"}, okey, http.StatusOK)
	must("PUT", "/kv/notes/keys/mine", map[string]any{"value": "kv-from-the-visitor"}, vis, http.StatusOK)
	must("POST", "/sqlite/wall/tables/entries/rows", map[string]any{"text": "row-from-the-visitor"}, vis, http.StatusOK)
	must("PUT", "/files/assets/objects/mine.txt", "file-from-the-visitor", Browser(host, a.Session(t, vic, sid, host), "Content-Type", "text/plain"), http.StatusOK)

	if got := ReadStorageKVAuthor(t, a.Data, sid, "notes", "mine"); got != vid {
		t.Fatalf("before erasure the kv entry's author is %q, want the visitor %q", got, vid)
	}
	if r := a.At(t, "DELETE", PcSiteDomain, "/v1/me", map[string]string{"confirm": vhandle}, map[string]string{"X-API-Key": vic.Key}); r.Status != http.StatusNoContent {
		t.Fatalf("visitor deletes their account: %d %s", r.Status, r.Body)
	}

	// KV: kept, still under the visitor's id.
	if r := must("GET", "/kv/notes/keys/mine", nil, okey, http.StatusOK); !strings.Contains(string(r.Body), "kv-from-the-visitor") {
		t.Errorf("kv entry after erasure: %s", r.Body)
	}
	if got := ReadStorageKVAuthor(t, a.Data, sid, "notes", "mine"); got != vid {
		t.Errorf("kv entry's author after erasure: %q, want unchanged %q (not anonymised)", got, vid)
	}
	// SQLite: the row is kept, still under the visitor's id.
	r := must("GET", "/sqlite/wall/tables/entries/rows", nil, okey, http.StatusOK)
	if !strings.Contains(string(r.Body), "row-from-the-visitor") {
		t.Errorf("sqlite row after erasure: %s", r.Body)
	}
	if !strings.Contains(string(r.Body), vid) {
		t.Errorf("sqlite row no longer carries the visitor's id %s (anonymised?): %s", vid, r.Body)
	}
	// Files: the file is kept.
	if r := must("GET", "/files/assets/objects/mine.txt", nil, okey, http.StatusOK); string(r.Body) != "file-from-the-visitor" {
		t.Errorf("file after erasure: %q", r.Body)
	}
	var writer string
	if err := a.Database.QueryRow(`SELECT writer_id FROM site_storage_files WHERE site_id = $1 AND path = 'mine.txt'`, sid).Scan(&writer); err != nil || writer != vid {
		t.Errorf("file writer after erasure: %q (%v), want unchanged %q", writer, err, vid)
	}
}
