-- Tables were created as postgres and the service role had no grants.
-- Found 2026-09-05; already applied to production by hand.
GRANT SELECT, INSERT, UPDATE, DELETE ON TABLE visitor_sessions, visitor_establish_tokens TO simplehost;
