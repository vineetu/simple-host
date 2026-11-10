ALTER TABLE events ADD COLUMN IF NOT EXISTS organiser_judges uuid[] NOT NULL DEFAULT '{}'::uuid[];

-- Preserve current organisers with any score, including partial scores.
UPDATE events e SET organiser_judges = ARRAY(
 SELECT DISTINCT id FROM (
  SELECT unnest(e.organiser_judges) AS id
  UNION ALL
  SELECT m.user_id FROM event_members m WHERE m.event_id=e.id AND m.role='organiser'
   AND EXISTS (SELECT 1 FROM event_scores s WHERE s.event_id=e.id AND s.judge_id=m.user_id)
 ) selected ORDER BY id
);
