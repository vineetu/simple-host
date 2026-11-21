package datatest

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	appstorage "github.com/vsriram/simple-host/host/data"
	"github.com/vsriram/simple-host/host/server/servertest"

	runtime "github.com/vsriram/simple-host/host/storage"
)

func kvArchive(t *testing.T, s servertest.StorageTestSite) map[string][]byte {
	t.Helper()
	r := s.A.At(t, "GET", servertest.PcSiteDomain, "/v1/sites/shop/export.tar.gz", nil, s.Key)
	if r.Status != 200 {
		t.Fatalf("export: %d %s", r.Status, r.Body)
	}
	gz, err := gzip.NewReader(bytes.NewReader(r.Body))
	if err != nil {
		t.Fatal(err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	out := map[string][]byte{}
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		b, err := io.ReadAll(tr)
		if err != nil {
			t.Fatal(err)
		}
		out[h.Name] = b
	}
	return out
}

func TestKVExport(t *testing.T) {
	s := servertest.NewStorageTestSite(t)
	for _, name := range []string{"a", "z"} {
		s.Must(t, "PUT", "/resources/"+name, map[string]string{"kind": "kv"}, s.Key, 201)
		for _, key := range []string{"a", "z", "é", "中", "A", "a%_"} {
			s.Must(t, "PUT", "/kv/"+name+"/keys/"+url.PathEscape(key), `{"value":{"text":"é","n":1.0e2}}`, s.Key, 200)
		}
		path := runtime.RuntimeKVPath(filepath.Join(s.A.Sites.Disk.SiteDir(s.OwnerID, "shop"), "runtime"), name)
		conn, done, err := runtime.OpenRuntimeKV(context.Background(), path, true, false)
		if err != nil {
			t.Fatal(err)
		}
		err = conn.Exec(`UPDATE kv SET visitor_id='fixture-author'`)
		done()
		if err != nil {
			t.Fatal(err)
		}
	}
	s.Must(t, "PUT", "/resources/uploads", `{"kind":"files"}`, s.Key, 201)
	s.Must(t, "PUT", "/files/uploads/objects/keep.txt", "keep", s.Key, 200)
	// Ignore arbitrary leftover files.
	root := filepath.Join(s.A.Sites.Disk.SiteDir(s.OwnerID, "shop"), "runtime")
	if err := os.MkdirAll(filepath.Join(root, "kv"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "kv", "orphan.sqlite"), []byte("orphan"), 0600); err != nil {
		t.Fatal(err)
	}
	files := kvArchive(t, s)
	got := files["shop/storage/kv.json"]
	if len(got) == 0 {
		t.Fatal("no kv.json")
	}
	if string(files["shop/storage/runtime/files/uploads/keep.txt"]) != "keep" {
		t.Fatal("file bucket changed")
	}
	for name := range files {
		if strings.Contains(name, "/runtime/kv/") {
			t.Fatalf("raw KV file exported: %s", name)
		}
	}
	for _, mode := range []string{"query", "execute", "schema"} {
		s.Must(t, "POST", "/sqlite/z/"+mode, `{"sql":"SELECT 1"}`, s.Key, 409)
	}
}

func TestKVSQLitePeopleAndVisitorEmails(t *testing.T) {
	s := servertest.NewStorageTestSite(t)
	s.Must(t, "PUT", "/resources/entries", servertest.StorageRules("kv", servertest.RulesPersonal), s.Key, 201)
	s.Must(t, "PUT", "/kv/entries/keys/mine", `{"value":"hello"}`, s.Callers["alice"], 200)
	people := s.Must(t, "GET", "/people", nil, s.Key, 200)
	if !strings.Contains(string(people.Body), s.AliceID) || !strings.Contains(string(people.Body), `"has_data":true`) {
		t.Fatalf("missing file author: %s", people.Body)
	}
	if strings.Contains(string(people.Body), s.Ids["bob"]) {
		t.Fatalf("sign-in alone listed as data: %s", people.Body)
	}
	emails := s.Must(t, "GET", "/visitors?id="+s.AliceID, nil, s.Key, 200)
	if !strings.Contains(string(emails.Body), `"found":true`) {
		t.Fatalf("author email unresolved: %s", emails.Body)
	}
	s.Must(t, "DELETE", "/resources/entries", nil, s.Key, 200)
	if r := s.Must(t, "GET", "/people", nil, s.Key, 200); strings.Contains(string(r.Body), s.AliceID) {
		t.Fatalf("deleted author retained: %s", r.Body)
	}
}

func TestKVInvalidUTF8HTTPParity(t *testing.T) {
	s := servertest.NewStorageTestSite(t)
	s.Must(t, "PUT", "/resources/entries", map[string]string{"kind": "kv"}, s.Key, 201)
	r := s.Must(t, "PUT", "/kv/entries/keys/%FF", `{"value":1}`, s.Key, 500)
	if r.JSON(t)["code"] != "internal_error" {
		t.Fatalf("refusal changed: %s", r.Body)
	}
	if r = s.Must(t, "GET", "/kv/entries/keys", nil, s.Key, 200); strings.Contains(string(r.Body), `"value":1`) {
		t.Fatal("invalid key stored")
	}
}

func TestKVFileMissingAndUsageHTTP(t *testing.T) {
	s := servertest.NewStorageTestSite(t)
	s.Must(t, "PUT", "/resources/entries", map[string]string{"kind": "kv"}, s.Key, 201)
	root := filepath.Join(s.A.Sites.Disk.SiteDir(s.OwnerID, "shop"), "runtime")
	if err := os.Remove(runtime.RuntimeKVPath(root, "entries")); err != nil {
		t.Fatal(err)
	}
	r := s.Must(t, "GET", "/kv/entries/keys/one", nil, s.Key, 500)
	if r.JSON(t)["code"] != "storage_failed" || !strings.Contains(string(r.Body), "missing") {
		t.Fatalf("unclear missing file error: %s", r.Body)
	}
	s.Must(t, "GET", "/usage", nil, s.Key, 200)
}

func TestKVFileStatsWiringLiveAndTrashed(t *testing.T) {
	s := servertest.NewStorageTestSite(t)
	s.Must(t, "PUT", "/resources/entries", map[string]string{"kind": "kv"}, s.Key, 201)
	s.Must(t, "PUT", "/kv/entries/keys/long-key", `{"value":"é"}`, s.Key, 200)
	check := func() {
		t.Helper()
		n, b, err := s.A.Data.KVFileStats(context.Background(), s.Sid)
		if err != nil || n != 1 || b != 4 {
			t.Fatalf("file stats %d/%d: %v", n, b, err)
		}
	}
	check()
	if err := s.A.Sites.Disk.TrashSite(s.OwnerID, "shop", s.Sid); err != nil {
		t.Fatal(err)
	}
	if _, err := s.A.Sites.Database.Exec(`UPDATE sites SET deleted_at=now() WHERE id=$1`, s.Sid); err != nil {
		t.Fatal(err)
	}
	check()
}

func TestKVUsageBusyHTTP(t *testing.T) {
	s := servertest.NewStorageTestSite(t)
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
	if r.Status != 503 || r.JSON(t)["code"] != "storage_busy" || r.Header.Get("Retry-After") == "" {
		t.Fatalf("busy usage: %d %s", r.Status, r.Body)
	}
}

func TestKVQuotaBusySQLiteWritesHTTP(t *testing.T) {
	s := servertest.NewStorageTestSite(t)
	s.Must(t, "PUT", "/resources/sqlite", map[string]string{"kind": "kv"}, s.Key, 201)
	s.Must(t, "PUT", "/kv/sqlite/keys/one", `{"value":1}`, s.Key, 200)
	s.Must(t, "PUT", "/resources/db", map[string]string{"kind": "sqlite"}, s.Key, 201)
	s.Must(t, "POST", "/sqlite/db/schema", `{"sql":"CREATE TABLE items(id INTEGER PRIMARY KEY, v TEXT)"}`, s.Key, 200)
	s.Must(t, "POST", "/sqlite/db/execute", `{"sql":"INSERT INTO items VALUES(1, 'original')"}`, s.Key, 200)
	h := s.A.Data
	h.SqliteSites = appstorage.NewSiteSlots(4, appstorage.NewSQLiteGate(4, time.Second), time.Second)
	root := filepath.Join(h.Disk.SiteDir(s.OwnerID, "shop"), "runtime")
	err := h.SqliteSites.Admit(context.Background(), s.Sid, func(ctx context.Context, _ *appstorage.Admission) error {
		conn, done, err := runtime.OpenRuntimeKV(ctx, runtime.RuntimeKVPath(root, "sqlite"), true, false)
		if err != nil {
			return err
		}
		defer done()
		if err := conn.Exec("PRAGMA locking_mode=EXCLUSIVE; BEGIN EXCLUSIVE"); err != nil {
			return err
		}
		defer conn.Exec("ROLLBACK")
		// Other slots remain available: each refusal must come from measuring
		// the locked KV file, rather than waiting for site admission.
		for _, tc := range []struct{ name, method, path, body string }{
			{"add", "POST", "/sqlite/db/tables/items/rows", `{"v":"added"}`},
			{"edit", "PATCH", "/sqlite/db/tables/items/rows/1", `{"v":"edited"}`},
			{"delete", "DELETE", "/sqlite/db/tables/items/rows/1", ""},
			{"execute", "POST", "/sqlite/db/execute", `{"sql":"INSERT INTO items VALUES(2, 'execute')"}`},
			{"schema", "POST", "/sqlite/db/schema", `{"sql":"CREATE TABLE extra(id INTEGER PRIMARY KEY)"}`},
		} {
			t.Run(tc.name, func(t *testing.T) {
				r := s.Do(t, tc.method, tc.path, tc.body, s.Key)
				if r.Status != 503 || r.JSON(t)["code"] != "storage_busy" || r.Header.Get("Retry-After") == "" {
					t.Fatalf("busy quota: %d %s, Retry-After=%q", r.Status, r.Body, r.Header.Get("Retry-After"))
				}
			})
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	s.Must(t, "POST", "/sqlite/db/execute", `{"sql":"INSERT INTO items VALUES(2, 'retry')"}`, s.Key, 200)
}
