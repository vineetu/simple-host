package data

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log"
	"sort"
	"strings"
	"time"

	sqlite3 "github.com/ncruces/go-sqlite3"
	runtime "github.com/vsriram/simple-host/host/storage"
	ruleengine "github.com/vsriram/simple-host/host/storage/rules"
)

// addWrittenByColumn gives an existing table its written_by column on its
// first add for someone, in the same locked window as the add.
// authorizeTable already refused for on any table whose read rules are not
// limited to the row's visitor or the owner.
func addWrittenByColumn(conn *sqlite3.Conn, table string) error {
	err := runtime.EnsureWrittenByColumn(conn, table)
	if errors.Is(err, runtime.ErrWrittenBySchemaLocked) {
		return &Error{Code: "written_by_blocked", Message: "a trigger or view on this table may depend on its exact columns; remove or recreate it before adding a row with for"}
	}
	if err != nil {
		return sqliteRowFailure(err, &Error{Code: "internal_error", Message: "internal server error"})
	}
	return nil
}

// runTableWrite preserves the row-write error classification for table workflows.
func runTableWrite(conn *sqlite3.Conn, table string, owner bool, kind sqlite3.AuthorizerActionCode, query string, params []json.RawMessage, returning bool, resultID *int64, hooks ...func(string)) (int64, error) {
	if err := runtime.SetRowAuthorizer(conn, table, owner, kind); err != nil {
		return 0, sqliteRowFailure(err, &Error{Code: "internal_error", Message: "internal server error"})
	}
	id, stage, err := runtime.RunRowStatement(conn, query, params, returning, resultID, hooks...)
	if errors.Is(err, sqlite3.NOMEM) {
		return 0, sqliteFailure(err)
	}
	if stage == runtime.RowStatementPrepare {
		return 0, &Error{Code: "forbidden", Message: "this change would touch other tables; pages cannot make it, only the owner's tools"}
	}
	if stage == runtime.RowStatementBind {
		return 0, &Error{Code: "invalid_rows", Message: "invalid row request"}
	}
	if err != nil {
		switch {
		case errors.Is(err, sqlite3.CONSTRAINT_FOREIGNKEY):
			return 0, &Error{Code: "invalid_reference", Message: "referenced row not found"}
		case errors.Is(err, sqlite3.AUTH):
			return 0, &Error{Code: "forbidden", Message: "this change would touch other tables; pages cannot make it, only the owner's tools"}
		case errors.Is(err, sqlite3.CONSTRAINT):
			return 0, &Error{Code: "row_conflict", Message: "row cannot be saved"}
		case errors.Is(err, sqlite3.FULL):
			return 0, &Error{Code: "site_full", Message: "KV and SQLite storage is full; remove unused data before retrying"}
		default:
			return 0, &Error{Code: "invalid_rows", Message: "the change failed"}
		}
	}
	if returning && id == 0 {
		return 0, &Error{Code: "row_conflict", Message: "row cannot be saved"}
	}
	return id, nil
}

// checkRowReferences enforces the parent table's read policy on a row write.
func (s *Service) checkRowReferences(conn *sqlite3.Conn, c Call, table, rowid string, id int64) error {
	if c.Owner {
		return nil
	}
	_ = conn.SetAuthorizer(nil)
	status, err := runtime.CheckReferences(conn, table, rowid, id, func(parent string) (ruleengine.Predicate, error) {
		parentCall := c
		// Compile against the parent's actual columns and its own override.
		cols, order, err := runtime.TableXInfo(conn, parent)
		if err != nil {
			return ruleengine.Predicate{}, err
		}
		key, err := runtime.RowID(conn, parent, cols)
		if err != nil {
			return ruleengine.Predicate{}, err
		}
		predicate, err := s.ruleReadPredicate(c.Context, &parentCall, parent, rowMetadata{cols: cols, order: order, rowid: key})
		if e, ok := err.(*Error); ok && e.Code != "internal_error" && e.Code != "rules_invalid" {
			return ruleengine.Predicate{}, &Error{Code: "invalid_reference", Message: "referenced row not found"}
		}
		return predicate, err
	})
	if e, ok := err.(*Error); ok {
		return e
	}
	return rowReferenceError(status, err)
}

// rowReferenceError retains the reference check's wire error classification.
func rowReferenceError(status int, err error) error {
	if err == nil {
		return nil
	}
	if status == 404 {
		return sqliteRowFailure(err, &Error{Code: "invalid_reference", Message: "referenced row not found"})
	}
	return sqliteRowFailure(err, &Error{Code: "invalid_rows", Message: "cannot check references"})
}

// finishTableWrite commits before checkpointing; a checkpoint failure cannot
// turn an already committed change into a failed response.
func (s *Service) finishTableWrite(conn *sqlite3.Conn, c Call, commit func() error, a *Admission) error {
	if err := conn.SetAuthorizer(nil); err != nil {
		return sqliteRowFailure(err, &Error{Code: "internal_error", Message: "internal server error"})
	}
	s.sqliteStage("before_commit")
	if err := commit(); err != nil {
		if errors.Is(err, sqlite3.CONSTRAINT_FOREIGNKEY) {
			return sqliteRowFailure(err, &Error{Code: "invalid_reference", Message: "referenced row not found"})
		}
		return sqliteRowFailure(err, &Error{Code: "invalid_rows", Message: "cannot commit the change"})
	}
	a.Committed()
	// These remain mandatory even when checkpointing panics after a durable write.
	defer s.deps.MarkChanged()
	defer func() {
		if s.deps.Committed != nil {
			s.deps.Committed(c.Context)
		}
	}()
	s.sqliteStage("after_commit")
	if err := runtime.CheckpointRows(conn); err != nil {
		log.Printf("storage: site %s %s: checkpoint after commit: %v", c.SiteID, c.ResourceName, err)
	}
	return nil
}

// beginTableWrite starts the transaction shared by a row mutation and its
// reference checks.
func beginTableWrite(conn *sqlite3.Conn) (func() error, func(), error) {
	commit, rollback, err := runtime.BeginRowWrite(conn)
	if err != nil {
		return nil, nil, sqliteRowFailure(err, &Error{Code: "storage_busy", Message: "storage is busy; retry shortly", NoRetryAfter: true})
	}
	return commit, rollback, nil
}

// TableAdd performs an authorized row insert.
func (s *Service) TableAdd(ctx context.Context, in TableInput) (TableResult, error) {
	return s.withTable(ctx, in, "add", func(t *tableSession, out *TableResult) error {
		// The result is filled in a defer: addTableRow commits, and a panic
		// after its commit must keep the new row's id.
		var id int64
		defer func() {
			if id != 0 {
				// in.Values was stamped in place (stampInsertValues): its
				// visitor_id, if any, is the new row's own.
				*out = TableResult{Body: map[string]any{"changes": 1, "last_insert_id": id}, Notify: s.rowNotification(t.c, in.Table, "record.added", "", id, in.Values, rowValueVisitorID(in.Values))}
			}
		}()
		return s.addTableRow(t.conn, t.c, in, t.meta, t.a, &id)
	})
}

// InsertActor supplies the caller facts needed for server-owned columns.
type InsertActor struct {
	Owner, OwnerOnSite, Hold bool
	StampID                  string
	// OwnerFor: set only when Owner (or OwnerOnSite) is adding this row for
	// another, already-validated, signed-in visitor (ResolveOwnerFor). It
	// is the one way an owner write ends up with a non-empty visitor_id.
	OwnerFor string
}

// stampInsertValues fills server-owned values for an insert. visitor_id and
// written_by are always server-decided, never taken from the caller's
// values: a visitor can set neither (self-authored, written_by ”); a plain
// owner add leaves visitor_id empty (nobody's, written_by ”); only
// OwnerFor, already checked by ResolveOwnerFor, produces a row stamped to
// someone else's visitor_id and written_by "owner" — the server can never
// be asked to forge the other direction.
func stampInsertValues(cols map[string]string, values map[string]json.RawMessage, actor InsertActor) {
	now := rowJSON(time.Now().UTC().Format(time.RFC3339Nano))
	// SQLite column names are case-insensitive, and so are server columns.
	stamp := func(name string, v json.RawMessage) {
		if column := declaredColumn(cols, name); column != "" {
			values[column] = v
		}
	}
	discardSupplied(values, "visitor_id", "written_by")
	// onBehalf: an on-behalf-of add, whether reached by the owner's API
	// key/connector token or by the owner signed in on the site's own
	// address (OwnerOnSite) — both resolve and validate OwnerFor the same
	// way (ResolveOwnerFor), so both take the owner's path instead of the
	// ordinary OwnerOnSite self-add.
	onBehalf := (actor.Owner || actor.OwnerOnSite) && actor.OwnerFor != ""
	writtenBy := ""
	if actor.Owner || onBehalf {
		if _, set := values[declaredColumn(cols, "approved_at")]; actor.Hold && !set {
			stamp("approved_at", now)
		}
		if onBehalf {
			stamp("visitor_id", rowJSON(actor.OwnerFor))
			writtenBy = "owner"
		}
	} else {
		discardSupplied(values, "created_at", "updated_at")
		visitor := json.RawMessage("null")
		if actor.StampID != "" {
			visitor = rowJSON(actor.StampID)
		}
		stamp("visitor_id", visitor)
		stamp("created_at", now)
		if actor.Hold {
			discardSupplied(values, "approved_at")
			approval := json.RawMessage("null")
			if actor.OwnerOnSite {
				approval = now
			}
			stamp("approved_at", approval)
		}
	}
	stamp("written_by", rowJSON(writtenBy))
}

// discardSupplied drops caller values for server-owned columns, in any case.
func discardSupplied(values map[string]json.RawMessage, names ...string) {
	for supplied := range values {
		for _, name := range names {
			if strings.EqualFold(supplied, name) {
				delete(values, supplied)
			}
		}
	}
}

// addTableRow keeps the insert, reference check and commit in one transaction.
// Its caller must have authorized the operation and acquired the site lock.
func (s *Service) addTableRow(conn *sqlite3.Conn, c Call, in TableInput, meta rowMetadata, a *Admission, resultID *int64) error {
	// Check IDs before stamping, as the original workflow did.
	if !c.Owner {
		supplied, err := runtime.InsertSuppliesID(conn, in.Table, in.Values)
		if err != nil {
			return sqliteRowFailure(err, &Error{Code: "invalid_rows", Message: "invalid row request"})
		}
		if supplied {
			return &Error{Code: "invalid_rows", Message: "the server assigns ids"}
		}
	}
	stampInsertValues(meta.cols, in.Values, InsertActor{Owner: c.Owner, OwnerOnSite: c.OwnerOnSite, Hold: meta.hold, StampID: StampID(c.Owner, c.VisitorID), OwnerFor: c.OwnerFor})
	query, params, err := runtime.PrepareInsert(in.Table, meta.rowid, meta.cols, in.Values, !c.Owner)
	if err != nil {
		return sqliteRowFailure(err, &Error{Code: "invalid_rows", Message: "invalid row request"})
	}
	commit, rollback, err := beginTableWrite(conn)
	if err != nil {
		return err
	}
	defer rollback()
	id, err := runTableWrite(conn, in.Table, c.Owner, sqlite3.AUTH_INSERT, query, params, true, resultID, s.sqliteStage)
	if err != nil {
		return err
	}
	if err = s.checkRowReferences(conn, c, in.Table, meta.rowid, id); err != nil {
		return err
	}
	if err = s.checkWrittenRule(conn, c, in.Table, meta, id, ruleengine.OpAdd); err != nil {
		return err
	}
	if c.OwnerFor != "" {
		// A trigger the owner's own (otherwise unrestricted) schema route
		// created could still run as a side effect of this very INSERT and
		// rewrite visitor_id or written_by afterward — runTableWrite
		// installs no authorizer for c.Owner, so it cannot itself catch
		// that. Re-read the row, inside this same transaction, before
		// committing; a mismatch rolls the whole add back rather than ship a
		// row that looks like the visitor's own.
		if err = verifyOwnerForRow(conn, in.Table, meta.rowid, id, c.OwnerFor); err != nil {
			return err
		}
	}
	return s.finishTableWrite(conn, c, commit, a)
}

// verifyOwnerForRow confirms an on-behalf-of insert's own values survived
// unchanged (no trigger rewrote visitor_id or written_by as a side effect
// of the same INSERT). Must run before the caller commits.
func verifyOwnerForRow(conn *sqlite3.Conn, table, rowid string, id int64, target string) error {
	stmt, _, err := conn.Prepare(`SELECT visitor_id, written_by FROM ` + runtime.Quote(table) + ` WHERE ` + rowid + ` = ?`)
	if err != nil {
		return sqliteRowFailure(err, &Error{Code: "internal_error", Message: "internal server error"})
	}
	defer stmt.Close()
	if err := stmt.BindInt64(1, id); err != nil {
		return sqliteRowFailure(err, &Error{Code: "internal_error", Message: "internal server error"})
	}
	if !stmt.Step() {
		if err := stmt.Err(); err != nil {
			return sqliteRowFailure(err, &Error{Code: "internal_error", Message: "internal server error"})
		}
		return &Error{Code: "written_by_tampered", Message: "a trigger on this table removed the row before the add could be verified; the add was rolled back"}
	}
	visitorID, writtenBy := stmt.ColumnText(0), stmt.ColumnText(1)
	if visitorID != target || writtenBy != "owner" {
		return &Error{Code: "written_by_tampered", Message: "a trigger on this table changed visitor_id or written_by after the insert; the add was rolled back"}
	}
	return nil
}

// TableEdit performs an authorized row update.
func (s *Service) TableEdit(ctx context.Context, in TableInput) (TableResult, error) {
	return s.withTable(ctx, in, "edit", func(t *tableSession, out *TableResult) error {
		names, err := editColumns(t, in.Values)
		if err != nil {
			return err
		}
		query, params, err := editStatement(t, names, in.Values, in.ID)
		if err != nil {
			return err
		}
		commit, rollback, err := s.changeOneRow(t, sqlite3.AUTH_UPDATE, query, params)
		if err != nil {
			return err
		}
		defer rollback()
		if err = s.checkRowReferences(t.conn, t.c, in.Table, t.meta.rowid, in.ID); err != nil {
			return err
		}
		if err = s.checkWrittenRule(t.conn, t.c, in.Table, t.meta, in.ID, ruleengine.OpEdit); err != nil {
			return err
		}
		_ = t.conn.SetAuthorizer(nil)
		// The edited row may be anyone's (the owner, or rules not limited to
		// the actor's own rows), so its visitor_id is looked up directly.
		// Editing visitor_id itself is refused, so the value is the same
		// before and after this write.
		rowVisitorID := lookupRowVisitorID(t.conn, t.meta, in.Table, t.meta.rowid, in.ID)
		*out = TableResult{Body: map[string]any{"changes": 1}, Notify: s.rowNotification(t.c, in.Table, "record.changed", "edited", in.ID, in.Values, rowVisitorID)}
		return s.finishTableWrite(t.conn, t.c, commit, t.a)
	})
}

// serverColumns are set by the server, never by an edit (any case).
var serverColumns = map[string]bool{"visitor_id": true, "written_by": true, "created_at": true, "updated_at": true, "rowid": true, "_rowid_": true, "oid": true}

// editColumns decides which columns an edit sets: the caller's, sorted,
// refused when unknown or server-owned (the key, and approved_at while rows
// wait for approval), then updated_at, which is stamped here.
func editColumns(t *tableSession, values map[string]json.RawMessage) ([]string, error) {
	pk, err := runtime.PrimaryKeyColumns(t.conn, t.table)
	if err != nil {
		return nil, sqliteRowFailure(err, &Error{Code: "invalid_rows", Message: "invalid row request"})
	}
	approvalHeld := t.meta.hold && !seesEveryRow(t.c)
	names := make([]string, 0, len(values))
	for name := range values {
		if serverColumns[strings.ToLower(name)] || pk[name] || approvalHeld && strings.EqualFold(name, "approved_at") {
			return nil, &Error{Code: "server_column", Message: "id, visitor_id, written_by, created_at, and updated_at are set by the server"}
		}
		if _, ok := t.meta.cols[name]; !ok {
			return nil, &Error{Code: "invalid_rows", Message: "invalid row request"}
		}
		names = append(names, name)
	}
	sort.Strings(names)
	if updatedAt := declaredColumn(t.meta.cols, "updated_at"); updatedAt != "" {
		names = append(names, updatedAt)
		values[updatedAt] = rowJSON(time.Now().UTC().Format(time.RFC3339Nano))
	}
	return names, nil
}

// editStatement builds the UPDATE for one row, under the rules' read and
// edit predicates when the table has rules.
func editStatement(t *tableSession, names []string, values map[string]json.RawMessage, id int64) (string, []json.RawMessage, error) {
	if t.c.rulePlan == nil {
		query, params := runtime.RowUpdateStatement(t.table, t.meta.rowid, names, values, rowJSON(id), t.c.Owner, t.meta.hold && !seesEveryRow(t.c))
		return query, params, nil
	}
	read, edit, err := ruleWritePredicates(t.c, ruleengine.OpEdit)
	if err != nil {
		return "", nil, err
	}
	scalars := make([]ruleengine.Value, len(names))
	for i, name := range names {
		if scalars[i], err = ruleScalar(values[name]); err != nil {
			return "", nil, &Error{Code: "invalid_rows", Message: "invalid row request"}
		}
	}
	query, args, err := runtime.RuleUpdate(t.table, t.meta.rowid, names, scalars, ruleengine.Int(id), read, edit, "")
	if err != nil {
		return "", nil, rulesInvalid()
	}
	return query, ruleJSONArgs(args), nil
}

// deleteStatement builds the DELETE for one row, under the rules' read and
// delete predicates when the table has rules.
func deleteStatement(t *tableSession, id int64) (string, []json.RawMessage, error) {
	if t.c.rulePlan == nil {
		query, params := runtime.RowDeleteStatement(t.table, t.meta.rowid, rowJSON(id), t.meta.hold && !seesEveryRow(t.c))
		return query, params, nil
	}
	read, del, err := ruleWritePredicates(t.c, ruleengine.OpDelete)
	if err != nil {
		return "", nil, err
	}
	query, args, err := runtime.RuleDelete(t.table, t.meta.rowid, ruleengine.Int(id), read, del, "")
	if err != nil {
		return "", nil, rulesInvalid()
	}
	return query, ruleJSONArgs(args), nil
}

// changeOneRow starts the row transaction and runs an UPDATE or DELETE that
// must change exactly the requested row (a page may not reach others, for
// example through a trigger). On success the caller defers rollback, which
// is a no-op once commit ran.
func (s *Service) changeOneRow(t *tableSession, kind sqlite3.AuthorizerActionCode, query string, params []json.RawMessage) (func() error, func(), error) {
	commit, rollback, err := beginTableWrite(t.conn)
	if err != nil {
		return nil, nil, err
	}
	before := t.conn.TotalChanges()
	_, err = runTableWrite(t.conn, t.table, t.c.Owner, kind, query, params, false, nil, s.sqliteStage)
	switch {
	case err != nil:
	case t.conn.Changes() == 0:
		err = &Error{Code: "row_not_found", Message: "row not found"}
	case !t.c.Owner && t.conn.TotalChanges()-before != 1:
		err = &Error{Code: "forbidden", Message: "this change would touch other rows; pages cannot make it, only the owner's tools"}
	}
	if err != nil {
		rollback()
		return nil, nil, err
	}
	return commit, rollback, nil
}

// lookupRowVisitorID reads one row's own visitor_id column directly, for an
// edit or delete of a row that might belong to another visitor. Best-effort:
// any failure yields "", which only means no push target was found, never a
// write failure.
func lookupRowVisitorID(conn *sqlite3.Conn, meta rowMetadata, table, rowid string, id int64) string {
	if _, ok := meta.cols["visitor_id"]; !ok {
		return ""
	}
	stmt, _, err := conn.Prepare(`SELECT visitor_id FROM ` + runtime.Quote(table) + ` WHERE ` + rowid + ` = ?`)
	if err != nil {
		return ""
	}
	defer stmt.Close()
	if stmt.BindInt64(1, id) != nil || !stmt.Step() || stmt.ColumnType(0) == sqlite3.NULL {
		return ""
	}
	return stmt.ColumnText(0)
}

// rowValueVisitorID reads values["visitor_id"] back out as a plain string
// ("" for absent or JSON null), for the row's own identity rather than the
// actor's.
func rowValueVisitorID(values map[string]json.RawMessage) string {
	raw, ok := values["visitor_id"]
	if !ok {
		return ""
	}
	var id string
	if json.Unmarshal(raw, &id) != nil {
		return ""
	}
	return id
}

// rowNotification builds the deferred notify callback for one row write.
// rowVisitorID is the row's own visitor_id (who the data belongs to, not
// necessarily who wrote it). ownerWrite is true for the owner's key and for
// an owner-on-behalf-of add (OwnerFor): neither notifies the owner's own
// webhook or MCP-event subscriptions, but an on-behalf-of add has a real
// rowVisitorID and must still reach that visitor's push devices. Plain
// OwnerOnSite stays an ordinary signed-in write here.
func (s *Service) rowNotification(c Call, table, name, change string, id int64, values map[string]json.RawMessage, rowVisitorID string) func() {
	return func() {
		if s.deps.Notify == nil {
			return
		}
		ownerWrite := c.Owner || c.OwnerFor != ""
		if ownerWrite && rowVisitorID == "" {
			// Owner write unrelated to any visitor's row: nobody to tell.
			return
		}
		data := map[string]any{"table": table, "row_id": id, "owner_write": ownerWrite}
		if rowVisitorID != "" {
			data["row_visitor_id"] = rowVisitorID
		}
		if change != "" {
			data["change"] = change
		}
		if values != nil {
			fields := map[string]any{}
			for k, v := range values {
				if k != "visitor_id" {
					var value any
					dec := json.NewDecoder(bytes.NewReader(v))
					dec.UseNumber()
					if !json.Valid(v) || dec.Decode(&value) != nil {
						value = string(v)
					}
					fields[k] = value
				}
			}
			data["fields"] = fields
		}
		s.deps.Notify(c, name, data)
	}
}

// TableRemove performs an authorized delete of one visible row.
func (s *Service) TableRemove(ctx context.Context, in TableInput) (TableResult, error) {
	return s.withTable(ctx, in, "delete", func(t *tableSession, out *TableResult) error {
		// Read before the row is gone: this delete may remove any visitor's
		// row, so its visitor_id is looked up while the row still exists.
		rowVisitorID := lookupRowVisitorID(t.conn, t.meta, in.Table, t.meta.rowid, in.ID)
		query, params, err := deleteStatement(t, in.ID)
		if err != nil {
			return err
		}
		commit, rollback, err := s.changeOneRow(t, sqlite3.AUTH_DELETE, query, params)
		if err != nil {
			return err
		}
		defer rollback()
		*out = TableResult{Body: map[string]any{"deleted": true}, Notify: s.rowNotification(t.c, in.Table, "record.changed", "deleted", in.ID, nil, rowVisitorID)}
		return s.finishTableWrite(t.conn, t.c, commit, t.a)
	})
}
