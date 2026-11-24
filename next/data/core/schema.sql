-- data: one row per site namespace. The data itself is in
-- <data dir>/sites/<site id>/data/<name>.sqlite.
-- rules: {"<table, _values or _files>": <rule set>}. Empty = owner only.
CREATE TABLE namespaces (
  site_id          UUID NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
  name             TEXT NOT NULL,
  rules            JSONB NOT NULL DEFAULT '{}',
  turnstile_key    TEXT NOT NULL DEFAULT '',
  turnstile_secret TEXT NOT NULL DEFAULT '',
  created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (site_id, name)
);
