-- Entries and participant journey, slice 1 (owner-delegated build plan,
-- 2026-11-02): the entry summary card (problem, audience, tools, maturity),
-- explicit submit/unsubmit against per-event rules, and the settle sweep.
-- One transaction; only adds things, so a rollback of the binary is safe.
--
-- `zu-` is reserved by the guardian-consent change in progress elsewhere
-- (wt-hack-audit1, feature/audit-quick-fixes), so this one is `zv-`: it
-- sorts after `zt-hack-teams-redesign.sql` (the newest on origin/main at the
-- time this was written) and after `zu-` whenever it lands.
ALTER TABLE event_entries
  ADD COLUMN IF NOT EXISTS problem      text    NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS audience     text    NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS tools        text[]  NOT NULL DEFAULT '{}',
  ADD COLUMN IF NOT EXISTS maturity     text    NOT NULL DEFAULT ''
        CHECK (maturity IN ('', 'working', 'partial', 'concept')),
  ADD COLUMN IF NOT EXISTS submitted_at timestamptz,
  ADD COLUMN IF NOT EXISTS submitted_by uuid REFERENCES users(id) ON DELETE SET NULL,
  ADD COLUMN IF NOT EXISTS submit_auto  boolean NOT NULL DEFAULT false,
  -- The deadline sweep has decided this entry (submitted it, or left it not
  -- submitted): reset by an extension, a stage move back to building, or
  -- the event deadline moving later.
  ADD COLUMN IF NOT EXISTS submit_settled_at timestamptz,
  -- Coordinator decision, 2026-11-02 (recorded in INTENT.md, owner may
  -- overrule): an incomplete draft settled as "not submitted" is still
  -- judged, carrying an Incomplete flag, rather than dropped from judging.
  -- excluded_at/by is the organiser's hand exclude of a flagged entry from
  -- the Entries screen (with a confirm) -- the only way such an entry stops
  -- being judged. Excluding a complete, submitted entry is allowed too (an
  -- organiser call, not gated on status).
  ADD COLUMN IF NOT EXISTS excluded_at timestamptz,
  ADD COLUMN IF NOT EXISTS excluded_by uuid REFERENCES users(id) ON DELETE SET NULL;

ALTER TABLE events
  ADD COLUMN IF NOT EXISTS entry_preset text NOT NULL DEFAULT 'custom'
        CHECK (entry_preset IN ('build', 'code', 'ideas', 'custom')),
  ADD COLUMN IF NOT EXISTS entry_rules jsonb NOT NULL DEFAULT '{}'::jsonb,
  ADD COLUMN IF NOT EXISTS judge_first text NOT NULL DEFAULT 'video'
        CHECK (judge_first IN ('video', 'site'));

-- Existing events: Custom rules built from entry_required (those lines
-- required, the rest optional).
UPDATE events SET entry_rules = jsonb_build_object('v', 1, 'fields',
  (SELECT jsonb_object_agg(f, CASE WHEN f = ANY(entry_required) THEN 'required' ELSE 'optional' END)
     FROM unnest(ARRAY['title','tagline','description','video_url','code_url','screenshot']) f))
 WHERE entry_rules = '{}'::jsonb;

-- Entries in events already past building count as submitted, so judging
-- queues do not change under this release.
UPDATE event_entries en
   SET submitted_at = COALESCE(t.pinned_at, en.updated_at), submit_auto = true, submit_settled_at = now()
  FROM event_teams t JOIN events e ON e.id = t.event_id
 WHERE t.id = en.team_id AND e.stage IN ('closed','judging','results','archived') AND en.submitted_at IS NULL;

CREATE INDEX IF NOT EXISTS event_entries_unsettled_idx
  ON event_entries (event_id) WHERE submitted_at IS NULL AND submit_settled_at IS NULL;
