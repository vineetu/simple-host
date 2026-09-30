-- Honest API numbers on the admin page (additive and idempotent).
--
-- 1. Calls from this server itself (loopback and the box's own public address:
--    health checks, canaries, local tools) are counted here,
--    apart from api_request_daily, so they never show as calls or errors.
CREATE TABLE IF NOT EXISTS api_self_daily (
  day    DATE NOT NULL,
  route  TEXT NOT NULL,
  status SMALLINT NOT NULL,
  calls  BIGINT NOT NULL DEFAULT 0,
  PRIMARY KEY (day, route, status)
);

-- 2. Requests that matched no API route were counted as API calls: under the
--    site catch-all "GET /" (bots probing /v1/<anything>), or, with no route
--    for the method, as a 405 under their own path. They are not API calls.
--    Take each out of the growth count of the kind the old build put it in
--    (the CASE mirrors apiRouteGroup in internal/handler/apigrowth.go), then
--    delete them. A second run finds none and changes nothing.
UPDATE api_growth_daily g
SET calls = GREATEST(g.calls - u.calls, 0)
FROM (
  SELECT day,
    CASE
      WHEN route !~ '^[A-Z]+ /v1/' THEN 'other'
      WHEN route ~ '^[A-Z]+ /v1/admin(/|$)' THEN 'admin'
      WHEN route ~ '^[A-Z]+ /v1/(auth|visitor|site-unlock)(/|$)' THEN 'auth'
      WHEN route ~ '^[A-Z]+ /v1/me/(keys|api-key|email|identities|sign-out|connections)(/|$)' THEN 'auth'
      WHEN route ~ '^[A-Z]+ /v1/data-notify(/|$)' THEN 'data'
      WHEN route ~ '^[A-Z]+ /v1/(generate|events|export)(/|$)' THEN 'deploy'
      WHEN route ~ '^[A-Z]+ /v1/(u/[^/]+/)?sites/[^/]+/(state|data|collections|me|savers)(/|$)' THEN 'data'
      WHEN route ~ '^[A-Z]+ /v1/(u/[^/]+/)?sites/[^/]+/visitor(/|$)' THEN 'auth'
      WHEN route ~ '^[A-Z]+ /v1/(u|sites)(/|$)' THEN 'deploy'
      ELSE 'other'
    END AS grp,
    SUM(calls) AS calls
  FROM api_request_daily
  WHERE (route ~ '^[A-Z]+ /' AND route !~ '^[A-Z]+ /(v1/|mcp$|mcp/)')
     OR (status = 405 AND route ~ '^[A-Z]+ /v1/')
  GROUP BY 1, 2
) u
WHERE g.day = u.day AND g.dim = 'group' AND g.key = u.grp;

DELETE FROM api_request_daily
WHERE (route ~ '^[A-Z]+ /' AND route !~ '^[A-Z]+ /(v1/|mcp$|mcp/)')
   OR (status = 405 AND route ~ '^[A-Z]+ /v1/');
