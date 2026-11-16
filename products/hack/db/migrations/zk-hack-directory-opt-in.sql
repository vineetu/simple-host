-- Leave existing event listings unchanged.
ALTER TABLE events ALTER COLUMN directory_listed SET DEFAULT FALSE;
