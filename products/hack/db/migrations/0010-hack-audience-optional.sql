-- SIM-395 (owner, 2026-11-06): "Who it's for" (audience) is Optional in every
-- entry-rules preset. Events still on a preset get the new default; custom
-- rules (entry_preset = 'custom') stay as the organiser set them, and events
-- from Closed on keep the rules they were judged under. Data only,
-- safe to repeat, and a no-op on a fresh schema.
UPDATE events
   SET entry_rules = jsonb_set(entry_rules, '{fields,audience}', '"optional"')
 WHERE entry_preset <> 'custom'
   AND stage IN ('draft', 'open', 'building')
   AND entry_rules #>> '{fields,audience}' = 'required';
