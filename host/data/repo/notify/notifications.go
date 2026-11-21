package notify

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	accountsrepo "github.com/vsriram/simple-host/host/access/repo"
	"github.com/vsriram/simple-host/host/model"
	sitesrepo "github.com/vsriram/simple-host/host/sites/repo"
	"github.com/vsriram/simple-host/host/sqlkit"

	"github.com/lib/pq"
)

const notificationColumns = `n.id,n.site_id,n.owner_id,'',n.source,n.grant_id,n.app_name,n.label,n.url,n.events,n.storage,n.args_canonical,n.secret,n.prev_secret,n.prev_secret_until,n.auth_header_name,n.auth_header_value,n.state,n.verified_at,n.expires_at,n.failing_since,n.last_attempt_at,n.last_success_at,n.last_result,n.created_at,'',n.kind,n.subscriber_role,n.push_p256dh,n.push_auth,n.device_label,n.muted_resources`

func scanNotification(row interface{ Scan(...any) error }) (model.Notification, error) {
	var n model.Notification
	err := row.Scan(&n.ID, &n.SiteID, &n.OwnerID, &n.Site, &n.Source, &n.GrantID, &n.AppName, &n.Label, &n.URL, pq.Array(&n.Events), &n.Storage, &n.ArgsCanonical, &n.Secret, &n.PrevSecret, &n.PrevSecretUntil, &n.AuthHeaderName, &n.AuthHeaderValue, &n.State, &n.VerifiedAt, &n.ExpiresAt, &n.FailingSince, &n.LastAttemptAt, &n.LastSuccessAt, &n.LastResult, &n.CreatedAt, &n.Event, &n.Kind, &n.SubscriberRole, &n.PushP256dh, &n.PushAuth, &n.DeviceLabel, pq.Array(&n.MutedResources))
	return n, err
}
func GetNotification(ctx context.Context, q sqlkit.Querier, id string) (model.Notification, error) {
	n, err := scanNotification(q.QueryRowContext(ctx, `SELECT `+notificationColumns+` FROM site_notifications n WHERE n.id=$1`, id))
	if err != nil {
		return n, err
	}
	out, err := withSiteNames(ctx, q, []model.Notification{n}, false)
	if err != nil {
		return n, err
	}
	if len(out) == 0 {
		return model.Notification{}, sql.ErrNoRows
	}
	return out[0], nil
}
func ListNotifications(ctx context.Context, database *sql.DB, owner, site string) ([]model.Notification, error) {
	return queryNotifications(ctx, database, true, `SELECT `+notificationColumns+` FROM site_notifications n WHERE n.owner_id=$1 AND ($2='' OR n.site_id::text=$2) ORDER BY n.created_at DESC`, owner, site)
}

// queryNotifications scans the rows and fills each one's site name, dropping
// rows whose site is gone (and, with liveOnly, in Recently deleted).
func queryNotifications(ctx context.Context, database *sql.DB, liveOnly bool, query string, args ...any) ([]model.Notification, error) {
	rows, err := database.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Notification{}
	for rows.Next() {
		n, err := scanNotification(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	return withSiteNames(ctx, database, out, liveOnly)
}

// withSiteNames sets each notification's Site from one SitesByIDs call.
func withSiteNames(ctx context.Context, q sqlkit.Querier, list []model.Notification, liveOnly bool) ([]model.Notification, error) {
	if len(list) == 0 {
		return list, nil
	}
	ids := make([]string, 0, len(list))
	for _, n := range list {
		ids = append(ids, n.SiteID)
	}
	sites, err := sitesrepo.SitesByIDs(ctx, q, ids)
	if err != nil {
		return nil, err
	}
	out := list[:0]
	for _, n := range list {
		s, ok := sites[n.SiteID]
		if !ok || (liveOnly && s.Deleted) {
			continue
		}
		n.Site = s.Name
		out = append(out, n)
	}
	return out, nil
}

// lockSite serializes subscription creation, event sequencing and replay on
// one site.
func lockSite(ctx context.Context, tx *sql.Tx, site string) error {
	return sitesrepo.LockSite(ctx, tx, site)
}

// wakeDeliveries makes a resumed subscription's waiting deliveries due now.
func wakeDeliveries(ctx context.Context, tx *sql.Tx, id string) error {
	_, err := tx.ExecContext(ctx, `UPDATE site_event_deliveries SET next_attempt_at=now() WHERE subscription_id=$1 AND state='pending' AND (lease_until IS NULL OR lease_until<=now())`, id)
	return err
}

// sealLegacy preserves ciphertext and empty values, including during rotation.
func sealLegacy(id, value string, seal func(string, string) (string, error)) (string, error) {
	if value == "" || strings.HasPrefix(value, "enc1:") {
		return value, nil
	}
	return seal(id, value)
}

// storedNotification is the row SaveNotification replaces, read under its lock.
type storedNotification struct {
	site, state, secret, prev, auth string
	exists                          bool
	count                           int
}

// SaveNotification accepts plaintext secrets and stores only sealed values.
// Lock the site before counting, so concurrent REST and MCP creates share the cap.
func SaveNotification(ctx context.Context, database *sql.DB, n model.Notification, max int, cursor *int64, historyDays int, secrets model.NotificationSecrets, access AccessChecks) (bool, bool, error) {
	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		return false, false, err
	}
	defer tx.Rollback()
	old, err := lockStoredNotification(ctx, tx, n)
	if err != nil {
		return false, false, err
	}
	if err = checkSave(ctx, tx, n, old, max, access); err != nil {
		return false, false, err
	}
	n = withDefaultKindRole(n)
	prev, changed, err := sealForSave(&n, old, secrets)
	if err != nil {
		return false, false, err
	}
	if err = upsertNotification(ctx, tx, n, changed, prev); err != nil {
		return false, false, err
	}
	if old.state == "paused" || old.state == "suspended" {
		if err = wakeDeliveries(ctx, tx, n.ID); err != nil {
			return false, false, err
		}
	}
	truncated := false
	if cursor != nil {
		if truncated, err = replayFrom(ctx, tx, n, *cursor, historyDays); err != nil {
			return false, false, err
		}
	}
	return old.exists, truncated, tx.Commit()
}

// lockStoredNotification locks n's site and n's existing row, and counts the
// site's subscriptions.
func lockStoredNotification(ctx context.Context, tx *sql.Tx, n model.Notification) (storedNotification, error) {
	var old storedNotification
	if err := lockSite(ctx, tx, n.SiteID); err != nil {
		return old, err
	}
	err := tx.QueryRowContext(ctx, `SELECT site_id,state,secret,prev_secret,auth_header_value FROM site_notifications WHERE id=$1 FOR UPDATE`, n.ID).Scan(&old.site, &old.state, &old.secret, &old.prev, &old.auth)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return old, err
	}
	if old.site != "" && old.site != n.SiteID {
		return old, model.ErrNotificationSite
	}
	err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM site_notifications WHERE id=$1), (SELECT count(*) FROM site_notifications WHERE site_id=$2)`, n.ID, n.SiteID).Scan(&old.exists, &old.count)
	return old, err
}

// checkSave refuses a product save by someone who may no longer act for the
// site, and a new subscription over the site's cap.
func checkSave(ctx context.Context, tx *sql.Tx, n model.Notification, old storedNotification, max int, access AccessChecks) error {
	if n.Product {
		ok, err := access.ActsOnSite(ctx, tx, n.SiteID, n.OwnerID)
		if err != nil {
			return err
		}
		if !ok {
			return model.ErrNotificationAccess
		}
	}
	if !old.exists && old.count >= max {
		return model.ErrNotificationCap
	}
	return nil
}

// withDefaultKindRole fills kind and subscriber role for callers that predate
// them: MCP Events creates mcp-event rows, every other source
// webhook rows, and the subscriber is the owner.
func withDefaultKindRole(n model.Notification) model.Notification {
	if n.Kind == "" {
		n.Kind = "webhook"
		if n.Source == "mcp" {
			n.Kind = "mcp-event"
		}
	}
	if n.SubscriberRole == "" {
		n.SubscriberRole = "owner"
	}
	return n
}

// sealForSave seals n's secret and auth header in place and returns the
// sealed previous secret to keep and whether the signing secret changed.
// An existing row keeps its stored header. Plaintext is compared under the
// row lock, since random nonces cannot indicate a change; a stored secret
// this key cannot open (the key changed) is replaced, and no unreadable
// previous secret is kept.
func sealForSave(n *model.Notification, old storedNotification, secrets model.NotificationSecrets) (prev string, changed bool, err error) {
	prev = old.prev
	if old.exists {
		plain, openErr := secrets.Open(n.ID, old.secret)
		changed = openErr != nil || plain != n.Secret
		switch {
		case openErr != nil:
			prev = ""
		case changed:
			prev = old.secret
		}
		n.AuthHeaderValue, err = sealLegacy(n.ID+":header", old.auth, secrets.Seal)
	} else {
		n.AuthHeaderValue, err = secrets.Seal(n.ID+":header", n.AuthHeaderValue)
	}
	if err != nil {
		return "", false, err
	}
	if n.Secret, err = secrets.Seal(n.ID, n.Secret); err != nil {
		return "", false, err
	}
	prev, err = sealLegacy(n.ID, prev, secrets.Seal)
	return prev, changed, err
}

// upsertNotification writes n, reactivating an existing row on the same site.
// An id taken on another site is model.ErrNotificationSite.
func upsertNotification(ctx context.Context, tx *sql.Tx, n model.Notification, changed bool, prev string) error {
	saved, err := tx.ExecContext(ctx, `INSERT INTO site_notifications(id,site_id,owner_id,source,grant_id,app_name,label,url,events,storage,args_canonical,secret,auth_header_name,auth_header_value,verified_at,expires_at,kind,subscriber_role,push_p256dh,push_auth,device_label)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$19,$20,$21,$22,$23)
 ON CONFLICT(id) DO UPDATE SET prev_secret=$18,
 prev_secret_until=CASE WHEN $17 THEN now()+interval '24 hours' ELSE site_notifications.prev_secret_until END,
 secret=EXCLUDED.secret,auth_header_value=EXCLUDED.auth_header_value,expires_at=EXCLUDED.expires_at,verified_at=EXCLUDED.verified_at,grant_id=EXCLUDED.grant_id,app_name=EXCLUDED.app_name,
 events=EXCLUDED.events,label=EXCLUDED.label,push_p256dh=EXCLUDED.push_p256dh,push_auth=EXCLUDED.push_auth,device_label=EXCLUDED.device_label,
 state='active',suspended_at=NULL,suspend_emailed_at=NULL,failing_since=NULL,window_start=NULL,window_attempts=0,window_failures=0,updated_at=now() WHERE site_notifications.site_id=EXCLUDED.site_id`, n.ID, n.SiteID, n.OwnerID, n.Source, n.GrantID, n.AppName, n.Label, n.URL, pq.Array(n.Events), n.Storage, n.ArgsCanonical, n.Secret, n.AuthHeaderName, n.AuthHeaderValue, n.VerifiedAt, n.ExpiresAt, changed, prev, n.Kind, n.SubscriberRole, n.PushP256dh, n.PushAuth, n.DeviceLabel)
	if err != nil {
		return err
	}
	rows, err := saved.RowsAffected()
	if err == nil && rows == 0 {
		err = model.ErrNotificationSite
	}
	return err
}

// replayFrom queues every stored event after cursor that n matches, and
// reports whether history no longer reaches back to cursor. Replay applies
// the same actor rule as the live fan-out (fanOutSiteEvent), keyed by n's own
// kind, role and owner, so reconnecting with an old cursor never delivers an
// event the subscriber would not have received live.
func replayFrom(ctx context.Context, tx *sql.Tx, n model.Notification, cursor int64, historyDays int) (bool, error) {
	var oldest sql.NullInt64
	if err := tx.QueryRowContext(ctx, `SELECT min(seq) FROM site_events WHERE site_id=$1 AND created_at>=now()-$2*interval '1 day'`, n.SiteID, historyDays).Scan(&oldest); err != nil {
		return false, err
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO site_event_deliveries(event_seq,subscription_id) SELECT seq,$1 FROM site_events
 WHERE site_id=$2 AND seq>$3 AND name=ANY($4) AND ($5='' OR storage=$5) AND created_at>=now()-$6*interval '1 day'
 AND CASE
   WHEN $7<>'web-push' THEN push_actor<>'owner'
   ELSE CASE $8
     WHEN 'owner' THEN push_actor<>'owner' AND push_actor<>$9
     WHEN 'visitor' THEN push_target<>'' AND push_target=$9 AND push_target<>push_actor
     ELSE false END
   END
 ON CONFLICT DO NOTHING`, n.ID, n.SiteID, cursor, pq.Array(n.Events), n.Storage, historyDays, n.Kind, n.SubscriberRole, n.OwnerID)
	return oldest.Valid && cursor < oldest.Int64, err
}

func DeleteNotification(ctx context.Context, q sqlkit.Querier, id string) (bool, error) {
	r, err := q.ExecContext(ctx, `DELETE FROM site_notifications WHERE id=$1`, id)
	if err != nil {
		return false, err
	}
	n, err := r.RowsAffected()
	return n > 0, err
}
func PauseNotification(ctx context.Context, database *sql.DB, id string, paused bool) error {
	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var oldState string
	if err = tx.QueryRowContext(ctx, `SELECT state FROM site_notifications WHERE id=$1 FOR UPDATE`, id).Scan(&oldState); err != nil {
		return err
	}
	state := "active"
	if paused {
		state = "paused"
	}
	_, err = tx.ExecContext(ctx, `UPDATE site_notifications SET state=$2,updated_at=now(),suspended_at=NULL,suspend_emailed_at=NULL,failing_since=NULL,window_start=NULL,window_attempts=0,window_failures=0 WHERE id=$1`, id, state)
	if err != nil {
		return err
	}
	if !paused && (oldState == "paused" || oldState == "suspended") {
		if err = wakeDeliveries(ctx, tx, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// RotateNotification accepts a plaintext replacement and keeps a readable current
// secret for the rotation grace period. Both signing secrets use the same AD.
func RotateNotification(ctx context.Context, database *sql.DB, id, secret string, secrets model.NotificationSecrets) (*time.Time, error) {
	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var old, header string
	if err = tx.QueryRowContext(ctx, `SELECT secret,auth_header_value FROM site_notifications WHERE id=$1 FOR UPDATE`, id).Scan(&old, &header); err != nil {
		return nil, err
	}
	if _, err := secrets.Open(id, old); err != nil {
		old = ""
	}
	old, err = sealLegacy(id, old, secrets.Seal)
	if err != nil {
		return nil, err
	}
	header, err = sealLegacy(id+":header", header, secrets.Seal)
	if err != nil {
		return nil, err
	}
	secret, err = secrets.Seal(id, secret)
	if err != nil {
		return nil, err
	}
	var until *time.Time
	err = tx.QueryRowContext(ctx, `UPDATE site_notifications SET prev_secret=$3,secret=$2,auth_header_value=$4,
 prev_secret_until=CASE WHEN $3='' THEN NULL ELSE now()+interval '24 hours' END,updated_at=now() WHERE id=$1 RETURNING prev_secret_until`, id, secret, old, header).Scan(&until)
	if err != nil {
		return nil, err
	}
	return until, tx.Commit()
}

const legacySecrets = `(secret<>'' AND secret NOT LIKE 'enc1:%') OR (prev_secret<>'' AND prev_secret NOT LIKE 'enc1:%') OR (auth_header_value<>'' AND auth_header_value NOT LIKE 'enc1:%')`

// SealNotificationSecrets is a data-only sweep, safe to rerun alongside writers.
// The count is rows updated. Locked rows are skipped until the next start.
func SealNotificationSecrets(ctx context.Context, database *sql.DB, seal func(string, string) (string, error)) (int64, error) {
	pending, err := legacySecretIDs(ctx, database)
	if err != nil {
		return 0, err
	}
	var count int64
	for _, id := range pending {
		updated, err := sealLegacyRow(ctx, database, id, seal)
		if err != nil {
			return count, err
		}
		if updated {
			count++
		}
	}
	return count, nil
}

// legacySecretIDs lists subscriptions still holding an unsealed value.
func legacySecretIDs(ctx context.Context, database *sql.DB) ([]string, error) {
	rows, err := database.QueryContext(ctx, `SELECT id FROM site_notifications WHERE `+legacySecrets)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// sealLegacyRow seals one subscription's unsealed values in its own
// transaction. It reports false when the row is locked or already sealed.
func sealLegacyRow(ctx context.Context, database *sql.DB, id string, seal func(string, string) (string, error)) (bool, error) {
	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var secret, prev, header string
	err = tx.QueryRowContext(ctx, `SELECT secret,prev_secret,auth_header_value FROM site_notifications WHERE id=$1 AND (`+legacySecrets+`) FOR UPDATE SKIP LOCKED`, id).Scan(&secret, &prev, &header)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if secret, err = sealLegacy(id, secret, seal); err != nil {
		return false, err
	}
	if prev, err = sealLegacy(id, prev, seal); err != nil {
		return false, err
	}
	if header, err = sealLegacy(id+":header", header, seal); err != nil {
		return false, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE site_notifications SET secret=$2,prev_secret=$3,auth_header_value=$4 WHERE id=$1`, id, secret, prev, header); err != nil {
		return false, err
	}
	return true, tx.Commit()
}

func HasNotifications(ctx context.Context, q sqlkit.Querier, site string) (bool, error) {
	return anyRealOwner(ctx, q, `SELECT DISTINCT owner_id::text FROM site_notifications n WHERE n.site_id=$1 AND n.state IN ('active','suspended') AND (n.expires_at IS NULL OR n.expires_at>now())`, site)
}

// HasNotificationsFor is HasNotifications narrowed to subscriptions naming
// name, so a site event nobody asked for spends no budget and stores nothing.
func HasNotificationsFor(ctx context.Context, q sqlkit.Querier, site, name string) (bool, error) {
	return anyRealOwner(ctx, q, `SELECT DISTINCT owner_id::text FROM site_notifications n WHERE n.site_id=$1 AND $2=ANY(n.events) AND n.state IN ('active','suspended') AND (n.expires_at IS NULL OR n.expires_at>now())`, site, name)
}

// anyRealOwner reports whether the query's subscription owners include an
// account that is not an event account. No subscription costs one query.
func anyRealOwner(ctx context.Context, q sqlkit.Querier, query string, args ...any) (bool, error) {
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	var owners []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return false, err
		}
		owners = append(owners, id)
	}
	if err = rows.Err(); err != nil || len(owners) == 0 {
		return false, err
	}
	users, err := accountsrepo.UsersByIDs(ctx, q, owners)
	if err != nil {
		return false, err
	}
	for _, u := range users {
		if !u.EventAccount {
			return true, nil
		}
	}
	return false, nil
}
func EmitSiteEvent(ctx context.Context, database *sql.DB, site, id, name, storage string, data []byte) error {
	_, err := emitSiteEvent(ctx, database, site, id, name, storage, data, "", 0, "", "")
	return err
}

// EmitSiteEventOnce is EmitSiteEvent for a visitor-bearing event that fires
// once per visitor inside within: the RecentSiteEvent check runs under the
// site's row lock, so two simultaneous emissions store one event. stored is
// false when the event was skipped.
func EmitSiteEventOnce(ctx context.Context, database *sql.DB, site, id, name string, data []byte, visitor string, within time.Duration) (bool, error) {
	return emitSiteEvent(ctx, database, site, id, name, "", data, visitor, within, "", "")
}

// EmitSiteEventFor is EmitSiteEvent with the two facts web-push
// fan-out needs, known at emission time regardless of who wrote the row:
// pushActor is "" when the site owner performed the write, "owner" when it
// was explicitly the owner acting with no self-notify intent (reserved for
// callers that already resolved it), or the acting visitor's own users.id;
// pushTarget is the row's own visitor_id (who the data belongs to), or ""
// when the row has none. A web-push subscription never matches unless
// pushTarget equals that subscriber's own id (never another visitor's
// data), and a subscription never fires back to the exact actor that made
// the write (no self-notify), for either an owner's or a visitor's own
// devices. webhook/mcp-event subscriptions are unaffected: this only
// narrows kind='web-push' rows.
func EmitSiteEventFor(ctx context.Context, database *sql.DB, site, id, name, storage string, data []byte, pushActor, pushTarget string) error {
	_, err := emitSiteEvent(ctx, database, site, id, name, storage, data, "", 0, pushActor, pushTarget)
	return err
}
func emitSiteEvent(ctx context.Context, database *sql.DB, site, id, name, storage string, data []byte, visitor string, within time.Duration, pushActor, pushTarget string) (bool, error) {
	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	if err = lockSite(ctx, tx, site); err != nil {
		return false, err
	}
	if within > 0 {
		recent, err := RecentSiteEvent(ctx, tx, site, name, visitor, within)
		if err != nil || recent {
			return false, err
		}
	}
	// pushActor and pushTarget are stored on the event so a cursor replay
	// (replayFrom) can apply the same rule as the live fan-out.
	// They are columns, never part of the delivered data.
	var seq int64
	err = tx.QueryRowContext(ctx, `INSERT INTO site_events(site_id,event_id,name,storage,data,push_actor,push_target) VALUES($1,$2,$3,$4,$5,$6,$7) RETURNING seq`, site, id, name, storage, data, pushActor, pushTarget).Scan(&seq)
	if err != nil {
		return false, err
	}
	if err = fanOutSiteEvent(ctx, tx, seq, site, name, storage, pushActor, pushTarget); err != nil {
		return false, err
	}
	return true, tx.Commit()
}

// fanOutSiteEvent queues event seq for every subscription on site that names
// it. A subscription's storage filter narrows record and file events; a site
// event (empty storage) reaches every subscription naming it.
//
// An owner-authored write (pushActor 'owner') never reaches a webhook or MCP
// Events subscription: before web push such a write stored no event at all,
// and storing it now for push must not change that. A visitor's own write
// (empty pushActor, or their id) reaches them as before.
//
// A web-push subscription reaches the owner's devices unless the owner made
// the write, and a visitor's devices only when that visitor owns the row
// (pushTarget) and did not make this write. The owner branch also excludes
// pushActor == owner_id: an owner signed in on their own site (D1)
// writes with their own users.id as the actor, and must not get a push back
// on the device that made the write.
func fanOutSiteEvent(ctx context.Context, tx *sql.Tx, seq int64, site, name, storage, pushActor, pushTarget string) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO site_event_deliveries(event_seq,subscription_id) SELECT $1,id FROM site_notifications
 WHERE site_id=$2 AND state IN ('active','suspended') AND $3=ANY(events) AND ($4='' OR storage='' OR storage=$4) AND (expires_at IS NULL OR expires_at>now())
 AND CASE
   WHEN kind<>'web-push' THEN $5<>'owner'
   ELSE CASE subscriber_role
     WHEN 'owner' THEN $5<>'owner' AND $5<>owner_id::text
     WHEN 'visitor' THEN $6<>'' AND owner_id::text=$6 AND owner_id::text<>$5
     ELSE false END
   END`, seq, site, name, storage, pushActor, pushTarget)
	return err
}

// RecentSiteEvent reports whether a name event for visitor on site was stored
// inside the last within (the once-a-day rule for viewer.access_requested).
func RecentSiteEvent(ctx context.Context, q sqlkit.Querier, site, name, visitor string, within time.Duration) (bool, error) {
	var yes bool
	err := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM site_events WHERE site_id=$1 AND name=$2 AND data->>'visitor'=$3 AND created_at>now()-make_interval(secs=>$4))`, site, name, visitor, within.Seconds()).Scan(&yes)
	return yes, err
}

func ClaimNotificationDelivery(ctx context.Context, database *sql.DB, lease time.Duration, maxAttempts, historyDays int, window time.Duration) (model.NotificationDelivery, error) {
	var d model.NotificationDelivery
	// Expired leases are failed attempts, even if their worker never finished.
	_, err := database.ExecContext(ctx, `WITH expired AS (
 UPDATE site_event_deliveries SET state=CASE WHEN attempts>=$1 THEN 'failed' ELSE state END,lease_until=NULL,last_category='timeout'
 WHERE state='pending' AND lease_until<=now() RETURNING subscription_id,last_attempt_at
 ), counts AS (
 SELECT e.subscription_id,min(e.last_attempt_at) AS first_attempt,count(*) FILTER (WHERE e.last_attempt_at>=n.window_start) AS failures
 FROM expired e JOIN site_notifications n ON n.id=e.subscription_id GROUP BY e.subscription_id
 ) UPDATE site_notifications n SET window_failures=window_failures+counts.failures,
 failing_since=COALESCE(failing_since,counts.first_attempt),last_result='timeout' FROM counts WHERE n.id=counts.subscription_id`, maxAttempts)
	if err != nil {
		return d, err
	}
	err = database.QueryRowContext(ctx, `WITH due AS (
 SELECT d.event_seq,d.subscription_id FROM site_event_deliveries d JOIN site_notifications n ON n.id=d.subscription_id JOIN site_events e ON e.seq=d.event_seq
 WHERE d.state='pending' AND d.attempts<$2 AND e.created_at>=now()-$3*interval '1 day' AND d.next_attempt_at<=now() AND (d.lease_until IS NULL OR d.lease_until<=now()) AND n.state='active' AND (n.expires_at IS NULL OR n.expires_at>now())
 ORDER BY d.next_attempt_at,d.event_seq LIMIT 1 FOR UPDATE OF d SKIP LOCKED
 ), claimed AS (UPDATE site_event_deliveries d SET lease_until=now()+$1*interval '1 millisecond',attempts=attempts+1,last_attempt_at=now() FROM due WHERE d.event_seq=due.event_seq AND d.subscription_id=due.subscription_id RETURNING d.*),
 accounted AS (UPDATE site_notifications n SET
 window_attempts=CASE WHEN window_start IS NULL OR window_start<=now()-$4*interval '1 millisecond' THEN 1 ELSE window_attempts+1 END,
 window_failures=CASE WHEN window_start IS NULL OR window_start<=now()-$4*interval '1 millisecond' THEN 0 ELSE window_failures END,
 window_start=CASE WHEN window_start IS NULL OR window_start<=now()-$4*interval '1 millisecond' THEN now() ELSE window_start END,
 last_attempt_at=now()
 FROM claimed d WHERE n.id=d.subscription_id RETURNING n.id)
 SELECT d.event_seq,d.subscription_id,d.attempts,d.lease_until,d.last_attempt_at,e.event_id,e.name,e.data,e.created_at FROM claimed d JOIN site_events e ON e.seq=d.event_seq JOIN accounted n ON n.id=d.subscription_id`, lease.Milliseconds(), maxAttempts, historyDays, window.Milliseconds()).Scan(&d.Seq, &d.SubscriptionID, &d.Attempts, &d.LeaseUntil, &d.LastAttemptAt, &d.EventID, &d.Name, &d.Data, &d.CreatedAt)
	return d, err
}

// DeferNotificationDelivery returns a throttled claim without spending an attempt.
func DeferNotificationDelivery(ctx context.Context, database *sql.DB, d model.NotificationDelivery) error {
	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var id string
	if err = tx.QueryRowContext(ctx, `SELECT id FROM site_notifications WHERE id=$1 FOR UPDATE`, d.SubscriptionID).Scan(&id); err != nil {
		return err
	}
	r, err := tx.ExecContext(ctx, `UPDATE site_event_deliveries SET attempts=attempts-1,lease_until=NULL,next_attempt_at=now()+interval '2 seconds'
 WHERE event_seq=$1 AND subscription_id=$2 AND state='pending' AND lease_until=$3 AND lease_until>now()`, d.Seq, d.SubscriptionID, d.LeaseUntil)
	if err != nil {
		return err
	}
	rows, err := r.RowsAffected()
	if err != nil {
		return err
	}
	if rows > 0 {
		_, err = tx.ExecContext(ctx, `UPDATE site_notifications SET window_attempts=GREATEST(0,window_attempts-1) WHERE id=$1 AND window_start<=$2`, id, d.LastAttemptAt)
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}
func NotificationLeaseHeld(ctx context.Context, q sqlkit.Querier, d model.NotificationDelivery) (bool, error) {
	var held bool
	err := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM site_event_deliveries WHERE event_seq=$1 AND subscription_id=$2 AND state='pending' AND lease_until=$3 AND lease_until>now())`, d.Seq, d.SubscriptionID, d.LeaseUntil).Scan(&held)
	return held, err
}
func NotificationCursor(ctx context.Context, q sqlkit.Querier, id, site string) (int64, error) {
	var seq int64
	err := q.QueryRowContext(ctx, `SELECT COALESCE(max(seq),0) FROM site_events WHERE site_id=$2 AND seq<COALESCE((SELECT min(event_seq) FROM site_event_deliveries WHERE subscription_id=$1 AND state='pending'),9223372036854775807)`, id, site).Scan(&seq)
	return seq, err
}

// FinishNotificationDelivery checks the lease token so an overdue worker cannot
// overwrite an attempt claimed by another worker.
func FinishNotificationDelivery(ctx context.Context, database *sql.DB, d model.NotificationDelivery, state, category string, status, duration int, next time.Time, window time.Duration, minAttempts, failPercent int, failFor time.Duration) (bool, error) {
	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var id string
	if err = tx.QueryRowContext(ctx, `SELECT id FROM site_notifications WHERE id=$1 FOR UPDATE`, d.SubscriptionID).Scan(&id); err != nil {
		return false, err
	}
	r, err := tx.ExecContext(ctx, `UPDATE site_event_deliveries SET state=$3,next_attempt_at=$4,lease_until=NULL,last_http=$5,last_category=$6,duration_ms=$7 WHERE event_seq=$1 AND subscription_id=$2 AND lease_until=$8`, d.Seq, d.SubscriptionID, state, next, status, category, duration, d.LeaseUntil)
	if err != nil {
		return false, err
	}
	rows, _ := r.RowsAffected()
	if rows == 0 {
		return false, nil
	}
	ignored := state == "skipped" || status == 410 || status == 413
	if ignored {
		_, err = tx.ExecContext(ctx, `UPDATE site_notifications SET last_result=$2 WHERE id=$1`, id, category)
	} else {
		_, err = tx.ExecContext(ctx, `UPDATE site_notifications SET
 window_failures=window_failures+CASE WHEN NOT $3 AND $2>=window_start THEN 1 ELSE 0 END,
 failing_since=CASE WHEN $3 THEN NULL ELSE COALESCE(failing_since,$2) END,
 last_success_at=CASE WHEN $3 THEN now() ELSE last_success_at END,last_result=$4 WHERE id=$1`, id, d.LastAttemptAt, state == "delivered", category)
	}
	if err != nil {
		return false, err
	}
	if ignored {
		return false, tx.Commit()
	}
	r, err = tx.ExecContext(ctx, `UPDATE site_notifications SET state='suspended',suspended_at=now(),updated_at=now() WHERE id=$1 AND state='active' AND ((window_start>now()-$5*interval '1 millisecond' AND window_attempts>=$2 AND window_failures::bigint*100>window_attempts::bigint*$3) OR failing_since<=now()-$4*interval '1 millisecond')`, id, minAttempts, failPercent, failFor.Milliseconds(), window.Milliseconds())
	if err != nil {
		return false, err
	}
	rows, _ = r.RowsAffected()
	return rows > 0, tx.Commit()
}
func ClaimNotificationEmail(ctx context.Context, q sqlkit.Querier, id string) (bool, error) {
	r, err := q.ExecContext(ctx, `UPDATE site_notifications SET suspend_emailed_at=now() WHERE id=$1 AND state='suspended' AND suspend_emailed_at IS NULL`, id)
	if err != nil {
		return false, err
	}
	n, err := r.RowsAffected()
	return n > 0, err
}
func SweepNotifications(ctx context.Context, q sqlkit.Querier, days int) error {
	_, err := SweepNotificationsCount(ctx, q, days)
	return err
}
func SweepNotificationsCount(ctx context.Context, q sqlkit.Querier, days int) (int64, error) {
	var total int64
	for _, query := range []string{
		`DELETE FROM site_events WHERE created_at<now()-$1*interval '1 day'`,
		`DELETE FROM site_notifications WHERE expires_at<now()-$1*interval '1 day'`,
	} {
		result, err := q.ExecContext(ctx, query, days)
		if err != nil {
			return total, err
		}
		n, err := result.RowsAffected()
		total += n
		if err != nil {
			return total, err
		}
	}
	return total, nil
}

// PendingNotificationDeliveries counts the partial pending index, including leased and retrying rows.
func PendingNotificationDeliveries(ctx context.Context, q sqlkit.Querier) (int64, error) {
	var n int64
	err := q.QueryRowContext(ctx, `SELECT count(*) FROM site_event_deliveries WHERE state='pending'`).Scan(&n)
	return n, err
}
func NotificationHistory(ctx context.Context, database *sql.DB, id string, days int) ([]json.RawMessage, error) {
	rows, err := database.QueryContext(ctx, `SELECT jsonb_build_object('event_id',e.event_id,'name',e.name,'created_at',e.created_at,'state',d.state,'attempts',d.attempts,'last_attempt_at',d.last_attempt_at,'last_http',d.last_http,'result',d.last_category,'duration_ms',d.duration_ms) FROM site_event_deliveries d JOIN site_events e ON e.seq=d.event_seq WHERE subscription_id=$1 AND e.created_at>=now()-$2*interval '1 day' ORDER BY e.seq DESC LIMIT 200`, id, days)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []json.RawMessage{}
	for rows.Next() {
		var raw json.RawMessage
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		out = append(out, raw)
	}
	return out, rows.Err()
}
func RecentlyVerifiedNotification(ctx context.Context, q sqlkit.Querier, owner, url string) (bool, error) {
	var yes bool
	err := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM site_notifications WHERE owner_id=$1 AND url=$2 AND source='mcp' AND verified_at>now()-interval '30 days')`, owner, url).Scan(&yes)
	return yes, err
}

// ListPushDevices lists every web-push subscription on a site (both the
// owner's and visitors' own devices), for the owner's device-management view.
func ListPushDevices(ctx context.Context, database *sql.DB, site string) ([]model.Notification, error) {
	return queryNotifications(ctx, database, false, `SELECT `+notificationColumns+` FROM site_notifications n WHERE n.site_id=$1 AND n.kind='web-push' ORDER BY n.created_at DESC`, site)
}

// ListVisitorPushDevices lists one visitor's own web-push devices on one site.
func ListVisitorPushDevices(ctx context.Context, database *sql.DB, site, visitorID string) ([]model.Notification, error) {
	return queryNotifications(ctx, database, false, `SELECT `+notificationColumns+` FROM site_notifications n WHERE n.site_id=$1 AND n.kind='web-push' AND n.subscriber_role='visitor' AND n.owner_id=$2 ORDER BY n.created_at DESC`, site, visitorID)
}

// UpsertPushSubscription creates or updates one push device, keyed by
// (site, subscriber, endpoint, resource) -- not by the client-generated
// id newID, which is used only when no existing row matches. The lookup
// and insert run under the site-row lock (as SaveNotification does), so two
// concurrent subscribes for the same browser subscription cannot both
// insert; the unique index site_notifications_push_identity is only the
// backstop. created reports whether newID is the row that now exists; max
// is checked only on that path.
func UpsertPushSubscription(ctx context.Context, database *sql.DB, n model.Notification, newID string, max int) (id string, created bool, err error) {
	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		return "", false, err
	}
	defer tx.Rollback()
	if err = lockSite(ctx, tx, n.SiteID); err != nil {
		return "", false, err
	}
	var existing string
	err = tx.QueryRowContext(ctx, `SELECT id FROM site_notifications WHERE site_id=$1 AND kind='web-push' AND owner_id=$2 AND url=$3 AND storage=$4 FOR UPDATE`, n.SiteID, n.OwnerID, n.URL, n.Storage).Scan(&existing)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", false, err
	}
	id = existing
	created = existing == ""
	if created {
		id = newID
		var count int
		if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM site_notifications WHERE site_id=$1`, n.SiteID).Scan(&count); err != nil {
			return "", false, err
		}
		if count >= max {
			return "", false, model.ErrNotificationCap
		}
		// secret has no column default (every other creation path seals
		// a real one for webhook signing), but push never signs anything
		// with it -- delivery goes through sendPush, never client.send --
		// so an explicit empty literal satisfies the NOT NULL column
		// without the dead Seal/secret-generation work every other path
		// does for a value push will never read.
		_, err = tx.ExecContext(ctx, `INSERT INTO site_notifications(id,site_id,owner_id,source,label,url,events,storage,secret,kind,subscriber_role,push_p256dh,push_auth,device_label,verified_at,state,push_family)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8,'','web-push',$9,$10,$11,$12,$13,'active',$14)`,
			id, n.SiteID, n.OwnerID, n.Source, n.Label, n.URL, pq.Array(n.Events), n.Storage, n.SubscriberRole, n.PushP256dh, n.PushAuth, n.DeviceLabel, n.VerifiedAt, n.PushFamily)
	} else {
		_, err = tx.ExecContext(ctx, `UPDATE site_notifications SET events=$2,label=$3,push_p256dh=$4,push_auth=$5,device_label=$6,verified_at=$7,push_family=$8,
 state='active',suspended_at=NULL,suspend_emailed_at=NULL,failing_since=NULL,window_start=NULL,window_attempts=0,window_failures=0,updated_at=now() WHERE id=$1`,
			id, pq.Array(n.Events), n.Label, n.PushP256dh, n.PushAuth, n.DeviceLabel, n.VerifiedAt, n.PushFamily)
	}
	if err != nil {
		return "", false, err
	}
	return id, created, tx.Commit()
}

// DeleteNotificationForOwner deletes id only if it belongs to site and, when
// restrictVisitor is set, only a visitor-role push row owned by that
// visitor; used by the subscriber's own unsubscribe/mute routes so one
// person can never reach another's subscription by guessing an id.
func DeleteNotificationForOwner(ctx context.Context, q sqlkit.Querier, site, id, visitorID string) (bool, error) {
	var r sql.Result
	var err error
	if visitorID != "" {
		r, err = q.ExecContext(ctx, `DELETE FROM site_notifications WHERE id=$1 AND site_id=$2 AND kind='web-push' AND subscriber_role='visitor' AND owner_id=$3`, id, site, visitorID)
	} else {
		r, err = q.ExecContext(ctx, `DELETE FROM site_notifications WHERE id=$1 AND site_id=$2`, id, site)
	}
	if err != nil {
		return false, err
	}
	n, err := r.RowsAffected()
	return n > 0, err
}

// DeletePushSubscriptionsForFamily removes every web-push device created
// by one browser session or (for an SSO-signed-in-everywhere session)
// its whole family of sibling sessions across every site they span. It is
// scoped by the session's stamped identity (PushFamily at subscribe time),
// not by person and site, so another browser signed in as the same person
// keeps its devices. A row with no family (the owner's API key) is never
// touched.
func DeletePushSubscriptionsForFamily(ctx context.Context, q sqlkit.Querier, family string) (int64, error) {
	if family == "" {
		return 0, nil
	}
	r, err := q.ExecContext(ctx, `DELETE FROM site_notifications WHERE kind='web-push' AND push_family<>'' AND push_family=$1`, family)
	if err != nil {
		return 0, err
	}
	return r.RowsAffected()
}

// DeletePushSubscriptionsForSite removes every web-push device on a site,
// the owner's and every visitor's: the dashboard's "turn push off" bulk
// action.
func DeletePushSubscriptionsForSite(ctx context.Context, q sqlkit.Querier, site string) (int64, error) {
	r, err := q.ExecContext(ctx, `DELETE FROM site_notifications WHERE site_id=$1 AND kind='web-push'`, site)
	if err != nil {
		return 0, err
	}
	return r.RowsAffected()
}

// SetMutedResources replaces one push subscription's per-resource mute list.
func SetMutedResources(ctx context.Context, q sqlkit.Querier, id string, resources []string) error {
	if resources == nil {
		resources = []string{}
	}
	_, err := q.ExecContext(ctx, `UPDATE site_notifications SET muted_resources=$2,updated_at=now() WHERE id=$1 AND kind='web-push'`, id, pq.Array(resources))
	return err
}

// GetPushRule reads the owner's effective push routing/payload control for
// one resource: an exact-resource rule when one is configured, else the
// site default row (resource ""), else the "both, generic text" built-in
// default. Neither a missing exact rule nor a missing default row is an
// error; a real query failure is, so the caller can defer the delivery
// instead of silently widening who may receive it.
func GetPushRule(ctx context.Context, q sqlkit.Querier, site, resource string) (model.PushRule, error) {
	rule := model.PushRule{SiteID: site, Resource: resource, Recipients: "both"}
	err := q.QueryRowContext(ctx, `SELECT recipients,title,body_column FROM site_push_rules WHERE site_id=$1 AND resource=$2`, site, resource).Scan(&rule.Recipients, &rule.Title, &rule.BodyColumn)
	if err == nil {
		return rule, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return model.PushRule{}, err
	}
	if resource == "" {
		return rule, nil
	}
	fallback := model.PushRule{SiteID: site, Resource: "", Recipients: "both"}
	err = q.QueryRowContext(ctx, `SELECT recipients,title,body_column FROM site_push_rules WHERE site_id=$1 AND resource=''`, site).Scan(&fallback.Recipients, &fallback.Title, &fallback.BodyColumn)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return model.PushRule{}, err
	}
	return fallback, nil
}

// ListPushRules lists every configured (non-default) push rule on a site.
func ListPushRules(ctx context.Context, database *sql.DB, site string) ([]model.PushRule, error) {
	rows, err := database.QueryContext(ctx, `SELECT resource,recipients,title,body_column FROM site_push_rules WHERE site_id=$1 ORDER BY resource`, site)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.PushRule{}
	for rows.Next() {
		r := model.PushRule{SiteID: site}
		if err := rows.Scan(&r.Resource, &r.Recipients, &r.Title, &r.BodyColumn); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// SetPushRule upserts the owner's push control for one resource.
func SetPushRule(ctx context.Context, q sqlkit.Querier, rule model.PushRule) error {
	_, err := q.ExecContext(ctx, `INSERT INTO site_push_rules(site_id,resource,recipients,title,body_column,updated_at) VALUES($1,$2,$3,$4,$5,now())
 ON CONFLICT(site_id,resource) DO UPDATE SET recipients=EXCLUDED.recipients,title=EXCLUDED.title,body_column=EXCLUDED.body_column,updated_at=now()`,
		rule.SiteID, rule.Resource, rule.Recipients, rule.Title, rule.BodyColumn)
	return err
}

// AccessChecks rechecks, under the site lock, that a person saving a
// subscription on an account's site may still act for it.
type AccessChecks interface {
	ActsOnSite(ctx context.Context, q sqlkit.Querier, siteID, person string) (bool, error)
}

// VerifiedAt is when owner last verified url for an MCP Events subscription.
func VerifiedAt(ctx context.Context, q sqlkit.Querier, owner, url string) (time.Time, error) {
	var at time.Time
	err := q.QueryRowContext(ctx, `SELECT max(verified_at) FROM site_notifications WHERE owner_id=$1 AND url=$2 AND source='mcp'`, owner, url).Scan(&at)
	return at, err
}
