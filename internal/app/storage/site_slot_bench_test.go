//go:build bench

package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	_ "github.com/lib/pq"
	sqlite3 "github.com/ncruces/go-sqlite3"
	rules "github.com/vsriram/simple-host/internal/rules/storage"
	runtimeStorage "github.com/vsriram/simple-host/internal/storage"
)

// Run the compiled test with -test.run=^$ -test.bench=^BenchmarkSiteSlots$
// -test.benchtime=1x under systemd-run -p CPUQuota=100% -p MemoryMax=1G.
// DB_DSN must name a disposable database containing db/schema.sql.
// For the pre-R1-S3a run, only this file is added: omit the SiteSlots setup
// below and replace benchConnectionCap with the old fixed 256 MiB ceiling.
// All workload, timing, tracing and reporting code stays identical.
type benchConnectionKey struct{}

func init() {
	sqlite3.AutoExtension(func(c *sqlite3.Conn) error {
		if hook, ok := c.GetInterrupt().Value(benchConnectionKey{}).(func(*sqlite3.Conn) error); ok {
			return hook(c)
		}
		return nil
	})
}

func benchConnectionCap(ctx context.Context) int64 { return runtimeStorage.SQLiteMemoryLimit(ctx) }

func BenchmarkSiteSlots(b *testing.B) {
	if b.N != 1 {
		b.Fatal("run with -benchtime=1x: this is one fixed 60-second open-loop trial")
	}
	if os.Getenv("DB_DSN") == "" {
		b.Fatal("DB_DSN must point at the disposable fixture")
	}
	db, err := sql.Open("postgres", os.Getenv("DB_DSN"))
	if err != nil {
		b.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(4)
	root := b.TempDir()
	seedPath := filepath.Join(root, "seed.sqlite")
	seed, err := sqlite3.Open(seedPath)
	if err != nil {
		b.Fatal(err)
	}
	if err = seed.Exec("CREATE TABLE posts(id INTEGER PRIMARY KEY, visitor_id TEXT, title TEXT, body TEXT, created_at TEXT); BEGIN"); err != nil {
		b.Fatal(err)
	}
	stmt, _, err := seed.Prepare("INSERT INTO posts VALUES (?, ?, ?, ?, ?)")
	if err != nil {
		b.Fatal(err)
	}
	for i := 1; i <= 200; i++ {
		stmt.BindInt(1, i)
		stmt.BindText(2, "bench-visitor")
		stmt.BindText(3, strings.Repeat("t", 20))
		stmt.BindText(4, strings.Repeat("b", 140))
		stmt.BindText(5, "2026-01-01T00:00:00Z")
		if err = stmt.Exec(); err != nil {
			b.Fatal(err)
		}
		stmt.Reset()
	}
	stmt.Close()
	if err = seed.Exec("COMMIT"); err != nil {
		b.Fatal(err)
	}
	seed.Close()
	data, err := os.ReadFile(seedPath)
	if err != nil {
		b.Fatal(err)
	}
	gate := NewSQLiteGate(4, 250*time.Millisecond)
	deps := Deps{SQLiteGate: gate, SQLiteTimeout: 5 * time.Second, Locks: &sync.Map{}, WriteLockWait: 250 * time.Millisecond, WriteTimeout: 5 * time.Second, MarkChanged: func() {}, ViewersAllow: func() bool { return true }}
	deps.SiteSlots = NewSiteSlots(1, gate, 250*time.Millisecond)
	services := make([]*Service, 1000)
	calls := make([]Call, 1000)
	matrix, _ := rules.PresetMatrix("wall")
	for i := range services {
		dir := filepath.Join(root, fmt.Sprint(i))
		path := filepath.Join(dir, "sqlite", "data.sqlite")
		if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			b.Fatal(err)
		}
		if err = os.WriteFile(path, data, 0600); err != nil {
			b.Fatal(err)
		}
		d := deps
		d.SQLitePath = path
		services[i] = New(db, d)
		calls[i] = Call{Owner: true, SiteID: fmt.Sprintf("00000000-0000-0000-0000-%012d", i+1), OwnerID: "bench-owner", SiteName: fmt.Sprint(i), ResourceName: "data", RuntimeDir: dir, Resource: rules.Resource{Name: "data", Access: &matrix}}
	}
	var mu sync.Mutex
	var openCaps, peakCaps int64
	ctx := context.WithValue(context.Background(), benchConnectionKey{}, func(c *sqlite3.Conn) error {
		cap := benchConnectionCap(c.GetInterrupt())
		mu.Lock()
		openCaps += cap
		peakCaps = max(peakCaps, openCaps)
		mu.Unlock()
		return c.Trace(sqlite3.TRACE_CLOSE, func(sqlite3.TraceEvent, any, any) error { mu.Lock(); openCaps -= cap; mu.Unlock(); return nil })
	})
	durations := make([][]time.Duration, 4)
	errorsByCode := map[string]int{}
	counts := [4]int{}
	rng := rand.New(rand.NewSource(104))
	zipf := rand.NewZipf(rng, 1.1, 1, 999)
	var wg sync.WaitGroup
	b.ResetTimer()
	start := time.Now()
	for i := 0; i < 30000; i++ {
		due := start.Add(time.Duration(i) * 2 * time.Millisecond)
		time.Sleep(time.Until(due))
		site := int(zipf.Uint64())
		draw := rng.Intn(100)
		kind := 0
		if draw >= 95 {
			kind = 3
		} else if draw >= 85 {
			kind = 2
		} else if draw >= 70 {
			kind = 1
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			s := services[site]
			in := TableInput{Call: calls[site], Table: "posts", ID: 1, Query: url.Values{"limit": {"20"}}, Values: map[string]json.RawMessage{"title": json.RawMessage(`"benchmark"`), "body": json.RawMessage(`"` + strings.Repeat("b", 140) + `"`)}}
			var out TableResult
			var err error
			switch kind {
			case 0:
				out, err = s.TableList(ctx, in)
			case 1:
				out, err = s.TableGet(ctx, in)
			case 2:
				in.Authorized = s.AuthorizeTableWrite(ctx, in, "add")
				out, err = s.TableAdd(ctx, in)
			case 3:
				in.Authorized = s.AuthorizeTableWrite(ctx, in, "edit")
				out, err = s.TableEdit(ctx, in)
			}
			code := ""
			if err != nil {
				if e, ok := err.(*Error); ok {
					code = e.Code
				} else {
					code = "other"
				}
			} else if out.Authorization != nil {
				code = out.Authorization.Code
			}
			elapsed := time.Since(due) // includes scheduling delay: no coordinated omission
			mu.Lock()
			counts[kind]++
			durations[kind] = append(durations[kind], elapsed)
			if code != "" {
				errorsByCode[code]++
			}
			mu.Unlock()
		}()
	}
	wg.Wait()
	b.StopTimer()
	if openCaps != 0 {
		b.Fatalf("connection caps still open: %d", openCaps)
	}
	summary := map[string]any{"sites": 1000, "seed": 104, "zipf_s": 1.1, "rate": 500, "scheduled_seconds": 60, "calls": 30000, "elapsed_seconds": time.Since(start).Seconds(), "errors": errorsByCode, "peak_open_caps_bytes": peakCaps}
	kinds := map[string]any{}
	for i, name := range []string{"list", "get", "add", "edit"} {
		sort.Slice(durations[i], func(a, c int) bool { return durations[i][a] < durations[i][c] })
		percentile := func(p int) float64 {
			return float64(durations[i][(len(durations[i])*p+99)/100-1]) / float64(time.Millisecond)
		}
		kinds[name] = map[string]any{"count": counts[i], "p50_ms": percentile(50), "p99_ms": percentile(99)}
	}
	summary["kinds"] = kinds
	status, err := os.ReadFile("/proc/self/status")
	if err != nil {
		b.Fatal(err)
	}
	for _, line := range strings.Split(string(status), "\n") {
		if strings.HasPrefix(line, "VmHWM:") {
			summary["VmHWM"] = strings.TrimSpace(strings.TrimPrefix(line, "VmHWM:"))
		}
	}
	encoded, _ := json.Marshal(summary)
	fmt.Printf("R1-S4-BENCH %s\n", encoded)
}
