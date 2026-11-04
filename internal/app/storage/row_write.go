package storage

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
	rules "github.com/vsriram/simple-host/internal/rules/storage"
	runtime "github.com/vsriram/simple-host/internal/storage"
)

// addTableRowLocked owns the lock, connection and metadata checks for an
// already-authorized insert. Idempotency replay must still happen after
// authorization and before this workflow starts.
func (s *Service) addTableRowLocked(ctx context.Context, c Call, in TableInput, filter string) (int64, error) {
	ctx, unlock, err := s.writeLock(ctx, c.OwnerID+"/"+c.SiteName, s.deps.WriteLockWait, s.deps.WriteTimeout, s.deps.Recheck)
	if err != nil {
		return 0, err
	}
	defer unlock()
	c.Context = ctx
	conn, done, err := s.openTableConn(ctx, c, true)
	if err != nil {
		return 0, err
	}
	defer done()
	meta, err := tableMetadata(conn, c, in.Table, filter)
	if err != nil {
		return 0, err
	}
	return s.addTableRow(conn, c, in, meta)
}

// runTableWrite preserves the row-write error classification for table workflows.
func runTableWrite(conn *sqlite3.Conn, table string, owner bool, kind sqlite3.AuthorizerActionCode, query string, params []json.RawMessage, returning bool) (int64, error) {
	if err := runtime.SetRowAuthorizer(conn, table, owner, kind); err != nil {
		return 0, &Error{Code: "internal_error", Message: "internal server error"}
	}
	id, stage, err := runtime.RunRowStatement(conn, query, params, returning)
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

// rowMayRead mirrors the non-answering parent-row visibility check.
func rowMayRead(c Call, value string) (string, bool) {
	if c.Owner {
		return "", true
	}
	switch value {
	case rules.WhoAnyone, rules.WhoChecked:
		return "", true
	case rules.WhoSignedIn:
		return "", c.VisitorID != ""
	case rules.WhoOwn:
		if c.VisitorID == "" {
			return "", false
		}
		if c.OwnerOnSite {
			return "", true
		}
		return c.VisitorID, true
	case rules.WhoOwner:
		return "", c.OwnerOnSite
	}
	return "", false
}

// checkRowReferences enforces the parent table's read policy on a row write.
func checkRowReferences(conn *sqlite3.Conn, c Call, table, rowid string, id int64) error {
	if c.Owner {
		return nil
	}
	_ = conn.SetAuthorizer(nil)
	status, err := runtime.CheckReferences(conn, table, rowid, id, func(parent string) (string, bool, bool) {
		filter, ok := rowMayRead(c, c.Resource.TableMatrix(parent).Read)
		return filter, ok, c.Resource.Holds(parent) && !c.OwnerOnSite
	})
	return rowReferenceError(status, err)
}

// rowReferenceError retains the reference check's wire error classification.
func rowReferenceError(status int, err error) error {
	if err == nil {
		return nil
	}
	if status == 404 {
		return &Error{Code: "invalid_reference", Message: "referenced row not found"}
	}
	return &Error{Code: "invalid_rows", Message: "cannot check references"}
}

// finishTableWrite commits before checkpointing; a checkpoint failure cannot
// turn an already committed change into a failed response.
func (s *Service) finishTableWrite(conn *sqlite3.Conn, c Call, commit func() error) error {
	if err := conn.SetAuthorizer(nil); err != nil {
		return &Error{Code: "internal_error", Message: "internal server error"}
	}
	if err := commit(); err != nil {
		if errors.Is(err, sqlite3.CONSTRAINT_FOREIGNKEY) {
			return &Error{Code: "invalid_reference", Message: "referenced row not found"}
		}
		return &Error{Code: "invalid_rows", Message: "cannot commit the change"}
	}
	if err := runtime.CheckpointRows(conn); err != nil {
		log.Printf("storage: site %s %s: checkpoint after commit: %v", c.SiteID, c.ResourceName, err)
	}
	if s.deps.Committed != nil {
		s.deps.Committed(c.Context)
	}
	s.deps.MarkChanged()
	return nil
}

// beginTableWrite starts the transaction shared by a row mutation and its
// reference checks.
func beginTableWrite(conn *sqlite3.Conn) (func() error, func(), error) {
	commit, rollback, err := runtime.BeginRowWrite(conn)
	if err != nil {
		return nil, nil, &Error{Code: "storage_busy", Message: "storage is busy; retry shortly", NoRetryAfter: true}
	}
	return commit, rollback, nil
}

// TableAdd performs an authorized row insert.
func (s *Service) TableAdd(ctx context.Context, in TableInput) (TableResult, error) {
	if !in.Authorized.Allowed() || in.Authorized.table != in.Table || in.Authorized.action != "add" {
		return TableResult{Authorization: in.Authorized.Authorization}, nil
	}
	c, filter := in.Authorized.call, in.Authorized.filter
	id, err := s.addTableRowLocked(ctx, c, in, filter)
	if err != nil {
		return TableResult{}, err
	}
	return TableResult{Body: map[string]any{"changes": 1, "last_insert_id": id}, Notify: s.rowNotification(c, in.Table, "record.added", "", id, in.Values)}, nil
}

// InsertActor supplies the caller facts needed for server-owned columns.
type InsertActor struct {
	Owner, OwnerOnSite, Hold bool
	StampID                  string
}

// stampInsertValues fills server-owned values for an insert.
func stampInsertValues(cols map[string]string, values map[string]json.RawMessage, actor InsertActor) {
	owner, ownerOnSite, hold, stampID := actor.Owner, actor.OwnerOnSite, actor.Hold, actor.StampID
	encode := func(v any) json.RawMessage { b, _ := json.Marshal(v); return b }
	if !owner {
		delete(values, "visitor_id")
		delete(values, "created_at")
		delete(values, "updated_at")
		if _, ok := cols["visitor_id"]; ok {
			values["visitor_id"] = encode(stampID)
			if stampID == "" {
				values["visitor_id"] = json.RawMessage("null")
			}
		}
		if _, ok := cols["created_at"]; ok {
			values["created_at"] = encode(time.Now().UTC().Format(time.RFC3339Nano))
		}
		if hold {
			values["approved_at"] = json.RawMessage("null")
			if ownerOnSite {
				values["approved_at"] = encode(time.Now().UTC().Format(time.RFC3339Nano))
			}
		}
	} else if _, set := values["approved_at"]; hold && !set {
		values["approved_at"] = encode(time.Now().UTC().Format(time.RFC3339Nano))
	}
}

// addTableRow keeps the insert, reference check and commit in one transaction.
// Its caller must have authorized the operation and acquired the site lock.
func (s *Service) addTableRow(conn *sqlite3.Conn, c Call, in TableInput, meta rowMetadata) (int64, error) {
	// Check IDs before stamping, as the original workflow did.
	if !c.Owner {
		supplied, err := runtime.InsertSuppliesID(conn, in.Table, in.Values)
		if err != nil {
			return 0, &Error{Code: "invalid_rows", Message: "invalid row request"}
		}
		if supplied {
			return 0, &Error{Code: "invalid_rows", Message: "the server assigns ids"}
		}
	}
	stampInsertValues(meta.cols, in.Values, InsertActor{Owner: c.Owner, OwnerOnSite: c.OwnerOnSite, Hold: meta.hold, StampID: StampID(c.Owner, c.VisitorID)})
	query, params, err := runtime.PrepareInsert(in.Table, meta.rowid, meta.cols, in.Values, !c.Owner)
	if err != nil {
		return 0, &Error{Code: "invalid_rows", Message: "invalid row request"}
	}
	commit, rollback, err := beginTableWrite(conn)
	if err != nil {
		return 0, err
	}
	defer rollback()
	id, err := runTableWrite(conn, in.Table, c.Owner, sqlite3.AUTH_INSERT, query, params, true)
	if err != nil {
		return 0, err
	}
	if err = checkRowReferences(conn, c, in.Table, meta.rowid, id); err != nil {
		return 0, err
	}
	if err = s.finishTableWrite(conn, c, commit); err != nil {
		return 0, err
	}
	return id, nil
}

// TableEdit authorizes and performs the complete row update.
func (s *Service) TableEdit(ctx context.Context, in TableInput) (TableResult, error) {
	if !in.Authorized.Allowed() || in.Authorized.table != in.Table || in.Authorized.action != "edit" {
		return TableResult{Authorization: in.Authorized.Authorization}, nil
	}
	c, filter := in.Authorized.call, in.Authorized.filter
	values := in.Values
	ctx, unlock, err := s.writeLock(ctx, c.OwnerID+"/"+c.SiteName, s.deps.WriteLockWait, s.deps.WriteTimeout, s.deps.Recheck)
	if err != nil {
		return TableResult{}, err
	}
	defer unlock()
	c.Context = ctx
	conn, done, err := s.openTableConn(ctx, c, true)
	if err != nil {
		return TableResult{}, err
	}
	defer done()
	meta, err := tableMetadata(conn, c, in.Table, filter)
	if err != nil {
		return TableResult{}, err
	}
	pk, err := runtime.PrimaryKeyColumns(conn, in.Table)
	if err != nil {
		return TableResult{}, &Error{Code: "invalid_rows", Message: "invalid row request"}
	}
	names := make([]string, 0, len(values))
	for name := range values {
		lower := strings.ToLower(name)
		if lower == "visitor_id" || lower == "created_at" || lower == "updated_at" || lower == "rowid" || lower == "_rowid_" || lower == "oid" || pk[name] || meta.hold && !c.Owner && !c.OwnerOnSite && strings.EqualFold(name, "approved_at") {
			return TableResult{}, &Error{Code: "server_column", Message: "id, visitor_id, created_at, and updated_at are set by the server"}
		}
		if _, ok := meta.cols[name]; !ok {
			return TableResult{}, &Error{Code: "invalid_rows", Message: "invalid row request"}
		}
		names = append(names, name)
	}
	sort.Strings(names)
	if _, ok := meta.cols["updated_at"]; ok {
		names = append(names, "updated_at")
		values["updated_at"] = rowJSON(time.Now().UTC().Format(time.RFC3339Nano))
	}
	query, params := runtime.RowUpdateStatement(in.Table, meta.rowid, names, values, rowJSON(in.ID), filter, c.Owner, meta.hold && !c.Owner && !c.OwnerOnSite)
	commit, rollback, err := beginTableWrite(conn)
	if err != nil {
		return TableResult{}, err
	}
	defer rollback()
	before := conn.TotalChanges()
	if _, err = runTableWrite(conn, in.Table, c.Owner, sqlite3.AUTH_UPDATE, query, params, false); err != nil {
		return TableResult{}, err
	}
	if conn.Changes() == 0 {
		return TableResult{}, &Error{Code: "row_not_found", Message: "row not found"}
	}
	if !c.Owner && conn.TotalChanges()-before != 1 {
		return TableResult{}, &Error{Code: "forbidden", Message: "this change would touch other rows; pages cannot make it, only the owner's tools"}
	}
	if err = checkRowReferences(conn, c, in.Table, meta.rowid, in.ID); err != nil {
		return TableResult{}, err
	}
	if err = s.finishTableWrite(conn, c, commit); err != nil {
		return TableResult{}, err
	}
	return TableResult{Body: map[string]any{"changes": 1}, Notify: s.rowNotification(c, in.Table, "record.changed", "edited", in.ID, values)}, nil
}

func (s *Service) rowNotification(c Call, table, name, change string, id int64, values map[string]json.RawMessage) func() {
	return func() {
		if c.Owner || s.deps.Notify == nil {
			return
		}
		data := map[string]any{"table": table, "row_id": id}
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

// TableRemove authorizes and removes one visible row in a single workflow.
func (s *Service) TableRemove(ctx context.Context, in TableInput) (TableResult, error) {
	if !in.Authorized.Allowed() || in.Authorized.table != in.Table || in.Authorized.action != "delete" {
		return TableResult{Authorization: in.Authorized.Authorization}, nil
	}
	c, filter := in.Authorized.call, in.Authorized.filter
	var out TableResult
	ctx, unlock, err := s.writeLock(ctx, c.OwnerID+"/"+c.SiteName, s.deps.WriteLockWait, s.deps.WriteTimeout, s.deps.Recheck)
	if err != nil {
		return out, err
	}
	defer unlock()
	c.Context = ctx
	conn, done, err := s.openTableConn(ctx, c, true)
	if err != nil {
		return out, err
	}
	defer done()
	meta, err := tableMetadata(conn, c, in.Table, filter)
	if err != nil {
		return out, err
	}
	query, params := runtime.RowDeleteStatement(in.Table, meta.rowid, rowJSON(in.ID), filter, meta.hold && !c.Owner && !c.OwnerOnSite)
	commit, rollback, err := beginTableWrite(conn)
	if err != nil {
		return out, err
	}
	defer rollback()
	before := conn.TotalChanges()
	if _, err = runTableWrite(conn, in.Table, c.Owner, sqlite3.AUTH_DELETE, query, params, false); err != nil {
		return out, err
	}
	if conn.Changes() == 0 {
		return out, &Error{Code: "row_not_found", Message: "row not found"}
	}
	if !c.Owner && conn.TotalChanges()-before != 1 {
		return out, &Error{Code: "forbidden", Message: "this change would touch other rows; pages cannot make it, only the owner's tools"}
	}
	if err = s.finishTableWrite(conn, c, commit); err != nil {
		return out, err
	}
	out.Body = map[string]any{"deleted": true}
	out.Notify = s.rowNotification(c, in.Table, "record.changed", "deleted", in.ID, nil)
	return out, nil
}
