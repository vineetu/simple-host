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
	ctx, unlock, err := s.WriteLock(ctx, c.OwnerID+"/"+c.SiteName, s.deps.WriteLockWait, s.deps.WriteTimeout, s.deps.Recheck)
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
	if _, _, err := conn.WALCheckpoint("main", sqlite3.CHECKPOINT_TRUNCATE); err != nil {
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
		return nil, nil, &Error{Code: "storage_busy", Message: "storage is busy; retry shortly"}
	}
	return commit, rollback, nil
}

// TableAdd authorizes before entering the request's idempotency boundary.
// The callback decodes the request only on a cache miss.
func (s *Service) TableAdd(ctx context.Context, call *Call, in TableInput) (TableResult, error) {
	c, filter, auth, allowed := s.authorizeTable(ctx, *call, in.Table, "add")
	if !allowed {
		return TableResult{Authorization: auth}, nil
	}
	s.deps.TableWriteRequest(c, auth, func(values map[string]json.RawMessage) (TableResult, error) {
		in.Values = values
		id, err := s.addTableRowLocked(ctx, c, in, filter)
		if err != nil {
			return TableResult{}, err
		}
		return TableResult{Body: map[string]any{"changes": 1, "last_insert_id": id}, Notify: s.rowNotification(c, in.Table, "record.added", "", id, values)}, nil
	})
	return TableResult{}, nil
}

// addTableRow keeps the insert, reference check and commit in one transaction.
// Its caller must have authorized the operation and acquired the site lock.
func (s *Service) addTableRow(conn *sqlite3.Conn, c Call, in TableInput, meta rowMetadata) (int64, error) {
	query, params, err := runtime.PrepareInsert(conn, in.Table, meta.rowid, meta.cols, in.Values, runtime.InsertActor{
		Owner: c.Owner, OwnerOnSite: c.OwnerOnSite, Hold: meta.hold, StampID: StampID(c.Owner, c.VisitorID),
	})
	if err != nil {
		if errors.Is(err, runtime.ErrInsertSuppliesID) {
			return 0, &Error{Code: "invalid_rows", Message: "the server assigns ids"}
		}
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
func (s *Service) TableEdit(ctx context.Context, call *Call, in TableInput) (TableResult, error) {
	c, filter, auth, allowed := s.authorizeTable(ctx, *call, in.Table, "edit")
	if !allowed {
		return TableResult{Authorization: auth}, nil
	}
	s.deps.TableWriteRequest(c, auth, func(values map[string]json.RawMessage) (TableResult, error) {
		ctx, unlock, err := s.WriteLock(ctx, c.OwnerID+"/"+c.SiteName, s.deps.WriteLockWait, s.deps.WriteTimeout, s.deps.Recheck)
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
		pk := map[string]bool{}
		info, _, err := conn.Prepare("PRAGMA table_info(" + runtime.Quote(in.Table) + ")")
		if err != nil {
			return TableResult{}, &Error{Code: "invalid_rows", Message: "invalid row request"}
		}
		for info.Step() {
			if info.ColumnInt(5) > 0 {
				pk[info.ColumnText(1)] = true
			}
		}
		info.Close()
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
		sets := []string{}
		params := []json.RawMessage{}
		for _, name := range names {
			sets = append(sets, runtime.Quote(name)+" = ?")
			params = append(params, values[name])
		}
		if _, ok := meta.cols["updated_at"]; ok {
			sets = append(sets, `"updated_at" = ?`)
			values["updated_at"] = rowJSON(time.Now().UTC().Format(time.RFC3339Nano))
			params = append(params, values["updated_at"])
		}
		query := "UPDATE OR ABORT " + runtime.Quote(in.Table) + " SET " + strings.Join(sets, ", ") + " WHERE " + meta.rowid + " = ?"
		if c.Owner {
			query = "UPDATE " + runtime.Quote(in.Table) + " SET " + strings.Join(sets, ", ") + " WHERE " + meta.rowid + " = ?"
		}
		params = append(params, rowJSON(in.ID))
		if filter != "" {
			query += " AND visitor_id = ?"
			params = append(params, rowJSON(filter))
		}
		if meta.hold && !c.Owner && !c.OwnerOnSite {
			query += " AND approved_at IS NOT NULL"
		}
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
	})
	return TableResult{}, nil
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
func (s *Service) TableRemove(ctx context.Context, call *Call, in TableInput) (TableResult, error) {
	c, filter, auth, allowed := s.authorizeTable(ctx, *call, in.Table, "delete")
	if !allowed {
		return TableResult{Authorization: auth}, nil
	}
	s.deps.TableWriteRequest(c, auth, func(_ map[string]json.RawMessage) (TableResult, error) {
		var out TableResult
		ctx, unlock, err := s.WriteLock(ctx, c.OwnerID+"/"+c.SiteName, s.deps.WriteLockWait, s.deps.WriteTimeout, s.deps.Recheck)
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
		query := "DELETE FROM " + runtime.Quote(in.Table) + " WHERE " + meta.rowid + " = ?"
		params := []json.RawMessage{rowJSON(in.ID)}
		if filter != "" {
			query += " AND visitor_id = ?"
			params = append(params, rowJSON(filter))
		}
		if meta.hold && !c.Owner && !c.OwnerOnSite {
			query += " AND approved_at IS NOT NULL"
		}
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
	})
	return TableResult{}, nil
}
