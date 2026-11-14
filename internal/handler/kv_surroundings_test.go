package handler

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	appstorage "github.com/vsriram/simple-host/internal/app/storage"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	runtime "github.com/vsriram/simple-host/internal/storage"
)

func kvArchive(t *testing.T, s presetSite) map[string][]byte {
	t.Helper()
	r := s.a.at(t, "GET", pcSiteDomain, "/v1/sites/shop/export.tar.gz", nil, s.key)
	if r.status != 200 {
		t.Fatalf("export: %d %s", r.status, r.body)
	}
	gz, err := gzip.NewReader(bytes.NewReader(r.body))
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

func TestKVExportBothStoresAndMixed(t *testing.T) {
	var reference []byte
	for _, stores := range [][]string{{"postgres", "postgres"}, {"sqlite", "sqlite"}, {"postgres", "sqlite"}} {
		t.Run(strings.Join(stores, "-"), func(t *testing.T) {
			s := newPresetSite(t)
			for i, name := range []string{"a", "z"} {
				t.Setenv("KV_STORE", stores[i])
				s.must(t, "PUT", "/resources/"+name, `{"kind":"kv","preset":"private"}`, s.key, 201)
				for _, key := range []string{"a", "z", "é", "中", "A", "a%_"} {
					s.must(t, "PUT", "/kv/"+name+"/keys/"+url.PathEscape(key), `{"value":{"text":"é","n":1.0e2}}`, s.key, 200)
				}
				// Identical historical authors in each independent store fixture.
				if stores[i] == "postgres" {
					if _, err := s.a.sites.database.Exec(`UPDATE site_storage_kv SET writer_id='fixture-author' WHERE site_id=$1 AND resource_name=$2`, s.sid, name); err != nil {
						t.Fatal(err)
					}
				} else {
					path := runtime.RuntimeKVPath(filepath.Join(s.a.sites.disk.SiteDir(s.ownerID, "shop"), "runtime"), name)
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

			}
			s.must(t, "PUT", "/resources/uploads", `{"kind":"files","preset":"private"}`, s.key, 201)
			s.must(t, "PUT", "/files/uploads/objects/keep.txt", "keep", s.key, 200)
			// Ignore stale KV files beside Postgres markers and arbitrary leftover files.
			root := filepath.Join(s.a.sites.disk.SiteDir(s.ownerID, "shop"), "runtime")
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
			if reference == nil {
				reference = got
			} else if !bytes.Equal(reference, got) {
				t.Fatalf("export differs:\n%s\n%s", reference, got)
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
				s.must(t, "POST", "/sqlite/z/"+mode, `{"sql":"SELECT 1"}`, s.key, 409)
			}
		})
	}
}

func TestKVSQLitePeopleAndVisitorEmails(t *testing.T) {
	t.Setenv("KV_STORE", "sqlite")
	s := newPresetSite(t)
	s.must(t, "PUT", "/resources/entries", `{"kind":"kv","preset":"personal"}`, s.key, 201)
	s.must(t, "PUT", "/kv/entries/keys/mine", `{"value":"hello"}`, s.callers["alice"], 200)
	people := s.must(t, "GET", "/people", nil, s.key, 200)
	if !strings.Contains(string(people.body), s.aliceID) || !strings.Contains(string(people.body), `"has_data":true`) {
		t.Fatalf("missing file author: %s", people.body)
	}
	if strings.Contains(string(people.body), s.ids["bob"]) {
		t.Fatalf("sign-in alone listed as data: %s", people.body)
	}
	emails := s.must(t, "GET", "/visitors?id="+s.aliceID, nil, s.key, 200)
	if !strings.Contains(string(emails.body), `"found":true`) {
		t.Fatalf("author email unresolved: %s", emails.body)
	}
	var n int
	if err := s.a.sites.database.QueryRow(`SELECT count(*) FROM site_storage_kv WHERE site_id=$1`, s.sid).Scan(&n); err != nil || n != 0 {
		t.Fatalf("not file-only: %d %v", n, err)
	}
	s.must(t, "DELETE", "/resources/entries", nil, s.key, 200)
	if r := s.must(t, "GET", "/people", nil, s.key, 200); strings.Contains(string(r.body), s.aliceID) {
		t.Fatalf("deleted author retained: %s", r.body)
	}
}

func TestKVInvalidUTF8HTTPParity(t *testing.T) {
	for _, store := range []string{"postgres", "sqlite"} {
		t.Run(store, func(t *testing.T) {
			t.Setenv("KV_STORE", store)
			s := newPresetSite(t)
			s.must(t, "PUT", "/resources/entries", `{"kind":"kv","preset":"private"}`, s.key, 201)
			r := s.must(t, "PUT", "/kv/entries/keys/%FF", `{"value":1}`, s.key, 500)
			if r.json(t)["code"] != "internal_error" {
				t.Fatalf("refusal changed: %s", r.body)
			}
			if r = s.must(t, "GET", "/kv/entries/keys", nil, s.key, 200); strings.Contains(string(r.body), `"value":1`) {
				t.Fatal("invalid key stored")
			}
		})
	}
}

func TestKVFileMissingAndUsageHTTP(t *testing.T) {
	t.Setenv("KV_STORE", "sqlite")
	s := newPresetSite(t)
	s.must(t, "PUT", "/resources/entries", `{"kind":"kv","preset":"private"}`, s.key, 201)
	root := filepath.Join(s.a.sites.disk.SiteDir(s.ownerID, "shop"), "runtime")
	if err := os.Remove(runtime.RuntimeKVPath(root, "entries")); err != nil {
		t.Fatal(err)
	}
	r := s.must(t, "GET", "/kv/entries/keys/one", nil, s.key, 500)
	if r.json(t)["code"] != "storage_failed" || !strings.Contains(string(r.body), "missing") {
		t.Fatalf("unclear missing file error: %s", r.body)
	}
	s.must(t, "GET", "/usage", nil, s.key, 200)
}

func TestKVFileStatsWiringLiveAndTrashed(t *testing.T) {
	t.Setenv("KV_STORE", "sqlite")
	s := newPresetSite(t)
	s.must(t, "PUT", "/resources/entries", `{"kind":"kv","preset":"private"}`, s.key, 201)
	s.must(t, "PUT", "/kv/entries/keys/long-key", `{"value":"é"}`, s.key, 200)
	check := func() {
		t.Helper()
		n, b, err := s.a.sites.KVFileStats(context.Background(), s.sid)
		if err != nil || n != 1 || b != 4 {
			t.Fatalf("file stats %d/%d: %v", n, b, err)
		}
	}
	check()
	if err := s.a.sites.disk.TrashSite(s.ownerID, "shop", s.sid); err != nil {
		t.Fatal(err)
	}
	if _, err := s.a.sites.database.Exec(`UPDATE sites SET deleted_at=now() WHERE id=$1`, s.sid); err != nil {
		t.Fatal(err)
	}
	check()
}

func TestKVUsageBusyHTTP(t *testing.T) {
	t.Setenv("KV_STORE", "sqlite")
	s := newPresetSite(t)
	s.must(t, "PUT", "/resources/entries", `{"kind":"kv","preset":"private"}`, s.key, 201)
	h := s.a.sites
	h.sqliteSites = appstorage.NewSiteSlots(1, appstorage.NewSQLiteGate(1, time.Second), 20*time.Millisecond)
	entered, release := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- h.siteSlotsFor().Admit(context.Background(), s.sid, func(context.Context, *appstorage.Admission) error { close(entered); <-release; return nil })
	}()
	<-entered
	r := s.do(t, "GET", "/usage", nil, s.key)
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if r.status != 503 || r.json(t)["code"] != "storage_busy" || r.header.Get("Retry-After") == "" {
		t.Fatalf("busy usage: %d %s", r.status, r.body)
	}
}

func TestKVQuotaBusySQLiteWritesHTTP(t *testing.T) {
	s := newPresetSite(t)
	for _, store := range []string{"postgres", "sqlite"} {
		t.Setenv("KV_STORE", store)
		s.must(t, "PUT", "/resources/"+store, `{"kind":"kv","preset":"private"}`, s.key, 201)
		s.must(t, "PUT", "/kv/"+store+"/keys/one", `{"value":1}`, s.key, 200)
	}
	s.must(t, "PUT", "/resources/db", `{"kind":"sqlite","preset":"private"}`, s.key, 201)
	s.must(t, "POST", "/sqlite/db/schema", `{"sql":"CREATE TABLE items(id INTEGER PRIMARY KEY, v TEXT)"}`, s.key, 200)
	s.must(t, "POST", "/sqlite/db/execute", `{"sql":"INSERT INTO items VALUES(1, 'original')"}`, s.key, 200)
	h := s.a.sites
	h.sqliteSites = appstorage.NewSiteSlots(4, appstorage.NewSQLiteGate(4, time.Second), time.Second)
	root := filepath.Join(h.disk.SiteDir(s.ownerID, "shop"), "runtime")
	err := h.siteSlotsFor().Admit(context.Background(), s.sid, func(ctx context.Context, _ *appstorage.Admission) error {
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
				r := s.do(t, tc.method, tc.path, tc.body, s.key)
				if r.status != 503 || r.json(t)["code"] != "storage_busy" || r.header.Get("Retry-After") == "" {
					t.Fatalf("busy quota: %d %s, Retry-After=%q", r.status, r.body, r.header.Get("Retry-After"))
				}
			})
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	s.must(t, "POST", "/sqlite/db/execute", `{"sql":"INSERT INTO items VALUES(2, 'retry')"}`, s.key, 200)
}
