package datatest

import (
	"context"
	"io"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	sqlite3 "github.com/ncruces/go-sqlite3"
	appstorage "github.com/vsriram/simple-host/host/data"
	"github.com/vsriram/simple-host/host/data/datahttp"
	"github.com/vsriram/simple-host/host/server/servertest"
	appsites "github.com/vsriram/simple-host/host/sites"
	runtime "github.com/vsriram/simple-host/host/storage"
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
	s := servertest.NewStorageTestSite(t)
	s.Must(t, "PUT", "/resources/data", map[string]string{"kind": "sqlite"}, s.Key, 201)
	s.Must(t, "POST", "/sqlite/data/schema", map[string]string{"sql": "CREATE TABLE notes(id INTEGER PRIMARY KEY, body TEXT)"}, s.Key, 200)
	h := s.A.Data
	resource, err := h.LoadStorageResource(context.Background(), s.Sid, "data")
	if err != nil {
		t.Fatal(err)
	}
	call := datahttp.StorageCall{SiteID: s.Sid, OwnerID: s.OwnerID, SiteName: "shop", ResourceName: "data", Resource: resource, Owner: true}
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	t.Cleanup(func() { once.Do(func() { close(release) }) })
	ctx := context.WithValue(context.Background(), admissionQueryHookKey{}, func() { close(entered); <-release })
	queryDone := make(chan error, 1)
	go func() {
		_, err := h.StorageFor(call).LegacySQL(ctx, appstorage.LegacySQLInput{Call: call, Mode: "query", ReadBody: func() ([]byte, error) { return []byte(`{"sql":"SELECT * FROM notes"}`), nil }, ResultLimit: 1 << 20})
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
		for entry, err := range s.A.Sites.LifecycleSvc().Export(context.Background(), appsites.ExportInput{SiteID: s.Sid, Prefix: "shop", Files: runtime.FileVersion{UserID: s.OwnerID, SiteName: "shop"}}) {
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
	s := servertest.NewStorageTestSite(t)
	s.Must(t, "PUT", "/resources/data", servertest.StorageRules("sqlite", servertest.RulesPersonal), s.Key, 201)
	s.Must(t, "POST", "/sqlite/data/schema", map[string]string{"sql": "CREATE TABLE notes(id INTEGER PRIMARY KEY, body TEXT)"}, s.Key, 200)
	s.Must(t, "POST", "/sqlite/data/tables/notes/rows", map[string]string{"body": "saved"}, s.Callers["alice"], 200)
	if list := s.Must(t, "GET", "/people", nil, s.Key, 200).JSON(t)["people"].([]any); len(list) != 1 {
		t.Fatalf("seeded people=%v", list)
	}
	h := s.A.Data
	h.SqliteSites = appstorage.NewSiteSlots(1, h.Sqlite, 20*time.Millisecond)
	entered, release := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	var once sync.Once
	t.Cleanup(func() { once.Do(func() { close(release) }) })
	go func() {
		done <- h.SqliteSites.Admit(context.Background(), s.Sid, func(context.Context, *appstorage.Admission) error { close(entered); <-release; return nil })
	}()
	<-entered
	if list := s.Must(t, "GET", "/people", nil, s.Key, 200).JSON(t)["people"].([]any); len(list) != 0 {
		t.Fatalf("busy resource was not skipped: %v", list)
	}
	once.Do(func() { close(release) })
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if list := s.Must(t, "GET", "/people", nil, s.Key, 200).JSON(t)["people"].([]any); len(list) != 1 {
		t.Fatalf("people did not recover: %v", list)
	}
}

func TestResourceSaveAdmissionPanicStatus(t *testing.T) {
	s := servertest.NewStorageTestSite(t)
	s.Must(t, "PUT", "/resources/data", map[string]string{"kind": "sqlite"}, s.Key, 201)
	s.Must(t, "POST", "/sqlite/data/schema", map[string]string{"sql": "CREATE TABLE notes(id INTEGER PRIMARY KEY, body TEXT)"}, s.Key, 200)
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
			req := httptest.NewRequest("PUT", "https://"+servertest.PcSiteDomain+"/v1/sites/shop/storage/resources/data", strings.NewReader(servertest.StorageRules("sqlite", servertest.RulesPersonal))).WithContext(ctx)
			req.Header.Set("X-API-Key", s.Owner.Key)
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			s.A.Srv.Config.Handler.ServeHTTP(rec, req)
			if !injected || rec.Code != tc.status || !strings.Contains(rec.Body.String(), tc.code) {
				t.Fatalf("injected=%v response=%d %s", injected, rec.Code, rec.Body.String())
			}
			// The failure must Release the write lock and admission for the next save.
			s.Must(t, "PUT", "/resources/data", map[string]string{"kind": "sqlite"}, s.Key, 200)
		})
	}
}
