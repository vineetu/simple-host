-- Leave this event (SIM-340, 2026-10-31). A judge, or an organiser who judged, who leaves
-- keeps the scores that counted when they left: counted_team_ids is that set, frozen, so
-- later assignment or panel changes don't move them. Conflicts still apply.
CREATE TABLE IF NOT EXISTS event_former_judges (
  event_id         UUID NOT NULL REFERENCES events(id) ON DELETE CASCADE,
  user_id          UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  display_name     TEXT NOT NULL DEFAULT '',
  role             TEXT NOT NULL CHECK (role IN ('judge','organiser')),
  counted_team_ids UUID[] NOT NULL DEFAULT '{}',
  left_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (event_id, user_id)
);

-- Joining again in any role ends the record: a returning judge counts as a member again,
-- and someone who comes back as a participant must not keep counted scores. Rejoining as
-- a participant also deletes their event_scores rows outright (Grok security review,
-- 2026-10-31): a judge who left, rejoined as a participant, and could later be given the
-- judge link again should not have their old rows quietly start counting again.
CREATE OR REPLACE FUNCTION clear_hack_former_judge() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 DELETE FROM event_former_judges WHERE event_id = NEW.event_id AND user_id = NEW.user_id;
 IF NEW.role = 'participant' THEN
   DELETE FROM event_scores WHERE event_id = NEW.event_id AND judge_id = NEW.user_id;
 END IF;
 RETURN NULL;
END;
$$;
CREATE OR REPLACE TRIGGER hack_members_clear_former_judge
 AFTER INSERT ON event_members
 FOR EACH ROW EXECUTE FUNCTION clear_hack_former_judge();
