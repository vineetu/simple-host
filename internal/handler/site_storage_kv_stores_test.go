package handler

import (
	"context"
	"errors"
	sqlite3 "github.com/ncruces/go-sqlite3"
	"strings"
	"testing"

	runtime "github.com/vsriram/simple-host/internal/storage"
)

func storageKVFixtureCall(t *testing.T, h *SiteHandler, siteID, resource string) storageCall {
	t.Helper()
	c := storageCall{siteID: siteID, resourceName: resource}
	if err := h.database.QueryRow(`SELECT user_id,name FROM sites WHERE id=$1`, siteID).Scan(&c.ownerID, &c.siteName); err != nil {
		t.Fatal(err)
	}
	var err error
	c.resource, err = h.loadStorageResource(context.Background(), siteID, resource)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func seedStorageKV(t *testing.T, h *SiteHandler, siteID, resource, key, value, writer string) {
	t.Helper()
	c := storageKVFixtureCall(t, h, siteID, resource)
	if c.resource.KVStore != "sqlite" {
		if _, err := h.database.Exec(`INSERT INTO site_storage_kv(site_id,resource_name,key,value,writer_id) VALUES($1,$2,$3,$4,$5)`, siteID, resource, key, value, writer); err != nil {
			t.Fatal(err)
		}
		return
	}
	conn, done, err := runtime.OpenRuntimeKV(context.Background(), runtime.RuntimeKVPath(h.storageRuntimeDir(c), resource), true, false)
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	if err = conn.Exec("BEGIN"); err != nil {
		t.Fatal(err)
	}
	if err = runtime.RuntimeKVPut(conn, key, value, writer, false, 0); err != nil {
		t.Fatal(err)
	}
	if err = conn.Exec("COMMIT; PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		t.Fatal(err)
	}
}

func readStorageKVAuthor(t *testing.T, h *SiteHandler, siteID, resource, key string) string {
	t.Helper()
	c := storageKVFixtureCall(t, h, siteID, resource)
	if c.resource.KVStore != "sqlite" {
		var writer string
		if err := h.database.QueryRow(`SELECT writer_id FROM site_storage_kv WHERE site_id=$1 AND resource_name=$2 AND key=$3`, siteID, resource, key).Scan(&writer); err != nil {
			t.Fatal(err)
		}
		return writer
	}
	conn, done, err := runtime.OpenRuntimeKV(context.Background(), runtime.RuntimeKVPath(h.storageRuntimeDir(c), resource), false, false)
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	item, err := runtime.RuntimeKVGet(conn, key, "")
	if err != nil {
		t.Fatal(err)
	}
	return item.VisitorID
}

func TestKVMarkerSurvivesHandlerCopies(t *testing.T) {
	for _, store := range []string{"postgres", "sqlite"} {
		r := storageResource{Name: "kv", Kind: "kv", KVStore: store}
		round := fromRulesResource(r.rulesResource())
		if round.KVStore != store || round.fingerprint() != r.fingerprint() {
			t.Fatalf("marker lost: %+v", round)
		}
	}
}

func insertStorageKVRace(t *testing.T, h *SiteHandler, c storageCall, key, value, writer string) (bool, error) {
	if c.resource.KVStore != "sqlite" {
		return h.storageSvc().InsertKV(context.Background(), c.siteID, c.resourceName, key, []byte(value), false, writer)
	}
	conn, done, err := runtime.OpenRuntimeKV(context.Background(), runtime.RuntimeKVPath(h.storageRuntimeDir(c), c.resourceName), true, false)
	if err != nil {
		return false, err
	}
	defer done()
	if err = conn.Exec("BEGIN IMMEDIATE"); err != nil {
		return false, err
	}
	defer conn.Exec("ROLLBACK")
	if err = runtime.RuntimeKVPut(conn, key, value, writer, false, 0); err != nil {
		if errors.Is(err, sqlite3.CONSTRAINT) {
			return false, nil
		}
		return false, err
	}
	return true, conn.Exec("COMMIT")
}

func readStorageKVValue(t *testing.T, h *SiteHandler, c storageCall, key string) string {
	t.Helper()
	if c.resource.KVStore != "sqlite" {
		var value string
		if err := h.database.QueryRow(`SELECT value::text FROM site_storage_kv WHERE site_id=$1 AND resource_name=$2 AND key=$3`, c.siteID, c.resourceName, key).Scan(&value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	conn, done, err := runtime.OpenRuntimeKV(context.Background(), runtime.RuntimeKVPath(h.storageRuntimeDir(c), c.resourceName), false, false)
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	item, err := runtime.RuntimeKVGet(conn, key, "")
	if err != nil {
		t.Fatal(err)
	}
	return string(item.Value)
}

// R2-S4a requires the existing legacy-to-preset expectations on both stores,
// and clears legacy writers from a SQLite namespace as the Postgres path does.
// This is the SQLite counterpart of TestStoragePresetsReviewRound2's final case.
func TestKVLegacyPresetConversionOnSQLite(t *testing.T) {
	t.Setenv("KV_STORE", "sqlite")
	s := newPresetSite(t)
	s.must(t, "PUT", "/resources/oldkv", map[string]string{"kind": "kv", "read": "anyone", "write": "signed-in", "write_mode": "full"}, s.key, 201)
	if _, err := s.a.database.Exec(`UPDATE site_storage_resources SET acc_read=NULL,acc_add=NULL,acc_edit=NULL,acc_delete=NULL,preset_base='' WHERE site_id=$1 AND name='oldkv'`, s.sid); err != nil {
		t.Fatal(err)
	}
	c := storageKVFixtureCall(t, s.a.sites, s.sid, "oldkv")
	if c.resource.KVStore != "sqlite" {
		t.Fatal("fixture must actually be on SQLite")
	}
	bob := s.callers["bob"]
	s.must(t, "PUT", "/kv/oldkv/keys/profile", `{"value":"bob wrote"}`, bob, 200)
	s.must(t, "PUT", "/resources/oldkv", map[string]string{"kind": "kv", "preset": "personal"}, s.key, 200)
	s.must(t, "GET", "/kv/oldkv/keys/profile", nil, bob, 404)
	if got := s.must(t, "GET", "/kv/oldkv/keys/profile", nil, s.key, 200).json(t); got["visitor_id"] != nil {
		t.Fatalf("writer kept: %v", got)
	}
}

// legacyKVOnSQLite makes a legacy full-mode namespace kept in a file, with one
// entry bob wrote before any preset existed.
func legacyKVOnSQLite(t *testing.T, name string) (presetSite, storageCall) {
	t.Helper()
	t.Setenv("KV_STORE", "sqlite")
	s := newPresetSite(t)
	s.must(t, "PUT", "/resources/"+name, map[string]string{"kind": "kv", "read": "anyone", "write": "signed-in", "write_mode": "full"}, s.key, 201)
	if _, err := s.a.database.Exec(`UPDATE site_storage_resources SET acc_read=NULL,acc_add=NULL,acc_edit=NULL,acc_delete=NULL,preset_base='' WHERE site_id=$1 AND name=$2`, s.sid, name); err != nil {
		t.Fatal(err)
	}
	c := storageKVFixtureCall(t, s.a.sites, s.sid, name)
	if c.resource.KVStore != "sqlite" {
		t.Fatal("fixture must actually be on SQLite")
	}
	s.must(t, "PUT", "/kv/"+name+"/keys/profile", `{"value":"bob wrote"}`, s.callers["bob"], 200)
	if got := readStorageKVAuthor(t, s.a.sites, s.sid, name, "profile"); got == "" {
		t.Fatal("fixture must record bob as the writer")
	}
	return s, c
}

func kvResourceIsLegacy(t *testing.T, s presetSite, name string) bool {
	t.Helper()
	var legacy bool
	if err := s.a.database.QueryRow(`SELECT acc_read IS NULL FROM site_storage_resources WHERE site_id=$1 AND name=$2`, s.sid, name).Scan(&legacy); err != nil {
		t.Fatal(err)
	}
	return legacy
}

// If the file cannot be changed, the preset is not saved and nothing is cleared.
func TestKVLegacyConversionFileFailureSavesNothing(t *testing.T) {
	s, c := legacyKVOnSQLite(t, "busykv")
	// Another connection holds the file's write lock for longer than the
	// store waits, so the conversion's own transaction cannot start.
	conn, done, err := runtime.OpenRuntimeKV(context.Background(), runtime.RuntimeKVPath(s.a.sites.storageRuntimeDir(c), "busykv"), true, false)
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	if err = conn.Exec("BEGIN IMMEDIATE"); err != nil {
		t.Fatal(err)
	}
	r := s.do(t, "PUT", "/resources/busykv", map[string]string{"kind": "kv", "preset": "personal"}, s.key)
	if r.status < 500 {
		t.Fatalf("save with the file locked: want a failure, got %d %s", r.status, r.body)
	}
	if err = conn.Exec("ROLLBACK"); err != nil {
		t.Fatal(err)
	}
	if !kvResourceIsLegacy(t, s, "busykv") {
		t.Fatal("preset saved although the writers could not be cleared")
	}
	if got := readStorageKVAuthor(t, s.a.sites, s.sid, "busykv", "profile"); got == "" {
		t.Fatal("writer cleared although the save failed")
	}
	// The retry completes the conversion.
	s.must(t, "PUT", "/resources/busykv", map[string]string{"kind": "kv", "preset": "personal"}, s.key, 200)
	s.must(t, "GET", "/kv/busykv/keys/profile", nil, s.callers["bob"], 404)
}

// If Postgres refuses the commit after the file has committed, the writers
// stay cleared on a resource that is still legacy, and a retry completes it.
func TestKVLegacyConversionPostgresCommitFailure(t *testing.T) {
	s, _ := legacyKVOnSQLite(t, "commitkv")
	// A deferred constraint trigger fails exactly at COMMIT, for this row only.
	fn := "kv_commit_fail_" + strings.ReplaceAll(s.sid, "-", "_")
	if _, err := s.a.database.Exec(`CREATE FUNCTION ` + fn + `() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'commit refused for the test'; END $$`); err != nil {
		t.Fatal(err)
	}
	drop := func() {
		s.a.database.Exec(`DROP TRIGGER IF EXISTS ` + fn + ` ON site_storage_resources`)
		s.a.database.Exec(`DROP FUNCTION IF EXISTS ` + fn + `()`)
	}
	t.Cleanup(drop)
	if _, err := s.a.database.Exec(`CREATE CONSTRAINT TRIGGER ` + fn + ` AFTER UPDATE ON site_storage_resources DEFERRABLE INITIALLY DEFERRED FOR EACH ROW WHEN (NEW.site_id::text = '` + s.sid + `' AND NEW.name = 'commitkv') EXECUTE FUNCTION ` + fn + `()`); err != nil {
		t.Fatal(err)
	}
	s.must(t, "PUT", "/resources/commitkv", map[string]string{"kind": "kv", "preset": "personal"}, s.key, 500)
	if !kvResourceIsLegacy(t, s, "commitkv") {
		t.Fatal("preset saved although Postgres refused the commit")
	}
	if got := readStorageKVAuthor(t, s.a.sites, s.sid, "commitkv", "profile"); got != "" {
		t.Fatalf("writer %q kept after the file committed", got)
	}
	// Still legacy and readable by anyone, so bob still reads the value.
	s.must(t, "GET", "/kv/commitkv/keys/profile", nil, s.callers["bob"], 200)
	drop()
	s.must(t, "PUT", "/resources/commitkv", map[string]string{"kind": "kv", "preset": "personal"}, s.key, 200)
	s.must(t, "GET", "/kv/commitkv/keys/profile", nil, s.callers["bob"], 404)
}

// A namespace deleted and created again under the same name starts empty.
func TestKVSQLiteRecreatedNamespaceIsEmpty(t *testing.T) {
	t.Setenv("KV_STORE", "sqlite")
	s := newPresetSite(t)
	create := map[string]string{"kind": "kv", "preset": "personal"}
	s.must(t, "PUT", "/resources/again", create, s.key, 201)
	if storageKVFixtureCall(t, s.a.sites, s.sid, "again").resource.KVStore != "sqlite" {
		t.Fatal("fixture must actually be on SQLite")
	}
	bob := s.callers["bob"]
	s.must(t, "PUT", "/kv/again/keys/secret", `{"value":"bob only"}`, bob, 200)
	s.must(t, "DELETE", "/resources/again", nil, s.key, 200)
	s.must(t, "PUT", "/resources/again", create, s.key, 201)
	s.must(t, "GET", "/kv/again/keys/secret", nil, bob, 404)
	s.must(t, "GET", "/kv/again/keys/secret", nil, s.key, 404)
	if items := s.must(t, "GET", "/kv/again/keys", nil, s.key, 200).json(t)["items"].([]any); len(items) != 0 {
		t.Fatalf("old entries came back: %v", items)
	}
	c := storageKVFixtureCall(t, s.a.sites, s.sid, "again")
	conn, done, err := runtime.OpenRuntimeKV(context.Background(), runtime.RuntimeKVPath(s.a.sites.storageRuntimeDir(c), "again"), false, false)
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	if meta, err := runtime.RuntimeKVMeta(conn); err != nil || meta.Entries != 0 || meta.LogicalBytes != 0 {
		t.Fatalf("recreated namespace is charged: %+v %v", meta, err)
	}
}
