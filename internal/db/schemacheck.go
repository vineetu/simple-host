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
	// w2-account-signin-email.sql adds signin_alerts and the two tables below.
	// v075-signup-source.sql adds the signup_* columns.
	"users":         {"id", "username", "is_admin", "handle", "display_name", "handle_changed_at", "suspended_at", "suspended_reason", "signin_alerts", "event_account", "signup_source", "signup_agent", "signup_method", "signup_inferred"},
	"email_changes": {"user_id", "new_email", "code_hash", "old_code_hash", "attempts", "expires_at"},
	// w2-signin-email-undo.sql adds old_code_hash above and this table.
	"email_change_undos": {"token_hash", "user_id", "old_email", "new_email", "created_at", "expires_at", "used_at"},
	"signin_alerts_sent": {"user_id", "summary", "day"},
	// hash-api-keys.sql, cp-keys-key-names.sql, w3-keys-scope-expiry.sql
	"api_keys": {"id", "key_hash", "user_id", "name", "last4", "created_at", "last_used_at", "scope", "expires_at", "idle_from"},
	"sites":    {"id", "user_id", "name", "active_version", "visibility", "state", "custom_domain", "deleted_at", "previous_domain", "domain_cert_status", "domain_failing_since", "domain_lapse_notified_at", "previous_domain_failing_since", "suspended_at", "suspended_reason", "domain_token", "domain_proof_exempt", "offline_at", "idle_keep", "idle_kept_at", "idle_warned_at", "idle_removed_at", "idle_token_hash", "purge_at", "idle_remove_at", "domain_release_at", "state_bytes", "data_bytes", "history_bytes", "legacy_data", "savers_mode", "keep_versions", "passcode_enc", "passcode_set_at", "passcode_generation"},
	// knobs-promised-dates.sql adds purge_at, idle_remove_at and domain_release_at.
	// w2-sites-offline.sql adds offline_at; w2-addr-idle-cleanup.sql the idle_* columns.
	// v075-site-keep-versions.sql adds keep_versions; v076-site-passcode.sql the passcode_* columns.
	// w2-sites-old-names.sql
	"site_name_aliases": {"user_id", "name", "site_id"},
	// v077-address-families.sql
	"address_families":     {"id", "user_id", "suffix", "site_prefix", "rank", "canonical", "token", "status", "last_error", "bound_at", "verified_at", "checked_at", "failing_since", "lapse_notified_at", "release_at", "cert_mode", "cert_name", "proof_exempt", "created_at"},
	"family_cert_requests": {"user_id", "host", "requested_at"},
	// cp-proof-domain-ownership.sql (also the two domain_* columns above)
	"domain_cert_requests": {"user_id", "domain", "requested_at"},
	// sd1-saved-data-safety.sql adds deleted_at, submitted_email and the three tables below;
	// sd1-saved-data-safety2-limits.sql adds sites.state_bytes/data_bytes (above),
	// sd1-saved-data-safety3-history-bytes.sql sites.history_bytes,
	// data_history.diff and idempotency_keys.site_id/ref/body_hash.
	"collection_items": {"id", "site_id", "collection", "data", "submitted_by", "deleted_at", "submitted_email", "version"},
	"data_history":     {"id", "site_id", "kind", "name", "item_id", "op", "prev", "actor_id", "actor_kind", "actor_email", "created_at", "diff"},
	"idempotency_keys": {"scope", "status", "etag", "created_at", "site_id", "ref", "body_hash"},
	"data_watch":       {"day", "site_id", "metric", "count", "last_at"},
	// private-collections.sql; sd2-saved-data-kinds.sql adds kind .. declared_at,
	// sites.savers_mode (above) and site_savers.
	// v072-notify-last-id.sql adds notify_last_id.
	"collection_settings": {"site_id", "collection", "private", "kind", "one_per_person", "notify", "notify_sent_at", "declared_at", "notify_last_id"},
	"site_savers":         {"site_id", "list", "pattern", "added_at"},
	"site_view_hourly":    {"site_id", "hour", "class", "views"},
	// w3-analytics-pages-referrers.sql
	"site_page_daily":     {"site_id", "day", "path", "views"},
	"site_referrer_daily": {"site_id", "day", "domain", "views"},
	// w3-network-usage.sql
	"traffic_daily":      {"day", "kind", "bytes", "requests"},
	"site_traffic_daily": {"site_id", "day", "kind", "bytes", "requests"},
	"net_usage_daily":    {"day", "iface", "rx_bytes", "tx_bytes"},
	"net_counter_state":  {"iface", "boot_id", "rx_bytes", "tx_bytes", "counting_since", "sampled_at"},
	"instance_config":    {"key", "value"},
	"oauth_clients":      {"client_id", "client_secret_hash", "client_name", "redirect_uris", "token_endpoint_auth_method", "pkce_required", "dynamic"},
	// w3-connection-device.sql adds device to oauth_grants and oauth_codes.
	"oauth_grants": {"id", "user_id", "client_id", "scope", "resource", "last_used_at", "device"},
	"oauth_codes":  {"code_hash", "client_id", "user_id", "redirect_uri", "code_challenge", "resource", "expires_at", "used_at", "grant_id", "device"},
	"auth_tokens":  {"id", "link_token", "nonce_hash"},
	"oauth_tokens": {"token_hash", "grant_id", "kind", "expires_at", "used_at"},
	// handle-aliases.sql
	"handle_aliases": {"handle", "user_id"},
	// visitor-signin-nonce.sql
	"oauth_states":             {"state", "return_to", "host", "site_id", "purpose", "nonce_hash"},
	"visitor_establish_tokens": {"once", "session_id", "host", "return_to", "nonce_hash"},
	// ask-daily-count.sql
	"ask_daily": {"day", "count"},
	// v061-setup-check-daily.sql
	"setup_check_daily": {"day", "count"},
	// v073-setup-assist-daily.sql
	"setup_assist_daily": {"day", "count"},
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
