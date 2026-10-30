-- Visitor connector attempts are counted across apps and process restarts.
CREATE TABLE IF NOT EXISTS visitor_ai_daily (
 user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 site_id UUID NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
 day DATE NOT NULL,
 writes INTEGER NOT NULL DEFAULT 0,
 PRIMARY KEY(user_id,site_id,day)
);
ALTER TABLE data_history ADD COLUMN IF NOT EXISTS actor_app TEXT NOT NULL DEFAULT '';

-- Storage audit markers must never join a same-named legacy collection.
ALTER TABLE data_history DROP CONSTRAINT IF EXISTS data_history_kind_check;
ALTER TABLE data_history ADD CONSTRAINT data_history_kind_check CHECK (kind IN ('state','list','storage'));
UPDATE data_history SET kind='storage'
 WHERE kind='list' AND item_id IS NULL AND prev IS NULL
 AND op IN ('add kv','edit kv','delete kv','add sqlite','edit sqlite','delete sqlite');
-- One last-visitor-use record per grant, independently of other app activity.
ALTER TABLE oauth_grants ADD COLUMN IF NOT EXISTS last_visitor_site JSONB;
