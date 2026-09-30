// Package netusage records the box's own network use, in and out, from the
// kernel's interface counters (/proc/net/dev), so the admin page can show this
// month's transfer against the cloud's free allowance.
//
// The counters count from boot and restart at zero on a reboot, so a reading
// alone cannot answer "how much this month". Each sample adds the growth since
// the previous one to that day's row in net_usage_daily; the last reading and
// the boot it came from live in net_counter_state. A reboot (a new boot id) or
// a counter that went backwards starts from zero again: what the new boot has
// counted so far is growth since the previous sample, bar what went out
// between that sample and the reboot, which is lost.
package netusage

import (
	"bufio"
	"context"
	"database/sql"
	"fmt"
	"io"
	"log"
	"os"
	"strconv"
	"strings"
	"time"
)

// Paths are variables so tests can point them at fixtures.
var (
	procNetDev   = "/proc/net/dev"
	procNetRoute = "/proc/net/route"
	procBootID   = "/proc/sys/kernel/random/boot_id"
	procStat     = "/proc/stat"
)

// Counters is one interface's receive and transmit byte counts.
type Counters struct {
	RX, TX int64
}

// Sampler adds counter growth to net_usage_daily.
type Sampler struct {
	db    *sql.DB
	iface string // "" picks the interface of the default route on each sample
	now   func() time.Time
}

// New builds a sampler. iface is NETWORK_INTERFACE; empty means the interface
// the default route leaves by.
func New(db *sql.DB, iface string) *Sampler {
	return &Sampler{db: db, iface: strings.TrimSpace(iface), now: time.Now}
}

// Start samples now and then every interval until ctx ends.
func (s *Sampler) Start(ctx context.Context, interval time.Duration) {
	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			func() {
				defer func() {
					if rec := recover(); rec != nil {
						log.Printf("network usage sample panic: %v", rec)
					}
				}()
				c, cancel := context.WithTimeout(ctx, 30*time.Second)
				defer cancel()
				if err := s.Sample(c); err != nil {
					log.Printf("network usage sample: %v", err)
				}
			}()
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
		}
	}()
}

// Interface is the interface this sampler reads.
func (s *Sampler) Interface() (string, error) {
	if s.iface != "" {
		return s.iface, nil
	}
	return DefaultInterface()
}

// Sample reads the counters once and adds their growth since the previous
// sample to today's row (UTC). The first sample ever only records where the
// counters stand, unless the box booted this month: then everything counted
// since boot belongs to this month and is added too.
func (s *Sampler) Sample(ctx context.Context) error {
	iface, err := s.Interface()
	if err != nil {
		return err
	}
	cur, err := ReadCounters(iface)
	if err != nil {
		return err
	}
	boot := bootID()
	now := s.now().UTC()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var prevBoot string
	var prev Counters
	err = tx.QueryRowContext(ctx, `SELECT boot_id, rx_bytes, tx_bytes FROM net_counter_state WHERE iface = $1 FOR UPDATE`, iface).
		Scan(&prevBoot, &prev.RX, &prev.TX)
	var delta Counters
	since := now
	switch {
	case err == sql.ErrNoRows:
		if bt, ok := bootTime(); ok && !bt.Before(monthStart(now)) {
			delta, since = cur, bt
		}
	case err != nil:
		return err
	default:
		delta = Growth(prev, cur, prevBoot == boot)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO net_counter_state (iface, boot_id, rx_bytes, tx_bytes, counting_since, sampled_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (iface) DO UPDATE SET boot_id = EXCLUDED.boot_id, rx_bytes = EXCLUDED.rx_bytes,
			tx_bytes = EXCLUDED.tx_bytes, sampled_at = EXCLUDED.sampled_at
	`, iface, boot, cur.RX, cur.TX, since, now); err != nil {
		return err
	}
	if delta.RX > 0 || delta.TX > 0 {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO net_usage_daily (day, iface, rx_bytes, tx_bytes) VALUES ($1::date, $2, $3, $4)
			ON CONFLICT (day, iface) DO UPDATE SET rx_bytes = net_usage_daily.rx_bytes + EXCLUDED.rx_bytes,
				tx_bytes = net_usage_daily.tx_bytes + EXCLUDED.tx_bytes
		`, now.Format("2006-01-02"), iface, delta.RX, delta.TX); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Growth is how much the counters grew from prev to cur. On the same boot a
// counter that went backwards was reset (a driver reload), so cur is all
// growth since; on a new boot the counters began at zero.
func Growth(prev, cur Counters, sameBoot bool) Counters {
	g := func(p, c int64) int64 {
		if sameBoot && c >= p {
			return c - p
		}
		return c
	}
	return Counters{g(prev.RX, cur.RX), g(prev.TX, cur.TX)}
}

// Month is one month's use on one interface.
type Month struct {
	Iface string `json:"interface"`
	// RX and TX: bytes in and out this calendar month (UTC), up to now.
	RX int64 `json:"in_bytes"`
	TX int64 `json:"out_bytes"`
	// Since is when counting began, if that was during this month (the
	// month's figures cover only the time since); nil otherwise.
	Since *time.Time `json:"counting_since"`
	// SampledAt is the last stored sample.
	SampledAt *time.Time `json:"sampled_at"`
}

// ThisMonth is this month's use: every stored sample since the month began,
// plus what the counters have grown since the last one (so the figure is
// current, not up to an interval old). Nothing is written.
func (s *Sampler) ThisMonth(ctx context.Context) (Month, error) {
	iface, err := s.Interface()
	if err != nil {
		return Month{}, err
	}
	now := s.now().UTC()
	start := monthStart(now)
	m := Month{Iface: iface}
	if err := s.db.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(rx_bytes), 0), COALESCE(SUM(tx_bytes), 0)
		FROM net_usage_daily WHERE iface = $1 AND day >= $2::date
	`, iface, start.Format("2006-01-02")).Scan(&m.RX, &m.TX); err != nil {
		return Month{}, err
	}
	var prevBoot string
	var prev Counters
	var since, sampled time.Time
	err = s.db.QueryRowContext(ctx, `
		SELECT boot_id, rx_bytes, tx_bytes, counting_since, sampled_at FROM net_counter_state WHERE iface = $1
	`, iface).Scan(&prevBoot, &prev.RX, &prev.TX, &since, &sampled)
	if err == sql.ErrNoRows {
		return m, nil // not sampled yet
	}
	if err != nil {
		return Month{}, err
	}
	sampled = sampled.UTC()
	m.SampledAt = &sampled
	if since.After(start) {
		since = since.UTC()
		m.Since = &since
	}
	if cur, err := ReadCounters(iface); err == nil {
		g := Growth(prev, cur, prevBoot == bootID())
		m.RX += g.RX
		m.TX += g.TX
	}
	return m, nil
}

func monthStart(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
}

// ReadCounters reads one interface's byte counters from /proc/net/dev.
func ReadCounters(iface string) (Counters, error) {
	f, err := os.Open(procNetDev)
	if err != nil {
		return Counters{}, err
	}
	defer f.Close()
	return parseNetDev(f, iface)
}

func parseNetDev(r io.Reader, iface string) (Counters, error) {
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		name, rest, ok := strings.Cut(sc.Text(), ":")
		if !ok || strings.TrimSpace(name) != iface {
			continue
		}
		f := strings.Fields(rest)
		// receive: bytes packets errs drop fifo frame compressed multicast;
		// transmit: bytes ...
		if len(f) < 9 {
			return Counters{}, fmt.Errorf("%s: short line for %s", procNetDev, iface)
		}
		rx, err1 := strconv.ParseInt(f[0], 10, 64)
		tx, err2 := strconv.ParseInt(f[8], 10, 64)
		if err1 != nil || err2 != nil {
			return Counters{}, fmt.Errorf("%s: unreadable counters for %s", procNetDev, iface)
		}
		return Counters{RX: rx, TX: tx}, nil
	}
	if err := sc.Err(); err != nil {
		return Counters{}, err
	}
	return Counters{}, fmt.Errorf("no interface %q in %s", iface, procNetDev)
}

// DefaultInterface is the interface the IPv4 default route leaves by.
func DefaultInterface() (string, error) {
	f, err := os.Open(procNetRoute)
	if err != nil {
		return "", err
	}
	defer f.Close()
	return parseDefaultRoute(f)
}

func parseDefaultRoute(r io.Reader) (string, error) {
	sc := bufio.NewScanner(r)
	best, bestMetric := "", int64(-1)
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		// Iface Destination Gateway Flags RefCnt Use Metric Mask ...
		if len(f) < 8 || f[1] != "00000000" || f[7] != "00000000" {
			continue
		}
		metric, _ := strconv.ParseInt(f[6], 10, 64)
		if best == "" || metric < bestMetric {
			best, bestMetric = f[0], metric
		}
	}
	if best == "" {
		return "", fmt.Errorf("no default route in %s (set NETWORK_INTERFACE)", procNetRoute)
	}
	return best, nil
}

func bootID() string {
	b, err := os.ReadFile(procBootID)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// bootTime is when the box booted (btime in /proc/stat).
func bootTime() (time.Time, bool) {
	f, err := os.Open(procStat)
	if err != nil {
		return time.Time{}, false
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if v, ok := strings.CutPrefix(sc.Text(), "btime "); ok {
			n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
			if err != nil {
				return time.Time{}, false
			}
			return time.Unix(n, 0).UTC(), true
		}
	}
	return time.Time{}, false
}
