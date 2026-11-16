-- SIM-394: the hack organiser assistant's per-organiser daily question count
-- and the instance-wide monthly spend ledger. hack_assistant_usage has no
-- ON DELETE effect beyond the row cascade: a deleted account's daily counts
-- disappear with it, same as event_members. hack_assistant_spend carries no
-- user id (an instance-wide total), so it survives any account deletion.
CREATE TABLE IF NOT EXISTS hack_assistant_usage (
    user_id   UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    day       DATE NOT NULL,
    questions INT NOT NULL DEFAULT 0,
    PRIMARY KEY (user_id, day)
);
CREATE TABLE IF NOT EXISTS hack_assistant_spend (
    month         DATE PRIMARY KEY,
    cost_micro_usd BIGINT NOT NULL DEFAULT 0
);
