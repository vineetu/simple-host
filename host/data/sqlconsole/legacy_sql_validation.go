package sqlconsole

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/vsriram/simple-host/host/data"

	runtime "github.com/vsriram/simple-host/host/storage"
)

// ValidateLegacySQL checks a decoded legacy SQL request in wire response order.
func ValidateLegacySQL(sql string, params []json.RawMessage) *data.Error {
	switch {
	case strings.TrimSpace(sql) == "":
		return &data.Error{Code: "sql_request", Message: "sql is empty: send one SQL statement"}
	case len(sql) > runtime.SQLStatementLimit:
		return &data.Error{Code: "sql_request", Message: "sql is longer than " + commas(runtime.SQLStatementLimit) + " characters: send a shorter statement, with values in params"}
	case len(params) > 100:
		return &data.Error{Code: "sql_request", Message: "at most 100 params per call: split the change into several calls"}
	}
	if i := runtime.LegacyInvalidParam(params); i >= 0 {
		return &data.Error{Code: "sql_params", Message: fmt.Sprintf("params[%d] is not a single value: each param must be a string, number, true, false or null (store an object or list as a JSON string)", i)}
	}
	return nil
}

// commas prints 32768 as 32,768.
func commas(n int) string {
	s := strconv.Itoa(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}
