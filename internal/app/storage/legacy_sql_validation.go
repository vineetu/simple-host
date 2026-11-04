package storage

import (
	"encoding/json"
	"fmt"
	"strings"

	runtime "github.com/vsriram/simple-host/internal/storage"
)

// ValidateLegacySQL checks a decoded legacy SQL request in wire response order.
func ValidateLegacySQL(sql string, params []json.RawMessage) *Error {
	switch {
	case strings.TrimSpace(sql) == "":
		return &Error{Code: "sql_request", Message: "sql is empty: send one SQL statement"}
	case len(sql) > 32768:
		return &Error{Code: "sql_request", Message: "sql is longer than 32,768 characters: send a shorter statement, with values in params"}
	case len(params) > 100:
		return &Error{Code: "sql_request", Message: "at most 100 params per call: split the change into several calls"}
	}
	if i := runtime.LegacyInvalidParam(params); i >= 0 {
		return &Error{Code: "sql_params", Message: fmt.Sprintf("params[%d] is not a single value: each param must be a string, number, true, false or null (store an object or list as a JSON string)", i)}
	}
	return nil
}
