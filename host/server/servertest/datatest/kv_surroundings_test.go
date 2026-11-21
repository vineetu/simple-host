package datatest

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	appstorage "github.com/vsriram/simple-host/host/data"
	runtime "github.com/vsriram/simple-host/host/storage"
)

func kvRoot(s Shop) string { return filepath.Join(s.A.Disk.SiteDir(s.OwnerID, "shop"), "runtime") }

// A key-value resource is not a SQLite database to the SQL routes.
func TestKVRefusesSQL(t *testing.T) {
	s := NewShop(t)
	s.Must(t, "PUT", "/resources/z", map[string]string{"kind": "kv"}, s.Key, 201)
	for _, mode := range []string{"query", "execute", "schema"} {
		s.Must(t, "POST", "/sqlite/z/"+mode, `{"sql":"SELECT 1"}`, s.Key, 409)
	}
}

// The people pick list and the visitor lookup see key-value authors, and
// forget them when the resource goes.
func TestKVPeopleAndVisitorEmails(t *testing.T) {
	s := NewShop(t)
	s.Must(t, "PUT", "/resources/entries", StorageRules("kv", RulesPersonal), s.Key, 201)
	s.Must(t, "PUT", "/kv/entries/keys/mine", `{"value":"hello"}`, s.Callers["alice"], 200)
	people := string(s.Must(t, "GET", "/people", nil, s.Key, 200).Body)
	if !strings.Contains(people, s.AliceID) || !strings.Contains(people, `"has_data":true`) || strings.Contains(people, s.Ids["bob"]) {
		t.Fatalf("people: %s", people)
	}
	if r := s.Must(t, "GET", "/visitors?id="+s.AliceID, nil, s.Key, 200); !strings.Contains(string(r.Body), `"found":true`) {
		t.Fatalf("author email unresolved: %s", r.Body)
	}
	s.Must(t, "GET", "/visitors?id="+s.AliceID, nil, s.Callers["alice"], 403)
	s.Must(t, "DELETE", "/resources/entries", nil, s.Key, 200)
	if r := s.Must(t, "GET", "/people", nil, s.Key, 200); strings.Contains(string(r.Body), s.AliceID) {
		t.Fatalf("deleted author retained: %s", r.Body)
	}
}

func TestKVInvalidUTF8KeyRefused(t *testing.T) {
	s := NewShop(t)
	s.Must(t, "PUT", "/resources/entries", map[string]string{"kind": "kv"}, s.Key, 201)
	s.Must(t, "PUT", "/kv/entries/keys/%FF", `{"value":1}`, s.Key, 500)
	if r := s.Must(t, "GET", "/kv/entries/keys", nil, s.Key, 200); strings.Contains(string(r.Body), `"value":1`) {
		t.Fatal("invalid key stored")
	}
}

// A key-value file gone from disk is a clear failure, and usage still answers.
func TestKVFileMissing(t *testing.T) {
	s := NewShop(t)
	s.Must(t, "PUT", "/resources/entries", map[string]string{"kind": "kv"}, s.Key, 201)
	if err := os.Remove(runtime.RuntimeKVPath(kvRoot(s), "entries")); err != nil {
		t.Fatal(err)
	}
	if r := s.Must(t, "GET", "/kv/entries/keys/one", nil, s.Key, 500); r.Code() != "storage_failed" {
		t.Fatalf("missing file: %s", r.Body)
	}
	s.Must(t, "GET", "/usage", nil, s.Key, 200)
}

// Key-value bytes still count for a site in the trash.
func TestKVFileStatsLiveAndTrashed(t *testing.T) {
	s := NewShop(t)
	s.Must(t, "PUT", "/resources/entries", map[string]string{"kind": "kv"}, s.Key, 201)
	s.Must(t, "PUT", "/kv/entries/keys/long-key", `{"value":"é"}`, s.Key, 200)
	check := func() {
		t.Helper()
		if n, b, err := s.A.Data.KVFileStats(context.Background(), s.Sid); err != nil || n != 1 || b != 4 {
			t.Fatalf("file stats %d/%d: %v", n, b, err)
		}
	}
	check()
	if err := s.A.Disk.TrashSite(s.OwnerID, "shop", s.Sid); err != nil {
		t.Fatal(err)
	}
	if _, err := s.A.Database.Exec(`UPDATE sites SET deleted_at=now() WHERE id=$1`, s.Sid); err != nil {
		t.Fatal(err)
	}
	check()
}

func TestKVUsageBusy(t *testing.T) {
	s := NewShop(t)
	s.Must(t, "PUT", "/resources/entries", map[string]string{"kind": "kv"}, s.Key, 201)
	h := s.A.Data
	h.SqliteSites = appstorage.NewSiteSlots(1, appstorage.NewSQLiteGate(1, time.Second), 20*time.Millisecond)
	entered, release := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- h.SqliteSites.Admit(context.Background(), s.Sid, func(context.Context, *appstorage.Admission) error { close(entered); <-release; return nil })
	}()
	<-entered
	r := s.Do(t, "GET", "/usage", nil, s.Key)
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if r.Status != 503 || r.Code() != "storage_busy" || r.Header.Get("Retry-After") == "" {
		t.Fatalf("busy usage: %d %s", r.Status, r.Body)
	}
}

// While the site's key-value file is locked, SQLite writes cannot measure
// the quota and are refused as busy (retryable), never let through.
func TestKVQuotaBusySQLiteWrites(t *testing.T) {
	s := NewShop(t)
	s.Must(t, "PUT", "/resources/sqlite", map[string]string{"kind": "kv"}, s.Key, 201)
	s.Must(t, "PUT", "/kv/sqlite/keys/one", `{"value":1}`, s.Key, 200)
	s.Must(t, "PUT", "/resources/db", map[string]string{"kind": "sqlite"}, s.Key, 201)
	s.Must(t, "POST", "/sqlite/db/schema", `{"sql":"CREATE TABLE items(id INTEGER PRIMARY KEY, v TEXT)"}`, s.Key, 200)
	s.Must(t, "POST", "/sqlite/db/execute", `{"sql":"INSERT INTO items VALUES(1, 'original')"}`, s.Key, 200)
	h := s.A.Data
	h.SqliteSites = appstorage.NewSiteSlots(4, appstorage.NewSQLiteGate(4, time.Second), time.Second)
	err := h.SqliteSites.Admit(context.Background(), s.Sid, func(ctx context.Context, _ *appstorage.Admission) error {
		conn, done, err := runtime.OpenRuntimeKV(ctx, runtime.RuntimeKVPath(kvRoot(s), "sqlite"), true, false)
		if err != nil {
			return err
		}
		defer done()
		if err := conn.Exec("PRAGMA locking_mode=EXCLUSIVE; BEGIN EXCLUSIVE"); err != nil {
			return err
		}
		defer conn.Exec("ROLLBACK")
		for _, tc := range []struct{ method, path, body string }{
			{"POST", "/sqlite/db/tables/items/rows", `{"v":"added"}`},
			{"POST", "/sqlite/db/execute", `{"sql":"INSERT INTO items VALUES(2, 'execute')"}`},
		} {
			if r := s.Do(t, tc.method, tc.path, tc.body, s.Key); r.Status != 503 || r.Code() != "storage_busy" || r.Header.Get("Retry-After") == "" {
				t.Errorf("%s %s: %d %s", tc.method, tc.path, r.Status, r.Body)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	s.Must(t, "POST", "/sqlite/db/execute", `{"sql":"INSERT INTO items VALUES(2, 'retry')"}`, s.Key, 200)
}
