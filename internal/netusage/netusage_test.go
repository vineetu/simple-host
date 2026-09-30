package netusage

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "github.com/lib/pq"
)

const netDevFixture = `Inter-|   Receive                                                |  Transmit
 face |bytes    packets errs drop fifo frame compressed multicast|bytes    packets errs drop fifo colls carrier compressed
    lo: 122684936431 120220653    0    0    0     0          0         0 122684936431 120220653    0    0    0     0       0          0
enp0s6: 563264081267 353264814    0    0    0     0          0         0 840769437529 228776578    0    0    0     0       0          0
docker0: 2510304470 9854757    0    0    0     0          0         0 4843206443 9817024    0   24    0     0       0          0
`

func TestParseNetDev(t *testing.T) {
	c, err := parseNetDev(strings.NewReader(netDevFixture), "enp0s6")
	if err != nil || c.RX != 563264081267 || c.TX != 840769437529 {
		t.Fatalf("enp0s6 = %+v, %v", c, err)
	}
	if _, err := parseNetDev(strings.NewReader(netDevFixture), "eth0"); err == nil {
		t.Fatal("missing interface read without an error")
	}
	// "enp0s6" must not match a longer name that merely contains it.
	if _, err := parseNetDev(strings.NewReader(netDevFixture), "s6"); err == nil {
		t.Fatal("partial interface name matched")
	}
}

func TestParseDefaultRoute(t *testing.T) {
	route := "Iface\tDestination\tGateway \tFlags\tRefCnt\tUse\tMetric\tMask\t\tMTU\tWindow\tIRTT\n" +
		"docker0\t000011AC\t00000000\t0001\t0\t0\t0\t0000FFFF\t0\t0\t0\n" +
		"wlan0\t00000000\t0101A8C0\t0003\t0\t0\t600\t00000000\t0\t0\t0\n" +
		"enp0s6\t00000000\t0101000A\t0003\t0\t0\t100\t00000000\t0\t0\t0\n"
	got, err := parseDefaultRoute(strings.NewReader(route))
	if err != nil || got != "enp0s6" {
		t.Fatalf("default route = %q, %v; want enp0s6 (lowest metric)", got, err)
	}
	if _, err := parseDefaultRoute(strings.NewReader("Iface\tDestination\n")); err == nil {
		t.Fatal("no default route read without an error")
	}
}

func TestGrowth(t *testing.T) {
	cases := []struct {
		prev, cur, want Counters
		same            bool
	}{
		{Counters{100, 200}, Counters{150, 260}, Counters{50, 60}, true},
		{Counters{100, 200}, Counters{40, 260}, Counters{40, 60}, true}, // RX reset in place
		{Counters{100, 200}, Counters{40, 50}, Counters{40, 50}, false}, // rebooted
		{Counters{100, 200}, Counters{150, 260}, Counters{150, 260}, false},
	}
	for _, c := range cases {
		if got := Growth(c.prev, c.cur, c.same); got != c.want {
			t.Errorf("Growth(%+v, %+v, %v) = %+v, want %+v", c.prev, c.cur, c.same, got, c.want)
		}
	}
}

// isolatedDB applies db/schema.sql to a throwaway schema. Needs DB_DSN.
func isolatedDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("DB_DSN")
	if dsn == "" {
		t.Skip("DB_DSN not set")
	}
	admin, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { admin.Close() })
	schema := fmt.Sprintf("netusage_test_%d", time.Now().UnixNano())
	if _, err := admin.Exec(`CREATE SCHEMA ` + schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { admin.Exec(`DROP SCHEMA ` + schema + ` CASCADE`) })
	sep := "?"
	if strings.Contains(dsn, "?") {
		sep = "&"
	}
	db, err := sql.Open("postgres", dsn+sep+"search_path="+schema)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	ddl, err := os.ReadFile("../../db/schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(string(ddl)); err != nil {
		t.Fatalf("apply schema: %v", err)
	}
	return db
}

// fakeProc points the /proc paths at files the test controls.
type fakeProc struct{ dir string }

func newFakeProc(t *testing.T, boot time.Time) *fakeProc {
	t.Helper()
	p := &fakeProc{dir: t.TempDir()}
	old := [...]string{procNetDev, procBootID, procStat}
	procNetDev = filepath.Join(p.dir, "dev")
	procBootID = filepath.Join(p.dir, "boot_id")
	procStat = filepath.Join(p.dir, "stat")
	t.Cleanup(func() { procNetDev, procBootID, procStat = old[0], old[1], old[2] })
	p.boot("boot-1", boot)
	return p
}

func (p *fakeProc) counters(t *testing.T, rx, tx int64) {
	t.Helper()
	line := fmt.Sprintf("Inter-|\n face |\n  eth9: %d 1 0 0 0 0 0 0 %d 1 0 0 0 0 0 0\n", rx, tx)
	if err := os.WriteFile(procNetDev, []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
}

func (p *fakeProc) boot(id string, at time.Time) {
	os.WriteFile(procBootID, []byte(id+"\n"), 0o644)
	os.WriteFile(procStat, []byte(fmt.Sprintf("cpu 1 2 3\nbtime %d\n", at.Unix())), 0o644)
}

func TestSampleAndThisMonth(t *testing.T) {
	db := isolatedDB(t)
	ctx := context.Background()
	now := time.Date(2026, 9, 20, 10, 0, 0, 0, time.UTC)
	p := newFakeProc(t, time.Date(2026, 8, 30, 6, 0, 0, 0, time.UTC)) // booted last month
	s := New(db, "eth9")
	s.now = func() time.Time { return now }
	month := func() Month {
		t.Helper()
		m, err := s.ThisMonth(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return m
	}

	// First sample: booted before this month, so what the counters hold is
	// not this month's; nothing is counted yet.
	p.counters(t, 1000, 5000)
	if err := s.Sample(ctx); err != nil {
		t.Fatal(err)
	}
	if m := month(); m.RX != 0 || m.TX != 0 || m.Since == nil || !m.Since.Equal(now) {
		t.Fatalf("after the first sample: %+v", m)
	}

	// Growth since the last sample shows at once, before the next sample.
	p.counters(t, 1500, 7000)
	if m := month(); m.RX != 500 || m.TX != 2000 {
		t.Fatalf("live growth: %+v", m)
	}
	now = now.Add(time.Hour)
	if err := s.Sample(ctx); err != nil {
		t.Fatal(err)
	}
	if m := month(); m.RX != 500 || m.TX != 2000 {
		t.Fatalf("after the second sample: %+v", m)
	}

	// A reboot: the counters start again from zero and are all new growth.
	p.boot("boot-2", now)
	p.counters(t, 100, 300)
	now = now.Add(time.Hour)
	if err := s.Sample(ctx); err != nil {
		t.Fatal(err)
	}
	if m := month(); m.RX != 600 || m.TX != 2300 {
		t.Fatalf("after a reboot: %+v", m)
	}

	// Next month counts from its first day only, and counting began before it.
	now = time.Date(2026, 10, 1, 0, 30, 0, 0, time.UTC)
	p.counters(t, 150, 400)
	if err := s.Sample(ctx); err != nil {
		t.Fatal(err)
	}
	if m := month(); m.RX != 50 || m.TX != 100 || m.Since != nil {
		t.Fatalf("next month: %+v", m)
	}
}

// A box that booted this month: everything since boot is this month's, and
// counting is said to start at the boot.
func TestFirstSampleAfterBootThisMonth(t *testing.T) {
	db := isolatedDB(t)
	ctx := context.Background()
	boot := time.Date(2026, 9, 3, 8, 0, 0, 0, time.UTC)
	p := newFakeProc(t, boot)
	s := New(db, "eth9")
	s.now = func() time.Time { return time.Date(2026, 9, 20, 10, 0, 0, 0, time.UTC) }
	p.counters(t, 1000, 5000)
	if err := s.Sample(ctx); err != nil {
		t.Fatal(err)
	}
	m, err := s.ThisMonth(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if m.RX != 1000 || m.TX != 5000 || m.Since == nil || !m.Since.Equal(boot) {
		t.Fatalf("booted this month: %+v", m)
	}
}
