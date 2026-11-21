package repo

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

// MoveSiteBaseReport is what MoveSiteBase found (and, when applied, changed).
type MoveSiteBaseReport struct {
	CustomDomains   int // sites.custom_domain rows rewritten
	PreviousDomains int // sites.previous_domain rows rewritten
	RetiredNames    int // legacy_hostnames rows rewritten
	// Conflicts are names whose new form is already held by another row;
	// they are left as they are (lookups find either form).
	Conflicts int
	// Links are the rewritten names a site serves (its current or earlier
	// address), for the caller to link on disk: domains/<To> next to
	// domains/<From>.
	Links []MovedName
}

// MovedName is one rewritten name.
type MovedName struct {
	SiteID, UserID, SiteName string
	From, To                 string
}

// MoveSiteBase rewrites the stored one-label names under from (claimed free
// names and retired names: sites.custom_domain, sites.previous_domain,
// legacy_hostnames.hostname) to the same label under to, in one transaction
// (docs/history/site-base-domain-move.md, C3). Custom domains, multi-label
// names and names skip reports as reserved are never touched. With apply
// false nothing is written and the report says what would change. Running
// it again finds nothing left to do. Lookups accept both forms, so the order
// of this against a mode change does not matter and a rollback needs no
// reverse rewrite.
func MoveSiteBase(ctx context.Context, database *sql.DB, from, to string, skip func(label string) bool, apply bool) (MoveSiteBaseReport, error) {
	var rep MoveSiteBaseReport
	from = strings.ToLower(strings.TrimSpace(from))
	to = strings.ToLower(strings.TrimSpace(to))
	if from == "" || to == "" || from == to {
		return rep, errors.New("move-site-base: --from and --to must be two different domains")
	}
	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		return rep, err
	}
	defer tx.Rollback()
	if err := moveSiteDomains(ctx, tx, from, to, skip, apply, &rep); err != nil {
		return rep, err
	}
	if err := moveRetiredNames(ctx, tx, from, to, skip, apply, &rep); err != nil {
		return rep, err
	}
	if !apply {
		return rep, nil
	}
	return rep, tx.Commit()
}

// movableLabel returns the single label host carries under from, or false
// when host is not a movable name: another base, multi-label, www, or one
// skip reserves.
func movableLabel(host, from string, skip func(label string) bool) (string, bool) {
	host = strings.ToLower(strings.TrimSpace(host))
	if !strings.HasSuffix(host, "."+from) {
		return "", false
	}
	l := strings.TrimSuffix(host, "."+from)
	if l == "" || strings.Contains(l, ".") || l == "www" || (skip != nil && skip(l)) {
		return "", false
	}
	return l, true
}

// moveSiteDomains locks the sites holding a name under from and moves each
// movable custom_domain and previous_domain to to, unless another site
// already holds the new name.
func moveSiteDomains(ctx context.Context, tx *sql.Tx, from, to string, skip func(label string) bool, apply bool, rep *MoveSiteBaseReport) error {
	type row struct {
		id, userID, name string
		cur, prev        sql.NullString
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT id::text, user_id::text, name, custom_domain, previous_domain FROM sites
		WHERE lower(custom_domain) LIKE '%.' || $1 OR lower(previous_domain) LIKE '%.' || $1
		ORDER BY id FOR UPDATE`, from)
	if err != nil {
		return err
	}
	var sites []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.userID, &r.name, &r.cur, &r.prev); err != nil {
			rows.Close()
			return err
		}
		sites = append(sites, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	for _, r := range sites {
		for _, col := range []struct {
			name  string
			val   sql.NullString
			count *int
		}{{"custom_domain", r.cur, &rep.CustomDomains}, {"previous_domain", r.prev, &rep.PreviousDomains}} {
			l, ok := movableLabel(col.val.String, from, skip)
			if !col.val.Valid || !ok {
				continue
			}
			newHost := l + "." + to
			var busy bool
			if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM sites WHERE id::text <> $2 AND (lower(custom_domain) = $1 OR lower(previous_domain) = $1))`, newHost, r.id).Scan(&busy); err != nil {
				return err
			}
			if busy {
				rep.Conflicts++
				continue
			}
			if apply {
				if _, err := tx.ExecContext(ctx, `UPDATE sites SET `+col.name+` = $2 WHERE id::text = $1`, r.id, newHost); err != nil {
					return err
				}
			}
			*col.count++
			rep.Links = append(rep.Links, MovedName{SiteID: r.id, UserID: r.userID, SiteName: r.name, From: strings.ToLower(col.val.String), To: newHost})
		}
	}
	return nil
}

// moveRetiredNames moves each movable legacy_hostnames name under from to
// to, unless the new name is already retired.
func moveRetiredNames(ctx context.Context, tx *sql.Tx, from, to string, skip func(label string) bool, apply bool, rep *MoveSiteBaseReport) error {
	rows, err := tx.QueryContext(ctx, `SELECT hostname FROM legacy_hostnames WHERE lower(hostname) LIKE '%.' || $1 ORDER BY hostname`, from)
	if err != nil {
		return err
	}
	var retired []string
	for rows.Next() {
		var h string
		if err := rows.Scan(&h); err != nil {
			rows.Close()
			return err
		}
		retired = append(retired, h)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	for _, h := range retired {
		l, ok := movableLabel(h, from, skip)
		if !ok {
			continue
		}
		newHost := l + "." + to
		var exists bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM legacy_hostnames WHERE lower(hostname) = $1)`, newHost).Scan(&exists); err != nil {
			return err
		}
		if exists {
			rep.Conflicts++
			continue
		}
		if apply {
			if _, err := tx.ExecContext(ctx, `UPDATE legacy_hostnames SET hostname = $2 WHERE hostname = $1`, h, newHost); err != nil {
				return err
			}
		}
		rep.RetiredNames++
	}
	return nil
}
