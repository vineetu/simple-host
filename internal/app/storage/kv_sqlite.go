package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	ruleengine "github.com/vsriram/simple-host/host/storage/rules"
	"log"
	"os"
	"time"
	"unicode/utf8"

	sqlite3 "github.com/ncruces/go-sqlite3"
	repository "github.com/vsriram/simple-host/internal/repository/storage"
	runtime "github.com/vsriram/simple-host/internal/storage"
)

func (s *Service) createKVSQLite(ctx context.Context, call Call, resource string) error {
	return s.deps.SiteSlots.Admit(ctx, call.SiteID, func(ctx context.Context, a *Admission) error {
		_, done, err := runtime.OpenRuntimeKV(ctx, runtime.RuntimeKVPath(call.RuntimeDir, resource), true, true)
		if err != nil {
			return err
		}
		defer closeSQLite(done)
		return nil
	})
}

// clearKVWritersSQLite forgets every writer of a namespace kept on SQLite, as
// ClearResourceWriters does for one kept on Postgres. save does the Postgres
// work and calls commit right before its own commit; any earlier failure rolls
// the file back with the Postgres transaction. The caller owns the site write
// lock.
func (s *Service) clearKVWritersSQLite(ctx context.Context, call Call, resource string, save func(commit func() error) error) error {
	return s.deps.SiteSlots.Admit(ctx, call.SiteID, func(ctx context.Context, _ *Admission) error {
		conn, done, err := runtime.OpenRuntimeKV(ctx, runtime.RuntimeKVPath(call.RuntimeDir, resource), true, false)
		if err != nil {
			return err
		}
		defer closeSQLite(done)
		if err = conn.Exec("BEGIN IMMEDIATE"); err != nil {
			return err
		}
		defer conn.Exec("ROLLBACK")
		if err = runtime.RuntimeKVClearWriters(conn); err != nil {
			return err
		}
		return save(func() error {
			if err := conn.Exec("COMMIT"); err != nil {
				return err
			}
			if err := conn.Exec("PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
				log.Print("storage: KV checkpoint failed after commit")
			}
			return nil
		})
	})
}

// withKV keeps the connection and all accounting reads inside one admission.
// The caller already owns the site write lock for a mutation.
func (s *Service) withKV(ctx context.Context, call Call, write bool, fn func(context.Context, *sqlite3.Conn, *Admission) error) error {
	if kvHasRules(call) && call.Resource.KVStore != "sqlite" {
		return rulesInvalid()
	}
	if call.Resource.KVStore != "sqlite" {
		return fn(ctx, nil, nil)
	}
	err := s.deps.SiteSlots.Admit(ctx, call.SiteID, func(ctx context.Context, a *Admission) error {
		if !write && s.deps.SQLiteTimeout > 0 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, s.deps.SQLiteTimeout)
			defer cancel()
		}
		conn, done, err := runtime.OpenRuntimeKV(ctx, runtime.RuntimeKVPath(call.RuntimeDir, call.ResourceName), write, false)
		if err != nil {
			return err
		}
		defer closeSQLite(done)
		return fn(ctx, conn, a)
	})
	return kvFileError(err)
}

func (s *Service) kvWriter(ctx context.Context, conn *sqlite3.Conn, siteID, resource, key string) (string, error) {
	if conn == nil {
		return repository.KVWriter(ctx, s.db, siteID, resource, key)
	}
	// Postgres rejects these during the same author lookup, before a write.
	if !utf8.ValidString(key) {
		return "", errors.New("invalid UTF-8 key")
	}
	item, err := runtime.RuntimeKVGet(conn, key, "")
	return item.VisitorID, err
}

func (s *Service) putKVSQLite(ctx context.Context, conn *sqlite3.Conn, a *Admission, call Call, key string, value json.RawMessage, exists bool) error {
	usage, err := s.measureSiteStorageAdmitted(ctx, call.SiteID, call.RuntimeDir)
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
	normalized, err := repository.KVNormalize(ctx, s.db, value)
	if err != nil {
		return &Error{Code: "invalid_value", Message: "invalid JSON value"}
	}
	overQuota := s.BudgetUsed(usage)-oldBytes+int64(len(key)+len(normalized)) > SiteLimitBytes(s.deps.SiteLimit, s.deps.CombinedPool)
	if overQuota && !kvHasRules(call) {
		return kvFull()
	}
	if !kvHasRules(call) {
		if err = conn.Exec("BEGIN IMMEDIATE"); err != nil {
			return err
		}
		defer conn.Exec("ROLLBACK")
	}
	if kvHasRules(call) {
		return s.putRuleKVSQLite(ctx, conn, a, call, key, normalized, exists, oldBytes, overQuota)
	}
	if err = runtime.RuntimeKVPut(conn, key, normalized, StampID(call.Owner, call.VisitorID), exists, oldBytes); err != nil {
		return err
	}
	return s.commitKVSQLite(ctx, conn, a)
}

func (s *Service) commitKVSQLite(ctx context.Context, conn *sqlite3.Conn, a *Admission) error {
	if err := conn.Exec("COMMIT"); err != nil {
		return err
	}
	a.Committed()
	s.deps.Committed(ctx)
	if err := conn.Exec("PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		log.Print("storage: KV checkpoint failed after commit")
	}
	return nil
}

func (s *Service) deleteKVSQLite(ctx context.Context, call Call, key, filter string) (n int64, writer string, err error) {
	ctx, unlock, err := s.writeLock(ctx, call.OwnerID+"/"+call.SiteName, s.deps.WriteLockWait, s.deps.WriteTimeout, s.deps.Recheck)
	if err != nil {
		return 0, "", err
	}
	defer unlock()
	err = s.withKV(ctx, call, true, func(ctx context.Context, conn *sqlite3.Conn, a *Admission) error {
		if kvHasRules(call) {
			return s.deleteRuleKVSQLite(ctx, conn, a, &call, key, &n, &writer)
		}
		item, e := runtime.RuntimeKVGet(conn, key, filter)
		if errors.Is(e, sql.ErrNoRows) {
			if filter != "" {
				return &Error{Code: "key_not_found", Message: "key not found"}
			}
			s.deps.Committed(ctx)
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
		return s.commitKVSQLite(ctx, conn, a)
	})
	return
}

func kvFileError(err error) error {
	if errors.Is(err, sqlite3.BUSY) || errors.Is(err, sqlite3.LOCKED) {
		return sqliteBusy()
	}
	if errors.Is(err, os.ErrNotExist) {
		return &Error{Code: "storage_failed", Message: "key-value namespace file is missing"}
	}
	return err
}

// KVFileStats is the admitted disk half of paper metrics. A failure discards
// this site's partial result; the collector can skip and count that site.
func (s *Service) KVFileStats(ctx context.Context, siteID, root string) (entries, bytes int64, err error) {
	names, err := repository.SQLiteKVNamespaces(ctx, s.db, siteID)
	if err != nil {
		return 0, 0, err
	}
	for _, name := range names {
		var n, b int64
		err = s.deps.SiteSlots.Admit(ctx, siteID, func(ctx context.Context, _ *Admission) error {
			conn, done, e := runtime.OpenRuntimeKV(ctx, runtime.RuntimeKVPath(root, name), false, false)
			if e != nil {
				return e
			}
			defer closeSQLite(done)
			n, b, e = runtime.RuntimeKVStats(conn)
			return e
		})
		if err != nil {
			return 0, 0, kvFileError(err)
		}
		entries += n
		bytes += b
	}
	return entries, bytes, nil
}

// Owner tools keep their unrestricted path; owner-on-site is a rule binding.
func kvHasRules(c Call) bool { return !c.Owner && c.Resource.RulesStatus() != "preset" }

func (s *Service) kvAccess(c *Call, action string, op ruleengine.Op) (string, error) {
	if !kvHasRules(*c) {
		return s.Access(c, action)
	}
	if err := s.Gate(*c, op != ruleengine.OpRead); err != nil {
		return "", err
	}
	if c.Resource.KVStore != "sqlite" {
		return "", rulesInvalid()
	}
	if err := s.authorizeRuleOperation(c.Context, c, "kv", op); err != nil {
		return "", err
	}
	return "", nil
}

func (s *Service) compileKVRules(c *Call) error {
	if c.Resource.RulesStatus() != "rules" {
		return rulesInvalid()
	}
	return s.compileCallRules(c, ruleengine.Table{Name: "kv", RowKey: `"key"`, Columns: []ruleengine.Column{{Name: "key"}, {Name: "value"}, {Name: "visitor_id"}}})
}

func (s *Service) kvRuleRead(ctx context.Context, c *Call) (ruleengine.Predicate, error) {
	if err := s.compileKVRules(c); err != nil {
		return ruleengine.Predicate{}, err
	}
	if err := s.ruleValues(ctx, c, ruleengine.OpRead, true); err != nil {
		return ruleengine.Predicate{}, err
	}
	if err := s.ruleDecision(c, ruleengine.OpRead); err != nil {
		return ruleengine.Predicate{}, err
	}
	return c.rulePlan.Using(ruleengine.OpRead, c.ruleValues)
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

func (s *Service) getRuleKV(ctx context.Context, conn *sqlite3.Conn, c *Call, key string) ([]runtime.KVEntry, error) {
	p, err := s.kvRuleRead(ctx, c)
	if err != nil {
		return nil, err
	}
	query, args, err := runtime.RuleSelectOne("kv", `"key"`, ruleengine.Text(key), p)
	if err != nil {
		return nil, err
	}
	return kvRuleItems(conn, query, args)
}

func (s *Service) listRuleKV(ctx context.Context, conn *sqlite3.Conn, c *Call, prefix, after string, limit int) ([]runtime.KVEntry, error) {
	p, err := s.kvRuleRead(ctx, c)
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

func (s *Service) prepareKVRuleWrite(c *Call, action string, exists bool) error {
	if err := s.compileKVRules(c); err != nil {
		return err
	}
	op := ruleengine.OpAdd
	if exists {
		op = ruleengine.OpEdit
	}
	// Add bindings are needed for the existing-key fallback even on an edit.
	if err := s.ruleValues(c.Context, c, op, true, ruleengine.OpAdd); err != nil {
		return err
	}
	if exists {
		checked := cloneRuleValues(c.ruleValues)
		checked["auth.checked"] = ruleengine.Bool(true)
		if c.rulePlan.Partial(op, c.ruleValues) == ruleengine.False && c.rulePlan.Partial(ruleengine.OpAdd, checked) != ruleengine.False {
			return &Error{Code: "key_exists", Message: "this key already exists"}
		}
	}
	unchecked := cloneRuleValues(c.ruleValues)
	unchecked["auth.checked"] = ruleengine.Bool(false)
	c.CheckedPost = action == "add" && c.SpamCheck == "ok" && c.rulePlan.ReferencesBinding(op, "auth.checked") && c.rulePlan.Partial(op, unchecked) != ruleengine.True
	if err := s.ruleDecision(c, op); err != nil {
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

func (s *Service) putRuleKVSQLite(ctx context.Context, conn *sqlite3.Conn, a *Admission, c Call, key, value string, exists bool, oldBytes int64, overQuota bool) error {
	op := ruleengine.OpAdd
	if exists {
		op = ruleengine.OpEdit
		read, edit, err := ruleWritePredicates(c, op)
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
			return &Error{Code: "key_exists", Message: "this key already exists"}
		}
		if err = kvRuleExec(conn, `UPDATE kv_meta SET logical_bytes=logical_bytes+? WHERE id=1`, []ruleengine.Value{ruleengine.Int(int64(len(key)+len(value)) - oldBytes)}); err != nil {
			return err
		}
	} else if err := runtime.RuntimeKVPut(conn, key, value, StampID(c.Owner, c.VisitorID), false, 0); err != nil {
		return err
	}
	check, err := c.rulePlan.Check(op, c.ruleValues)
	if err != nil {
		return rulesInvalid()
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
			return &Error{Code: "key_exists", Message: "this key already exists"}
		}
		if err := s.ruleDecision(&c, op); err != nil {
			return err
		}
		return &Error{Code: "forbidden", Message: "add is not open to this visitor here"}
	}
	// Check visibility and the stored row before reporting quota: a hidden
	// key must still answer key_exists, regardless of its charged size.
	if overQuota {
		return kvFull()
	}
	return s.commitKVSQLite(ctx, conn, a)
}

func kvFull() *Error {
	return &Error{Code: "site_full", Message: "KV and SQLite storage is full; remove unused data before retrying"}
}

func (s *Service) deleteRuleKVSQLite(ctx context.Context, conn *sqlite3.Conn, a *Admission, c *Call, key string, n *int64, writer *string) error {
	c.Context = ctx
	if err := s.compileKVRules(c); err != nil {
		return err
	}
	if err := s.ruleValues(ctx, c, ruleengine.OpDelete, true); err != nil {
		return err
	}
	if err := s.ruleDecision(c, ruleengine.OpDelete); err != nil {
		return err
	}
	read, del, err := ruleWritePredicates(*c, ruleengine.OpDelete)
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
		// not remove, as the preset path does for a non-empty filter.
		if c.VisitorAI || c.rulePlan.Partial(ruleengine.OpDelete, c.ruleValues) != ruleengine.True || c.rulePlan.Partial(ruleengine.OpRead, c.ruleValues) != ruleengine.True {
			return &Error{Code: "key_not_found", Message: "key not found"}
		}
		// A delete that succeeds without removing anything still counts as a
		// committed write, as on the preset path (R3-S5a ruling 5).
		s.deps.Committed(ctx)
		return nil
	}
	if err := kvRuleExec(conn, `UPDATE kv_meta SET entries=entries-1,logical_bytes=logical_bytes-? WHERE id=1`, []ruleengine.Value{ruleengine.Int(int64(len(key) + len(item.Value)))}); err != nil {
		return err
	}
	if err := s.commitKVSQLite(ctx, conn, a); err != nil {
		return err
	}
	*n, *writer = 1, item.VisitorID
	return nil
}
