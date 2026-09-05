package analytics

import (
	"context"
	"database/sql"
	"net"

	"github.com/lib/pq"
)

// UnknownCountry is written when an address is not in ip_country_ranges (empty
// table, private address, a block the dataset does not cover). Kept as a real
// value rather than dropping the row so per-country totals still add up to the
// site's totals.
const UnknownCountry = "XX"

// countryLookupChunk bounds one lookup query's array parameter.
const countryLookupChunk = 1000

// resolveCountries maps raw addresses to ISO-3166 alpha-2 codes.
//
// Ranges never overlap, so the containing range is the LAST one starting at or
// below the address: an index descent, then one bound check. Written as a
// two-sided BETWEEN instead, an address in a gap between allocations would walk
// backwards through the whole table.
//
// Addresses are passed in and thrown away here — nothing about them is stored.
func resolveCountries(ctx context.Context, database *sql.DB, ips map[string]struct{}) (map[string]string, error) {
	out := make(map[string]string, len(ips))
	batch := make([]string, 0, countryLookupChunk)

	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		rows, err := database.QueryContext(ctx, `
			SELECT q.n, r.country
			FROM unnest($1::inet[]) WITH ORDINALITY AS q(ip, n)
			LEFT JOIN LATERAL (
				SELECT country, end_ip FROM ip_country_ranges
				WHERE start_ip <= q.ip
				ORDER BY start_ip DESC LIMIT 1
			) r ON r.end_ip >= q.ip
		`, pq.Array(batch))
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var n int
			var country sql.NullString
			if err := rows.Scan(&n, &country); err != nil {
				return err
			}
			code := UnknownCountry
			if country.Valid {
				code = country.String
			}
			out[batch[n-1]] = code
		}
		if err := rows.Err(); err != nil {
			return err
		}
		batch = batch[:0]
		return nil
	}

	for ip := range ips {
		// One malformed address would fail the whole query, taking the run's
		// view counts with it.
		if net.ParseIP(ip) == nil {
			out[ip] = UnknownCountry
			continue
		}
		batch = append(batch, ip)
		if len(batch) == countryLookupChunk {
			if err := flush(); err != nil {
				return nil, err
			}
		}
	}
	if err := flush(); err != nil {
		return nil, err
	}
	return out, nil
}
