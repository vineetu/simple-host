-- SIM-360: Web Push as a new delivery kind in the existing notifications
-- engine (webhook | mcp-event | web-push), behind a flag. A push
-- subscription is a row in site_notifications like any other: kind names
-- the delivery, subscriber_role names whose device owner_id is (the site
-- owner, or a signed-in visitor subscribing their own device -- owner_id is
-- then that visitor's own users.id). push_p256dh/push_auth hold the
-- device's Web Push keys; url holds the push service endpoint for
-- kind='web-push'. muted_resources lets one device mute specific tables
-- without unsubscribing entirely.
-- Every ADD COLUMN below has a constant default (fast in Postgres 11+: no
-- table rewrite). Every CHECK is added NOT VALID, then validated in a
-- separate statement: VALIDATE CONSTRAINT takes ShareUpdateExclusiveLock,
-- not the AccessExclusiveLock a validated-on-add CHECK would hold for the
-- same scan, so concurrent subscription and outbox activity is never
-- blocked by it, only concurrent schema changes are.
ALTER TABLE site_notifications ADD COLUMN IF NOT EXISTS kind TEXT NOT NULL DEFAULT 'webhook';
ALTER TABLE site_notifications DROP CONSTRAINT IF EXISTS site_notifications_kind_check;
ALTER TABLE site_notifications ADD CONSTRAINT site_notifications_kind_check CHECK (kind IN ('webhook','mcp-event','web-push')) NOT VALID;
ALTER TABLE site_notifications VALIDATE CONSTRAINT site_notifications_kind_check;
ALTER TABLE site_notifications ADD COLUMN IF NOT EXISTS subscriber_role TEXT NOT NULL DEFAULT 'owner';
ALTER TABLE site_notifications DROP CONSTRAINT IF EXISTS site_notifications_subscriber_role_check;
ALTER TABLE site_notifications ADD CONSTRAINT site_notifications_subscriber_role_check CHECK (subscriber_role IN ('owner','visitor')) NOT VALID;
ALTER TABLE site_notifications VALIDATE CONSTRAINT site_notifications_subscriber_role_check;
ALTER TABLE site_notifications ADD COLUMN IF NOT EXISTS push_p256dh TEXT NOT NULL DEFAULT '';
ALTER TABLE site_notifications ADD COLUMN IF NOT EXISTS push_auth TEXT NOT NULL DEFAULT '';
ALTER TABLE site_notifications ADD COLUMN IF NOT EXISTS device_label TEXT NOT NULL DEFAULT '';
ALTER TABLE site_notifications ADD COLUMN IF NOT EXISTS muted_resources TEXT[] NOT NULL DEFAULT '{}';
-- Existing rows (webhook/mcp-event, created before this migration) have no
-- Source-derived kind recorded; backfill mcp-event from the source they
-- were already created with, same as new MCP rows now get at creation.
UPDATE site_notifications SET kind='mcp-event' WHERE kind='webhook' AND source='mcp';
CREATE INDEX IF NOT EXISTS site_notifications_push ON site_notifications(site_id, owner_id) WHERE kind='web-push';

-- Owner's per-site, per-resource push routing and payload controls.
-- resource='' is the site default row. recipients restricts who a
-- push-kind subscription may reach for that resource's events, on top of
-- (never wider than) the hard visitor-own-data boundary enforced in the
-- event fan-out itself.
CREATE TABLE IF NOT EXISTS site_push_rules (
  site_id UUID NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
  resource TEXT NOT NULL DEFAULT '',
  recipients TEXT NOT NULL DEFAULT 'both',
  title TEXT NOT NULL DEFAULT '',
  body_column TEXT NOT NULL DEFAULT '',
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (site_id, resource)
);
ALTER TABLE site_push_rules DROP CONSTRAINT IF EXISTS site_push_rules_recipients_check;
ALTER TABLE site_push_rules ADD CONSTRAINT site_push_rules_recipients_check CHECK (recipients IN ('owner','row_owner','both','none')) NOT VALID;
ALTER TABLE site_push_rules VALIDATE CONSTRAINT site_push_rules_recipients_check;
