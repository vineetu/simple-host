package data

import (
	"bytes"
	"encoding/json"
	"io"
	"strconv"

	"github.com/vsriram/simple-host/host/storage"
)

// The row routes' input rules. Visitors send an action and values, never
// SQL; the server builds every statement from the table's rules.

// RowsMaxValues is the most column values one add or edit sends.
const RowsMaxValues = 100

// RowRequest checks the shape of a row request: an action the route knows
// (edit and delete name a row; add never does), a positive row id, and a
// table name.
func RowRequest(action, table, rawID string) (int64, error) {
	if action == "" || (action == "edit" || action == "delete") && rawID == "" {
		return 0, &Error{Code: "method_not_allowed", Message: "method not allowed"}
	}
	var id int64
	if rawID != "" {
		n, err := strconv.ParseInt(rawID, 10, 64)
		if err != nil || n < 1 || action == "add" {
			return 0, invalidRows("row id is a positive whole number")
		}
		id = n
	}
	if !TableNameRE.MatchString(table) {
		return 0, invalidRows("invalid row request")
	}
	return id, nil
}

// DecodeRowValues reads the column values of an add or an edit: one JSON
// object of at most RowsMaxValues scalars (an edit changes at least one).
func DecodeRowValues(body []byte, action string) (map[string]json.RawMessage, error) {
	var values map[string]json.RawMessage
	dec := json.NewDecoder(bytes.NewReader(body))
	if dec.Decode(&values) != nil || values == nil || len(values) > RowsMaxValues || dec.Decode(new(any)) != io.EOF || action == "edit" && len(values) == 0 {
		return nil, invalidRows("send a JSON object of column values")
	}
	for _, raw := range values {
		if !storage.StorageScalar(raw) {
			return nil, invalidRows("values are strings, numbers, true, false, or null")
		}
	}
	return values, nil
}

func invalidRows(message string) error { return &Error{Code: "invalid_rows", Message: message} }
