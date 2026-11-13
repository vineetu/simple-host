package handler

import (
	"context"
	"io"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	sqlite3 "github.com/ncruces/go-sqlite3"
	appsites "github.com/vsriram/simple-host/internal/app/sites"
	appstorage "github.com/vsriram/simple-host/internal/app/storage"
)

type admissionQueryHookKey struct{}
type admissionSetupHookKey struct{}

func init() {
	sqlite3.AutoExtension(func(c *sqlite3.Conn) error {
		if hook, ok := c.GetInterrupt().Value(admissionSetupHookKey{}).(func(*sqlite3.Conn) error); ok {
			return hook(c)
		}
		if hook, ok := c.GetInterrupt().Value(admissionQueryHookKey{}).(func()); ok {
			return c.Trace(sqlite3.TRACE_STMT, func(_ sqlite3.TraceEvent, _, sql any) error {
				if sql == "SELECT * FROM notes" {
					hook()
				}
				return nil
			})
		}
		return nil
	})
}

func TestExportWaitsForOwnerQueryAdmission(t *testing.T) {
	s := newPresetSite(t)
	s.must(t, "PUT", "/resources/data", map[string]string{"kind": "sqlite", "preset": "private"}, s.key, 201)
	s.must(t, "POST", "/sqlite/data/schema", map[string]string{"sql": "CREATE TABLE notes(id INTEGER PRIMARY KEY, body TEXT)"}, s.key, 200)
	h := s.a.sites
	resource, err := h.loadStorageResource(context.Background(), s.sid, "data")
	if err != nil {
		t.Fatal(err)
	}
	call := storageCall{siteID: s.sid, ownerID: s.ownerID, siteName: "shop", resourceName: "data", resource: resource, owner: true}
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	t.Cleanup(func() { once.Do(func() { close(release) }) })
	ctx := context.WithValue(context.Background(), admissionQueryHookKey{}, func() { close(entered); <-release })
	r := httptest.NewRequest("POST", "/", nil).WithContext(ctx)
	queryDone := make(chan error, 1)
	go func() {
		_, err := h.storageRequestSvc(r, call).LegacySQL(ctx, appstorage.LegacySQLInput{Call: h.storageServiceCall(call), Mode: "query", ReadBody: func() ([]byte, error) { return []byte(`{"sql":"SELECT * FROM notes"}`), nil }, ResultLimit: 1 << 20})
		queryDone <- err
	}()
	select {
	case <-entered:
	case err := <-queryDone:
		t.Fatalf("owner query stopped early: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("query did not start")
	}
	exportDone := make(chan error, 1)
	catalog := make(chan struct{})
	exported := false
	go func() {
		for entry, err := range h.lifecycleSvc().Export(context.Background(), appsites.ExportInput{SiteID: s.sid, Prefix: "shop", Current: filepath.Join(h.disk.SiteDir(s.ownerID, "shop"), "current")}) {
			if err != nil {
				exportDone <- err
				return
			}
			if strings.HasSuffix(entry.Name, "resources.json") {
				close(catalog)
			}
			if strings.HasSuffix(entry.Name, "data.sqlite") {
				exported = true
			}
			if _, err := io.Copy(io.Discard, entry.Reader); err != nil {
				exportDone <- err
				return
			}
		}
		exportDone <- nil
	}()
	select {
	case <-catalog:
	case err := <-exportDone:
		t.Fatalf("export stopped early: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("export did not reach runtime catalog")
	}
	select {
	case err := <-exportDone:
		t.Fatalf("export bypassed owner query slot: %v", err)
	case <-time.After(30 * time.Millisecond):
	}
	once.Do(func() { close(release) })
	if err := <-queryDone; err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-exportDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("export did not resume")
	}
	if !exported {
		t.Fatal("SQLite database missing from export")
	}
}

func TestPeopleSkipsBusySQLiteResource(t *testing.T) {
	s := newPresetSite(t)
	s.must(t, "PUT", "/resources/data", map[string]string{"kind": "sqlite", "preset": "personal"}, s.key, 201)
	s.must(t, "POST", "/sqlite/data/schema", map[string]string{"sql": "CREATE TABLE notes(id INTEGER PRIMARY KEY, body TEXT)"}, s.key, 200)
	s.must(t, "POST", "/sqlite/data/tables/notes/rows", map[string]string{"body": "saved"}, s.callers["alice"], 200)
	if list := s.must(t, "GET", "/people", nil, s.key, 200).json(t)["people"].([]any); len(list) != 1 {
		t.Fatalf("seeded people=%v", list)
	}
	h := s.a.sites
	h.sqliteSites = appstorage.NewSiteSlots(1, h.sqliteGateFor(), 20*time.Millisecond)
	entered, release := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	var once sync.Once
	t.Cleanup(func() { once.Do(func() { close(release) }) })
	go func() {
		done <- h.siteSlotsFor().Admit(context.Background(), s.sid, func(context.Context, *appstorage.Admission) error { close(entered); <-release; return nil })
	}()
	<-entered
	if list := s.must(t, "GET", "/people", nil, s.key, 200).json(t)["people"].([]any); len(list) != 0 {
		t.Fatalf("busy resource was not skipped: %v", list)
	}
	once.Do(func() { close(release) })
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if list := s.must(t, "GET", "/people", nil, s.key, 200).json(t)["people"].([]any); len(list) != 1 {
		t.Fatalf("people did not recover: %v", list)
	}
}

func TestResourceSaveAdmissionPanicStatus(t *testing.T) {
	s := newPresetSite(t)
	s.must(t, "PUT", "/resources/data", map[string]string{"kind": "sqlite", "preset": "private"}, s.key, 201)
	s.must(t, "POST", "/sqlite/data/schema", map[string]string{"sql": "CREATE TABLE notes(id INTEGER PRIMARY KEY, body TEXT)"}, s.key, 200)
	for _, tc := range []struct {
		panicValue any
		code       string
		status     int
	}{
		{"injected", "storage_failed", 500}, {"sqlite3: out of memory", "query_too_expensive", 422},
	} {
		t.Run(tc.code, func(t *testing.T) {
			injected := false
			ctx := context.WithValue(context.Background(), admissionSetupHookKey{}, func(c *sqlite3.Conn) error {
				return c.Trace(sqlite3.TRACE_STMT, func(_ sqlite3.TraceEvent, _, query any) error {
					if query == "PRAGMA page_size" {
						injected = true
						panic(tc.panicValue)
					}
					return nil
				})
			})
			req := httptest.NewRequest("PUT", "https://"+pcSiteDomain+"/v1/sites/shop/storage/resources/data", strings.NewReader(`{"kind":"sqlite","preset":"personal"}`)).WithContext(ctx)
			req.Header.Set("X-API-Key", s.owner.key)
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			s.a.srv.Config.Handler.ServeHTTP(rec, req)
			if !injected || rec.Code != tc.status || !strings.Contains(rec.Body.String(), tc.code) {
				t.Fatalf("injected=%v response=%d %s", injected, rec.Code, rec.Body.String())
			}
			// The failure must release the write lock and admission for the next save.
			s.must(t, "PUT", "/resources/data", map[string]string{"kind": "sqlite", "preset": "private"}, s.key, 200)
		})
	}
}
