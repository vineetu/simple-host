-- One name per person (2026-11-06). Keep named accounts untouched; recover
-- the latest nonblank historical event name for accounts without a name.
-- Apply owns the transaction. Safe to repeat, including on a fresh schema.
WITH candidates AS (
  SELECT user_id, event_id, btrim(display_name) AS name, joined_at AS named_at, 0 AS source
    FROM event_members WHERE COALESCE(btrim(display_name),'') <> ''
  UNION ALL
  SELECT user_id, event_id, btrim(display_name), left_at, 1
    FROM event_former_judges WHERE COALESCE(btrim(display_name),'') <> ''
), latest AS (
  SELECT DISTINCT ON (user_id) user_id, name
    FROM candidates
   ORDER BY user_id, named_at DESC, event_id DESC, source DESC
)
UPDATE users u SET display_name = left(latest.name, 100)
  FROM latest
 WHERE u.id = latest.user_id AND COALESCE(btrim(u.display_name),'') = '';
