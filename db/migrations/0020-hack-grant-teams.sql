-- Keep the old column for old programs until D3-S5b. The first deploy runs
-- selected-team-reconcile.sql with all writers stopped before starting this reader.
CREATE TABLE IF NOT EXISTS hack_grant_teams (
  grant_id UUID PRIMARY KEY REFERENCES oauth_grants(id) ON DELETE CASCADE,
  team_id UUID NOT NULL,
  selected_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

INSERT INTO hack_grant_teams (grant_id, team_id)
SELECT id, selected_team_id FROM oauth_grants WHERE selected_team_id IS NOT NULL
ON CONFLICT (grant_id) DO UPDATE SET team_id = EXCLUDED.team_id;
