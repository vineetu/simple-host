-- Simple Hack on Simple Host teams (TM-S2): a member publishes with their own
-- sign-in naming the event as owner, so team keys and the connected app's
-- selected team are gone. Their tables, the selection column and every key
-- that was a team key go with them. Simple Hack keeps no data across this
-- change: its events are wiped at install.
DELETE FROM api_keys WHERE scope = 'team';
DROP TABLE IF EXISTS event_team_keys;
DROP TABLE IF EXISTS hack_grant_teams;
ALTER TABLE oauth_grants DROP COLUMN IF EXISTS selected_team_id;
