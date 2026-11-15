-- R2-S5c: one random id per install, also written to <DATA_DIR>/.install-id, so
-- a maintenance command can refuse a database and a data directory that do not
-- belong together. The server writes the single row at start; nothing else does.
CREATE TABLE IF NOT EXISTS install_identity (
    singleton  BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK (singleton),
    id         TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
