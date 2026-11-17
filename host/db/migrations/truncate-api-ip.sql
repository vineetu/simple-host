-- Truncate stored API caller IPs to their network: IPv4 → /24 (a.b.c.0),
-- IPv6 → /48. Matches truncateIP in internal/handler/apimetrics.go, which
-- writes new rows this way. Rows that collapse onto the same (day, ip) are
-- merged: calls summed, newest last_seen and its last_route kept.
--
-- Idempotent (already-truncated rows map to themselves) and all-or-nothing.
-- Loopback and unparseable values are left as they are, like the Go side.
-- Needs PostgreSQL 16+ (pg_input_is_valid).

BEGIN;

LOCK TABLE api_ip_daily IN SHARE ROW EXCLUSIVE MODE;

CREATE TEMP TABLE api_ip_daily_trunc ON COMMIT DROP AS
SELECT day, ip, SUM(calls) AS calls,
       (ARRAY_AGG(last_route ORDER BY last_seen DESC))[1] AS last_route,
       MAX(last_seen) AS last_seen
FROM (
  SELECT day, calls, last_route, last_seen,
    CASE
      WHEN NOT pg_input_is_valid(ip, 'inet') THEN ip
      WHEN ip::inet << '127.0.0.0/8'::inet OR host(ip::inet) = '::1' THEN ip
      -- IPv4-mapped IPv6 (::ffff:a.b.c.d): Go treats it as IPv4.
      WHEN family(ip::inet) = 6 AND ip::inet << '::ffff:0.0.0.0/96'::inet
        THEN host(network(set_masklen(regexp_replace(host(ip::inet), '^.*:', '')::inet, 24)))
      WHEN family(ip::inet) = 4 THEN host(network(set_masklen(ip::inet, 24)))
      ELSE host(network(set_masklen(ip::inet, 48)))
    END AS ip
  FROM api_ip_daily
) t
GROUP BY day, ip;

DELETE FROM api_ip_daily;
INSERT INTO api_ip_daily (day, ip, calls, last_route, last_seen)
SELECT day, ip, calls, last_route, last_seen FROM api_ip_daily_trunc;

COMMIT;
