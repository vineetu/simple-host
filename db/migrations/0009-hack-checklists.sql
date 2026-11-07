-- SIM-393: shared optional-item marks and explicit rubric/conflict acknowledgements.
CREATE TABLE IF NOT EXISTS event_checklist_dismissals (
    event_id uuid NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    item_id text NOT NULL,
    marked_by uuid REFERENCES users(id) ON DELETE SET NULL,
    marked_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (event_id, item_id)
);
CREATE TABLE IF NOT EXISTS event_checklist_acknowledgements (
    event_id uuid NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    subject_id text NOT NULL,
    item_id text NOT NULL,
    version text NOT NULL DEFAULT '',
    acknowledged_by uuid REFERENCES users(id) ON DELETE SET NULL,
    acknowledged_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (event_id, subject_id, item_id)
);
