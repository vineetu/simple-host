-- The table `simple-host migrate` records applied files in (completeness plan,
-- cp/ops-rel). migrate creates it itself before running anything, so this file
-- is here for the record and for a database where SQL is applied by hand.
-- Idempotent; a new empty table takes no lock on anything else.
CREATE TABLE IF NOT EXISTS schema_migrations (
  name       TEXT PRIMARY KEY,
  applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
