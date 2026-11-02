-- SIM-152: who made a version (?author= on a deploy), shown in the version list.
ALTER TABLE versions ADD COLUMN IF NOT EXISTS author TEXT NOT NULL DEFAULT '';
