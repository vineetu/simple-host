package storage

import (
	"encoding/json"
	"strings"

	sqlite3 "github.com/ncruces/go-sqlite3"
)

// RowStatementStage identifies where preparing or binding a row statement failed.
type RowStatementStage int

const (
	// RowStatementPrepare denotes an invalid or unauthorized statement.
	RowStatementPrepare RowStatementStage = iota + 1
	// RowStatementBind denotes invalid statement parameters.
	RowStatementBind
)

// RunRowStatement prepares, binds and executes a single SQLite row write.
func RunRowStatement(conn *sqlite3.Conn, query string, params []json.RawMessage, returning bool) (int64, RowStatementStage, error) {
	stmt, tail, err := conn.Prepare(query)
	if err != nil || stmt == nil || strings.TrimSpace(tail) != "" {
		return 0, RowStatementPrepare, err
	}
	defer stmt.Close()
	if len(params) != stmt.BindCount() {
		return 0, RowStatementBind, nil
	}
	for i, raw := range params {
		if err := BindStorageParam(stmt, i+1, raw); err != nil {
			return 0, RowStatementBind, err
		}
	}
	var id int64
	if returning {
		if stmt.Step() {
			id = stmt.ColumnInt64(0)
			stmt.Step()
		}
	} else {
		stmt.Step()
	}
	if err = stmt.Err(); err == nil {
		err = stmt.Close()
	}
	return id, 0, err
}
