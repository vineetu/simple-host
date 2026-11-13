package handler

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
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	sqlite3 "github.com/ncruces/go-sqlite3"
	appsites "github.com/vsriram/simple-host/internal/app/sites"
	appstorage "github.com/vsriram/simple-host/internal/app/storage"
	"github.com/vsriram/simple-host/internal/model"
	runtime "github.com/vsriram/simple-host/internal/storage"
)

func admissionPresetSite(t *testing.T) presetSite {
	t.Helper()
	s := newPresetSite(t)
	s.must(t, "PUT", "/resources/data", map[string]string{"kind": "sqlite", "preset": "private"}, s.key, 201)
	s.must(t, "POST", "/sqlite/data/schema", map[string]string{"sql": "CREATE TABLE notes(id INTEGER PRIMARY KEY, body TEXT)"}, s.key, 200)
	s.must(t, "POST", "/sqlite/data/execute", map[string]string{"sql": "INSERT INTO notes(body) VALUES ('saved')"}, s.key, 200)
	return s
}

// Observe actual closes, including a second no-op Close to distinguish a closed
// connection from a failed SQLITE_BUSY close that also emits TRACE_CLOSE.
func admissionPanicContext(t *testing.T, stage string, value any) (context.Context, func()) {
	t.Helper()
	var conn *sqlite3.Conn
	closes, hits := 0, 0
	ctx := context.WithValue(context.Background(), admissionSetupHookKey{}, func(c *sqlite3.Conn) error {
		conn = c
		return c.Trace(sqlite3.TRACE_CLOSE, func(sqlite3.TraceEvent, any, any) error { closes++; return nil })
	})
	ctx = runtime.WithSQLiteStageHook(ctx, func(at string) {
		if at == stage {
			hits++
			panic(value)
		}
	})
	return ctx, func() {
		t.Helper()
		if hits != 1 || conn == nil {
			t.Fatalf("panic hits=%d opened=%v", hits, conn != nil)
		}
		for stmt := range conn.Stmts() {
			t.Errorf("statement leaked: %s", stmt.SQL())
			stmt.Close()
		}
		if closes != 1 {
			t.Errorf("close attempts=%d, want 1", closes)
		}
		if err := conn.Close(); err != nil {
			t.Fatal(err)
		}
		if closes != 1 {
			t.Fatal("connection was left open")
		}
	}
}

func assertAdmissionReleased(t *testing.T, s presetSite) {
	t.Helper()
	h := s.a.sites
	if n := len(h.sqliteGateFor().Slots); n != 0 {
		t.Fatalf("global slots held=%d", n)
	}
	mu, ok := h.uploadLocks.Load(s.ownerID + "/shop")
	if !ok || !mu.(*sync.Mutex).TryLock() {
		t.Fatal("site write lock not released")
	}
	mu.(*sync.Mutex).Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := h.siteSlotsFor().Admit(ctx, s.sid, func(context.Context, *appstorage.Admission) error { return nil }); err != nil {
		t.Fatalf("site admission not released: %v", err)
	}
}

func TestPresetSaveAdmissionInjectedPanics(t *testing.T) {
	for _, code := range []string{"storage_failed", "query_too_expensive"} {
		for _, stage := range []string{"after_open", "after_prepare", "after_step", "before_commit", "after_commit", "close"} {
			t.Run(code+"/"+stage, func(t *testing.T) {
				s := admissionPresetSite(t)
				value := "injected"
				if code == "query_too_expensive" {
					value = "sqlite3: out of memory"
				}
				ctx, closed := admissionPanicContext(t, stage, value)
				req := httptest.NewRequest("PUT", "https://"+pcSiteDomain+"/v1/sites/shop/storage/resources/data", strings.NewReader(`{"kind":"sqlite","preset":"personal","tables":{"notes":{"preset":"records"},"later":{"preset":"private"}}}`)).WithContext(ctx)
				req.Header.Set("X-API-Key", s.owner.key)
				req.Header.Set("Content-Type", "application/json")
				rec := httptest.NewRecorder()
				// No test-side recovery: production admission must catch the panic.
				s.a.srv.Config.Handler.ServeHTTP(rec, req)
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
				stored, err := s.a.sites.loadStorageResource(context.Background(), s.sid, "data")
				if err != nil {
					t.Fatal(err)
				}
				if success {
					if body["preset"] != "personal" || stored.PresetBase != "personal" || stored.Tables["notes"].PresetBase != "records" || stored.Tables["later"].PresetBase != "private" {
						t.Fatalf("committed preset metadata lost: response=%v stored=%+v", body, stored)
					}
					warnings, _ := body["warnings"].([]any)
					if len(warnings) != 1 || warnings[0] != "table later does not exist yet; its preset applies once it is created" {
						t.Fatalf("committed table inventory lost: warnings=%v", warnings)
					}
				} else if body["code"] != code || stored.PresetBase != "private" || len(stored.Tables) != 0 {
					t.Fatalf("failure changed preset: response=%v stored=%+v", body, stored)
				}
				rows := s.must(t, "POST", "/sqlite/data/query", map[string]string{"sql": "SELECT id, body FROM notes"}, s.key, 200).json(t)["rows"].([]any)
				if len(rows) != 1 || rows[0].([]any)[1] != "saved" {
					t.Fatalf("saved data changed: %v", rows)
				}
				// A subsequent write takes both the site lock and admission again.
				s.must(t, "PUT", "/resources/data", map[string]any{"kind": "sqlite", "preset": "private", "tables": map[string]any{}}, s.key, 200)
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
				for entry, err := range s.a.sites.lifecycleSvc().Export(ctx, appsites.ExportInput{SiteID: s.sid, Prefix: "shop", Current: filepath.Join(s.a.sites.disk.SiteDir(s.ownerID, "shop"), "current")}) {
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
				result := s.a.at(t, "GET", pcSiteDomain, "/v1/sites/shop/export.tar.gz", nil, s.key)
				assertCompleteSQLiteArchive(t, result.body, false)
			})
		}
	}
}

func holdExportOwnerQuery(t *testing.T, s presetSite) func() {
	t.Helper()
	h := s.a.sites
	resource, err := h.loadStorageResource(context.Background(), s.sid, "data")
	if err != nil {
		t.Fatal(err)
	}
	call := storageCall{siteID: s.sid, ownerID: s.ownerID, siteName: "shop", resourceName: "data", resource: resource, owner: true}
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
	r := httptest.NewRequest("POST", "/", nil).WithContext(ctx)
	go func() {
		_, err := h.storageRequestSvc(r, call).LegacySQL(ctx, appstorage.LegacySQLInput{Call: h.storageServiceCall(call), Mode: "query", ReadBody: func() ([]byte, error) { return []byte(`{"sql":"SELECT * FROM notes"}`), nil }, ResultLimit: 1 << 20})
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
	h := s.a.sites
	h.sqliteSites = appstorage.NewSiteSlots(1, h.sqliteGateFor(), 250*time.Millisecond)
	// ZIP buffers compressed entries. An incompressible state document makes
	// it emit archive bytes before reaching the blocked SQLite checkpoint.
	padding := make([]byte, 16<<10)
	if _, err := rand.Read(padding); err != nil {
		t.Fatal(err)
	}
	state, err := json.Marshal(map[string]string{"padding": hex.EncodeToString(padding)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.a.database.Exec(`UPDATE sites SET state=$2 WHERE id=$1`, s.sid, string(state)); err != nil {
		t.Fatal(err)
	}
	// A throwaway event uses the same site to exercise the event export consumer.
	ev := model.Event{AccountID: s.ownerID, Slug: uniqueLabel(t, "export")}
	if err := s.a.database.QueryRow(`INSERT INTO events(slug,account_id,title,organiser_name,contact_email,join_code,judge_code) VALUES($1,$2,'Export','Test','export@example.com',$3,$4) RETURNING id`, ev.Slug, ev.AccountID, uniqueLabel(t, "join"), uniqueLabel(t, "judge")).Scan(&ev.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.a.database.Exec(`INSERT INTO event_teams(event_id,slug,name,code) VALUES($1,'shop','Shop',$2)`, ev.ID, uniqueLabel(t, "team")); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, path, key string
		zip             bool
	}{
		{"site", "/v1/sites/shop/export.tar.gz", s.owner.key, false},
		{"link", "/v1/export?token=" + h.signExportToken(s.ownerID, s.sid, time.Now().Add(time.Minute)), "", false},
		{"account", "/v1/me/export.zip", s.owner.key, true},
		{"admin", "/v1/admin/export.tar.gz", s.a.admin, false},
		{"event", "/event", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			release := holdExportOwnerQuery(t, s)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// Force bytes onto the wire before SQLite admission fails, so this
				// checks a broken HTTP body rather than only a pre-header EOF.
				w = flushExportWriter{w}
				if tc.name == "event" {
					h.ExportEventProjects(w, r, ev, "")
				} else {
					s.a.srv.Config.Handler.ServeHTTP(w, r)
				}
			}))
			defer server.Close()
			request := func() *http.Response {
				t.Helper()
				req, err := http.NewRequest("GET", server.URL+tc.path, nil)
				if err != nil {
					t.Fatal(err)
				}
				req.Host = pcSiteDomain
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
