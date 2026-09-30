-- API growth for the admin page (GET /v1/admin/growth): API calls (/v1/* and
-- /mcp) per UTC day, counted twice over: once by caller country (dim
-- 'country', key an ISO-3166 alpha-2 code, 'XX' when unknown or this server
-- itself) and once by kind of call (dim 'group', key deploy / data / auth /
-- connector / admin / other). No address of any kind is stored. Rows older
-- than API_GROWTH_RETENTION_DAYS are pruned. Additive and idempotent.
CREATE TABLE IF NOT EXISTS api_growth_daily (
  day   DATE NOT NULL,
  dim   TEXT NOT NULL,
  key   TEXT NOT NULL,
  calls BIGINT NOT NULL DEFAULT 0,
  PRIMARY KEY (day, dim, key)
);
