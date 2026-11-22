package datatest

import (
	"strings"
	"testing"
)

// Storage SQL refusals from real SQLite carry their status and stable code
// and leak no paths. Request validation order is host/data's.
func TestSiteStorageSQLErrors(t *testing.T) {
	s := newShop(t)
	s.Must(t, "PUT", "/resources/db", map[string]string{"kind": "sqlite"}, s.Key, 201)
	for _, ddl := range []string{
		"CREATE TABLE parents (id INTEGER PRIMARY KEY)",
		"CREATE TABLE customers (id INTEGER PRIMARY KEY, email TEXT UNIQUE, name TEXT NOT NULL, age INTEGER CHECK (age > 0), parent_id INTEGER REFERENCES parents(id))",
	} {
		s.Must(t, "POST", "/sqlite/db/schema", map[string]string{"sql": ddl}, s.Key, 200)
	}
	s.Must(t, "POST", "/sqlite/db/execute", `{"sql":"INSERT INTO customers (email, name) VALUES (?, ?)","params":["a@example.com","Ann"]}`, s.Key, 200)
	for _, tc := range []struct {
		route, body string
		status      int
		code        string
	}{
		{"query", `{"sql":"SELEC * FROM customers"}`, 400, "sql_syntax"},
		{"query", `{"sql":"SELECT 1; SELECT 2"}`, 400, "sql_multiple_statements"},
		{"query", `{"sql":"SELECT 1; -- done"}`, 200, ""},
		{"query", `{"sql":"SELECT * FROM customers WHERE id = ? AND name = ?","params":[1]}`, 400, "sql_param_count"},
		{"query", `{"sql":"PRAGMA table_info(customers)"}`, 400, "sql_not_available"},
		{"query", `{"sql":"SELECT * FROM pragma_table_info('customers')"}`, 400, "sql_not_available"},
		{"execute", `{"sql":"ATTACH DATABASE 'x.db' AS other"}`, 400, "sql_not_available"},
		{"schema", `{"sql":"CREATE TEMP TABLE scratch (x)"}`, 400, "sql_not_available"},
		{"schema", `{"sql":"CREATE VIRTUAL TABLE docs USING fts5(body)"}`, 400, "sql_not_available"},
		{"execute", `{"sql":"BEGIN"}`, 400, "sql_not_available"},
		{"query", `{"sql":"SELECT load_extension('x')"}`, 400, "sql_no_such_function"},
		{"query", `{"sql":"INSERT INTO customers (name) VALUES ('Bo')"}`, 400, "sql_use_execute"},
		{"execute", `{"sql":"SELECT * FROM customers"}`, 400, "sql_use_query"},
		{"query", `{"sql":"DROP TABLE customers"}`, 400, "sql_use_schema"},
		{"query", `{"sql":"SELECT * FROM orders"}`, 400, "sql_no_such_table"},
		{"query", `{"sql":"SELECT phone FROM customers"}`, 400, "sql_no_such_column"},
		{"execute", `{"sql":"INSERT INTO customers (email, name) VALUES ('a@example.com', 'Al')"}`, 409, "sql_unique_failed"},
		{"execute", `{"sql":"INSERT INTO customers (email) VALUES ('b@example.com')"}`, 400, "sql_not_null_failed"},
		{"execute", `{"sql":"INSERT INTO customers (name, age) VALUES ('Cy', -1)"}`, 400, "sql_check_failed"},
		{"execute", `{"sql":"INSERT INTO customers (name, parent_id) VALUES ('Di', 99)"}`, 400, "sql_foreign_key_failed"},
		{"query", `{"sql":`, 400, "sql_request"},
		{"query", `{"sql":"SELECT json_extract('x', '$')"}`, 400, "sql_error"},
	} {
		r := s.Do(t, "POST", "/sqlite/db/"+tc.route, tc.body, s.Key)
		if r.Status != tc.status || r.Code() != tc.code {
			t.Errorf("%s: %d %s, want %d %s", tc.body, r.Status, r.Code(), tc.status, tc.code)
		}
		if b := string(r.Body); strings.Contains(b, "/runtime/") || strings.Contains(b, ".sqlite") || strings.Contains(b, "sqlite3:") {
			t.Errorf("%s: internal detail in %s", tc.body, b)
		}
	}
}
