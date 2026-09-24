// Package geoip resolves an IP address to country, city and network operator
// ON THIS BOX, from two DB-IP Lite MaxMind-format files on disk.
//
// There is no network path in this package, by design: a caller's address must
// never be sent to a third party to find out where it is. If the files are
// missing or unreadable, lookups return a blank Info and one log line says why;
// nothing falls back to an online service.
//
// Data: DB-IP "IP to City Lite" and "IP to ASN Lite", CC BY 4.0. Attribution
// required wherever the results are shown: "IP Geolocation by DB-IP"
// <https://db-ip.com>. Refreshed monthly by scripts/geoip-refresh.sh, which
// swaps each file with an atomic rename; Watch notices the new file and
// reopens it without a restart.
//
// Memory: files are mmap'd read-only, so they live in the shared page cache
// and only the pages a lookup touches become resident; they are not copied
// onto the Go heap.
package geoip

import (
	"errors"
	"io/fs"
	"log"
	"net/netip"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/oschwald/maxminddb-golang/v2"
)

// File names inside the directory. These are the names DB-IP's downloads
// decompress to minus the month, so the refresh script can write them as-is.
const (
	CityFile = "dbip-city-lite.mmdb"
	ASNFile  = "dbip-asn-lite.mmdb"
)

// Info is what a lookup yields. Any field may be empty.
type Info struct {
	Country string
	City    string
	Org     string
}

// DB holds the two readers and reloads them when their files change.
// The zero value is not usable; call Open.
type DB struct {
	dir string

	mu   sync.RWMutex // held for reading across a lookup, for writing across a swap
	city *slot
	asn  *slot
}

type slot struct {
	name   string
	reader *maxminddb.Reader
	stamp  fileStamp // identity of the file the reader was opened from
	state  string    // last logged state, so a steady condition logs once
	seen   bool      // stamp is meaningful (false until the first check)
}

type fileStamp struct {
	size  int64
	mtime time.Time
	ino   uint64 // a rename-into-place always changes this, even at equal size/mtime
	ok    bool
}

// Open loads whatever databases are present in dir. It never fails: a missing
// directory or file just means blank lookups until the file appears.
func Open(dir string) *DB {
	d := &DB{
		dir:  dir,
		city: &slot{name: CityFile},
		asn:  &slot{name: ASNFile},
	}
	d.Reload()
	return d
}

// Dir is the directory the databases are read from.
func (d *DB) Dir() string { return d.dir }

// Watch polls the files every interval and reopens any that changed. The
// refresh script replaces files by rename, which changes size/mtime; polling a
// stat once a minute costs nothing and needs no inotify dependency.
func (d *DB) Watch(interval time.Duration) {
	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		for range t.C {
			d.Reload()
		}
	}()
}

// Reload checks both files and reopens any whose on-disk identity changed.
// A new file that fails to open leaves the previous reader in place.
func (d *DB) Reload() {
	for _, s := range []*slot{d.city, d.asn} {
		d.reloadSlot(s)
	}
}

func (d *DB) reloadSlot(s *slot) {
	path := filepath.Join(d.dir, s.name)
	st, err := os.Stat(path)
	var now fileStamp
	if err == nil {
		now = fileStamp{size: st.Size(), mtime: st.ModTime(), ok: true}
		if sys, ok := st.Sys().(*syscall.Stat_t); ok {
			now.ino = uint64(sys.Ino)
		}
	}

	d.mu.RLock()
	same := s.seen && now == s.stamp
	d.mu.RUnlock()
	if same {
		return
	}
	s.seen = true

	if !now.ok {
		// Gone (or never there). Serve blanks rather than a file that is no
		// longer what the operator has on disk.
		d.mu.Lock()
		old := s.reader
		s.reader, s.stamp = nil, now
		d.mu.Unlock()
		if old != nil {
			_ = old.Close()
		}
		msg := "missing"
		if !errors.Is(err, fs.ErrNotExist) {
			msg = err.Error()
		}
		s.logOnce("missing", "geoip: %s unavailable (%s) — caller locations will be blank; no network lookup is made. Install it with scripts/geoip-refresh.sh", path, msg)
		return
	}

	r, err := maxminddb.Open(path)
	if err != nil {
		d.mu.Lock()
		s.stamp = now // do not retry the same broken file every tick
		d.mu.Unlock()
		s.logOnce("bad:"+err.Error(), "geoip: cannot open %s: %v — keeping the previous data (blank if none)", path, err)
		return
	}
	d.mu.Lock()
	old := s.reader
	s.reader, s.stamp = r, now
	d.mu.Unlock()
	if old != nil {
		_ = old.Close() // safe: no lookup holds the read lock any more
	}
	s.state = "ok"
	log.Printf("geoip: loaded %s (%s, built %s)", path, r.Metadata.DatabaseType,
		r.Metadata.BuildTime().UTC().Format("2006-01-02"))
}

func (s *slot) logOnce(state, format string, args ...any) {
	if s.state == state {
		return
	}
	s.state = state
	log.Printf(format, args...)
}

// Loaded reports which databases are currently open.
func (d *DB) Loaded() (city, asn bool) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.city.reader != nil, d.asn.reader != nil
}

// Lookup resolves one address. Unparseable input, unknown addresses and
// missing databases all yield a blank (or partly blank) Info.
func (d *DB) Lookup(ip string) Info {
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return Info{}
	}
	addr = addr.Unmap()

	d.mu.RLock()
	defer d.mu.RUnlock()

	// DecodePath pulls out just the English names rather than decoding every
	// language the record carries. A failed path leaves the field blank.
	var out Info
	if r := d.city.reader; r != nil {
		if res := r.Lookup(addr); res.Found() {
			_ = res.DecodePath(&out.Country, "country", "names", "en")
			_ = res.DecodePath(&out.City, "city", "names", "en")
		}
	}
	if r := d.asn.reader; r != nil {
		if res := r.Lookup(addr); res.Found() {
			_ = res.DecodePath(&out.Org, "autonomous_system_organization")
		}
	}
	return out
}

// Close releases both readers.
func (d *DB) Close() {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, s := range []*slot{d.city, d.asn} {
		if s.reader != nil {
			_ = s.reader.Close()
			s.reader = nil
		}
	}
}

// Verify opens one file and checks it answers a well-known public address.
// The refresh script calls this (via `simple-host geoip-verify`) before
// swapping a download into place, so a truncated or wrong file never goes live.
func Verify(path string) (string, error) {
	r, err := maxminddb.Open(path)
	if err != nil {
		return "", err
	}
	defer r.Close()
	res := r.Lookup(netip.MustParseAddr("8.8.8.8"))
	if err := res.Err(); err != nil {
		return "", err
	}
	if !res.Found() {
		return "", errors.New("8.8.8.8 not found — not a full IP database")
	}
	var probe map[string]any
	if err := res.Decode(&probe); err != nil {
		return "", err
	}
	return r.Metadata.DatabaseType, nil
}
