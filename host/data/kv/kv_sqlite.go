package kv

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"time"
	"unicode/utf8"

	"github.com/vsriram/simple-host/host/data"

	"github.com/vsriram/simple-host/host/papermetrics"

	ruleengine "github.com/vsriram/simple-host/host/storage/rules"

	sqlite3 "github.com/ncruces/go-sqlite3"
	repository "github.com/vsriram/simple-host/host/data/repo"
	runtime "github.com/vsriram/simple-host/host/storage"
)

// withKV keeps the connection and all accounting reads inside one admission.
// The caller already owns the site write lock for a mutation.
func withKV(s *data.Service, ctx context.Context, call data.Call, write bool, fn func(context.Context, *sqlite3.Conn, *data.Admission) error) error {
	err := s.Deps.SiteSlots.Admit(ctx, call.SiteID, func(ctx context.Context, a *data.Admission) error {
		if !write && s.Deps.SQLiteTimeout > 0 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, s.Deps.SQLiteTimeout)
			defer cancel()
		}
		conn, done, err := runtime.OpenRuntimeKV(ctx, runtime.RuntimeKVPath(call.RuntimeDir, call.ResourceName), write, false)
		if err != nil {
			return err
		}
		defer data.CloseSQLite(done)
		return fn(ctx, conn, a)
	})
	return data.KVFileError(err)
}

func kvWriter(conn *sqlite3.Conn, key string) (string, error) {
	// Reject these during the author lookup, before a write.
	if !utf8.ValidString(key) {
		return "", errors.New("invalid UTF-8 key")
	}
	item, err := runtime.RuntimeKVGet(conn, key, "")
	return item.VisitorID, err
}

func putKVSQLite(s *data.Service, ctx context.Context, conn *sqlite3.Conn, a *data.Admission, call data.Call, key string, value json.RawMessage, exists bool) error {
	usage, err := s.MeasureSiteStorageAdmitted(ctx, call.SiteID, call.RuntimeDir)
	if err != nil {
		return err
	}
	old, err := runtime.RuntimeKVGet(conn, key, "")
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	var oldBytes int64
	if exists {
		oldBytes = int64(len(key) + len(old.Value))
	}
	normalized, err := repository.KVNormalize(ctx, s.DB, value)
	if err != nil {
		return &data.Error{Code: "invalid_value", Message: "invalid JSON value"}
	}
	overQuota := s.BudgetUsed(usage)-oldBytes+int64(len(key)+len(normalized)) > data.SiteLimitBytes(s.Deps.SiteLimit, s.Deps.CombinedPool)
	if !call.Owner {
		return putRuleKVSQLite(s, ctx, conn, a, call, key, normalized, exists, oldBytes, overQuota)
	}
	if overQuota {
		return data.KVFull()
	}
	if err = conn.Exec("BEGIN IMMEDIATE"); err != nil {
		return err
	}
	defer conn.Exec("ROLLBACK")
	if err = runtime.RuntimeKVPut(conn, key, normalized, data.StampID(call.Owner, call.VisitorID), exists, oldBytes); err != nil {
		return err
	}
	return commitKVSQLite(s, ctx, conn, a)
}

func commitKVSQLite(s *data.Service, ctx context.Context, conn *sqlite3.Conn, a *data.Admission) error {
	if err := conn.Exec("COMMIT"); err != nil {
		return err
	}
	a.Committed()
	papermetrics.From(ctx).WriteCommitted(ctx)
	if err := conn.Exec("PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		log.Print("storage: KV checkpoint failed after commit")
	}
	return nil
}

func deleteKVSQLite(s *data.Service, ctx context.Context, call data.Call, key string) (n int64, writer string, err error) {
	ctx, unlock, err := s.WriteLock(ctx, call, true)
	if err != nil {
		return 0, "", err
	}
	defer unlock()
	err = withKV(s, ctx, call, true, func(ctx context.Context, conn *sqlite3.Conn, a *data.Admission) error {
		if !call.Owner {
			return deleteRuleKVSQLite(s, ctx, conn, a, &call, key, &n, &writer)
		}
		item, e := runtime.RuntimeKVGet(conn, key, "")
		if errors.Is(e, sql.ErrNoRows) {
			papermetrics.From(ctx).WriteCommitted(ctx)
			return nil
		}
		if e != nil {
			return e
		}
		if e = conn.Exec("BEGIN IMMEDIATE"); e != nil {
			return e
		}
		defer conn.Exec("ROLLBACK")
		if e = runtime.RuntimeKVDelete(conn, key, int64(len(key)+len(item.Value))); e != nil {
			return e
		}
		n, writer = 1, item.VisitorID
		return commitKVSQLite(s, ctx, conn, a)
	})
	return
}

func compileKVRules(s *data.Service, c *data.Call) error {
	if c.Resource.RulesStatus() != "rules" {
		return data.RulesInvalid()
	}
	return s.CompileCallRules(c, ruleengine.Table{Name: "kv", RowKey: `"key"`, Columns: []ruleengine.Column{{Name: "key"}, {Name: "value"}, {Name: "visitor_id"}}})
}

func kvRuleRead(s *data.Service, ctx context.Context, c *data.Call) (ruleengine.Predicate, error) {
	if err := compileKVRules(s, c); err != nil {
		return ruleengine.Predicate{}, err
	}
	if err := s.RuleValues(ctx, c, ruleengine.OpRead, true); err != nil {
		return ruleengine.Predicate{}, err
	}
	if err := s.RuleDecision(c, ruleengine.OpRead); err != nil {
		return ruleengine.Predicate{}, err
	}
	return c.RulePlan.Using(ruleengine.OpRead, c.RuleValues)
}

func kvRuleStatement(conn *sqlite3.Conn, query string, args []ruleengine.Value) (*sqlite3.Stmt, error) {
	stmt, _, err := conn.Prepare(query)
	if err != nil {
		return nil, err
	}
	if err := runtime.BindRuleArgs(stmt, 1, args); err != nil {
		stmt.Close()
		return nil, err
	}
	return stmt, nil
}

func kvRuleItems(conn *sqlite3.Conn, query string, args []ruleengine.Value) ([]runtime.KVEntry, error) {
	stmt, err := kvRuleStatement(conn, query, args)
	if err != nil {
		return nil, err
	}
	defer stmt.Close()
	var items []runtime.KVEntry
	for stmt.Step() {
		items = append(items, runtime.KVEntry{Key: stmt.ColumnText(0), Value: json.RawMessage(stmt.ColumnText(1)), VisitorID: stmt.ColumnText(2)})
	}
	return items, stmt.Err()
}

func getRuleKV(s *data.Service, ctx context.Context, conn *sqlite3.Conn, c *data.Call, key string) ([]runtime.KVEntry, error) {
	p, err := kvRuleRead(s, ctx, c)
	if err != nil {
		return nil, err
	}
	query, args, err := runtime.RuleSelectOne("kv", `"key"`, ruleengine.Text(key), p)
	if err != nil {
		return nil, err
	}
	return kvRuleItems(conn, query, args)
}

func listRuleKV(s *data.Service, ctx context.Context, conn *sqlite3.Conn, c *data.Call, prefix, after string, limit int) ([]runtime.KVEntry, error) {
	p, err := kvRuleRead(s, ctx, c)
	if err != nil {
		return nil, err
	}
	query, args := kvRuleListQuery(prefix, after, limit, p)
	return kvRuleItems(conn, query, args)
}

func kvRuleListQuery(prefix, after string, limit int, p ruleengine.Predicate) (string, []ruleengine.Value) {
	query, bounds := runtime.RuntimeKVListQuery(prefix, after)
	args := make([]ruleengine.Value, 0, len(bounds)+len(p.Args)+1)
	for _, bound := range bounds {
		args = append(args, ruleengine.Text(bound))
	}
	query += ` AND (` + p.SQL + `) ORDER BY key COLLATE BINARY LIMIT ?`
	args = append(args, p.Args...)
	args = append(args, ruleengine.Int(int64(limit)))
	return query, args
}

func prepareKVRuleWrite(s *data.Service, c *data.Call, action string, exists bool) error {
	if err := compileKVRules(s, c); err != nil {
		return err
	}
	op := ruleengine.OpAdd
	if exists {
		op = ruleengine.OpEdit
	}
	// Add bindings are needed for the existing-key fallback even on an edit.
	if err := s.RuleValues(c.Context, c, op, true, ruleengine.OpAdd); err != nil {
		return err
	}
	if exists {
		checked := data.CloneRuleValues(c.RuleValues)
		checked["auth.checked"] = ruleengine.Bool(true)
		if c.RulePlan.Partial(op, c.RuleValues) == ruleengine.False && c.RulePlan.Partial(ruleengine.OpAdd, checked) != ruleengine.False {
			return &data.Error{Code: "key_exists", Message: "this key already exists"}
		}
	}
	unchecked := data.CloneRuleValues(c.RuleValues)
	unchecked["auth.checked"] = ruleengine.Bool(false)
	c.CheckedPost = action == "add" && c.SpamCheck == "ok" && c.RulePlan.ReferencesBinding(op, "auth.checked") && c.RulePlan.Partial(op, unchecked) != ruleengine.True
	if err := s.RuleDecision(c, op); err != nil {
		return err
	}
	return nil
}

func kvRuleExec(conn *sqlite3.Conn, query string, args []ruleengine.Value) error {
	stmt, err := kvRuleStatement(conn, query, args)
	if err != nil {
		return err
	}
	defer stmt.Close()
	return stmt.Exec()
}

func putRuleKVSQLite(s *data.Service, ctx context.Context, conn *sqlite3.Conn, a *data.Admission, c data.Call, key, value string, exists bool, oldBytes int64, overQuota bool) error {
	op := ruleengine.OpAdd
	if exists {
		op = ruleengine.OpEdit
		read, edit, err := data.RuleWritePredicates(c, op)
		if err != nil {
			return err
		}
		stamp := time.Now().UTC().Format("2006-01-02T15:04:05.000000Z")
		query, args, err := runtime.RuleUpdate("kv", `"key"`, []string{"value", "updated_at"}, []ruleengine.Value{ruleengine.Text(value), ruleengine.Text(stamp)}, ruleengine.Text(key), read, edit, "")
		if err != nil {
			return err
		}
		if err = kvRuleExec(conn, query, args); err != nil {
			return err
		}
		if conn.Changes() == 0 {
			return &data.Error{Code: "key_exists", Message: "this key already exists"}
		}
		if err = kvRuleExec(conn, `UPDATE kv_meta SET logical_bytes=logical_bytes+? WHERE id=1`, []ruleengine.Value{ruleengine.Int(int64(len(key)+len(value)) - oldBytes)}); err != nil {
			return err
		}
	} else if err := runtime.RuntimeKVPut(conn, key, value, data.StampID(c.Owner, c.VisitorID), false, 0); err != nil {
		return err
	}
	check, err := c.RulePlan.Check(op, c.RuleValues)
	if err != nil {
		return data.RulesInvalid()
	}
	query, args, err := runtime.RuleCheck("kv", `"key"`, ruleengine.Text(key), check)
	if err != nil {
		return err
	}
	stmt, err := kvRuleStatement(conn, query, args)
	if err != nil {
		return err
	}
	passed := stmt.Step()
	err = stmt.Err()
	stmt.Close()
	if err != nil {
		return err
	}
	if !passed {
		if exists {
			return &data.Error{Code: "key_exists", Message: "this key already exists"}
		}
		if err := s.RuleDecision(&c, op); err != nil {
			return err
		}
		return &data.Error{Code: "forbidden", Message: "add is not open to this visitor here"}
	}
	// Check visibility and the stored row before reporting quota: a hidden
	// key must still answer key_exists, regardless of its charged size.
	if overQuota {
		return data.KVFull()
	}
	return commitKVSQLite(s, ctx, conn, a)
}

func deleteRuleKVSQLite(s *data.Service, ctx context.Context, conn *sqlite3.Conn, a *data.Admission, c *data.Call, key string, n *int64, writer *string) error {
	c.Context = ctx
	if err := compileKVRules(s, c); err != nil {
		return err
	}
	if err := s.RuleValues(ctx, c, ruleengine.OpDelete, true); err != nil {
		return err
	}
	if err := s.RuleDecision(c, ruleengine.OpDelete); err != nil {
		return err
	}
	read, del, err := data.RuleWritePredicates(*c, ruleengine.OpDelete)
	if err != nil {
		return err
	}
	if err := conn.Exec("BEGIN IMMEDIATE"); err != nil {
		return err
	}
	defer conn.Exec("ROLLBACK")
	item, err := runtime.RuntimeKVGet(conn, key, "")
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	query, args, err := runtime.RuleDelete("kv", `"key"`, ruleengine.Text(key), read, del, "")
	if err != nil {
		return err
	}
	if err := kvRuleExec(conn, query, args); err != nil {
		return err
	}
	if conn.Changes() == 0 {
		// A visitor's AI deletes only its author's keys (ruleAIConstraint), so
		// like any other author-filtered delete it reports the key it could
		// not remove.
		if c.VisitorAI || c.RulePlan.Partial(ruleengine.OpDelete, c.RuleValues) != ruleengine.True || c.RulePlan.Partial(ruleengine.OpRead, c.RuleValues) != ruleengine.True {
			return &data.Error{Code: "key_not_found", Message: "key not found"}
		}
		// A delete that succeeds without removing anything still counts as a
		// committed write, as for the owner (R3-S5a ruling 5).
		papermetrics.From(ctx).WriteCommitted(ctx)
		return nil
	}
	if err := kvRuleExec(conn, `UPDATE kv_meta SET entries=entries-1,logical_bytes=logical_bytes-? WHERE id=1`, []ruleengine.Value{ruleengine.Int(int64(len(key) + len(item.Value)))}); err != nil {
		return err
	}
	if err := commitKVSQLite(s, ctx, conn, a); err != nil {
		return err
	}
	*n, *writer = 1, item.VisitorID
	return nil
}
