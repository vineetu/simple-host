-- Failed event email attempts defer that row so other messages can be delivered.
ALTER TABLE event_announcement_deliveries ADD COLUMN IF NOT EXISTS attempted_at TIMESTAMPTZ;
ALTER TABLE event_entry_receipts ADD COLUMN IF NOT EXISTS attempted_at TIMESTAMPTZ;
