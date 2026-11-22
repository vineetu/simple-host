package rows

import (
	"bytes"
	"encoding/json"
	"errors"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/vsriram/simple-host/host/data"

	"github.com/vsriram/simple-host/host/papermetrics"

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
		return &data.Error{Code: "written_by_blocked", Message: "a trigger or view on this table may depend on its exact columns; remove or recreate it before adding a row with for"}
	}
	if err != nil {
		return data.SQLiteRowFailure(err, &data.Error{Code: "internal_error", Message: "internal server error"})
	}
	return nil
}

// runTableWrite preserves the row-write error classification for table workflows.
func runTableWrite(conn *sqlite3.Conn, table string, owner bool, kind sqlite3.AuthorizerActionCode, query string, params []json.RawMessage, returning bool, hooks ...func(string)) (int64, error) {
	if err := runtime.SetRowAuthorizer(conn, table, owner, kind); err != nil {
		return 0, data.SQLiteRowFailure(err, &data.Error{Code: "internal_error", Message: "internal server error"})
	}
	id, stage, err := runtime.RunRowStatement(conn, query, params, returning, nil, hooks...)
	if errors.Is(err, sqlite3.NOMEM) {
		return 0, data.SQLiteFailure(err)
	}
	if stage == runtime.RowStatementPrepare {
		return 0, &data.Error{Code: "forbidden", Message: "this change would touch other tables; pages cannot make it, only the owner's tools"}
	}
	if stage == runtime.RowStatementBind {
		return 0, &data.Error{Code: "invalid_rows", Message: "invalid row request"}
	}
	if err != nil {
		switch {
		case errors.Is(err, sqlite3.CONSTRAINT_FOREIGNKEY):
			return 0, &data.Error{Code: "invalid_reference", Message: "referenced row not found"}
		case errors.Is(err, sqlite3.AUTH):
			return 0, &data.Error{Code: "forbidden", Message: "this change would touch other tables; pages cannot make it, only the owner's tools"}
		case errors.Is(err, sqlite3.CONSTRAINT):
			return 0, &data.Error{Code: "row_conflict", Message: "row cannot be saved"}
		case errors.Is(err, sqlite3.FULL):
			return 0, &data.Error{Code: "site_full", Message: "KV and SQLite storage is full; remove unused data before retrying"}
		default:
			return 0, &data.Error{Code: "invalid_rows", Message: "the change failed"}
		}
	}
	if returning && id == 0 {
		return 0, &data.Error{Code: "row_conflict", Message: "row cannot be saved"}
	}
	return id, nil
}

// checkRowReferences enforces the parent table's read policy on a row write.
func checkRowReferences(s *data.Service, conn *sqlite3.Conn, c data.Call, table, rowid string, id int64) error {
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
		predicate, err := s.RuleReadPredicate(c.Context, &parentCall, parent, data.RowMetadata{Cols: cols, Order: order, RowID: key})
		if e, ok := err.(*data.Error); ok && e.Code != "internal_error" && e.Code != "rules_invalid" {
			return ruleengine.Predicate{}, &data.Error{Code: "invalid_reference", Message: "referenced row not found"}
		}
		return predicate, err
	})
	if e, ok := err.(*data.Error); ok {
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
		return data.SQLiteRowFailure(err, &data.Error{Code: "invalid_reference", Message: "referenced row not found"})
	}
	return data.SQLiteRowFailure(err, &data.Error{Code: "invalid_rows", Message: "cannot check references"})
}

// finishTableWrite commits before checkpointing; a checkpoint failure cannot
// turn an already committed change into a failed response.
func finishTableWrite(s *data.Service, conn *sqlite3.Conn, c data.Call, commit func() error, a *data.Admission) error {
	if err := conn.SetAuthorizer(nil); err != nil {
		return data.SQLiteRowFailure(err, &data.Error{Code: "internal_error", Message: "internal server error"})
	}
	s.SQLiteStage("before_commit")
	if err := commit(); err != nil {
		if errors.Is(err, sqlite3.CONSTRAINT_FOREIGNKEY) {
			return data.SQLiteRowFailure(err, &data.Error{Code: "invalid_reference", Message: "referenced row not found"})
		}
		return data.SQLiteRowFailure(err, &data.Error{Code: "invalid_rows", Message: "cannot commit the change"})
	}
	a.Committed()
	// These remain mandatory even when checkpointing panics after a durable write.
	defer s.Deps.Disk.MarkChanged()
	defer func() {
		papermetrics.From(c.Context).WriteCommitted(c.Context)
	}()
	s.SQLiteStage("after_commit")
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
		return nil, nil, data.SQLiteRowFailure(err, &data.Error{Code: "storage_busy", Message: "storage is busy; retry shortly", NoRetryAfter: true})
	}
	return commit, rollback, nil
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
	now := data.RowJSON(time.Now().UTC().Format(time.RFC3339Nano))
	// SQLite column names are case-insensitive, and so are server columns.
	stamp := func(name string, v json.RawMessage) {
		if column := data.DeclaredColumn(cols, name); column != "" {
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
		if _, set := values[data.DeclaredColumn(cols, "approved_at")]; actor.Hold && !set {
			stamp("approved_at", now)
		}
		if onBehalf {
			stamp("visitor_id", data.RowJSON(actor.OwnerFor))
			writtenBy = "owner"
		}
	} else {
		discardSupplied(values, "created_at", "updated_at")
		visitor := json.RawMessage("null")
		if actor.StampID != "" {
			visitor = data.RowJSON(actor.StampID)
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
	stamp("written_by", data.RowJSON(writtenBy))
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

// addRow inserts one row and checks it, inside the transaction TableWrite
// began. values is stamped in place (stampInsertValues): its visitor_id,
// if any, is the new row's own.
func addRow(s *data.Service, t *rowTable, values map[string]json.RawMessage) (TableResult, error) {
	c, meta := t.c, t.meta
	if !c.Owner {
		supplied, err := runtime.InsertSuppliesID(t.conn, t.table, values)
		if err != nil {
			return TableResult{}, data.SQLiteRowFailure(err, &data.Error{Code: "invalid_rows", Message: "invalid row request"})
		}
		if supplied {
			return TableResult{}, &data.Error{Code: "invalid_rows", Message: "the server assigns ids"}
		}
	}
	stampInsertValues(meta.Cols, values, InsertActor{Owner: c.Owner, OwnerOnSite: c.OwnerOnSite, Hold: meta.Hold, StampID: data.StampID(c.Owner, c.VisitorID), OwnerFor: c.OwnerFor})
	query, params, err := runtime.PrepareInsert(t.table, meta.RowID, meta.Cols, values, !c.Owner)
	if err != nil {
		return TableResult{}, data.SQLiteRowFailure(err, &data.Error{Code: "invalid_rows", Message: "invalid row request"})
	}
	id, err := runTableWrite(t.conn, t.table, c.Owner, sqlite3.AUTH_INSERT, query, params, true, s.SQLiteStage)
	if err != nil {
		return TableResult{}, err
	}
	if err = checkRowReferences(s, t.conn, c, t.table, meta.RowID, id); err != nil {
		return TableResult{}, err
	}
	if err = s.CheckWrittenRule(t.conn, c, t.table, meta, id, ruleengine.OpAdd); err != nil {
		return TableResult{}, err
	}
	if c.OwnerFor != "" {
		// A trigger the owner's own (otherwise unrestricted) schema route
		// created could still run as a side effect of this very INSERT and
		// rewrite visitor_id or written_by afterward — runTableWrite
		// installs no authorizer for c.Owner, so it cannot itself catch
		// that. Re-read the row, inside this same transaction, before
		// committing; a mismatch rolls the whole add back rather than ship a
		// row that looks like the visitor's own.
		if err = verifyOwnerForRow(t.conn, t.table, meta.RowID, id, c.OwnerFor); err != nil {
			return TableResult{}, err
		}
	}
	return TableResult{Body: map[string]any{"changes": 1, "last_insert_id": id}, Event: rowNotification(c, t.table, "record.added", "", id, values, rowValueVisitorID(values))}, nil
}

// verifyOwnerForRow confirms an on-behalf-of insert's own values survived
// unchanged (no trigger rewrote visitor_id or written_by as a side effect
// of the same INSERT). Must run before the caller commits.
func verifyOwnerForRow(conn *sqlite3.Conn, table, rowid string, id int64, target string) error {
	stmt, _, err := conn.Prepare(`SELECT visitor_id, written_by FROM ` + runtime.Quote(table) + ` WHERE ` + rowid + ` = ?`)
	if err != nil {
		return data.SQLiteRowFailure(err, &data.Error{Code: "internal_error", Message: "internal server error"})
	}
	defer stmt.Close()
	if err := stmt.BindInt64(1, id); err != nil {
		return data.SQLiteRowFailure(err, &data.Error{Code: "internal_error", Message: "internal server error"})
	}
	if !stmt.Step() {
		if err := stmt.Err(); err != nil {
			return data.SQLiteRowFailure(err, &data.Error{Code: "internal_error", Message: "internal server error"})
		}
		return &data.Error{Code: "written_by_tampered", Message: "a trigger on this table removed the row before the add could be verified; the add was rolled back"}
	}
	visitorID, writtenBy := stmt.ColumnText(0), stmt.ColumnText(1)
	if visitorID != target || writtenBy != "owner" {
		return &data.Error{Code: "written_by_tampered", Message: "a trigger on this table changed visitor_id or written_by after the insert; the add was rolled back"}
	}
	return nil
}

// editRow updates one row and checks it, inside the transaction TableWrite
// began.
func editRow(s *data.Service, t *rowTable, id int64, values map[string]json.RawMessage) (TableResult, error) {
	names, err := editColumns(t, values)
	if err != nil {
		return TableResult{}, err
	}
	query, params, err := editStatement(t, names, values, id)
	if err != nil {
		return TableResult{}, err
	}
	if err = changeOneRow(s, t, sqlite3.AUTH_UPDATE, query, params); err != nil {
		return TableResult{}, err
	}
	if err = checkRowReferences(s, t.conn, t.c, t.table, t.meta.RowID, id); err != nil {
		return TableResult{}, err
	}
	if err = s.CheckWrittenRule(t.conn, t.c, t.table, t.meta, id, ruleengine.OpEdit); err != nil {
		return TableResult{}, err
	}
	_ = t.conn.SetAuthorizer(nil)
	// The edited row may be anyone's (the owner, or rules not limited to
	// the actor's own rows), so its visitor_id is looked up directly.
	// Editing visitor_id itself is refused, so the value is the same
	// before and after this write.
	rowVisitorID := lookupRowVisitorID(t.conn, t.meta, t.table, t.meta.RowID, id)
	return TableResult{Body: map[string]any{"changes": 1}, Event: rowNotification(t.c, t.table, "record.changed", "edited", id, values, rowVisitorID)}, nil
}

// serverColumns are set by the server, never by an edit (any case).
var serverColumns = map[string]bool{"visitor_id": true, "written_by": true, "created_at": true, "updated_at": true, "rowid": true, "_rowid_": true, "oid": true}

// editColumns decides which columns an edit sets: the caller's, sorted,
// refused when unknown or server-owned (the key, and approved_at while rows
// wait for approval), then updated_at, which is stamped here.
func editColumns(t *rowTable, values map[string]json.RawMessage) ([]string, error) {
	pk, err := runtime.PrimaryKeyColumns(t.conn, t.table)
	if err != nil {
		return nil, data.SQLiteRowFailure(err, &data.Error{Code: "invalid_rows", Message: "invalid row request"})
	}
	approvalHeld := t.meta.Hold && !seesEveryRow(t.c)
	names := make([]string, 0, len(values))
	for name := range values {
		if serverColumns[strings.ToLower(name)] || pk[name] || approvalHeld && strings.EqualFold(name, "approved_at") {
			return nil, &data.Error{Code: "server_column", Message: "id, visitor_id, written_by, created_at, and updated_at are set by the server"}
		}
		if _, ok := t.meta.Cols[name]; !ok {
			return nil, &data.Error{Code: "invalid_rows", Message: "invalid row request"}
		}
		names = append(names, name)
	}
	sort.Strings(names)
	if updatedAt := data.DeclaredColumn(t.meta.Cols, "updated_at"); updatedAt != "" {
		names = append(names, updatedAt)
		values[updatedAt] = data.RowJSON(time.Now().UTC().Format(time.RFC3339Nano))
	}
	return names, nil
}

// editStatement builds the UPDATE for one row, under the rules' read and
// edit predicates when the table has rules.
func editStatement(t *rowTable, names []string, values map[string]json.RawMessage, id int64) (string, []json.RawMessage, error) {
	if t.c.RulePlan == nil {
		query, params := runtime.RowUpdateStatement(t.table, t.meta.RowID, names, values, data.RowJSON(id), t.c.Owner, t.meta.Hold && !seesEveryRow(t.c))
		return query, params, nil
	}
	read, edit, err := data.RuleWritePredicates(t.c, ruleengine.OpEdit)
	if err != nil {
		return "", nil, err
	}
	scalars := make([]ruleengine.Value, len(names))
	for i, name := range names {
		if scalars[i], err = ruleScalar(values[name]); err != nil {
			return "", nil, &data.Error{Code: "invalid_rows", Message: "invalid row request"}
		}
	}
	query, args, err := runtime.RuleUpdate(t.table, t.meta.RowID, names, scalars, ruleengine.Int(id), read, edit, "")
	if err != nil {
		return "", nil, data.RulesInvalid()
	}
	return query, data.RuleJSONArgs(args), nil
}

// deleteStatement builds the DELETE for one row, under the rules' read and
// delete predicates when the table has rules.
func deleteStatement(t *rowTable, id int64) (string, []json.RawMessage, error) {
	if t.c.RulePlan == nil {
		query, params := runtime.RowDeleteStatement(t.table, t.meta.RowID, data.RowJSON(id), t.meta.Hold && !seesEveryRow(t.c))
		return query, params, nil
	}
	read, del, err := data.RuleWritePredicates(t.c, ruleengine.OpDelete)
	if err != nil {
		return "", nil, err
	}
	query, args, err := runtime.RuleDelete(t.table, t.meta.RowID, ruleengine.Int(id), read, del, "")
	if err != nil {
		return "", nil, data.RulesInvalid()
	}
	return query, data.RuleJSONArgs(args), nil
}

// changeOneRow runs an UPDATE or DELETE that must change exactly the
// requested row (a page may not reach others, for example through a
// trigger), inside the transaction TableWrite began.
func changeOneRow(s *data.Service, t *rowTable, kind sqlite3.AuthorizerActionCode, query string, params []json.RawMessage) error {
	before := t.conn.TotalChanges()
	_, err := runTableWrite(t.conn, t.table, t.c.Owner, kind, query, params, false, s.SQLiteStage)
	switch {
	case err != nil:
		return err
	case t.conn.Changes() == 0:
		return &data.Error{Code: "row_not_found", Message: "row not found"}
	case !t.c.Owner && t.conn.TotalChanges()-before != 1:
		return &data.Error{Code: "forbidden", Message: "this change would touch other rows; pages cannot make it, only the owner's tools"}
	}
	return nil
}

// lookupRowVisitorID reads one row's own visitor_id column directly, for an
// edit or delete of a row that might belong to another visitor. Best-effort:
// any failure yields "", which only means no push target was found, never a
// write failure.
func lookupRowVisitorID(conn *sqlite3.Conn, meta data.RowMetadata, table, rowid string, id int64) string {
	if _, ok := meta.Cols["visitor_id"]; !ok {
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

// rowNotification is the event for one row write, nil when nobody hears of it.
// rowVisitorID is the row's own visitor_id (who the data belongs to, not
// necessarily who wrote it). ownerWrite is true for the owner's key and for
// an owner-on-behalf-of add (OwnerFor): neither notifies the owner's own
// webhook or MCP-event subscriptions, but an on-behalf-of add has a real
// rowVisitorID and must still reach that visitor's push devices. Plain
// OwnerOnSite stays an ordinary signed-in write here.
func rowNotification(c data.Call, table, name, change string, id int64, values map[string]json.RawMessage, rowVisitorID string) *data.Event {
	ownerWrite := c.Owner || c.OwnerFor != ""
	if ownerWrite && rowVisitorID == "" {
		// Owner write unrelated to any visitor's row: nobody to tell.
		return nil
	}
	payload := map[string]any{"table": table, "row_id": id, "owner_write": ownerWrite}
	if rowVisitorID != "" {
		payload["row_visitor_id"] = rowVisitorID
	}
	if change != "" {
		payload["change"] = change
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
		payload["fields"] = fields
	}
	return &data.Event{Call: c, Name: name, Data: payload}
}

// removeRow deletes one visible row, inside the transaction TableWrite
// began.
func removeRow(s *data.Service, t *rowTable, id int64) (TableResult, error) {
	// Read before the row is gone: this delete may remove any visitor's
	// row, so its visitor_id is looked up while the row still exists.
	rowVisitorID := lookupRowVisitorID(t.conn, t.meta, t.table, t.meta.RowID, id)
	query, params, err := deleteStatement(t, id)
	if err != nil {
		return TableResult{}, err
	}
	if err = changeOneRow(s, t, sqlite3.AUTH_DELETE, query, params); err != nil {
		return TableResult{}, err
	}
	return TableResult{Body: map[string]any{"deleted": true}, Event: rowNotification(t.c, t.table, "record.changed", "deleted", id, nil, rowVisitorID)}, nil
}
