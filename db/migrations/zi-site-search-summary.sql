-- Find sites by description (SIM-189), 2026-10-23: compact summaries extracted
-- once per deploy from index.html. NULL means never attempted; empty strings
-- mean processed with nothing found. Safe to apply again on existing databases.
ALTER TABLE sites ADD COLUMN IF NOT EXISTS summary_title TEXT;
ALTER TABLE sites ADD COLUMN IF NOT EXISTS summary_description TEXT;
ALTER TABLE sites ADD COLUMN IF NOT EXISTS summary_headings TEXT;
ALTER TABLE sites ADD COLUMN IF NOT EXISTS summary_text TEXT;
