-- Hosted event judging options: restricted assignments, track panels and result rules.
ALTER TABLE events ADD COLUMN IF NOT EXISTS score_mode TEXT NOT NULL DEFAULT 'raw';
ALTER TABLE events ADD COLUMN IF NOT EXISTS tie_criterion_id UUID REFERENCES rubric_criteria(id) ON DELETE SET NULL;
ALTER TABLE events ADD COLUMN IF NOT EXISTS public_scores BOOLEAN NOT NULL DEFAULT TRUE;
ALTER TABLE events ADD COLUMN IF NOT EXISTS public_ranks BOOLEAN NOT NULL DEFAULT TRUE;

CREATE TABLE IF NOT EXISTS event_track_panels (
  event_id UUID NOT NULL REFERENCES events(id) ON DELETE CASCADE,
  track_id UUID NOT NULL REFERENCES event_tracks(id) ON DELETE CASCADE,
  judge_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  PRIMARY KEY (event_id, track_id, judge_id)
);
CREATE INDEX IF NOT EXISTS event_track_panels_judge_idx ON event_track_panels(event_id, judge_id);
