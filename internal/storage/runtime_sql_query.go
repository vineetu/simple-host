package storage

import (
	"encoding/json"
	"errors"

	sqlite3 "github.com/ncruces/go-sqlite3"
)

// ErrLegacyQueryRows indicates that a query returned more than 500 rows.
var ErrLegacyQueryRows = errors.New("legacy query returned more than 500 rows")

// ErrLegacyQuerySize indicates that a query result exceeds its byte limit.
var ErrLegacyQuerySize = errors.New("legacy query result is too large")

// LegacyInvalidParam returns the index of the first non-scalar parameter, or -1.
func LegacyInvalidParam(params []json.RawMessage) int {
	for i, raw := range params {
		if !StorageScalar(raw) {
			return i
		}
	}
	return -1
}

// LegacyQueryResult reads and sizes a prepared legacy SQLite query result.
func LegacyQueryResult(stmt *sqlite3.Stmt, limit int) (map[string]any, error) {
	cols := make([]string, stmt.ColumnCount())
	for i := range cols {
		cols[i] = stmt.ColumnName(i)
	}
	rows := make([][]any, 0)
	encodedCols, _ := json.Marshal(cols)
	resultBytes := len(encodedCols) + 64
	for stmt.Step() {
		if len(rows) >= 500 {
			return nil, ErrLegacyQueryRows
		}
		row := make([]any, len(cols))
		for i := range cols {
			row[i] = StorageColumn(stmt, i)
		}
		encoded, err := json.Marshal(row)
		if err != nil || resultBytes+len(encoded) > limit {
			return nil, ErrLegacyQuerySize
		}
		resultBytes += len(encoded)
		rows = append(rows, row)
	}
	if err := stmt.Err(); err != nil {
		return nil, err
	}
	result := map[string]any{"columns": cols, "rows": rows}
	encoded, err := json.Marshal(result)
	if err != nil || len(encoded) > limit {
		return nil, ErrLegacyQuerySize
	}
	return result, nil
}
