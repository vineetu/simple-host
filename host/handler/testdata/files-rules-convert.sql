-- FR-S1 one-off: every files resource without rules gets the rules that allow
-- exactly what its current access allows. Run once per database (simple-host
-- and simple-hack), after the FR-S1 binary is live:
--   psql "$DSN" -X -v ON_ERROR_STOP=1 -f convert.sql
-- One transaction. Any access shape outside the seven approved ones raises and
-- changes nothing. The old settings go to rules_converted_from.
-- host/handler/testdata/files-rules-convert.sql is a byte-identical copy that
-- TestFilesRulesConvertKeepsAccess runs.
BEGIN;
LOCK TABLE site_storage_resources IN SHARE ROW EXCLUSIVE MODE;

CREATE TEMP TABLE fr_s1_plan ON COMMIT DROP AS
WITH files AS (
  -- Effective access: the acc_* columns, or the legacy fields when they are NULL.
  SELECT r.*, CASE
      WHEN r.acc_read IS NOT NULL THEN ARRAY[r.acc_read, r.acc_add, r.acc_edit, r.acc_delete]
      WHEN r.write_mode = 'add' THEN ARRAY[r.read_policy, CASE WHEN r.read_policy = 'own' AND r.write_policy = 'anyone' THEN 'signed-in' ELSE r.write_policy END, 'owner', 'owner']
      WHEN r.read_policy = 'own' THEN ARRAY['own', 'owner', 'owner', 'owner']
      ELSE ARRAY[r.read_policy, r.write_policy, r.write_policy, r.write_policy]
    END AS m
  FROM site_storage_resources r
  WHERE r.kind = 'files' AND r.rules IS NULL
), approved(m) AS (VALUES
  -- read, add, edit, delete
  (ARRAY['anyone', 'owner', 'owner', 'owner']),          -- public
  (ARRAY['owner', 'anyone', 'owner', 'owner']),          -- inbox
  (ARRAY['owner', 'owner', 'owner', 'owner']),           -- private
  (ARRAY['anyone', 'signed-in', 'owner', 'own']),        -- wall
  (ARRAY['own', 'signed-in', 'owner', 'owner']),         -- records
  (ARRAY['signed-in', 'signed-in', 'own', 'own']),       -- custom
  (ARRAY['anyone', 'anyone', 'owner', 'owner'])          -- custom or legacy
)
SELECT f.site_id, f.name, f.m, a.m IS NOT NULL AS approved,
  jsonb_build_object('version', 1, 'by', 'FR-S1 convert.sql', 'effective', to_jsonb(f.m), 'original', jsonb_build_object(
    'read_policy', f.read_policy, 'write_policy', f.write_policy, 'write_mode', f.write_mode,
    'acc_read', f.acc_read, 'acc_add', f.acc_add, 'acc_edit', f.acc_edit, 'acc_delete', f.acc_delete,
    'preset_base', f.preset_base, 'approve_first', f.approve_first, 'tables', '[]'::jsonb)) AS converted_from,
  (SELECT jsonb_build_object('version', 1, 'policies', COALESCE(jsonb_agg(jsonb_build_object(
      'name', c.cmd, 'command', c.cmd, CASE WHEN c.cmd = 'add' THEN 'check' ELSE 'using' END,
      CASE f.m[c.i]
        WHEN 'anyone' THEN 'true'
        WHEN 'signed-in' THEN 'auth.signed_in'
        WHEN 'own' THEN 'visitor_id == auth.id || auth.role == "owner"'
        WHEN 'owner' THEN 'auth.role == "owner"'
      END) ORDER BY c.i), '[]'::jsonb))
    FROM unnest(ARRAY['read', 'add', 'edit', 'delete']) WITH ORDINALITY AS c(cmd, i)
    WHERE f.m[c.i] <> 'nobody') AS rules
FROM files f LEFT JOIN approved a ON a.m = f.m;

DO $$
DECLARE refused text;
BEGIN
  SELECT string_agg(site_id::text || '/' || name || ' (' || array_to_string(m, ' | ', 'NULL') || ')', ', ') INTO refused
  FROM fr_s1_plan WHERE NOT approved;
  IF refused IS NOT NULL THEN
    RAISE EXCEPTION 'FR-S1: access not in the approved table, nothing changed: %', refused;
  END IF;
END $$;

UPDATE site_storage_resources r SET
  rules = p.rules, rules_converted_from = p.converted_from,
  acc_read = 'nobody', acc_add = 'nobody', acc_edit = 'nobody', acc_delete = 'nobody',
  preset_base = 'rules', read_policy = 'owner', write_policy = 'owner', write_mode = 'add', approve_first = false
FROM fr_s1_plan p
WHERE r.site_id = p.site_id AND r.name = p.name AND r.kind = 'files' AND r.rules IS NULL;

COMMIT;
