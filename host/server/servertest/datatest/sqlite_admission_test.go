package datatest

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	sqlite3 "github.com/ncruces/go-sqlite3"
	appstorage "github.com/vsriram/simple-host/host/data"
	"github.com/vsriram/simple-host/host/model"
	rt "github.com/vsriram/simple-host/host/server/servertest/routetest"
	runtime "github.com/vsriram/simple-host/host/storage"
)

type admissionSetupHookKey struct{}

func init() {
	sqlite3.AutoExtension(func(c *sqlite3.Conn) error {
		if hook, ok := c.GetInterrupt().Value(admissionSetupHookKey{}).(func(*sqlite3.Conn) error); ok {
			return hook(c)
		}
		return nil
	})
}

func admissionShop(t *testing.T) Shop {
	t.Helper()
	s := NewShop(t)
	s.Must(t, "PUT", "/resources/data", map[string]string{"kind": "sqlite"}, s.Key, 201)
	s.Must(t, "POST", "/sqlite/data/schema", map[string]string{"sql": "CREATE TABLE notes(id INTEGER PRIMARY KEY, body TEXT)"}, s.Key, 200)
	s.Must(t, "POST", "/sqlite/data/execute", map[string]string{"sql": "INSERT INTO notes(body) VALUES ('saved')"}, s.Key, 200)
	return s
}

// admissionPanicContext injects one panic the first time stage is reached
// and, after the call, checks every connection it opened was closed once
// with no statement left open.
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
			if hits++; hits == 1 {
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
			_ = conn.Close()
		}
	}
}

func assertAdmissionReleased(t *testing.T, s Shop) {
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

// A panic inside a rules save is caught by admission: the save fails with
// the right code and changes nothing (or, after commit, keeps the rules),
// and the lock, slots and connections are all released.
func TestRulesSaveAdmissionInjectedPanics(t *testing.T) {
	for _, code := range []string{"storage_failed", "query_too_expensive"} {
		for _, stage := range []string{"after_prepare", "before_commit", "after_commit"} {
			t.Run(code+"/"+stage, func(t *testing.T) {
				s := admissionShop(t)
				before, err := s.A.Data.LoadStorageResource(context.Background(), s.Sid, "data")
				if err != nil {
					t.Fatal(err)
				}
				value, wantStatus := "injected", 500
				if code == "query_too_expensive" {
					value, wantStatus = "sqlite3: out of memory", 422
				}
				success := stage == "after_commit"
				if success {
					wantStatus = 200
				}
				ctx, closed := admissionPanicContext(t, stage, value)
				req := httptest.NewRequest("PUT", "https://"+rt.SiteDomain+"/v1/sites/shop/storage/resources/data", strings.NewReader(`{"kind":"sqlite","rules":`+RulesPersonal+`,"tables":{"notes":{"rules":`+RulesRecords+`}}}`)).WithContext(ctx)
				req.Header.Set("X-API-Key", s.Owner.Key)
				req.Header.Set("Content-Type", "application/json")
				rec := httptest.NewRecorder()
				s.A.Srv.Config.Handler.ServeHTTP(rec, req) // no test-side recovery
				closed()
				assertAdmissionReleased(t, s)
				var body map[string]any
				_ = json.Unmarshal(rec.Body.Bytes(), &body)
				if rec.Code != wantStatus {
					t.Fatalf("response=%d %s, want %d", rec.Code, rec.Body.String(), wantStatus)
				}
				stored, err := s.A.Data.LoadStorageResource(context.Background(), s.Sid, "data")
				if err != nil {
					t.Fatal(err)
				}
				if success && (stored.RulesStatus() != "rules" || stored.Tables["notes"].Rules == nil) {
					t.Fatalf("committed rules lost: %+v", stored)
				}
				if !success && (body["code"] != code || stored.Fingerprint() != before.Fingerprint()) {
					t.Fatalf("failure changed rules: response=%v stored=%+v", body, stored)
				}
				rows := s.Must(t, "POST", "/sqlite/data/query", map[string]string{"sql": "SELECT body FROM notes"}, s.Key, 200).JSON(t)["rows"].([]any)
				if len(rows) != 1 || rows[0].([]any)[0] != "saved" {
					t.Fatalf("saved data changed: %v", rows)
				}
				s.Must(t, "PUT", "/resources/data", `{"kind":"sqlite","rules":`+string(model.OwnerOnlyRules)+`,"tables":{}}`, s.Key, 200)
			})
		}
	}
}

// A resource busy in SQLite admission is skipped by the pick list, not waited on.
func TestPeopleSkipsBusySQLiteResource(t *testing.T) {
	s := NewShop(t)
	s.Must(t, "PUT", "/resources/data", StorageRules("sqlite", RulesPersonal), s.Key, 201)
	s.Must(t, "POST", "/sqlite/data/schema", map[string]string{"sql": "CREATE TABLE notes(id INTEGER PRIMARY KEY, body TEXT)"}, s.Key, 200)
	s.Must(t, "POST", "/sqlite/data/tables/notes/rows", map[string]string{"body": "saved"}, s.Callers["alice"], 200)
	people := func() int { return len(s.Must(t, "GET", "/people", nil, s.Key, 200).JSON(t)["people"].([]any)) }
	if people() != 1 {
		t.Fatal("seeded people")
	}
	h := s.A.Data
	h.SqliteSites = appstorage.NewSiteSlots(1, h.Sqlite, 20*time.Millisecond)
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		done <- h.SqliteSites.Admit(context.Background(), s.Sid, func(context.Context, *appstorage.Admission) error { close(entered); <-release; return nil })
	}()
	<-entered
	n := people()
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if n != 0 || people() != 1 {
		t.Fatalf("busy=%d", n)
	}
}
