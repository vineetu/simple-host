-- A personal hosted connector can choose its current team site without a new OAuth grant.
ALTER TABLE oauth_grants ADD COLUMN IF NOT EXISTS selected_team_id UUID;
