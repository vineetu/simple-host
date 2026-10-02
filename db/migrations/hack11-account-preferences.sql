-- Hosted-event account preferences shared across events and devices.
CREATE TABLE IF NOT EXISTS hack_account_preferences (
  user_id UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
  theme TEXT NOT NULL DEFAULT 'system' CHECK (theme IN ('system','light','dark')),
  organiser_walkthrough_done BOOLEAN NOT NULL DEFAULT FALSE,
  judge_walkthrough_done BOOLEAN NOT NULL DEFAULT FALSE
);
