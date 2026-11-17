// Package upgrade checks compatibility before an upgrade changes an install.
package upgrade

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// Querier is implemented by *sql.DB and the connection holding the migration lock.
type Querier interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

type floor struct {
	release    string
	migrations []string
}

// These are frozen snapshots of Tracked() at the last tagged releases, not
// today's catalogues. Baseline files were never recorded and are excluded.
var floors = map[string]floor{
	"simple-host": {release: "v0.7.22", migrations: strings.Fields(`
ask-daily-count.sql
cp-domains-custom-domain-certs.sql
cp-gdpr-retired-handles.sql
cp-int-previous-domain-checks.sql
cp-keys-key-names.sql
cp-ops-schema-migrations.sql
cp-ops-suspend.sql
cp-proof-domain-ownership.sql
cp-recover-recently-deleted.sql
hack1-events.sql
hack10-event-icons.sql
hack11-account-preferences.sql
hack2-team-sites.sql
hack3-judging.sql
hack4-administration.sql
hack5-content.sql
hack6-registration.sql
hack7-mail-retries.sql
hack7-voting-directory.sql
hack8-judging-options.sql
hack9-event-websites.sql
knobs-promised-dates.sql
sd1-saved-data-safety.sql
sd1-saved-data-safety2-limits.sql
sd1-saved-data-safety3-history-bytes.sql
sd2-saved-data-kinds.sql
sd3-saved-data-personal-board.sql
sd3-saved-data-personal-private.sql
v061-setup-check-daily.sql
v071-event-account-flag.sql
v072-notify-last-id.sql
v073-setup-assist-daily.sql
v075-signup-source.sql
v075-site-keep-versions.sql
v076-site-passcode.sql
v077-address-families.sql
v078-api-growth.sql
v079-api-self-calls.sql
w2-account-signin-email.sql
w2-addr-idle-cleanup.sql
w2-signin-email-undo.sql
w2-sites-offline.sql
w2-sites-old-names.sql
w3-analytics-pages-referrers.sql
w3-connection-device.sql
w3-keys-scope-expiry.sql
w3-network-usage.sql
z6-hack-connector-selected-team.sql
z7-site-storage.sql
z8-hack-ink-color.sql
z9-home-page.sql
za-showcase-curation.sql
zb-storage-story.sql
zc-site-named-viewers.sql
zd-storage-access-presets.sql
ze-spam-checked-posts.sql
zf-signup-detail.sql
zg-oidc-signin.sql
zh-site-notifications.sql
zi-site-search-summary.sql
zj-hack-notifications.sql
zk-hack-directory-opt-in.sql
zk-hack-team-removal-notice.sql
zl-hack-team-finding.sql
zn-sso-hop.sql
zn-visitor-ai.sql
zo-sso-signout-confirm.sql
zp-hack-applications.sql
`)},
	"simple-hack": {release: "hack-v0.8.20", migrations: strings.Fields(`
0001-hack-judging-event-fk.sql
0002-password-sign-in.sql
0003-hack-notices-judges-messages.sql
0004-password-login.sql
0005-hack-entry-rules-chosen.sql
0006-hack-one-name.sql
0007-web-push.sql
0008-web-push-round-2.sql
0009-hack-checklists.sql
0010-hack-audience-optional.sql
0011-hack-event-theme.sql
0012-hack-assistant-usage.sql
0013-hack-organiser-judges.sql
0014-hack-overall-prizes.sql
ask-daily-count.sql
cp-domains-custom-domain-certs.sql
cp-gdpr-retired-handles.sql
cp-int-previous-domain-checks.sql
cp-keys-key-names.sql
cp-ops-schema-migrations.sql
cp-ops-suspend.sql
cp-proof-domain-ownership.sql
cp-recover-recently-deleted.sql
hack1-events.sql
hack10-event-icons.sql
hack11-account-preferences.sql
hack2-team-sites.sql
hack3-judging.sql
hack4-administration.sql
hack5-content.sql
hack6-registration.sql
hack7-mail-retries.sql
hack7-voting-directory.sql
hack8-judging-options.sql
hack9-event-websites.sql
knobs-promised-dates.sql
sd1-saved-data-safety.sql
sd1-saved-data-safety2-limits.sql
sd1-saved-data-safety3-history-bytes.sql
sd2-saved-data-kinds.sql
sd3-saved-data-personal-board.sql
sd3-saved-data-personal-private.sql
v061-setup-check-daily.sql
v071-event-account-flag.sql
v072-notify-last-id.sql
v073-setup-assist-daily.sql
v075-signup-source.sql
v075-site-keep-versions.sql
v076-site-passcode.sql
v077-address-families.sql
v078-api-growth.sql
v079-api-self-calls.sql
w2-account-signin-email.sql
w2-addr-idle-cleanup.sql
w2-signin-email-undo.sql
w2-sites-offline.sql
w2-sites-old-names.sql
w3-analytics-pages-referrers.sql
w3-connection-device.sql
w3-keys-scope-expiry.sql
w3-network-usage.sql
z6-hack-connector-selected-team.sql
z7-site-storage.sql
z8-hack-ink-color.sql
z9-home-page.sql
za-showcase-curation.sql
zb-storage-story.sql
zc-site-named-viewers.sql
zd-storage-access-presets.sql
ze-spam-checked-posts.sql
zf-signup-detail.sql
zg-oidc-signin.sql
zh-site-notifications.sql
zi-site-search-summary.sql
zj-hack-notifications.sql
zk-hack-directory-opt-in.sql
zk-hack-team-removal-notice.sql
zl-hack-team-finding.sql
zn-sso-hop.sql
zn-visitor-ai.sql
zo-sso-signout-confirm.sql
zp-hack-applications.sql
zq-hack-leave-event.sql
zr-hack-team-kept-after-leave.sql
zr-paper-metrics.sql
zs-hack-paper-metrics.sql
zt-hack-teams-redesign.sql
zv-hack-entry-summary.sql
zw-hack-wait-notices.sql
zw-sim-152-version-author.sql
zx-hack-entry-reminders.sql
zx-sim-151-owner-visitor-marks.sql
zz-sim-200-image-scan.sql
`)},
}

// Check reads only. Call after product validation and before recording a product
// or applying migrations. supported lists the release notes' supported recorded
// releases (D4-S11 supplies the single-train release list); the legacy tagged
// floor is always supported. Unknown recorded releases require an explicit list
// entry rather than a numeric comparison that might permit a downgrade.
func Check(ctx context.Context, q Querier, product string, supported ...string) error {
	f, ok := floors[product]
	if !ok {
		return fmt.Errorf("unknown upgrade product %q", product)
	}
	var hasRelease bool
	if err := q.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema='public' AND table_name='install_product' AND column_name='release')`).Scan(&hasRelease); err != nil {
		return err
	}
	if hasRelease {
		var release string
		err := q.QueryRowContext(ctx, `SELECT release FROM public.install_product WHERE "only"`).Scan(&release)
		if err != nil && err != sql.ErrNoRows {
			return err
		}
		if release != "" {
			for _, allowed := range append([]string{f.release}, supported...) {
				if normalizeRelease(release) == normalizeRelease(allowed) {
					return nil
				}
			}
			return fmt.Errorf("unsupported starting release %s for %s; upgrade to %s first", release, product, f.release)
		}
	}
	refuse := func() error {
		return fmt.Errorf("unsupported migration ledger for %s; upgrade to %s first", product, f.release)
	}
	var exists bool
	if err := q.QueryRowContext(ctx, `SELECT to_regclass('public.schema_migrations') IS NOT NULL`).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return refuse()
	}
	rows, err := q.QueryContext(ctx, `SELECT name FROM public.schema_migrations`)
	if err != nil {
		return err
	}
	applied := map[string]bool{}
	for rows.Next() {
		var name string
		if err = rows.Scan(&name); err != nil {
			rows.Close()
			return err
		}
		applied[name] = true
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, name := range f.migrations {
		if !applied[name] {
			return refuse()
		}
	}
	// The original account/site and connector tables predate the runner;
	// no tracked ledger entry proves these baseline objects exist.
	var baseline bool
	err = q.QueryRowContext(ctx, `SELECT to_regclass('public.users') IS NOT NULL AND to_regclass('public.sites') IS NOT NULL AND to_regclass('public.api_keys') IS NOT NULL AND to_regclass('public.oauth_grants') IS NOT NULL`).Scan(&baseline)
	if err != nil {
		return err
	}
	if !baseline {
		return refuse()
	}
	return nil
}

func normalizeRelease(s string) string {
	return strings.TrimPrefix(strings.TrimPrefix(s, "hack-"), "v")
}
