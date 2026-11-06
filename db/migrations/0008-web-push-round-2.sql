-- SIM-360 Codex round 2 hardening: two small, independent additions.
--
-- FIX 8 (concurrent subscribe dedupe): UpsertPushSubscription now looks up
-- and inserts/updates under the site's own row lock (serializing against
-- itself), so this unique index is a backstop against any other
-- insertion path, not the primary defense. A partial index, scoped to
-- kind='web-push', never constrains webhook/mcp-event rows (whose
-- identity has always been the client-supplied id alone).
CREATE UNIQUE INDEX IF NOT EXISTS site_notifications_push_identity
  ON site_notifications(site_id, owner_id, url, storage) WHERE kind='web-push';

-- FIX 1 (MCP cursor replay bypassing the owner-write exclusion): the
-- live fan-out's per-event routing facts (repository/notifications.go
-- EmitSiteEventFor's pushActor/pushTarget) are now persisted on the
-- event itself, not only used transiently for the live INSERT -- so a
-- cursor-based replay (an MCP subscriber reconnecting with an earlier
-- cursor, SaveNotification's own backfill query) can apply the exact
-- same eligibility CASE the live path does, instead of none at all.
-- Never delivered to a subscriber: notify.go deletes the two routing
-- hints from the JSON body before it is ever stored or sent; these are
-- separate, internal-only columns precisely so that stays true even for
-- the stored event row itself. Defaults ('') mean "an ordinary write",
-- matching every event stored before this migration (replay for those
-- behaves exactly as it always has).
ALTER TABLE site_events ADD COLUMN IF NOT EXISTS push_actor TEXT NOT NULL DEFAULT '';
ALTER TABLE site_events ADD COLUMN IF NOT EXISTS push_target TEXT NOT NULL DEFAULT '';

-- FIX 10 (sign-out scoping): a push subscription records the specific
-- browser session that created it -- the session's own SSO pass hash
-- when it is part of a signed-in-everywhere family (so one sign-out
-- revokes push on every site that family spans too), else the session's
-- own id (so an ordinary single-site session's sign-out still only ever
-- reaches its own device, never a different browser's subscription for
-- the same person on the same site -- the old owner_id+site_id-only
-- deletion could not tell those apart). Empty for any row created before
-- this migration or outside a tracked session (an owner's API-key
-- subscribe): never targeted by the family-scoped delete, left exactly
-- as every existing cleanup path already reaches it.
ALTER TABLE site_notifications ADD COLUMN IF NOT EXISTS push_family TEXT NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS site_notifications_push_family ON site_notifications(push_family) WHERE kind='web-push' AND push_family<>'';
