// Command ip-country-load fills ip_country_ranges, the table that turns a
// visitor's address into a country ON THIS BOX.
//
// The lookup is local on purpose. The privacy page says only API-caller IPs
// ever reach a third party; sending visitor addresses to a geolocation service
// would make that false. The ingester resolves each address in memory and
// stores only the two-letter code.
//
// Default source is DB-IP's IP-to-Country Lite database: CC BY 4.0 (so it can
// be redistributed, unlike MaxMind's GeoLite2, which needs an account), a fresh
// build every month, IPv4 and IPv6 in one CSV of `start_ip,end_ip,country`.
// Attribution: "IP Geolocation by DB-IP" <https://db-ip.com>.
//
// The dataset is NOT vendored in this repo — it is ~4.5 MB gzipped and stale
// within a month. Fetch it at deploy time, or point -source at a local copy:
//
//	ip-country-load                                    # this month, from db-ip.com
//	ip-country-load -source /tmp/dbip-country-lite.csv.gz
//	ip-country-load -source https://example.invalid/ranges.csv
//
// Re-run it monthly. Reloading replaces the whole table in one transaction;
// aggregates already written keep the country they were resolved with.
package main

import (
	"compress/gzip"
	"context"
	"database/sql"
	"encoding/csv"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/lib/pq"
)

func main() {
	source := flag.String("source", defaultSource(), "CSV (optionally .gz) of start_ip,end_ip,country — file path or http(s) URL")
	dsn := flag.String("dsn", os.Getenv("DB_DSN"), "Postgres DSN (defaults to $DB_DSN)")
	timeout := flag.Duration("timeout", 10*time.Minute, "overall deadline")
	flag.Parse()

	if *dsn == "" {
		log.Fatal("DB_DSN is not set and -dsn was not given")
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	rc, err := open(ctx, *source)
	if err != nil {
		log.Fatalf("open %s: %v", *source, err)
	}
	defer rc.Close()

	database, err := sql.Open("postgres", *dsn)
	if err != nil {
		log.Fatalf("open postgres: %v", err)
	}
	defer database.Close()

	started := time.Now()
	n, err := load(ctx, database, rc)
	if err != nil {
		log.Fatalf("load: %v", err)
	}

	var ranges, countries int64
	if err := database.QueryRowContext(ctx,
		`SELECT count(*), count(DISTINCT country) FROM ip_country_ranges`).Scan(&ranges, &countries); err != nil {
		log.Fatalf("count: %v", err)
	}
	fmt.Printf("loaded %d ranges (%d countries) from %s in %s\n",
		ranges, countries, *source, time.Since(started).Round(time.Second))
	if n != ranges {
		log.Fatalf("read %d rows but table holds %d", n, ranges)
	}
}

// defaultSource is the current month's DB-IP Lite build. Their URLs are dated,
// so a hardcoded one would rot; the first days of a month may 404 before the
// new file is published, in which case pass last month's with -source.
func defaultSource() string {
	return "https://download.db-ip.com/free/dbip-country-lite-" +
		time.Now().UTC().Format("2006-01") + ".csv.gz"
}

func open(ctx context.Context, source string) (io.ReadCloser, error) {
	var body io.ReadCloser
	if strings.HasPrefix(source, "http://") || strings.HasPrefix(source, "https://") {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, source, nil)
		if err != nil {
			return nil, err
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			return nil, fmt.Errorf("http %s", resp.Status)
		}
		body = resp.Body
	} else {
		f, err := os.Open(source)
		if err != nil {
			return nil, err
		}
		body = f
	}

	if !strings.HasSuffix(source, ".gz") {
		return body, nil
	}
	zr, err := gzip.NewReader(body)
	if err != nil {
		body.Close()
		return nil, err
	}
	return readCloser{Reader: zr, closes: []io.Closer{zr, body}}, nil
}

type readCloser struct {
	io.Reader
	closes []io.Closer
}

func (r readCloser) Close() error {
	for _, c := range r.closes {
		c.Close()
	}
	return nil
}

// load replaces the table in one transaction: readers see the old ranges until
// it commits, and a truncated download leaves the previous data in place.
func load(ctx context.Context, database *sql.DB, r io.Reader) (int64, error) {
	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `TRUNCATE ip_country_ranges`); err != nil {
		return 0, err
	}
	stmt, err := tx.PrepareContext(ctx, pq.CopyIn("ip_country_ranges", "start_ip", "end_ip", "country"))
	if err != nil {
		return 0, err
	}

	cr := csv.NewReader(r)
	cr.FieldsPerRecord = 3
	cr.ReuseRecord = true

	var n int64
	for {
		rec, err := cr.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return 0, err
		}
		// DB-IP marks reserved and unassigned space "ZZ". Leaving those rows out
		// means addresses in them fall through to the ingester's 'XX', so there
		// is exactly one code for "we don't know" end to end.
		if rec[2] == "ZZ" || len(rec[2]) != 2 {
			continue
		}
		if _, err := stmt.ExecContext(ctx, rec[0], rec[1], rec[2]); err != nil {
			return 0, err
		}
		n++
	}

	if _, err := stmt.ExecContext(ctx); err != nil {
		return 0, err
	}
	if err := stmt.Close(); err != nil {
		return 0, err
	}
	return n, tx.Commit()
}
