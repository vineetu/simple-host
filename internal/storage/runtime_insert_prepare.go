package storage

import (
	"encoding/json"
	"errors"
	sqlite3 "github.com/ncruces/go-sqlite3"
	"time"
)

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

// ErrInsertSuppliesID indicates a visitor supplied a server-assigned ID.
var ErrInsertSuppliesID = errors.New("the server assigns ids")

// PrepareInsert validates IDs, stamps server-owned values and constructs the insert statement.
func PrepareInsert(conn *sqlite3.Conn, table, rowid string, cols map[string]string, values map[string]json.RawMessage, actor InsertActor) (string, []json.RawMessage, error) {
	if !actor.Owner {
		supplied, err := insertSuppliesID(conn, table, values)
		if err != nil {
			return "", nil, err
		}
		if supplied {
			return "", nil, ErrInsertSuppliesID
		}
	}
	stampInsertValues(cols, values, actor)
	names, err := insertColumns(cols, values)
	if err != nil {
		return "", nil, err
	}
	query, params := insertRowStatement(table, rowid, !actor.Owner, names, values)
	return query, params, nil
}
