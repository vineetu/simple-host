package papermetrics

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestHackPanicCleanup(t *testing.T) {
	c, e := mockHack(t)
	h := c.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ResolveHackEvent(r.Context(), "event", "judging", "member")
		panic(http.ErrAbortHandler)
	}))
	func() {
		defer func() {
			if recover() != http.ErrAbortHandler {
				t.Error("panic not preserved")
			}
		}()
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "https://simple-hack.app/v1/hack/events/event/people", nil))
	}()
	rows := c.hack.drain(time.Now().UTC().Truncate(time.Minute).Add(time.Minute))
	if e.inflight.Load() != 0 || len(rows) != 1 {
		t.Fatalf("inflight=%d rows=%d", e.inflight.Load(), len(rows))
	}
	s := rows[0]
	if s.requests.Load() != 1 || s.pending.Load() != -1 || s.cells[classAPIRead][3].Load().hist.Snapshot().N != 1 {
		t.Fatal("panicked request not recorded as 5xx in its start slot")
	}
}
func seedReviewEvent(t *testing.T, d *sql.DB, stage string) string {
	t.Helper()
	var account, event string
	if err := d.QueryRow(`INSERT INTO users(username) VALUES('review-holder') RETURNING id`).Scan(&account); err != nil {
		t.Fatal(err)
	}
	starts := time.Now().UTC().Add(-30 * 24 * time.Hour)
	if err := d.QueryRow(`INSERT INTO events(slug,account_id,title,organiser_name,contact_email,join_code,judge_code,stage,starts_at,ends_at) VALUES('real-old',$1,'private','private','private@invalid.test','j','jj',$2,$3,$4) RETURNING id`, account, stage, starts, starts.Add(24*time.Hour)).Scan(&event); err != nil {
		t.Fatal(err)
	}
	return event
}
func TestHackRestartBaseline(t *testing.T) {
	d := isolatedDB(t)
	ctx := context.Background()
	event := seedReviewEvent(t, d, "archived")
	New(d, "").WithHack().refreshHack(ctx)
	if _, err := d.Exec(`UPDATE paper_hack_phase SET phase_slot=phase_slot-interval '2 hours'`); err != nil {
		t.Fatal(err)
	}
	New(d, "").WithHack().refreshHack(ctx)
	var count, baselines, observed int
	if err := d.QueryRow(`SELECT count(*),count(*) FILTER(WHERE baseline),count(*) FILTER(WHERE observed) FROM paper_hack_phase`).Scan(&count, &baselines, &observed); err != nil {
		t.Fatal(err)
	}
	t.Logf("two fresh collectors two hours apart: rows=%d baseline=%d observed=%d", count, baselines, observed)
	if count != 1 || baselines != 1 || observed != 0 {
		t.Fatalf("two fresh collectors: rows=%d baselines=%d observed=%d", count, baselines, observed)
	}
	if _, err := d.Exec(`UPDATE events SET stage='judging' WHERE id=$1`, event); err != nil {
		t.Fatal(err)
	}
	New(d, "").WithHack().refreshHack(ctx)
	New(d, "").WithHack().refreshHack(ctx)
	d.QueryRow(`SELECT count(*),count(*) FILTER(WHERE baseline),count(*) FILTER(WHERE observed) FROM paper_hack_phase`).Scan(&count, &baselines, &observed)
	if count != 2 || baselines != 1 || observed != 1 {
		t.Fatalf("genuine change: rows=%d baseline=%d observed=%d", count, baselines, observed)
	}
}
func TestHackHooksNeverWait(t *testing.T) {
	c, _ := mockHack(t)
	c.hack.refreshMu.Lock()
	defer c.hack.refreshMu.Unlock()
	done := make(chan struct{})
	go func() {
		c.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			for i := 0; i < 300; i++ {
				RecordHackStage(r.Context(), "event-id", "judging")
				HackPhasesChanged(r.Context())
			}
		})).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", "https://simple-hack.app/v1/hack/events/event/stage", nil))
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("stage hooks waited on refresh lock or database")
	}
	if len(c.hack.phases) != cap(c.hack.phases) || len(c.hack.refresh) != 1 {
		t.Fatal("missing bounded notices/nudge")
	}
}
func TestHackIdleMinuteOmitted(t *testing.T) {
	d := isolatedDB(t)
	seedReviewEvent(t, d, "judging")
	ctx := context.Background()
	c := New(d, "").WithHack()
	c.refreshHack(ctx)
	slot := time.Now().UTC().Truncate(time.Minute)
	c.flushHack(ctx, slot.Add(time.Minute))
	var count int
	if err := d.QueryRow(`SELECT count(*) FROM paper_hack_minute`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("idle rows=%d err=%v", count, err)
	}
	e := c.hack.meta.Load().events["real-old"]
	r := &hackRequest{metrics: c.hack, start: slot.Add(time.Minute), class: classAPIRead, writer: statusWriter{status: 200}}
	r.attach(e)
	r.finish(time.Microsecond)
	c.flushHack(ctx, slot.Add(2*time.Minute))
	if err := d.QueryRow(`SELECT count(*) FROM paper_hack_minute WHERE requests=1`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("active rows=%d err=%v", count, err)
	}
}
func TestPaperOffWritesNothing(t *testing.T) {
	d := isolatedDB(t)
	t.Setenv("PAPER_METRICS", "off")
	c := New(d, "").WithHack()
	c.Start(context.Background())
	t.Cleanup(func() { c.Stop(); c.Wait() })
	c.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		RecordHackStage(r.Context(), "ignored", "open")
		HackPhasesChanged(r.Context())
	})).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
	if err := c.RegisterRun(context.Background()); err == nil {
		t.Fatal("registered disabled run")
	}
	var count int
	if err := d.QueryRow(`SELECT count(*) FROM paper_run`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("off wrote %d runs: %v", count, err)
	}
}
func TestHostDailyExportPinned(t *testing.T) {
	d := isolatedDB(t)
	c := New(d, "")
	if _, err := d.Exec(`INSERT INTO paper_daily(day,metric,value)VALUES('2026-10-01','accounts',3)`); err != nil {
		t.Fatal(err)
	}
	// This exact Host response is unchanged from origin/main: no new metadata,
	// columns, joins or small-count suppression. Hack's separate shape is below.
	h := c.Export(func(*http.Request) (bool, bool) { return true, true })
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/?set=daily&from=2026-10-01&to=2026-10-01", nil))
	want := "{\"set\":\"daily\",\"from\":\"2026-10-01\",\"to\":\"2026-10-01\",\"columns\":[\"day\",\"metric\",\"value\"],\"rows\":[[\"2026-10-01\",\"accounts\",3]]}\n"
	if w.Code != 200 || w.Body.String() != want {
		t.Fatalf("Host daily changed: %s", w.Body.String())
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/?set=daily&format=csv&from=2026-10-01&to=2026-10-01", nil))
	if w.Body.String() != "day,metric,value\n2026-10-01,accounts,3\n" {
		t.Fatalf("Host CSV changed: %s", w.Body.String())
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/?from=2026-10-01&to=2026-10-01", nil))
	var summary map[string]json.RawMessage
	if err := json.Unmarshal(w.Body.Bytes(), &summary); err != nil {
		t.Fatal(err)
	}
	for key := range summary {
		if hackExportSet(key) || key == "metric_def_version" || key == "bucket_schemes" {
			t.Fatal("Host summary extended", key)
		}
	}
	t1, err := New(d, "").WithHack().setDaily(context.Background(), "2026-10-01", "2026-10-01", nil)
	if err != nil || !reflect.DeepEqual(t1.Columns, []string{"day", "metric", "value", "run_id", "metric_def_version", "value_suppressed"}) || t1.Rows[0][2] != nil || t1.Rows[0][5] != true {
		t.Fatal("Hack daily suppression/shape", t1, err)
	}
}
func TestHackDerivedSuppression(t *testing.T) {
	cols := []string{"judges", "assignments", "scores", "conflicts", "applications", "applications_approved"}
	cases := []struct {
		values []any
		hidden []bool
	}{
		{[]any{int64(3), int64(20), int64(30), int64(8), int64(10), int64(8)}, []bool{true, true, true, true, true, true}},
		{[]any{int64(5), int64(20), int64(30), int64(8), int64(10), int64(5)}, []bool{false, false, false, false, false, false}},
		{[]any{int64(5), int64(20), int64(30), int64(8), int64(20), int64(4)}, []bool{false, false, false, false, true, true}},
	}
	for _, tt := range cases {
		got := suppressPeople(table{Columns: append([]string{}, cols...), Rows: [][]any{tt.values}})
		for i, hidden := range tt.hidden {
			if (got.Rows[0][i] == nil) != hidden || got.Rows[0][len(cols)+i] != hidden {
				t.Fatalf("%s dependency suppression: %v", cols[i], got.Rows[0])
			}
		}
	}
	d := isolatedDB(t)
	seedReviewEvent(t, d, "archived")
	c := New(d, "").WithHack()
	c.refreshHack(context.Background())
	today := time.Now().UTC().Format("2006-01-02")
	phases, err := c.setHackPhases(context.Background(), today, today, httptest.NewRequest("GET", "/?include_small=1", nil))
	if err != nil || len(phases.Rows) != 1 {
		t.Fatal("phase export", err)
	}
	for _, col := range phases.Columns {
		if strings.Contains(col, "slot") || col == "recorded_at" {
			t.Fatal("absolute phase time exported", col)
		}
	}
}
func TestHackShardsExactMembers(t *testing.T) {
	c, e := mockHack(t)
	slot := time.Now().UTC().Truncate(time.Minute)
	var wg sync.WaitGroup
	for worker := 0; worker < 16; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				r := &hackRequest{metrics: c.hack, start: slot.Add(3 * time.Second), class: classDataWrite, shard: uint8(worker), member: fmt.Sprintf("member-%d", i), write: true, writer: statusWriter{status: 201}}
				r.attach(e)
				r.finish(10 * time.Microsecond)
			}
		}(worker)
	}
	wg.Wait()
	rows := c.hack.drain(slot.Add(time.Minute))
	if len(rows) != 1 {
		t.Fatal("lost shards")
	}
	s := rows[0]
	if s.requests.Load() != 1600 || s.writes.Load() != 1600 || s.dataWrites.Load() != 1600 || s.active.Load() != 100 || s.arrivals[3].Load() != 1600 || s.writes1s[3].Load() != 1600 {
		t.Fatal("shard sums or distinct counts incorrect")
	}
	h := s.cells[classDataWrite][0].Load().hist.Snapshot()
	if h.N != 1600 || h.SumUS != 16000 || h.MaxUS != 10 {
		t.Fatal("histogram shard merge", h)
	}
}

func TestHackDailyDependentSuppression(t *testing.T) {
	d := isolatedDB(t)
	ctx := context.Background()
	// Day one has a small judge cell and a small application remainder. Day two
	// reaches both thresholds; suppression must never bleed between days.
	_, err := d.Exec(`INSERT INTO paper_daily(day,metric,value) VALUES
 ('2026-10-01','hack_judges_total',3),('2026-10-01','hack_scores_total',36),('2026-10-01','hack_assignments_total',12),('2026-10-01','hack_conflicts_total',8),
 ('2026-10-01','hack_applications_total',20),('2026-10-01','hack_applications_approved_total',18),
 ('2026-10-02','hack_judges_total',5),('2026-10-02','hack_scores_total',36),('2026-10-02','hack_applications_total',20),('2026-10-02','hack_applications_approved_total',15)`)
	if err != nil {
		t.Fatal(err)
	}
	got, err := New(d, "").WithHack().setDaily(ctx, "2026-10-01", "2026-10-02", nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range got.Rows {
		suppressed := row[0] == "2026-10-01"
		if (row[2] == nil) != suppressed || row[5] != suppressed {
			t.Fatalf("daily dependency leaked or crossed dates: %v", row)
		}
	}
}

func TestHackExportHasNoAbsoluteEventTimes(t *testing.T) {
	d := isolatedDB(t)
	ctx := context.Background()
	event := seedReviewEvent(t, d, "building")
	c := New(d, "").WithHack()
	c.refreshHack(ctx)
	// Deliberately exceed JavaScript's exact integer range.
	const eventKey int64 = 9223372036854775806
	if _, err := d.Exec(`UPDATE paper_hack_key SET event_key=$2 WHERE event_id=$1`, event, eventKey); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`DELETE FROM paper_hack_phase`); err != nil {
		t.Fatal(err)
	}
	c = New(d, "").WithHack()
	c.refreshHack(ctx)
	now := time.Now().UTC()
	slot := now.Truncate(time.Minute)
	starts := slot.Add(-90 * time.Minute)
	if _, err := d.Exec(`UPDATE events SET starts_at=$2 WHERE id=$1`, event, starts); err != nil {
		t.Fatal(err)
	}
	e := c.hack.meta.Load().events["real-old"]
	r := &hackRequest{metrics: c.hack, start: slot, class: classAPIRead, writer: statusWriter{status: 200}}
	r.attach(e)
	r.finish(time.Millisecond)
	c.flushHack(ctx, slot.Add(time.Minute))
	if err := c.hackSnapshot(ctx, now); err != nil {
		t.Fatal(err)
	}
	day := now.Format("2006-01-02")
	for _, set := range []string{"hack_events", "hack_minutes", "hack_latency", "hack_phases"} {
		got, err := exportSets[set](c, ctx, day, day, httptest.NewRequest("GET", "/?include_small=1", nil))
		if err != nil {
			t.Fatal(err)
		}
		if len(got.Rows) == 0 {
			t.Fatal("missing fixture rows", set)
		}
		for _, row := range got.Rows {
			for i, col := range got.Columns {
				switch col {
				case "snapshot_day", "created_week", "slot", "phase_slot", "origin_slot", "origin_change", "recorded_at":
					t.Fatal("absolute event time exported", set, col)
				case "event_key":
					if row[i] != "9223372036854775806" {
						t.Fatalf("key lost precision/type: %T %v", row[i], row[i])
					}
				case "minute_offset":
					if set != "hack_phases" && toInt(row[i]) != 90 {
						t.Fatal("relative minute", row[i])
					}
				}
				if value, ok := row[i].(string); ok && (strings.Contains(value, "T") && strings.Contains(value, "Z") || len(value) == 10 && value[4] == '-' && value[7] == '-') {
					t.Fatal("absolute timestamp/date in event row", set, value)
				}
			}
		}
		// Pin actual JSON, not just the internal string representation.
		body, err := json.Marshal(got)
		if err != nil {
			t.Fatal(err)
		}
		var wire table
		if err = json.Unmarshal(body, &wire); err != nil {
			t.Fatal(err)
		}
		for _, row := range wire.Rows {
			for i, col := range wire.Columns {
				if col == "event_key" {
					if _, ok := row[i].(string); !ok {
						t.Fatal("JSON numeric key", set)
					}
				}
			}
		}
	}
	// A missing starts_at is unknown, never an epoch-shaped offset.
	if _, err := d.Exec(`UPDATE events SET starts_at=NULL WHERE id=$1`, event); err != nil {
		t.Fatal(err)
	}
	got, err := c.setHackMinutes(ctx, day, day, httptest.NewRequest("GET", "/?include_small=1", nil))
	if err != nil || got.Rows[0][1] != nil {
		t.Fatal("unknown start offset", err)
	}
}

func TestHackPhaseBoundaryOriginDedup(t *testing.T) {
	d := isolatedDB(t)
	ctx := context.Background()
	event := seedReviewEvent(t, d, "building")
	c := New(d, "").WithHack()
	c.refreshHack(ctx)
	origin := time.Now().UTC().Truncate(time.Minute).Add(59 * time.Second)
	if _, err := d.Exec(`UPDATE events SET stage='judging',updated_at=$2 WHERE id=$1`, event, origin); err != nil {
		t.Fatal(err)
	}
	// Refresh observes the committed stage on the next minute before the
	// captured post-commit notice is processed. Both describe one origin change.
	if err := c.recordHackPhase(ctx, event, "judging", origin.Add(2*time.Second), true, false, origin); err != nil {
		t.Fatal(err)
	}
	if err := c.recordHackPhase(ctx, event, "judging", origin, false, false, origin); err != nil {
		t.Fatal(err)
	}
	c.refreshHack(ctx)
	var count int
	var observed bool
	var capture time.Time
	if err := d.QueryRow(`SELECT count(*),bool_or(observed),min(phase_slot) FROM paper_hack_phase WHERE stage='judging'`).Scan(&count, &observed, &capture); err != nil {
		t.Fatal(err)
	}
	if count != 1 || observed || !capture.Equal(origin.Truncate(time.Minute)) {
		t.Fatalf("duplicate boundary transition: count=%d observed=%v capture=%v", count, observed, capture)
	}
	// A later real return to the same stage must remain a separate transition.
	if err := c.recordHackPhase(ctx, event, "closed", origin.Add(5*time.Minute), false, false, origin.Add(5*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := c.recordHackPhase(ctx, event, "judging", origin.Add(6*time.Minute), false, false, origin.Add(6*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := d.QueryRow(`SELECT count(*) FROM paper_hack_phase WHERE stage='judging'`).Scan(&count); err != nil || count != 2 {
		t.Fatal("collapsed a later origin change", count, err)
	}
}

func TestHackCellReuseIsEmpty(t *testing.T) {
	c, e := mockHack(t)
	slot := time.Now().UTC().Truncate(time.Minute)
	a := &hackRequest{metrics: c.hack, start: slot, class: classDataWrite, writer: statusWriter{status: 201}, write: true}
	a.attach(e)
	a.finish(100 * time.Millisecond)
	rows := c.hack.drain(slot.Add(time.Minute))
	c.hack.releaseCells(rows[0])
	b := &hackRequest{metrics: c.hack, start: slot.Add(time.Minute), class: classAPIRead, writer: statusWriter{status: 200}}
	b.attach(e)
	b.finish(time.Microsecond)
	rows = c.hack.drain(slot.Add(2 * time.Minute))
	s := rows[0]
	h := s.cells[classAPIRead][0].Load().hist.Snapshot()
	if s.requests.Load() != 1 || s.writes.Load() != 0 || s.dataWrites.Load() != 0 || h.N != 1 || h.SumUS != 1 || h.MaxUS != 1 {
		t.Fatal("reused cell retained previous samples", h)
	}
	if s.arrivals[0].Load() != 1 || s.writes1s[0].Load() != 0 {
		t.Fatal("reused cell retained second bins")
	}
}
