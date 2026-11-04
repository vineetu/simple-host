package sites

import (
	"context"
	"database/sql"
	"errors"

	siterules "github.com/vsriram/simple-host/internal/rules/sites"
)

// RetirePlatformName keeps a released claimed <name>.<SITE_DOMAIN> with its
// site: it is recorded in legacy_hostnames, so the name redirects to the
// site's current address (or says the site was removed) instead of passing to
// whichever site happens to share its name, and nobody else can claim it.
// Any other host is ignored.
func RetirePlatformName(ctx context.Context, q Querier, host, siteID, userID string, platformDomains ...string) error {
	if _, ok := siterules.PlatformNameLabel(host, platformDomains); !ok {
		return nil
	}
	_, err := q.ExecContext(ctx, `
		INSERT INTO legacy_hostnames (hostname, site_id, user_id) VALUES (lower($1), $2, $3)
		ON CONFLICT (hostname) DO UPDATE SET site_id = EXCLUDED.site_id, user_id = EXCLUDED.user_id`,
		host, siteID, userID)
	return err
}

// RetireSiteNames records the site's claimed <name>.<SITE_DOMAIN> addresses
// (current and earlier) as retired names before the site is deleted; the
// rows outlive it (site_id becomes NULL). Returns every domain the site had,
// for the caller to unbind on disk.
func RetireSiteNames(ctx context.Context, q Querier, siteID string, platformDomains ...string) ([]string, error) {
	var userID, cur, prev string
	err := q.QueryRowContext(ctx, `SELECT user_id, COALESCE(custom_domain, ''), COALESCE(previous_domain, '') FROM sites WHERE id = $1`, siteID).Scan(&userID, &cur, &prev)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []string
	for _, d := range []string{cur, prev} {
		if d == "" {
			continue
		}
		out = append(out, d)
		if err := RetirePlatformName(ctx, q, d, siteID, userID, platformDomains...); err != nil {
			return nil, err
		}
	}
	return out, nil
}
