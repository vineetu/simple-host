package servertest

import (
	"net/http"
	"strings"
	"testing"
)

// What account erasure does to what a person saved as a signed-in visitor on
// OTHER people's sites. The person's name, email and handle live only in
// the account row; everything else refers to them by account id. Erasure
// deletes the row, so:
//
//   - KV entries, SQLite rows and files the visitor wrote stay in the site
//     owner's storage with the old id (kv/sqlite visitor_id, files
//     writer_id), which no longer leads to anyone;
//   - the owner's who-wrote-this lookups answer with no name or email.
//
// The notice (host/access/accesshttp/account_data.go emailAccountDeleted)
// says: "What you saved on other people's sites stays there, no longer
// linked to your name or email."
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
		t.Errorf("kv entry's author after erasure: %q, want the old id %q", got, vid)
	}
	// SQLite: the row is kept, still under the visitor's id.
	r := must("GET", "/sqlite/wall/tables/entries/rows", nil, okey, http.StatusOK)
	if !strings.Contains(string(r.Body), "row-from-the-visitor") {
		t.Errorf("sqlite row after erasure: %s", r.Body)
	}
	if !strings.Contains(string(r.Body), vid) {
		t.Errorf("sqlite row no longer carries the old id %s: %s", vid, r.Body)
	}
	// Files: the file is kept.
	if r := must("GET", "/files/assets/objects/mine.txt", nil, okey, http.StatusOK); string(r.Body) != "file-from-the-visitor" {
		t.Errorf("file after erasure: %q", r.Body)
	}
	var writer string
	if err := a.Database.QueryRow(`SELECT writer_id FROM site_storage_files WHERE site_id = $1 AND path = 'mine.txt'`, sid).Scan(&writer); err != nil || writer != vid {
		t.Errorf("file writer after erasure: %q (%v), want unchanged %q", writer, err, vid)
	}
	// The account row is gone, and the owner's lookups name nobody.
	var rows int
	if err := a.Database.QueryRow(`SELECT count(*) FROM users WHERE id = $1`, vid).Scan(&rows); err != nil || rows != 0 {
		t.Errorf("account row after erasure: %d (%v)", rows, err)
	}
	for _, path := range []string{"/visitors?id=" + vid, "/people"} {
		r := must("GET", path, nil, okey, http.StatusOK)
		if strings.Contains(string(r.Body), vic.Email) || strings.Contains(string(r.Body), vhandle) || strings.Contains(string(r.Body), `"found":true`) {
			t.Errorf("%s names the erased visitor: %s", path, r.Body)
		}
	}
}
