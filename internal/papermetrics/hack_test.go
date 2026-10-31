package papermetrics

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestHackSnapshotAndExport(t *testing.T) {
	d := isolatedDB(t)
	ctx := context.Background()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := d.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	id := func(q string, args ...any) string {
		t.Helper()
		var id string
		if err := d.QueryRow(q, args...).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	user := func(name string) string { return id(`INSERT INTO users (username) VALUES ($1) RETURNING id`, name) }
	account, org, judge, p1, p2, pending := user("secret-holder"), user("secret-org"), user("secret-judge"), user("secret-one"), user("secret-two"), user("secret-pending")
	now := time.Now().UTC()
	start := now.Add(-4 * time.Hour).Truncate(time.Hour)
	event := id(`INSERT INTO events (account_id,slug,title,organiser_name,contact_email,join_code,judge_code,stage,created_at,starts_at,ends_at,expected_participants,results_published_at) VALUES ($1,'secret-slug','secret-title','secret-name','secret@example.com','secret-join','secret-judge','building',$2,$3,$4,100,now()) RETURNING id`, account, start.Add(-48*time.Hour), start, start.Add(24*time.Hour))
	// Newer idle active event has ordinal 2. A draft must have no minute rows.
	idleAccount, draftAccount := user("idle-holder"), user("draft-holder")
	exec(`INSERT INTO events (account_id,slug,title,organiser_name,contact_email,join_code,judge_code,stage) VALUES ($1,'idle','idle','idle','idle@invalid.test','idle-join','idle-judge','closed'),($2,'draft','draft','draft','draft@invalid.test','draft-join','draft-judge','draft')`, idleAccount, draftAccount)
	team := id(`INSERT INTO event_teams (event_id,slug,name,code,kept_after_leave) VALUES ($1,'secret-team','secret-team-name','secret-code',true) RETURNING id`, event)
	exec(`INSERT INTO event_teams (event_id,slug,name,code) VALUES ($1,'empty','empty','empty-code')`, event)
	exec(`INSERT INTO event_members (event_id,user_id,role,team_id) VALUES ($1,$2,'organiser',NULL),($1,$3,'judge',NULL),($1,$4,'participant',$6),($1,$5,'participant',$6)`, event, org, judge, p1, p2, team)
	exec(`INSERT INTO event_members (event_id,user_id,role,approval_status,joined_via,joined_at) VALUES ($1,$2,'participant','pending','application',$3)`, event, pending, now.AddDate(0, 0, -1))
	exec(`INSERT INTO event_entries (event_id,team_id,code_url,video_url,title) VALUES ($1,$2,'https://secret-code.test','https://secret-video.test','secret-entry')`, event, team)
	criterion := id(`INSERT INTO rubric_criteria (event_id,position,name,weight) VALUES ($1,1,'secret-criterion',1) RETURNING id`, event)
	exec(`INSERT INTO event_scores (event_id,team_id,judge_id,criterion_id,points,comment) VALUES ($1,$2,$3,$4,4,'secret-comment')`, event, team, judge, criterion)
	exec(`INSERT INTO event_assignments (event_id,team_id,judge_id) VALUES ($1,$2,$3)`, event, team, judge)
	exec(`INSERT INTO event_conflicts (event_id,team_id,judge_id) VALUES ($1,$2,$3)`, event, team, judge)
	exec(`INSERT INTO event_votes (event_id,team_id,voter_email) VALUES ($1,$2,'secret-voter@example.com')`, event, team)
	exec(`INSERT INTO event_announcements (event_id,title,body) VALUES ($1,'secret-announcement','secret-body')`, event)
	site := id(`INSERT INTO sites (user_id,name,site_url,active_version) VALUES ($1,'secret-team','',2) RETURNING id`, account)
	exec(`INSERT INTO sites (user_id,name,site_url,active_version) VALUES ($1,$2,'',1),($1,'deleted','',1)`, account, event)
	exec(`UPDATE sites SET deleted_at=now() WHERE name='deleted'`)
	exec(`INSERT INTO site_storage_resources (site_id,name,kind) VALUES ($1,'kv','kv')`, site)
	exec(`INSERT INTO site_storage_kv (site_id,resource_name,key,value) VALUES ($1,'kv','secret-key','"secret-value"')`, site)
	exec(`INSERT INTO versions (site_id,version_number,disk_path,status,created_at) VALUES ($1,1,'source-path-not-copied','ready',$2),($1,2,'source-path-not-copied','active',$3)`, site, start.Add(34*time.Minute), start.Add(94*time.Minute))
	var kvBytes int64
	if err := d.QueryRow(`SELECT pg_column_size(value) FROM site_storage_kv WHERE site_id=$1`, site).Scan(&kvBytes); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	write := func(name, path, body string) {
		t.Helper()
		full := filepath.Join(dir, "by-id", account, name, path)
		if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	write("secret-team", "current/index.html", "123")
	write("secret-team", "v1/index.html", "123")
	write("secret-team", "v2/index.html", "1234")
	write("secret-team", "runtime/sqlite/db.sqlite", "12345")
	write("secret-team", "runtime/files/bucket/file", "123456")
	write(event, "v1/index.html", "1234567")
	write(event, "runtime/files/bucket/file", "12345678")
	write("deleted", "v1/index.html", "must not count")
	exec(`INSERT INTO sites (user_id,name,site_url) VALUES ($1,'other','')`, idleAccount)
	// Match the Host walk: do not follow symlinks or count v-not-a-number.
	link := filepath.Join(dir, "by-id", account, "secret-team", "v1", "symlink")
	if err := os.Symlink(filepath.Join(dir, "by-id", account, event, "v1", "index.html"), link); err != nil {
		t.Fatal(err)
	}
	for _, ver := range []string{"v1", "v2"} {
		at := start.Add(3*time.Hour + 34*time.Minute)
		if ver == "v2" {
			at = at.Add(time.Hour)
		}
		if err := os.Chtimes(filepath.Join(dir, "by-id", account, "secret-team", ver), at, at); err != nil {
			t.Fatal(err)
		}
	}
	c := New(d, dir).WithHack()
	// Applying the migration repeatedly is safe even after the fresh schema.
	migration, err := os.ReadFile("../../db/migrations/zs-hack-paper-metrics.sql")
	if err != nil {
		t.Fatal(err)
	}
	exec(string(migration))
	exec(string(migration))
	if err := c.hackSnapshot(ctx, now); err != nil {
		t.Fatal(err)
	}
	var eventKey int64
	if err := d.QueryRow(`SELECT event_key FROM paper_hack_key WHERE event_id=$1`, event).Scan(&eventKey); err != nil {
		t.Fatal(err)
	}
	result, err := c.query(ctx, "raw-test", now.Format("2006-01-02"), now.Format("2006-01-02"), hackEventCols, `SELECT `+strings.Join(hackEventCols, ",")+` FROM paper_hack_event WHERE event_key=$1`, eventKey)
	if err != nil {
		t.Fatal(err)
	}
	var values map[string]any
	for _, row := range result.Rows {
		if toInt(row[1]) == eventKey {
			values = map[string]any{}
			for i, col := range result.Columns {
				values[col] = row[i]
			}
		}
	}
	if values == nil {
		t.Fatal("no oldest event")
	}
	expected := map[string]int64{"duration_hours": 24, "expected_participants": 100, "participants": 2, "organisers": 1, "judges": 1, "applications": 3, "applications_approved": 2, "teams": 2, "teams_with_entry": 1, "teams_kept_after_leave": 1, "team_size_p50": 0, "team_size_max": 2, "team_sites": 1, "team_site_bytes_sum": 21, "team_site_bytes_p50": 21, "team_site_versions_sum": 2, "event_storage_bytes": 19 + kvBytes, "submissions": 1, "entries_with_code_url": 1, "entries_with_video": 1, "scores": 1, "score_criteria": 1, "assignments": 1, "conflicts": 1, "votes": 1, "announcements": 1, "deploys_total": 3, "minutes_create_to_first_deploy": 30}
	for col, want := range expected {
		if got := toInt(values[col]); got != want {
			t.Errorf("%s=%v want %d", col, values[col], want)
		}
	}
	if values["results_published"] != true || values["stage"] != "building" {
		t.Fatalf("event flags=%v", values)
	}
	wantWeek := weekStart(start.Add(-48 * time.Hour)).Format("2006-01-02")
	if !strings.HasPrefix(fmt.Sprint(values["created_week"]), wantWeek) {
		t.Fatalf("week=%v want %s", values["created_week"], wantWeek)
	}
	// The database permits no identifying/free-text field other than enum stage.
	var textColumns int
	if err := d.QueryRow(`SELECT count(*) FROM information_schema.columns WHERE table_schema=current_schema() AND table_name IN ('paper_hack_event','paper_hack_minute') AND data_type IN ('text','character varying','uuid','json','jsonb','bytea') AND column_name<>'stage'`).Scan(&textColumns); err != nil {
		t.Fatal(err)
	}
	if textColumns != 0 {
		t.Fatal("identifying-capable columns")
	}
	for _, forbidden := range []string{"secret-", account, event, team, org, judge, p1, p2, pending, "@example.com"} {
		body, _ := json.Marshal(result)
		if strings.Contains(string(body), forbidden) {
			t.Fatalf("identifying text in snapshot: %s", forbidden)
		}
	}
	// One snapshot per day; the following day adds a new historical snapshot.
	exec(`INSERT INTO event_announcements (event_id,title,body) VALUES ($1,'second','body')`, event)
	if err := c.hackSnapshot(ctx, now); err != nil {
		t.Fatal(err)
	}
	var announcements int
	d.QueryRow(`SELECT announcements FROM paper_hack_event WHERE event_key=$1 ORDER BY snapshot_day DESC LIMIT 1`, eventKey).Scan(&announcements)
	if announcements != 1 {
		t.Fatal("rewrote same-day snapshot")
	}
	// A failing column query skips only that column.
	exec(`ALTER TABLE event_teams RENAME COLUMN kept_after_leave TO kept_temporarily`)
	if err := c.hackSnapshot(ctx, now.AddDate(0, 0, 1)); err != nil {
		t.Fatal(err)
	}
	var skipped bool
	d.QueryRow(`SELECT teams_kept_after_leave IS NULL,announcements FROM paper_hack_event WHERE event_key=$1 ORDER BY snapshot_day DESC LIMIT 1`, eventKey).Scan(&skipped, &announcements)
	if !skipped || announcements != 2 {
		t.Fatalf("column failure skipped=%v announcements=%d", skipped, announcements)
	}
	exec(`ALTER TABLE event_teams RENAME COLUMN kept_temporarily TO kept_after_leave`)
	// Host never writes Hack daily counts. Hack writes the additional metrics.
	host := New(d, dir)
	if err := host.dailySnapshot(ctx, now); err != nil {
		t.Fatal(err)
	}
	var count int
	d.QueryRow(`SELECT count(*) FROM paper_daily WHERE metric LIKE 'hack_%'`).Scan(&count)
	if count != 0 {
		t.Fatal("Host collected Hack metrics")
	}
	exec(`DELETE FROM paper_daily`)
	if err := c.dailySnapshot(ctx, now); err != nil {
		t.Fatal(err)
	}
	d.QueryRow(`SELECT count(*) FROM paper_daily WHERE metric LIKE 'hack_%'`).Scan(&count)
	if count != len(hackDailyMetrics)-1 {
		t.Fatalf("Hack daily metrics %d want %d", count, len(hackDailyMetrics))
	}
	for metric, want := range map[string]int64{"hack_events_total": 3, "hack_events_by_stage_building": 1, "hack_events_by_stage_closed": 1, "hack_participants_total": 2, "hack_judges_total": 1, "hack_teams_total": 2, "hack_applications_day": 1, "hack_team_sites_live": 2, "hack_event_websites_published": 1} {
		var got int64
		if err := d.QueryRow(`SELECT value FROM paper_daily WHERE metric=$1`, metric).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Errorf("%s=%d want %d", metric, got, want)
		}
	}
	// Start attribution is retained even when completion is in a later minute.
	c.refreshHack(ctx)
	slot := now.Truncate(hackSlot)
	runtimeEvent := c.hack.meta.Load().events["secret-slug"]
	r := &hackRequest{metrics: c.hack, start: slot, class: classAPIWrite, member: p1, write: true, deploy: true, writer: statusWriter{status: 200}}
	r.attach(runtimeEvent)
	r.finish(100 * time.Microsecond)
	c.flushHack(ctx, slot.Add(hackSlot))
	var requests, writes, deploys, members, maximum int64
	if err := d.QueryRow(`SELECT requests,data_writes,deploys,active_members,inflight_max FROM paper_hack_minute WHERE slot=$1 AND event_key=$2`, slot, eventKey).Scan(&requests, &writes, &deploys, &members, &maximum); err != nil {
		t.Fatal(err)
	}
	if requests != 1 || writes != 1 || deploys != 1 || members != 1 || maximum != 1 {
		t.Fatalf("flushed %d/%d/%d/%d/%d", requests, writes, deploys, members, maximum)
	}
	var n, sum, max int64
	if err := d.QueryRow(`SELECT n,sum_us,max_us FROM paper_hack_latency WHERE slot=$1 AND event_key=$2 AND class='api_write' AND status_group='2xx'`, slot, eventKey).Scan(&n, &sum, &max); err != nil {
		t.Fatal(err)
	}
	if n != 1 || sum != 100 || max != 100 {
		t.Fatalf("histogram moments=%d/%d/%d", n, sum, max)
	}
	exec(`INSERT INTO paper_hack_minute SELECT run_id,slot-interval '401 days',slot_secs,event_key,synthetic,requests,writes,data_writes,deploys,active_members,inflight_max,peak_rps_1s,arrivals_1s,writes_1s FROM paper_hack_minute LIMIT 1`)
	c.flushHack(ctx, slot.Add(hackSlot))
	d.QueryRow(`SELECT count(*) FROM paper_hack_minute WHERE slot<$1`, slot.AddDate(0, 0, -400)).Scan(&count)
	if count != 0 {
		t.Fatal("old minute rows not pruned")
	}
	// Default release rules exclude every seeded event (<5 teams). The override
	// only bypasses that filter; it never disables small-person-cell suppression.
	release, err := c.setHackEvents(ctx, now.Format("2006-01-02"), now.AddDate(0, 0, 1).Format("2006-01-02"), nil)
	if err != nil || len(release.Rows) != 0 {
		t.Fatalf("small events released: %v %+v", err, release)
	}
	override := httptest.NewRequest("GET", "/?include_small=1", nil)
	release, err = c.setHackEvents(ctx, now.Format("2006-01-02"), now.AddDate(0, 0, 1).Format("2006-01-02"), override)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range release.Rows {
		if toInt(row[1]) == eventKey {
			for i, col := range release.Columns {
				if col == "participants" && row[i] != nil {
					t.Fatal("unsuppressed participants")
				}
				if col == "participants_suppressed" && row[i] != true {
					t.Fatal("missing suppression flag")
				}
			}
		}
	}
	// Both new sets work through the admin HTTP exporter in JSON and CSV.
	for _, set := range []string{"hack_events", "hack_minutes", "hack_latency", "hack_phases", "runs"} {
		for _, format := range []string{"json", "csv"} {
			q := "include_small=1&set=" + set + "&format=" + format + "&to=" + now.AddDate(0, 0, 1).Format("2006-01-02")
			w := httptest.NewRecorder()
			c.Export(func(*http.Request) (bool, bool) { return true, true }).ServeHTTP(w, httptest.NewRequest("GET", "/v1/admin/paper-metrics?"+q, nil))
			if w.Code != 200 {
				t.Fatalf("%s/%s: %d %s", set, format, w.Code, w.Body.String())
			}
			if format == "json" {
				var result table
				if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || len(result.Rows) == 0 {
					t.Fatalf("json %v %s", err, w.Body.String())
				}
			} else {
				records, err := csv.NewReader(w.Body).ReadAll()
				if err != nil || len(records) < 2 {
					t.Fatalf("csv: %v", err)
				}
			}
		}
	}
}

func TestHackDailySkipsFailedQuery(t *testing.T) {
	d := isolatedDB(t)
	if _, err := d.Exec(`ALTER TABLE event_votes RENAME TO event_votes_unavailable`); err != nil {
		t.Fatal(err)
	}
	c := New(d, "").WithHack()
	if err := c.dailySnapshot(context.Background(), time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	var metrics, votes int
	if err := d.QueryRow(`SELECT count(*) FILTER (WHERE metric LIKE 'hack_%'),count(*) FILTER (WHERE metric='hack_votes_total') FROM paper_daily`).Scan(&metrics, &votes); err != nil {
		t.Fatal(err)
	}
	if metrics != len(hackDailyMetrics)-2 || votes != 0 {
		t.Fatalf("Hack metrics=%d votes=%d", metrics, votes)
	}
}

func mockHack(t testing.TB) (*Collector, *hackEventInfo) {
	t.Helper()
	c := New(nil, "", "simple-hack.app").WithHack()
	e := &hackEventInfo{id: "website", slug: "event", account: "account", key: 42}
	e.state.Store(&hackEventState{stage: "judging"})
	c.hack.meta.Store(&hackMetadata{events: map[string]*hackEventInfo{"event": e}, accounts: map[string]*hackEventInfo{"account": e}, members: map[string]map[string]struct{}{"event": {"member": {}}}, hosts: map[string]*hackEventInfo{"team.event.simple-hack.app": e}})
	return c, e
}
func TestHackCounterSlots(t *testing.T) {
	c, e := mockHack(t)
	slot := time.Now().UTC().Truncate(time.Minute)
	// Both requests start in one minute and overlap, including one crossing the
	// boundary. Until that request completes the start slot cannot be flushed.
	a := &hackRequest{metrics: c.hack, start: slot.Add(3 * time.Second), class: classAPIWrite, member: "a", write: true, deploy: true, writer: statusWriter{status: 201}}
	b := &hackRequest{metrics: c.hack, start: slot.Add(3 * time.Second), class: classAPIRead, member: "b", writer: statusWriter{status: 404}}
	a.attach(e)
	b.attach(e)
	a.finish(100 * time.Microsecond)
	if len(c.hack.drain(slot.Add(time.Minute))) != 0 {
		t.Fatal("flushed an in-flight request")
	}
	b.finish(70 * time.Second)
	rows := c.hack.drain(slot.Add(2 * time.Minute))
	if len(rows) != 1 {
		t.Fatalf("rows=%d", len(rows))
	}
	s := rows[0]
	if s.slot != slot || s.requests.Load() != 2 || s.writes.Load() != 1 || s.deploys.Load() != 1 || s.active.Load() != 2 || s.inflightMax.Load() != 2 || s.arrivals[3].Load() != 2 || s.writes1s[3].Load() != 1 {
		t.Fatal("wrong start-slot totals")
	}
	if s.cells[classAPIRead][2].Load().arrivals[3].Load() != 1 || s.cells[classAPIWrite][0].Load().writes1s[3].Load() != 1 {
		t.Fatal("class/status 1s bins")
	}
	if got := s.cells[classAPIRead][2].Load().hist.Snapshot(); got.N != 1 || got.MaxUS != 70000000 {
		t.Fatalf("wrong 4xx histogram: %+v", got)
	}
	if s.members.contains("a") {
		t.Fatal("member set retained after the slot")
	}
	if e.current.Load() != nil {
		t.Fatal("event cache retained a completed slot")
	}
	if len(c.hack.slots) != 0 || e.inflight.Load() != 0 {
		t.Fatal("retained completed counters")
	}
}
func TestHackActive(t *testing.T) {
	now := time.Now()
	for _, stage := range []string{"open", "building", "closed", "judging"} {
		if !hackActive(stage, time.Time{}, now) {
			t.Fatal(stage)
		}
	}
	if !hackActive("results", now.Add(-23*time.Hour), now) || hackActive("results", now.Add(-24*time.Hour), now) || hackActive("results", time.Time{}, now) || hackActive("archived", now, now) {
		t.Fatal("results window")
	}
}
func TestHackCapAndConcurrentBoundary(t *testing.T) {
	c, _ := mockHack(t)
	slot := time.Now().UTC().Truncate(time.Minute)
	for i := 0; i < 201; i++ {
		e := &hackEventInfo{key: int64(i + 1)}
		r := &hackRequest{metrics: c.hack, start: slot, class: classAPIRead, writer: statusWriter{status: 200}}
		r.attach(e)
		r.finish(time.Microsecond)
	}
	if len(c.hack.drain(slot.Add(time.Minute))) != 200 {
		t.Fatal("event cap")
	}
	c, e := mockHack(t)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 1000; j++ {
				r := &hackRequest{metrics: c.hack, start: slot, class: classAPIRead, member: "same", writer: statusWriter{status: 200}}
				r.attach(e)
				r.finish(time.Microsecond)
			}
		}()
	}
	wg.Wait()
	rows := c.hack.drain(slot.Add(time.Minute))
	if len(rows) != 1 || rows[0].requests.Load() != 8000 || rows[0].active.Load() != 1 || rows[0].cells[classAPIRead][0].Load().hist.Snapshot().N != 8000 {
		t.Fatal("concurrent counters")
	}
}
func TestLogHistogramMerge(t *testing.T) {
	var all, left, right LogHistogram
	samples := make([]int64, 20000)
	for i := range samples {
		samples[i] = int64(math.Exp2(float64(i%300)/8)) + int64(i%7)
		all.Observe(samples[i])
		if i%2 == 0 {
			left.Observe(samples[i])
		} else {
			right.Observe(samples[i])
		}
	}
	a := all.Snapshot()
	merged := MergeHistograms(left.Snapshot(), right.Snapshot())
	if a.N != merged.N || a.SumUS != merged.SumUS || a.MaxUS != merged.MaxUS || a.Offset != merged.Offset || fmt.Sprint(a.Counts) != fmt.Sprint(merged.Counts) {
		t.Fatal("merge differs from one histogram")
	}
	sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
	for _, q := range []float64{.5, .9, .99, .999} {
		actual := float64(samples[int(math.Ceil(float64(len(samples))*q))-1])
		estimate := merged.Quantile(q)
		if math.Abs(estimate/actual-1) > .044 {
			t.Fatalf("q=%v actual=%v estimate=%v", q, actual, estimate)
		}
	}
	for exponent := 1; exponent < 43; exponent++ {
		value := int64(1) << exponent
		if got := logBucket(value); got != exponent*8 {
			t.Fatalf("power of two %d => %d", value, got)
		}
	}
}
func TestHackRequestContextLifetime(t *testing.T) {
	c, _ := mockHack(t)
	type key struct{}
	base, cancel := context.WithCancel(context.WithValue(context.Background(), key{}, "kept"))
	defer cancel()
	var captured context.Context
	h := c.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = r.Context()
		ResolveHackEvent(r.Context(), "event", "judging", "member")
	}))
	r := httptest.NewRequest("GET", "https://simple-hack.app/v1/hack/events/event", nil).WithContext(base)
	h.ServeHTTP(httptest.NewRecorder(), r)
	if r.Context() != base || captured.Value(key{}) != "kept" || captured.Value(hackRequestKey{}) != nil {
		t.Fatal("context lifetime")
	}
	ResolveHackEvent(captured, "late", "open", "late-member")
	cancel()
	if captured.Err() != context.Canceled {
		t.Fatal("cancellation lost")
	}
}
func BenchmarkHackWrap(b *testing.B)         { benchmarkHack(b, false) }
func BenchmarkHackWrapParallel(b *testing.B) { benchmarkHack(b, true) }
func benchmarkHack(b *testing.B, parallel bool) {
	for _, kind := range []string{"event", "team_site", "team_write"} {
		b.Run(kind, func(b *testing.B) {
			c, _ := mockHack(b)
			h := c.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if kind == "event" {
					ResolveHackEvent(r.Context(), "event", "judging", "member")
				} else {
					ResolveHackSite(r.Context(), "account", "team", "member", kind == "team_write")
					if kind == "team_write" {
						HackWriteCommitted(r.Context())
					}
					HackDeploy(r.Context())
				}
			}))
			method, url := "GET", "https://simple-hack.app/v1/hack/events/event/people"
			if kind == "team_site" {
				url = "https://team.event.simple-hack.app/index.html"
			}
			if kind == "team_write" {
				method, url = "PUT", "https://team.event.simple-hack.app/v1/sites/team/storage/kv/store/keys/count"
			}

			run := func(n int) {
				r := httptest.NewRequest(method, url, nil)
				w := httptest.NewRecorder()
				for i := 0; i < n; i++ {
					h.ServeHTTP(w, r)
				}
			}
			b.ReportAllocs()
			b.ResetTimer()
			if parallel {
				b.RunParallel(func(pb *testing.PB) {
					r := httptest.NewRequest(method, url, nil)
					w := httptest.NewRecorder()
					for pb.Next() {
						h.ServeHTTP(w, r)
					}
				})
			} else {
				run(b.N)
			}
		})
	}
}
func TestRunConfigWhitelist(t *testing.T) {
	a := configHash(map[string]any{"person_hosts": "off", "DB_DSN": "secret-one", "ADMIN_API_KEY": "secret"})
	b := configHash(map[string]any{"person_hosts": "off", "DB_DSN": "different-secret"})
	if a != b || a == configHash(map[string]any{"person_hosts": "on"}) {
		t.Fatal("config whitelist")
	}
}

func TestHackStableKeysSyntheticAndPhases(t *testing.T) {
	d := isolatedDB(t)
	ctx := context.Background()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := d.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	id := func(q string, args ...any) string {
		t.Helper()
		var result string
		if err := d.QueryRow(q, args...).Scan(&result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	creator := id(`INSERT INTO users(username)VALUES('e2e-creator')RETURNING id`)
	makeEvent := func(slug string, creator any) string {
		account := id(`INSERT INTO users(username)VALUES($1)RETURNING id`, slug+"-holder")
		return id(`INSERT INTO events(slug,account_id,created_by,title,organiser_name,contact_email,join_code,judge_code,stage) VALUES($1,$2,$3,'not copied','not copied','private@example.com',$1||'-join',$1||'-judge','open')RETURNING id`, slug, account, creator)
	}
	older := makeEvent("older", nil)
	var events []string
	for _, slug := range []string{"e2e-event", "mock-event", "mock2-event", "local-event"} {
		events = append(events, makeEvent(slug, nil))
	}
	events = append(events, makeEvent("normal-address", creator))
	real := makeEvent("real", nil)
	c := New(d, "").WithRunConfig(map[string]any{"events": "hosted", "DB_DSN": "must-be-ignored"}).WithHack()
	c.refreshHack(ctx)
	var key int64
	if err := d.QueryRow(`SELECT event_key FROM paper_hack_key WHERE event_id=$1`, real).Scan(&key); err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		var synthetic bool
		if err := d.QueryRow(`SELECT synthetic FROM paper_hack_key WHERE event_id=$1`, event).Scan(&synthetic); err != nil || !synthetic {
			t.Fatal("synthetic rule", err)
		}
	}
	exec(`DELETE FROM events WHERE id=$1`, older)
	c.refreshHack(ctx)
	var after int64
	d.QueryRow(`SELECT event_key FROM paper_hack_key WHERE event_id=$1`, real).Scan(&after)
	if after != key {
		t.Fatal("key changed after deletion")
	}
	var missing int
	d.QueryRow(`SELECT count(*) FROM paper_hack_key WHERE event_id=$1`, older).Scan(&missing)
	if missing != 0 {
		t.Fatal("mapping outlived event")
	}
	// Unset starts_at uses the first captured open as its fixed origin.
	var unknown bool
	if err := d.QueryRow(`SELECT first_open_at IS NULL FROM paper_hack_key WHERE event_id=$1`, real).Scan(&unknown); err != nil || !unknown {
		t.Fatal("refresh invented a historical open", err)
	}
	var unknownOffset bool
	if err := d.QueryRow(`SELECT minute_offset IS NULL FROM paper_hack_phase WHERE event_key=$1 AND stage='open'`, key).Scan(&unknownOffset); err != nil || !unknownOffset {
		t.Fatal("unknown origin was not preserved", err)
	}
	if err := c.recordHackStage(ctx, real, "open", time.Now().UTC(), false); err != nil {
		t.Fatal(err)
	}
	var origin time.Time
	d.QueryRow(`SELECT first_open_at FROM paper_hack_key WHERE event_id=$1`, real).Scan(&origin)
	if origin.IsZero() {
		t.Fatal("no first-open fallback")
	}
	if err := c.recordHackStage(ctx, real, "judging", origin.Add(7*time.Minute), false); err != nil {
		t.Fatal(err)
	}
	var offset int
	var observed bool
	if err := d.QueryRow(`SELECT minute_offset,observed FROM paper_hack_phase WHERE event_key=$1 AND stage='judging'`, key).Scan(&offset, &observed); err != nil {
		t.Fatal(err)
	}
	if offset != 7 || observed {
		t.Fatal("stage-change phase")
	}
	exec(`UPDATE events SET submission_deadline=$2,judging_locked_at=$3,voting_opens_at=$2,voting_closes_at=$3,results_published_at=$3 WHERE id=$1`, real, origin.Add(20*time.Minute), origin.Add(30*time.Minute))
	if err := c.hackTimestampPhases(ctx); err != nil {
		t.Fatal(err)
	}
	var phases int
	d.QueryRow(`SELECT count(*) FROM paper_hack_phase WHERE event_key=$1 AND stage IN ('submission_deadline','judging_locked','results_published','voting_opens','voting_closes')`, key).Scan(&phases)
	if phases != 5 {
		t.Fatalf("timestamp phases=%d", phases)
	}
	// All active-window filters, including the membership cache, include judging
	// and recent results and exclude publication at/over the 24-hour boundary.
	member := id(`INSERT INTO users(username)VALUES('judge-member')RETURNING id`)
	exec(`INSERT INTO event_members(event_id,user_id,role)VALUES($1,$2,'judge')`, real, member)
	for _, window := range []struct {
		stage     string
		published time.Time
		active    bool
	}{
		{"judging", time.Time{}, true}, {"results", time.Now().Add(-23 * time.Hour), true}, {"results", time.Now().Add(-25 * time.Hour), false},
	} {
		var published any
		if !window.published.IsZero() {
			published = window.published
		}
		exec(`UPDATE events SET stage=$2,results_published_at=$3 WHERE id=$1`, real, window.stage, published)
		c.refreshHack(ctx)
		_, found := c.hack.meta.Load().members["real"][member]
		if found != window.active {
			t.Fatalf("membership window %s active=%v", window.stage, found)
		}
	}
	// Stage API instrumentation captures only after an actual successful change.
	h := c.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		exec(`UPDATE events SET stage='closed' WHERE id=$1`, real)
		RecordHackStage(r.Context(), real, "closed")
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", "https://simple-hack.app/v1/hack/events/real/stage", nil))
	c.refreshHackBackground(ctx)
	if err := d.QueryRow(`SELECT observed FROM paper_hack_phase WHERE event_key=$1 AND stage='closed' ORDER BY minute_offset DESC LIMIT 1`, key).Scan(&observed); err != nil || observed {
		t.Fatal("postcommit phase hook", err)
	}
	run, err := c.ensureRun(ctx)
	if err != nil {
		t.Fatal(err)
	}
	again, err := c.ensureRun(ctx)
	if err != nil || run != again {
		t.Fatal("duplicate run")
	}
	var runs, version int
	var hash, scheme, role string
	if err := d.QueryRow(`SELECT count(*) FROM paper_run`).Scan(&runs); err != nil {
		t.Fatal(err)
	}
	if err := d.QueryRow(`SELECT metric_def_version,config_hash,bucket_scheme,role FROM paper_run WHERE run_id=$1`, run).Scan(&version, &hash, &scheme, &role); err != nil {
		t.Fatal(err)
	}
	if runs != 1 || version != MetricDefVersion || len(hash) != 64 || scheme != BucketScheme || role != "hack" {
		t.Fatal("provenance")
	}
	// Small-event filtering applies to every per-event series, not only snapshots.
	for _, set := range []string{"hack_minutes", "hack_latency", "hack_phases"} {
		w := httptest.NewRecorder()
		c.Export(func(*http.Request) (bool, bool) { return true, true }).ServeHTTP(w, httptest.NewRequest("GET", "/v1/admin/paper-metrics?set="+set, nil))
		if w.Code != 200 {
			t.Fatalf("%s: %s", set, w.Body.String())
		}
		var result table
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || len(result.Rows) != 0 {
			t.Fatal("small series leaked", set, err)
		}
	}
}

func TestHackReleaseThresholds(t *testing.T) {
	d := isolatedDB(t)
	ctx := context.Background()
	c := New(d, "").WithHack()
	run, err := c.ensureRun(ctx)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	day := now.Format("2006-01-02")
	if _, err := d.Exec(`INSERT INTO paper_hack_event(snapshot_day,event_key,run_id,synthetic,stage,created_week,teams,participants,judges,organisers) VALUES($1,101,$2,false,'judging',$1,4,4,0,5),($1,102,$2,false,'judging',$1,5,5,4,0)`, day, run); err != nil {
		t.Fatal(err)
	}
	for _, key := range []int64{101, 102} {
		if _, err := d.Exec(`INSERT INTO paper_hack_minute(run_id,slot,slot_secs,event_key,synthetic,requests,writes,data_writes,deploys,active_members,inflight_max,peak_rps_1s,arrivals_1s,writes_1s) VALUES($1,$2,60,$3,false,1,0,0,0,4,1,1,array_fill(0,ARRAY[60]),array_fill(0,ARRAY[60]))`, run, now.Truncate(time.Minute), key); err != nil {
			t.Fatal(err)
		}
		if _, err := d.Exec(`INSERT INTO paper_hack_latency(run_id,slot,event_key,synthetic,class,status_group,n,sum_us,max_us,bytes_out,idx_offset,counts,arrivals_1s,writes_1s) VALUES($1,$2,$3,false,'api_read','2xx',1,100,100,0,53,ARRAY[1]::bigint[],array_fill(0,ARRAY[60]),array_fill(0,ARRAY[60]))`, run, now.Truncate(time.Minute), key); err != nil {
			t.Fatal(err)
		}
		if _, err := d.Exec(`INSERT INTO paper_hack_phase(run_id,event_key,synthetic,stage,phase_slot,minute_offset,origin_slot,observed) VALUES($1,$2,false,'judging',$3,2,$3,false)`, run, key, now.Truncate(time.Minute)); err != nil {
			t.Fatal(err)
		}
	}
	for _, set := range []string{"hack_events", "hack_minutes", "hack_latency", "hack_phases"} {
		for _, small := range []bool{false, true} {
			path := "/?set=" + set
			if small {
				path += "&include_small=1"
			}
			table, err := exportSets[set](c, ctx, day, day, httptest.NewRequest("GET", path, nil))
			if err != nil {
				t.Fatal(err)
			}
			want := 1
			if small {
				want = 2
			}
			if len(table.Rows) != want {
				t.Fatalf("%s override=%v rows=%d", set, small, len(table.Rows))
			}
			for _, row := range table.Rows {
				for i, col := range table.Columns {
					if col == "active_members" && row[i] != nil {
						t.Fatal("active member leak")
					}
					if col == "active_members_suppressed" && row[i] != true {
						t.Fatal("active member flag")
					}
					if col == "judges" && row[i] != nil {
						t.Fatal("judge leak")
					}
					if col == "participants" && !small && toInt(row[i]) != 5 {
						t.Fatal("threshold-five count suppressed")
					}
					if col == "participants_suppressed" && !small && row[i] != false {
						t.Fatal("threshold-five flag")
					}
				}
			}
		}
	}
}

func TestHackCommittedWrites(t *testing.T) {
	c, _ := mockHack(t)
	h := c.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ResolveHackSite(r.Context(), "account", "team", "member", true)
		if r.URL.Query().Has("commit") {
			HackWriteCommitted(r.Context())
			w.WriteHeader(500)
		}
	}))
	// A transport-200 failed tool operation must not count. Conversely, a
	// committed operation counts even if a later response/checkpoint fails.
	for _, path := range []string{"/?tool_error=1", "/?commit=1"} {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", path, nil))
	}
	rows := c.hack.drain(time.Now().UTC().Truncate(time.Minute).Add(time.Minute))
	if len(rows) != 1 || rows[0].requests.Load() != 2 || rows[0].writes.Load() != 1 {
		t.Fatal("write success was inferred from transport status")
	}
}

func TestHackJudgingWritesThroughMCP(t *testing.T) {
	c, _ := mockHack(t)
	h := c.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ResolveHackEvent(r.Context(), "event", "judging", "member")
		status := 403
		if r.URL.Query().Has("accepted") {
			status = 200
		}
		HackMutationResult(r.Context(), "PUT", "/v1/hack/events/event/scores", status)
	}))
	for _, path := range []string{"/mcp", "/mcp?accepted=1"} {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", path, nil))
	}
	rows := c.hack.drain(time.Now().UTC().Truncate(time.Minute).Add(time.Minute))
	if len(rows) != 1 || rows[0].requests.Load() != 2 || rows[0].writes.Load() != 1 || rows[0].dataWrites.Load() != 0 {
		t.Fatal("judging write was inferred from MCP transport status")
	}
}
