package db

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/lib/pq"
)

// requiredColumns are the tables and columns this build reads.
//
// This list exists because of a real outage: a binary that queried
// users.display_name was deployed to a database where the migration adding it
// had never been run. Every user lookup then failed, so the owner page fell
// through to a 404 and site creation answered 500. Nothing failed at startup,
// and the fresh-install gate could not catch it, because that gate proves a NEW
// database works and says nothing about one that already exists.
//
// Add an entry here whenever a migration adds something the code depends on.
var requiredColumns = map[string][]string{
	// cp-ops-suspend.sql adds the suspended_* columns.
	"users": {"id", "username", "is_admin", "handle", "display_name", "handle_changed_at", "suspended_at", "suspended_reason"},
	// hash-api-keys.sql, cp-keys-key-names.sql
	"api_keys": {"id", "key_hash", "user_id", "name", "last4", "created_at", "last_used_at"},
	"sites":    {"id", "user_id", "name", "active_version", "visibility", "state", "custom_domain", "deleted_at", "previous_domain", "domain_cert_status", "domain_failing_since", "domain_lapse_notified_at", "previous_domain_failing_since", "suspended_at", "suspended_reason", "domain_token", "domain_proof_exempt", "idle_keep", "idle_kept_at", "idle_warned_at", "idle_removed_at", "idle_token_hash"},
	// w2-addr-idle-cleanup.sql adds the idle_* columns above.
	// cp-proof-domain-ownership.sql (also the two domain_* columns above)
	"domain_cert_requests": {"user_id", "domain", "requested_at"},
	"collection_items":     {"id", "site_id", "collection", "data", "submitted_by"},
	// private-collections.sql
	"collection_settings": {"site_id", "collection", "private"},
	"site_view_hourly":    {"site_id", "hour", "class", "views"},
	"instance_config":     {"key", "value"},
	"oauth_clients":       {"client_id", "client_secret_hash", "client_name", "redirect_uris", "token_endpoint_auth_method", "pkce_required", "dynamic"},
	"oauth_grants":        {"id", "user_id", "client_id", "scope", "resource", "last_used_at"},
	"oauth_codes":         {"code_hash", "client_id", "user_id", "redirect_uri", "code_challenge", "resource", "expires_at", "used_at", "grant_id"},
	"auth_tokens":         {"id", "link_token", "nonce_hash"},
	"oauth_tokens":        {"token_hash", "grant_id", "kind", "expires_at", "used_at"},
	// handle-aliases.sql
	"handle_aliases": {"handle", "user_id"},
	// visitor-signin-nonce.sql
	"oauth_states":             {"state", "return_to", "host", "site_id", "purpose", "nonce_hash"},
	"visitor_establish_tokens": {"once", "session_id", "host", "return_to", "nonce_hash"},
}

// VerifySchema refuses to start against a database that is behind the code.
//
// A server that will not start is far better than one that starts and silently
// answers 404 for every page: the first is noticed in seconds, the second was
// noticed by the owner.
func VerifySchema(ctx context.Context, database *sql.DB) error {
	tables := make([]string, 0, len(requiredColumns))
	for t := range requiredColumns {
		tables = append(tables, t)
	}
	rows, err := database.QueryContext(ctx, `
		SELECT table_name, column_name
		  FROM information_schema.columns
		 WHERE table_schema = current_schema() AND table_name = ANY($1)`, pq.Array(tables))
	if err != nil {
		return fmt.Errorf("inspect schema: %w", err)
	}
	defer rows.Close()

	have := map[string]map[string]bool{}
	for rows.Next() {
		var t, c string
		if err := rows.Scan(&t, &c); err != nil {
			return err
		}
		if have[t] == nil {
			have[t] = map[string]bool{}
		}
		have[t][c] = true
	}
	if err := rows.Err(); err != nil {
		return err
	}

	var missing []string
	for table, cols := range requiredColumns {
		if have[table] == nil {
			missing = append(missing, table+" (whole table)")
			continue
		}
		for _, c := range cols {
			if !have[table][c] {
				missing = append(missing, table+"."+c)
			}
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("database is behind this build; missing: %s\n"+
			"Run `simple-host migrate` against this database (on a small box, re-running the install\n"+
			"command does it), or apply the files in db/migrations/ by hand and record each with\n"+
			"`simple-host migrate -mark FILE`. A new database takes db/schema.sql",
			strings.Join(missing, ", "))
	}
	return nil
}
