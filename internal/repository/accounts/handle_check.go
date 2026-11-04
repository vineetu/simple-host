package accounts

import (
	"context"
	"strings"
)

func HandleInUse(ctx context.Context, q Querier, userID, handle string) (bool, error) {
	h := strings.ToLower(handle)
	var taken bool
	err := q.QueryRowContext(ctx, `
		SELECT EXISTS (SELECT 1 FROM users WHERE handle = $1 AND id::text IS DISTINCT FROM $2)
		    OR EXISTS (SELECT 1 FROM handle_aliases WHERE handle = $1 AND user_id::text IS DISTINCT FROM $2)`,
		h, userID).Scan(&taken)
	if err != nil || taken {
		return taken, err
	}
	domain := CurrentPlatformDomain()
	if domain == "" {
		return false, nil
	}
	f := HostForms(h + "." + domain)
	err = q.QueryRowContext(ctx, `
		SELECT EXISTS (SELECT 1 FROM sites WHERE lower(custom_domain) IN ($1, $4) OR lower(previous_domain) IN ($1, $4))
		    OR EXISTS (SELECT 1 FROM legacy_hostnames WHERE lower(hostname) IN ($1, $4))
		    OR EXISTS (SELECT 1 FROM sites WHERE name = $2 AND deleted_at IS NULL AND user_id::text IS DISTINCT FROM $3)`, f[0], h, userID, f[1]).Scan(&taken)
	return taken, err
}
