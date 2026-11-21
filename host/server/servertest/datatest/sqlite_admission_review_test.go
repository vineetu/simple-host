package datatest

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	sqlite3 "github.com/ncruces/go-sqlite3"
	appstorage "github.com/vsriram/simple-host/host/data"
	"github.com/vsriram/simple-host/host/data/datahttp"
	"github.com/vsriram/simple-host/host/model"
	"github.com/vsriram/simple-host/host/server/servertest"
	appsites "github.com/vsriram/simple-host/host/sites"
	runtime "github.com/vsriram/simple-host/host/storage"
	"github.com/vsriram/simple-host/host/web"
)

func admissionPresetSite(t *testing.T) servertest.StorageTestSite {
	t.Helper()
	s := servertest.NewStorageTestSite(t)
	s.Must(t, "PUT", "/resources/data", map[string]string{"kind": "sqlite"}, s.Key, 201)
	s.Must(t, "POST", "/sqlite/data/schema", map[string]string{"sql": "CREATE TABLE notes(id INTEGER PRIMARY KEY, body TEXT)"}, s.Key, 200)
	s.Must(t, "POST", "/sqlite/data/execute", map[string]string{"sql": "INSERT INTO notes(body) VALUES ('saved')"}, s.Key, 200)
	return s
}

// Observe actual closes of every connection the call opens (a rules save
// reads the schema before it writes), including a second no-op Close to
// distinguish a closed connection from a failed SQLITE_BUSY close that also
// emits TRACE_CLOSE. One panic is injected, at the first time stage is reached.
func admissionPanicContext(t *testing.T, stage string, value any) (context.Context, func()) {
	t.Helper()
	var conns []*sqlite3.Conn
	closes := map[*sqlite3.Conn]int{}
	hits := 0
	ctx := context.WithValue(context.Background(), admissionSetupHookKey{}, func(c *sqlite3.Conn) error {
		conns = append(conns, c)
		return c.Trace(sqlite3.TRACE_CLOSE, func(sqlite3.TraceEvent, any, any) error { closes[c]++; return nil })
	})
	ctx = runtime.WithSQLiteStageHook(ctx, func(at string) {
		if at == stage {
			hits++
			if hits == 1 {
				panic(value)
			}
		}
	})
	return ctx, func() {
		t.Helper()
		if hits == 0 || len(conns) == 0 {
			t.Fatalf("panic hits=%d opened=%d", hits, len(conns))
		}
		for _, conn := range conns {
			for stmt := range conn.Stmts() {
				t.Errorf("statement leaked: %s", stmt.SQL())
				stmt.Close()
			}
			if closes[conn] != 1 {
				t.Errorf("close attempts=%d, want 1", closes[conn])
			}
			if err := conn.Close(); err != nil {
				t.Fatal(err)
			}
			if closes[conn] != 1 {
				t.Fatal("connection was left open")
			}
		}
	}
}

func assertAdmissionReleased(t *testing.T, s servertest.StorageTestSite) {
	t.Helper()
	h := s.A.Data
	if n := len(h.Sqlite.Slots); n != 0 {
		t.Fatalf("global slots held=%d", n)
	}
	mu, ok := h.UploadLocks.Load(s.OwnerID + "/shop")
	if !ok || !mu.(*sync.Mutex).TryLock() {
		t.Fatal("site write lock not released")
	}
	mu.(*sync.Mutex).Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := h.SqliteSites.Admit(ctx, s.Sid, func(context.Context, *appstorage.Admission) error { return nil }); err != nil {
		t.Fatalf("site admission not released: %v", err)
	}
}

func TestRulesSaveAdmissionInjectedPanics(t *testing.T) {
	for _, code := range []string{"storage_failed", "query_too_expensive"} {
		for _, stage := range []string{"after_open", "after_prepare", "after_step", "before_commit", "after_commit", "close"} {
			t.Run(code+"/"+stage, func(t *testing.T) {
				s := admissionPresetSite(t)
				before, err := s.A.Data.LoadStorageResource(context.Background(), s.Sid, "data")
				if err != nil {
					t.Fatal(err)
				}
				value := "injected"
				if code == "query_too_expensive" {
					value = "sqlite3: out of memory"
				}
				ctx, closed := admissionPanicContext(t, stage, value)
				req := httptest.NewRequest("PUT", "https://"+servertest.PcSiteDomain+"/v1/sites/shop/storage/resources/data", strings.NewReader(`{"kind":"sqlite","rules":`+servertest.RulesPersonal+`,"tables":{"notes":{"rules":`+servertest.RulesRecords+`},"later":{"rules":`+string(model.OwnerOnlyRules)+`}}}`)).WithContext(ctx)
				req.Header.Set("X-API-Key", s.Owner.Key)
				req.Header.Set("Content-Type", "application/json")
				rec := httptest.NewRecorder()
				// No test-side recovery: production admission must catch the panic.
				s.A.Srv.Config.Handler.ServeHTTP(rec, req)
				closed()
				assertAdmissionReleased(t, s)
				success := stage == "after_commit" || stage == "close"
				wantStatus := 500
				if code == "query_too_expensive" {
					wantStatus = 422
				}
				if success {
					wantStatus = 200
				}
				if rec.Code != wantStatus {
					t.Fatalf("response=%d %s, want %d", rec.Code, rec.Body.String(), wantStatus)
				}
				var body map[string]any
				if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
					t.Fatal(err)
				}
				stored, err := s.A.Data.LoadStorageResource(context.Background(), s.Sid, "data")
				if err != nil {
					t.Fatal(err)
				}
				if success {
					if body["rules_status"] != "rules" || stored.RulesStatus() != "rules" || stored.Fingerprint() == before.Fingerprint() || stored.Tables["notes"].Rules == nil || stored.Tables["later"].Rules == nil {
						t.Fatalf("committed rules lost: response=%v stored=%+v", body, stored)
					}
				} else if body["code"] != code || stored.Fingerprint() != before.Fingerprint() || len(stored.Tables) != 0 {
					t.Fatalf("failure changed rules: response=%v stored=%+v", body, stored)
				}
				rows := s.Must(t, "POST", "/sqlite/data/query", map[string]string{"sql": "SELECT id, body FROM notes"}, s.Key, 200).JSON(t)["rows"].([]any)
				if len(rows) != 1 || rows[0].([]any)[1] != "saved" {
					t.Fatalf("saved data changed: %v", rows)
				}
				// A subsequent write takes both the site lock and admission again.
				s.Must(t, "PUT", "/resources/data", `{"kind":"sqlite","rules":`+string(model.OwnerOnlyRules)+`,"tables":{}}`, s.Key, 200)
			})
		}
	}
}

func TestExportAdmissionInjectedPanics(t *testing.T) {
	for _, code := range []string{"storage_failed", "query_too_expensive"} {
		for _, stage := range []string{"after_open", "before_checkpoint", "close"} {
			t.Run(code+"/"+stage, func(t *testing.T) {
				s := admissionPresetSite(t)
				value := "injected"
				if code == "query_too_expensive" {
					value = "sqlite3: out of memory"
				}
				ctx, closed := admissionPanicContext(t, stage, value)
				var got error
				exported := false
				for entry, err := range s.A.Sites.LifecycleSvc().Export(ctx, appsites.ExportInput{SiteID: s.Sid, Prefix: "shop", Files: runtime.FileVersion{UserID: s.OwnerID, SiteName: "shop"}}) {
					if err != nil {
						got = err
						break
					}
					if strings.HasSuffix(entry.Name, "/data.sqlite") {
						exported = true
					}
					if _, err := io.Copy(io.Discard, entry.Reader); err != nil {
						t.Fatal(err)
					}
				}
				closed()
				assertAdmissionReleased(t, s)
				if stage == "close" {
					if got != nil || !exported {
						t.Fatalf("close panic lost export: exported=%v error=%v", exported, got)
					}
				} else {
					var e *appstorage.Error
					if !errors.As(got, &e) || e.Code != code || exported {
						t.Fatalf("exported=%v error=%v, want %s", exported, got, code)
					}
				}
				// A fresh export must still include the SQLite file.
				result := s.A.At(t, "GET", servertest.PcSiteDomain, "/v1/sites/shop/export.tar.gz", nil, s.Key)
				assertCompleteSQLiteArchive(t, result.Body, false)
			})
		}
	}
}

func holdExportOwnerQuery(t *testing.T, s servertest.StorageTestSite) func() {
	t.Helper()
	h := s.A.Data
	resource, err := h.LoadStorageResource(context.Background(), s.Sid, "data")
	if err != nil {
		t.Fatal(err)
	}
	call := datahttp.StorageCall{SiteID: s.Sid, OwnerID: s.OwnerID, SiteName: "shop", ResourceName: "data", Resource: resource, Owner: true}
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	var once sync.Once
	finish := func() {
		once.Do(func() {
			close(release)
			if err := <-done; err != nil {
				t.Errorf("held owner query: %v", err)
			}
		})
	}
	t.Cleanup(finish)
	ctx := context.WithValue(context.Background(), admissionQueryHookKey{}, func() { close(entered); <-release })
	go func() {
		_, err := h.StorageFor(call).LegacySQL(ctx, appstorage.LegacySQLInput{Call: call, Mode: "query", ReadBody: func() ([]byte, error) { return []byte(`{"sql":"SELECT * FROM notes"}`), nil }, ResultLimit: 1 << 20})
		done <- err
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("owner query did not acquire admission")
	}
	return finish
}

type flushExportWriter struct{ http.ResponseWriter }

func (w flushExportWriter) Write(b []byte) (int, error) {
	n, err := w.ResponseWriter.Write(b)
	w.ResponseWriter.(http.Flusher).Flush()
	return n, err
}

func TestExportAdmissionTimeoutAbortsHTTP(t *testing.T) {
	s := admissionPresetSite(t)
	h := s.A.Data
	h.SqliteSites = appstorage.NewSiteSlots(1, h.Sqlite, 250*time.Millisecond)
	s.A.Sites.SqliteSites = h.SqliteSites // the site exports admit through the same slots
	// ZIP buffers compressed entries. An incompressible file in a site the
	// account export writes first (the most recently updated) makes it emit
	// archive bytes before reaching the blocked SQLite checkpoint.
	padding := make([]byte, 16<<10)
	if _, err := rand.Read(padding); err != nil {
		t.Fatal(err)
	}
	if r := s.A.At(t, "POST", servertest.PcSiteDomain, "/v1/sites/pad/files", map[string]any{"files": map[string]string{"index.html": hex.EncodeToString(padding)}}, s.Key); r.Status != http.StatusCreated {
		t.Fatalf("padding site: %d %s", r.Status, r.Body)
	}
	// A group reference over the same site exercises the group export consumer.
	// The export reads only the holding account's sites, so no product row is needed.
	ev := web.GroupRef{ID: "00000000-0000-4000-8000-000000000021", AccountID: s.OwnerID, Slug: servertest.UniqueLabel(t, "export")}
	for _, tc := range []struct {
		name, path, key string
		zip             bool
	}{
		{"site", "/v1/sites/shop/export.tar.gz", s.Owner.Key, false},
		{"link", "/v1/export?token=" + s.A.Sites.SignExportToken(s.OwnerID, s.Sid, time.Now().Add(time.Minute)), "", false},
		{"account", "/v1/me/export.zip", s.Owner.Key, true},
		{"admin", "/v1/admin/export.tar.gz", s.A.Admin, false},
		{"event", "/event", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			release := holdExportOwnerQuery(t, s)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// Force bytes onto the wire before SQLite admission fails, so this
				// checks a broken HTTP body rather than only a pre-header EOF.
				w = flushExportWriter{w}
				if tc.name == "event" {
					s.A.Sites.ExportEventProjects(w, r, web.GroupRef{ID: ev.ID, Slug: ev.Slug, AccountID: ev.AccountID}, []string{"shop"}, "")
				} else {
					s.A.Srv.Config.Handler.ServeHTTP(w, r)
				}
			}))
			defer server.Close()
			request := func() *http.Response {
				t.Helper()
				req, err := http.NewRequest("GET", server.URL+tc.path, nil)
				if err != nil {
					t.Fatal(err)
				}
				req.Host = servertest.PcSiteDomain
				req.Header.Set("X-API-Key", tc.key)
				client := server.Client()
				client.Timeout = 5 * time.Second
				resp, err := client.Do(req)
				if err != nil {
					t.Fatal(err)
				}
				return resp
			}
			start := time.Now()
			resp := request()
			body, err := io.ReadAll(resp.Body)
			resp.Body.Close()
			if resp.StatusCode != 200 || len(body) == 0 || !errors.Is(err, io.ErrUnexpectedEOF) {
				t.Fatalf("partial export not aborted: status=%d bytes=%d error=%v", resp.StatusCode, len(body), err)
			}
			if time.Since(start) < 250*time.Millisecond {
				t.Fatal("export did not wait through admission deadline")
			}
			release()
			assertAdmissionReleased(t, s)
			resp = request()
			body, err = io.ReadAll(resp.Body)
			resp.Body.Close()
			if err != nil || resp.StatusCode != 200 {
				t.Fatalf("retry failed: status=%d error=%v", resp.StatusCode, err)
			}
			assertCompleteSQLiteArchive(t, body, tc.zip)
		})
	}
}

func assertCompleteSQLiteArchive(t *testing.T, body []byte, zipped bool) {
	t.Helper()
	foundDB, foundFile := false, false
	check := func(name string) {
		foundDB = foundDB || strings.HasSuffix(name, "/data.sqlite")
		foundFile = foundFile || strings.HasSuffix(name, "/files/index.html")
	}
	if zipped {
		zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range zr.File {
			check(f.Name)
		}
	} else {
		gz, err := gzip.NewReader(bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		defer gz.Close()
		tr := tar.NewReader(gz)
		for {
			header, err := tr.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatal(err)
			}
			check(header.Name)
		}
		if _, err := io.Copy(io.Discard, gz); err != nil {
			t.Fatal(err)
		}
	}
	if !foundDB || !foundFile {
		t.Fatalf("incomplete archive: sqlite=%v published file=%v", foundDB, foundFile)
	}
}
