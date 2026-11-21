package repo

import (
	"context"
	"database/sql"
	"strings"

	"github.com/lib/pq"

	accountsrepo "github.com/vsriram/simple-host/host/access/repo"
	"github.com/vsriram/simple-host/host/model"
)

// Sites' part of erasing an account (host/ops ErasePerson). The account
// row's cascade deletes the sites themselves, with their versions, saved
// data, lists and analytics.

// ErasureHolds counts what holds an erasure back on the sites side: event
// hostnames (event_domains cascade on the account, which would strand their
// DNS records) and sites the operator took down.
func ErasureHolds(ctx context.Context, tx *sql.Tx, userID string) (eventClaims, takenDown int, err error) {
	err = tx.QueryRowContext(ctx, `SELECT
		(SELECT count(*) FROM event_domains WHERE user_id = $1),
		(SELECT count(*) FROM sites WHERE user_id = $1 AND suspended_at IS NOT NULL)`, userID).Scan(&eventClaims, &takenDown)
	return eventClaims, takenDown, err
}

// LockAccountSiteRows locks every site row of the account, in id order (a
// save takes its site row after the saved data's, LockSavedData).
func LockAccountSiteRows(ctx context.Context, tx *sql.Tx, userID string) error {
	var n int64
	return tx.QueryRowContext(ctx, `SELECT count(*) FROM (SELECT 1 FROM sites WHERE user_id = $1 ORDER BY id FOR UPDATE) x`, userID).Scan(&n)
}

// EraseAccountSites records what the erase leaves for the caller to clean
// up after the commit: every site with its claimed addresses (retired, so
// never given out again) and the account's address families; and lets the
// account's retired names go to nobody.
func EraseAccountSites(ctx context.Context, tx *sql.Tx, userID string) (sites []model.ErasedSite, families []string, err error) {
	rows, err := tx.QueryContext(ctx, `SELECT id, name FROM sites WHERE user_id = $1 ORDER BY name`, userID)
	if err != nil {
		return nil, nil, err
	}
	for rows.Next() {
		var s model.ErasedSite
		if err := rows.Scan(&s.ID, &s.Name); err != nil {
			rows.Close()
			return nil, nil, err
		}
		sites = append(sites, s)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	for i := range sites {
		domains, err := RetireSiteNames(ctx, tx, sites[i].ID, accountsrepo.PlatformDomains()...)
		if err != nil {
			return nil, nil, err
		}
		for _, d := range domains {
			sites[i].Domains = append(sites[i].Domains, strings.ToLower(d))
		}
	}
	rows, err = tx.QueryContext(ctx, `SELECT suffix FROM address_families WHERE user_id = $1 ORDER BY suffix`, userID)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var f string
		if err := rows.Scan(&f); err != nil {
			return nil, nil, err
		}
		families = append(families, f)
	}
	return sites, families, rows.Err()
}

// ReleaseRetiredNames keeps the account's retired names held, by nobody
// (the account row's cascade would delete them and free the names).
func ReleaseRetiredNames(ctx context.Context, tx *sql.Tx, userID string) error {
	_, err := tx.ExecContext(ctx, `UPDATE legacy_hostnames SET user_id = NULL WHERE user_id = $1`, userID)
	return err
}

// LiveSiteCounts counts each account's sites not in Recently deleted (an
// account with none is left out).
func LiveSiteCounts(ctx context.Context, q Querier, accountIDs []string) (map[string]int, error) {
	rows, err := q.QueryContext(ctx, `SELECT user_id::text, count(*) FROM sites
		WHERE user_id = ANY($1::uuid[]) AND deleted_at IS NULL GROUP BY user_id`, pq.Array(accountIDs))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var id string
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			return nil, err
		}
		out[id] = n
	}
	return out, rows.Err()
}
