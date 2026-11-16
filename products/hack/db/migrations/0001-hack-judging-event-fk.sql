-- Restore event foreign keys accidentally dropped from schema.sql in a550ab1f;
-- fresh installs from hack-v0.8.14 lack them. These tables are small, so the
-- cleanup and constraint checks are safe on a live database.
DO $$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'event_conflicts'::regclass AND conname = 'event_conflicts_event_id_fkey') THEN
    DELETE FROM event_conflicts c WHERE NOT EXISTS (SELECT 1 FROM events e WHERE e.id = c.event_id);
    ALTER TABLE event_conflicts ADD CONSTRAINT event_conflicts_event_id_fkey
      FOREIGN KEY (event_id) REFERENCES events(id) ON DELETE CASCADE;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'event_scores'::regclass AND conname = 'event_scores_event_id_fkey') THEN
    DELETE FROM event_scores s WHERE NOT EXISTS (SELECT 1 FROM events e WHERE e.id = s.event_id);
    ALTER TABLE event_scores ADD CONSTRAINT event_scores_event_id_fkey
      FOREIGN KEY (event_id) REFERENCES events(id) ON DELETE CASCADE;
  END IF;
END $$;
