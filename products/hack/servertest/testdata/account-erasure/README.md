Statement roles recorded against the unmodified production implementation at
bde6f8f71b02d83219ca3c9f4362624da1fbc2de, before the D2-S8 move, using
`scripts/split/seed/seed.sql` on the disposable TEST_PG_URL server.
`TestAccountErasureStatementOrder` defines each fixture and classifies every
statement including transaction boundaries. No changes to the sequences are
expected: the holding lookup still reads events, from the product hook.
