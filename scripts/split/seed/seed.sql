\set ON_ERROR_STOP on
BEGIN;
SET LOCAL search_path = public;

-- Two ordinary owners, a holding account, and a separate organiser identity.
INSERT INTO users (id, username, handle, display_name, event_account, created_at, handle_changed_at) VALUES
  ('00000000-0000-4000-8000-000000000001', 'seed-one@example.com', 'seed-one', 'Seed One', false, '2026-01-15 12:00:00+00', NULL),
  ('00000000-0000-4000-8000-000000000002', 'seed-two@example.org', 'seed-two', 'Seed Two', false, '2026-01-15 12:00:00+00', NULL),
  ('00000000-0000-4000-8000-000000000003', 'event+00000000-0000-4000-8000-000000000010@events.invalid', 'seed-rehearsal', NULL, true, '2026-01-15 12:00:00+00', '2026-01-15 12:00:00+00'),
  ('00000000-0000-4000-8000-000000000004', 'seed-organiser@example.com', 'seed-organiser', 'Seed Organiser', false, '2026-01-15 12:00:00+00', NULL);

-- One event owned through the holding account; two surviving teams.
INSERT INTO events (id, slug, account_id, created_by, title, stage, organiser_name, contact_email, join_code, judge_code, created_at, updated_at) VALUES
  ('00000000-0000-4000-8000-000000000010', 'seed-rehearsal', '00000000-0000-4000-8000-000000000003', '00000000-0000-4000-8000-000000000004', 'Invented rehearsal', 'building', 'Seed Organiser', 'seed-organiser@example.com', 'FAKE-SEED-JOIN', 'FAKE-SEED-JUDGE', '2026-01-15 12:00:00+00', '2026-01-15 12:00:00+00');
INSERT INTO event_teams (id, event_id, slug, name, code, created_by, created_at) VALUES
  ('00000000-0000-4000-8000-000000000011', '00000000-0000-4000-8000-000000000010', 'seed-live', 'Seed Live', 'FAKE-TEAM-LIVE', '00000000-0000-4000-8000-000000000001', '2026-01-15 12:00:00+00'),
  ('00000000-0000-4000-8000-000000000012', '00000000-0000-4000-8000-000000000010', 'seed-trash', 'Seed Trash', 'FAKE-TEAM-TRASH', '00000000-0000-4000-8000-000000000002', '2026-01-15 12:00:00+00');
INSERT INTO event_used_names (event_slug, team_slug, used_at) VALUES
  ('seed-rehearsal', '', '2026-01-15 12:00:00+00'),
  ('seed-rehearsal', 'seed-live', '2026-01-15 12:00:00+00'),
  ('seed-rehearsal', 'seed-trash', '2026-01-15 12:00:00+00'),
  ('seed-rehearsal', 'seed-former', '2026-01-15 12:00:00+00');

-- Approved members on both teams and an organiser; applications are event_members rows today.
INSERT INTO event_members (event_id, user_id, role, team_id, coc_accepted_at, approval_status, team_admin, team_joined_at, joined_via, approval_decided_at, approval_decided_by, joined_at) VALUES
  ('00000000-0000-4000-8000-000000000010', '00000000-0000-4000-8000-000000000001', 'participant', '00000000-0000-4000-8000-000000000011', '2026-01-15 12:00:00+00', 'approved', true, '2026-01-15 12:00:00+00', 'link', '2026-01-15 12:00:00+00', '00000000-0000-4000-8000-000000000004', '2026-01-15 12:00:00+00'),
  ('00000000-0000-4000-8000-000000000010', '00000000-0000-4000-8000-000000000002', 'participant', '00000000-0000-4000-8000-000000000012', '2026-01-15 12:00:00+00', 'approved', true, '2026-01-15 12:00:00+00', 'application', '2026-01-15 12:00:00+00', '00000000-0000-4000-8000-000000000004', '2026-01-15 12:00:00+00'),
  ('00000000-0000-4000-8000-000000000010', '00000000-0000-4000-8000-000000000004', 'organiser', NULL, '2026-01-15 12:00:00+00', 'approved', false, NULL, 'link', '2026-01-15 12:00:00+00', '00000000-0000-4000-8000-000000000004', '2026-01-15 12:00:00+00');

-- Live and Recently deleted team sites: join sites.user_id=events.account_id and sites.name=event_teams.slug.
INSERT INTO sites (id, user_id, name, domain_token, deleted_at, purge_at, idle_token_hash, state, state_version, created_at, updated_at) VALUES
  ('00000000-0000-4000-8000-000000000021', '00000000-0000-4000-8000-000000000003', 'seed-live', 'FAKE-DOMAIN-TOKEN-21', NULL, NULL, NULL, '{"phase":"building","votes":2}', 1, '2026-01-15 12:00:00+00', '2026-01-15 12:00:00+00'),
  ('00000000-0000-4000-8000-000000000022', '00000000-0000-4000-8000-000000000003', 'seed-trash', 'FAKE-DOMAIN-TOKEN-22', now() - interval '1 day', now() + interval '29 days', NULL, '{"phase":"paused"}', 1, '2026-01-15 12:00:00+00', '2026-01-15 12:00:00+00');

-- Ordinary account sites: passcode, two named viewers, and an active custom domain. Passcode bytes are deliberately fake.
INSERT INTO sites (id, user_id, name, domain_token, passcode_enc, passcode_set_at, passcode_generation, access, custom_domain, domain_status, domain_cert_status, domain_bound_at, domain_verified_at, state, state_version, created_at, updated_at) VALUES
  ('00000000-0000-4000-8000-000000000023', '00000000-0000-4000-8000-000000000001', 'seed-passcode', 'FAKE-DOMAIN-TOKEN-23', convert_to('FAKE-SEALED-PASSCODE-NOT-DECRYPTABLE', 'UTF8'), '2026-01-15 12:00:00+00', 1, 'anyone', NULL, NULL, NULL, NULL, NULL, '{"count":3}', 1, '2026-01-15 12:00:00+00', '2026-01-15 12:00:00+00'),
  ('00000000-0000-4000-8000-000000000024', '00000000-0000-4000-8000-000000000002', 'seed-viewers', 'FAKE-DOMAIN-TOKEN-24', NULL, NULL, 0, 'specific', NULL, NULL, NULL, NULL, NULL, '{"note":"invented"}', 1, '2026-01-15 12:00:00+00', '2026-01-15 12:00:00+00'),
  ('00000000-0000-4000-8000-000000000025', '00000000-0000-4000-8000-000000000001', 'seed-domain', 'FAKE-DOMAIN-TOKEN-25', NULL, NULL, 0, 'anyone', 'seed.example.org', 'active', 'live', '2026-01-15 12:00:00+00', '2026-01-15 12:00:00+00', '{"ready":true}', 1, '2026-01-15 12:00:00+00', '2026-01-15 12:00:00+00');
INSERT INTO site_viewers (site_id, email, added_at) VALUES
  ('00000000-0000-4000-8000-000000000024', 'viewer-one@example.com', '2026-01-15 12:00:00+00'),
  ('00000000-0000-4000-8000-000000000024', 'viewer-two@example.org', '2026-01-15 12:00:00+00');

-- Connected app and selected live team; selection is recorded only in oauth_grants.selected_team_id.
INSERT INTO oauth_clients (client_id, client_name, redirect_uris, created_at) VALUES
  ('seed-connected-app', 'Invented seed app', ARRAY['https://example.org/seed/callback'], '2026-01-15 12:00:00+00');
INSERT INTO oauth_grants (id, user_id, client_id, scope, resource, selected_team_id, device, created_at, last_used_at) VALUES
  ('00000000-0000-4000-8000-000000000031', '00000000-0000-4000-8000-000000000001', 'seed-connected-app', 'sites events', 'https://example.org/mcp', '00000000-0000-4000-8000-000000000011', 'Seed live selection', '2026-01-15 12:00:00+00', '2026-01-15 12:00:00+00'),
  ('00000000-0000-4000-8000-000000000032', '00000000-0000-4000-8000-000000000002', 'seed-connected-app', 'sites events', 'https://example.org/mcp', '00000000-0000-4000-8000-000000000013', 'Seed deleted selection', '2026-01-15 12:00:00+00', '2026-01-15 12:00:00+00');

-- Grant whose selected team was deleted: NO selected_team_id FK in either dump, so UUID 13 remains dangling, not NULL. Tokens survive too.
INSERT INTO oauth_tokens (token_hash, grant_id, kind, expires_at, created_at) VALUES
  ('FAKE-NOT-A-HASH-ACCESS-31', '00000000-0000-4000-8000-000000000031', 'access', now() + interval '1 hour', '2026-01-15 12:00:00+00'),
  ('FAKE-NOT-A-HASH-REFRESH-32', '00000000-0000-4000-8000-000000000032', 'refresh', now() + interval '30 days', '2026-01-15 12:00:00+00');

-- Postgres KV values, resource declarations and SQLite table policy metadata (no SQLite bytes).
INSERT INTO site_storage_resources (site_id, name, kind, acc_read, acc_add, acc_edit, acc_delete) VALUES
  ('00000000-0000-4000-8000-000000000021', 'settings', 'kv', 'anyone', 'owner', 'owner', 'owner'),
  ('00000000-0000-4000-8000-000000000021', 'records', 'sqlite', 'anyone', 'owner', 'owner', 'owner'),
  ('00000000-0000-4000-8000-000000000021', 'uploads', 'files', 'anyone', 'owner', 'owner', 'owner');
INSERT INTO site_storage_tables (site_id, resource_name, table_name, acc_read, acc_add, acc_edit, acc_delete) VALUES
  ('00000000-0000-4000-8000-000000000021', 'records', 'notes', 'owner', 'owner', 'owner', 'owner'),
  ('00000000-0000-4000-8000-000000000021', 'records', 'tasks', 'owner', 'owner', 'owner', 'owner');
INSERT INTO site_storage_kv (site_id, resource_name, key, value, writer_id, updated_at, created_at) VALUES
  ('00000000-0000-4000-8000-000000000021', 'settings', 'theme', '"teal"', '00000000-0000-4000-8000-000000000001', '2026-01-15 12:00:00+00', '2026-01-15 12:00:00+00'),
  ('00000000-0000-4000-8000-000000000021', 'settings', 'counter', '2', '00000000-0000-4000-8000-000000000001', '2026-01-15 12:00:00+00', '2026-01-15 12:00:00+00'),
  ('00000000-0000-4000-8000-000000000021', 'settings', 'draft', '{"saved":true}', '00000000-0000-4000-8000-000000000001', '2026-01-15 12:00:00+00', '2026-01-15 12:00:00+00');

-- Legacy Postgres collections/settings in addition to sites.state; byte counters are maintained by dump triggers.
INSERT INTO collection_settings (site_id, collection, private, kind, declared_at, updated_at) VALUES
  ('00000000-0000-4000-8000-000000000021', 'notes', false, 'board', '2026-01-15 12:00:00+00', '2026-01-15 12:00:00+00'),
  ('00000000-0000-4000-8000-000000000024', 'responses', true, 'entries', '2026-01-15 12:00:00+00', '2026-01-15 12:00:00+00');
INSERT INTO collection_items (id, site_id, collection, data, submitted_by, submitted_email, created_at) VALUES
  (1, '00000000-0000-4000-8000-000000000021', 'notes', '{"text":"First invented note"}', '00000000-0000-4000-8000-000000000001', 'seed-one@example.com', '2026-01-15 12:00:00+00'),
  (2, '00000000-0000-4000-8000-000000000021', 'notes', '{"text":"Second invented note"}', '00000000-0000-4000-8000-000000000002', 'seed-two@example.org', '2026-01-15 12:00:00+00'),
  (3, '00000000-0000-4000-8000-000000000024', 'responses', '{"answer":"Yes"}', '00000000-0000-4000-8000-000000000001', 'seed-one@example.com', '2026-01-15 12:00:00+00');
SELECT setval('collection_items_id_seq', 3, true);

-- One submitted entry and one announcement, without media or mail deliveries.
INSERT INTO event_entries (team_id, event_id, title, description, maturity, updated_by, submitted_at, submitted_by, submit_settled_at, updated_at) VALUES
  ('00000000-0000-4000-8000-000000000011', '00000000-0000-4000-8000-000000000010', 'Seed project', 'An invented rehearsal entry.', 'working', '00000000-0000-4000-8000-000000000001', '2026-01-15 12:00:00+00', '00000000-0000-4000-8000-000000000001', '2026-01-15 12:00:00+00', '2026-01-15 12:00:00+00');
INSERT INTO event_announcements (id, event_id, title, body, created_at) VALUES
  ('00000000-0000-4000-8000-000000000040', '00000000-0000-4000-8000-000000000010', 'Rehearsal announcement', 'All content here is invented.', '2026-01-15 12:00:00+00');

-- Site notification subscriptions, push rules, events and pending/delivered outbox rows. No real endpoints or secrets.
INSERT INTO site_notifications (id, site_id, owner_id, source, grant_id, kind, url, events, storage, secret, verified_at, created_at, updated_at) VALUES
  ('seed-webhook', '00000000-0000-4000-8000-000000000025', '00000000-0000-4000-8000-000000000001', 'owner', NULL, 'webhook', 'https://example.org/seed/webhook', ARRAY['record.changed'], 'settings', 'FAKE-NOTIFICATION-SECRET', '2026-01-15 12:00:00+00', '2026-01-15 12:00:00+00', '2026-01-15 12:00:00+00'),
  ('seed-mcp', '00000000-0000-4000-8000-000000000021', '00000000-0000-4000-8000-000000000001', 'mcp', '00000000-0000-4000-8000-000000000031', 'mcp-event', 'https://example.com/seed/events', ARRAY['record.changed'], 'settings', 'FAKE-MCP-SECRET', '2026-01-15 12:00:00+00', '2026-01-15 12:00:00+00', '2026-01-15 12:00:00+00');
INSERT INTO site_push_rules (site_id, resource, recipients, title, updated_at) VALUES
  ('00000000-0000-4000-8000-000000000021', 'settings', 'owner', 'Seed KV update', '2026-01-15 12:00:00+00'),
  ('00000000-0000-4000-8000-000000000025', '', 'both', 'Seed site update', '2026-01-15 12:00:00+00');
INSERT INTO site_storage_resources (site_id, name, kind) VALUES
  ('00000000-0000-4000-8000-000000000025', 'settings', 'kv');
INSERT INTO site_storage_kv (site_id, resource_name, key, value, writer_id, updated_at, created_at) VALUES
  ('00000000-0000-4000-8000-000000000025', 'settings', 'enabled', 'true', '00000000-0000-4000-8000-000000000001', '2026-01-15 12:00:00+00', '2026-01-15 12:00:00+00');
INSERT INTO site_events (seq, event_id, site_id, name, storage, data, created_at) VALUES
  (1, 'seed-event-1', '00000000-0000-4000-8000-000000000021', 'record.changed', 'settings', '{"key":"counter","fields":2,"change":"edited"}', '2026-01-15 12:00:00+00'),
  (2, 'seed-event-2', '00000000-0000-4000-8000-000000000025', 'record.changed', 'settings', '{"key":"enabled","fields":true,"change":"edited"}', '2026-01-15 12:00:00+00'),
  (3, 'seed-event-3', '00000000-0000-4000-8000-000000000021', 'record.changed', 'settings', '{"key":"counter","fields":2,"change":"edited"}', '2026-01-15 12:00:00+00');
SELECT setval('site_events_seq_seq', 3, true);
INSERT INTO site_event_deliveries (event_seq, subscription_id, state, attempts, next_attempt_at, last_attempt_at, last_http) VALUES
  (1, 'seed-mcp', 'pending', 0, '2026-01-15 12:00:00+00', NULL, NULL),
  (2, 'seed-webhook', 'delivered', 1, '2026-01-15 12:00:00+00', '2026-01-15 12:00:00+00', 204),
  (3, 'seed-mcp', 'pending', 0, '2026-01-15 12:00:00+00', NULL, NULL);

-- Hack notices: unseen, seen, and a surviving notice referencing the deleted team (no team FK).
INSERT INTO event_notices (id, event_id, user_id, kind, team_id, detail, seen_at, email_state, created_at) VALUES
  ('00000000-0000-4000-8000-000000000041', '00000000-0000-4000-8000-000000000010', '00000000-0000-4000-8000-000000000001', 'team_admin', '00000000-0000-4000-8000-000000000011', 'Seed Live', NULL, 'pending', '2026-01-15 12:00:00+00'),
  ('00000000-0000-4000-8000-000000000042', '00000000-0000-4000-8000-000000000010', '00000000-0000-4000-8000-000000000002', 'team_removed', '00000000-0000-4000-8000-000000000013', 'Seed Former was removed by the organiser.', '2026-01-15 12:00:00+00', 'pending', '2026-01-15 12:00:00+00'),
  ('00000000-0000-4000-8000-000000000043', '00000000-0000-4000-8000-000000000010', '00000000-0000-4000-8000-000000000002', 'team_admin', '00000000-0000-4000-8000-000000000012', 'Seed Trash', NULL, 'pending', '2026-01-15 12:00:00+00');

-- Sanity checks: fail the entire transaction if any rehearsal case is missing.
DO $$ BEGIN
  IF (SELECT count(*) FROM users) <> 4 THEN RAISE EXCEPTION 'Seed: users'; END IF;
  IF (SELECT count(*) FROM users WHERE event_account AND handle = 'seed-rehearsal') <> 1 THEN RAISE EXCEPTION 'Seed: holding account'; END IF;
  IF (SELECT count(*) FROM events e JOIN users u ON u.id=e.account_id WHERE u.event_account AND u.handle = e.slug) <> 1 THEN RAISE EXCEPTION 'Seed: holding event'; END IF;
  IF (SELECT count(*) FROM event_teams) <> 2 THEN RAISE EXCEPTION 'Seed: two teams'; END IF;
  IF (SELECT count(*) FROM event_members WHERE role='participant' AND approval_status='approved' AND team_id IS NOT NULL) <> 2 THEN RAISE EXCEPTION 'Seed: approved team members'; END IF;
  IF (SELECT count(*) FROM event_members WHERE role='organiser') <> 1 THEN RAISE EXCEPTION 'Seed: organiser'; END IF;
  IF (SELECT count(*) FROM sites) <> 5 THEN RAISE EXCEPTION 'Seed: sites'; END IF;
  IF (SELECT count(*) FROM sites WHERE deleted_at IS NOT NULL) <> 1 THEN RAISE EXCEPTION 'Seed: exactly one deleted site'; END IF;
  IF (SELECT count(*) FROM sites WHERE deleted_at IS NOT NULL AND purge_at > now() AND idle_token_hash IS NULL) <> 1 THEN RAISE EXCEPTION 'Seed: Recently deleted'; END IF;
  IF (SELECT count(*) FROM sites s JOIN events e ON s.user_id=e.account_id JOIN event_teams t ON t.event_id=e.id AND t.slug=s.name) <> 2 THEN RAISE EXCEPTION 'Seed: team site links'; END IF;
  IF (SELECT count(*) FROM sites s JOIN event_teams t ON t.slug=s.name JOIN events e ON e.id=t.event_id AND e.account_id=s.user_id WHERE s.deleted_at IS NULL) <> 1 THEN RAISE EXCEPTION 'Seed: live team site'; END IF;
  IF (SELECT count(*) FROM sites WHERE passcode_enc IS NOT NULL AND passcode_generation=1) <> 1 THEN RAISE EXCEPTION 'Seed: passcode'; END IF;
  IF (SELECT count(*) FROM site_viewers v JOIN sites s ON s.id=v.site_id WHERE s.access='specific' AND s.passcode_enc IS NULL) <> 2 THEN RAISE EXCEPTION 'Seed: named viewers'; END IF;
  IF (SELECT count(*) FROM sites WHERE custom_domain='seed.example.org' AND domain_status='active' AND domain_cert_status='live') <> 1 THEN RAISE EXCEPTION 'Seed: custom domain'; END IF;
  IF (SELECT count(*) FROM oauth_grants) <> 2 THEN RAISE EXCEPTION 'Seed: two grants'; END IF;
  IF (SELECT count(*) FROM oauth_grants g JOIN event_teams t ON t.id=g.selected_team_id) <> 1 THEN RAISE EXCEPTION 'Seed: live selection'; END IF;
  IF (SELECT count(*) FROM oauth_grants g WHERE selected_team_id IS NOT NULL AND NOT EXISTS (SELECT 1 FROM event_teams t WHERE t.id=g.selected_team_id)) <> 1 THEN RAISE EXCEPTION 'Seed: dangling selection'; END IF;
  IF (SELECT count(*) FROM oauth_tokens WHERE grant_id='00000000-0000-4000-8000-000000000032') <> 1 THEN RAISE EXCEPTION 'Seed: surviving token'; END IF;
  IF (SELECT count(*) FROM site_storage_resources) <> 4 THEN RAISE EXCEPTION 'Seed: resources'; END IF;
  IF (SELECT count(*) FROM site_storage_tables) <> 2 THEN RAISE EXCEPTION 'Seed: table policies'; END IF;
  IF (SELECT count(*) FROM site_storage_kv) <> 4 THEN RAISE EXCEPTION 'Seed: KV'; END IF;
  IF (SELECT count(*) FROM sites WHERE state IS NOT NULL) <> 5 THEN RAISE EXCEPTION 'Seed: legacy state'; END IF;
  IF (SELECT count(*) FROM collection_settings) <> 2 THEN RAISE EXCEPTION 'Seed: collection settings'; END IF;
  IF (SELECT count(*) FROM collection_items) <> 3 THEN RAISE EXCEPTION 'Seed: collection items'; END IF;
  IF (SELECT count(*) FROM site_notifications) <> 2 THEN RAISE EXCEPTION 'Seed: subscriptions'; END IF;
  IF (SELECT count(*) FROM site_push_rules) <> 2 THEN RAISE EXCEPTION 'Seed: push rules'; END IF;
  IF (SELECT count(*) FROM site_events) <> 3 THEN RAISE EXCEPTION 'Seed: site events'; END IF;
  IF (SELECT count(*) FROM site_event_deliveries) <> 3 THEN RAISE EXCEPTION 'Seed: deliveries'; END IF;
  IF (SELECT count(*) FROM event_notices) <> 3 THEN RAISE EXCEPTION 'Seed: Hack notices'; END IF;
  IF (SELECT count(*) FROM event_notices WHERE seen_at IS NULL) <> 2 THEN RAISE EXCEPTION 'Seed: unseen notices'; END IF;
  IF (SELECT count(*) FROM event_notices WHERE team_id='00000000-0000-4000-8000-000000000013') <> 1 THEN RAISE EXCEPTION 'Seed: surviving deleted team notice'; END IF;
  IF (SELECT count(*) FROM event_entries WHERE submitted_at IS NOT NULL) <> 1 THEN RAISE EXCEPTION 'Seed: submitted entry'; END IF;
  IF (SELECT count(*) FROM event_announcements) <> 1 THEN RAISE EXCEPTION 'Seed: announcement'; END IF;
END $$;
COMMIT;
