package handler

import (
	"encoding/json"
	"strings"
	"testing"
)

// Every storage SQL refusal from real SQLite has its status and stable code,
// names the table, column or tool involved, and leaks no paths. Request
// validation order is host/data's TestValidateLegacySQLOrder.
func TestSiteStorageSQLErrorsAreActionable(t *testing.T) {
	a := newPrivateApp(t)
	owner := a.newPerson(t, "sqlerr-owner")
	a.deploy(t, owner, "shop")
	base := "/v1/sites/shop/storage"
	key := map[string]string{"X-API-Key": owner.key}
	if r := a.at(t, "PUT", pcSiteDomain, base+"/resources/db", map[string]string{"kind": "sqlite"}, key); r.status != 201 {
		t.Fatalf("resource %d %s", r.status, r.body)
	}
	for _, ddl := range []string{
		"CREATE TABLE parents (id INTEGER PRIMARY KEY)",
		"CREATE TABLE customers (id INTEGER PRIMARY KEY, email TEXT UNIQUE, name TEXT NOT NULL, age INTEGER CHECK (age > 0), parent_id INTEGER REFERENCES parents(id))",
	} {
		body, _ := json.Marshal(map[string]any{"sql": ddl})
		if r := a.at(t, "POST", pcSiteDomain, base+"/sqlite/db/schema", string(body), key); r.status != 200 {
			t.Fatalf("schema %d %s", r.status, r.body)
		}
	}
	if r := a.at(t, "POST", pcSiteDomain, base+"/sqlite/db/execute", `{"sql":"INSERT INTO customers (email, name) VALUES (?, ?)","params":["a@example.com","Ann"]}`, key); r.status != 200 {
		t.Fatalf("seed %d %s", r.status, r.body)
	}
	cases := []struct {
		name, route, body string
		status            int
		code              string
		want              []string
	}{
		{"syntax", "query", `{"sql":"SELEC * FROM customers"}`, 400, "sql_syntax", []string{`near "SELEC"`}},
		{"incomplete", "query", `{"sql":"SELECT * FROM customers WHERE"}`, 400, "sql_syntax", nil},
		{"two statements", "query", `{"sql":"SELECT 1; SELECT 2"}`, 400, "sql_multiple_statements", []string{"SELECT 2"}},
		{"trailing semicolon and comment are fine", "query", `{"sql":"SELECT 1; -- done"}`, 200, "", nil},
		{"placeholders", "query", `{"sql":"SELECT * FROM customers WHERE id = ? AND name = ?","params":[1]}`, 400, "sql_param_count", []string{"2 placeholders but 1 param"}},
		{"pragma", "query", `{"sql":"PRAGMA table_info(customers)"}`, 400, "sql_not_available", []string{"sqlite_schema"}},
		{"pragma function", "query", `{"sql":"SELECT * FROM pragma_table_info('customers')"}`, 400, "sql_not_available", []string{"PRAGMA"}},
		{"attach", "execute", `{"sql":"ATTACH DATABASE 'x.db' AS other"}`, 400, "sql_not_available", []string{"ATTACH"}},
		{"temp table", "schema", `{"sql":"CREATE TEMP TABLE scratch (x)"}`, 400, "sql_not_available", nil},
		{"virtual table", "schema", `{"sql":"CREATE VIRTUAL TABLE docs USING fts5(body)"}`, 400, "sql_not_available", nil},
		{"transaction", "execute", `{"sql":"BEGIN"}`, 400, "sql_not_available", nil},
		{"load_extension", "query", `{"sql":"SELECT load_extension('x')"}`, 400, "sql_no_such_function", []string{"load_extension()"}},
		{"unknown function", "query", `{"sql":"SELECT NOW()"}`, 400, "sql_no_such_function", []string{"NOW()", "datetime('now')"}},
		{"write on query", "query", `{"sql":"INSERT INTO customers (name) VALUES ('Bo')"}`, 400, "sql_use_execute", []string{"storage_sql_execute"}},
		{"read on execute", "execute", `{"sql":"SELECT * FROM customers"}`, 400, "sql_use_query", []string{"storage_sql_query"}},
		{"schema on execute", "execute", `{"sql":"CREATE TABLE x (a)"}`, 400, "sql_use_schema", []string{"storage_sql_schema"}},
		{"schema on query", "query", `{"sql":"DROP TABLE customers"}`, 400, "sql_use_schema", []string{"storage_sql_schema"}},
		{"write on schema", "schema", `{"sql":"DELETE FROM customers"}`, 400, "sql_use_execute", []string{"storage_sql_execute"}},
		{"no table", "query", `{"sql":"SELECT * FROM orders"}`, 400, "sql_no_such_table", []string{"orders", "customers, parents"}},
		{"no column", "query", `{"sql":"SELECT phone FROM customers"}`, 400, "sql_no_such_column", []string{"phone", "id, email, name, age, parent_id"}},
		{"no column named", "execute", `{"sql":"INSERT INTO customers (phone) VALUES ('1')"}`, 400, "sql_no_such_column", []string{"phone", "id, email"}},
		{"unique", "execute", `{"sql":"INSERT INTO customers (email, name) VALUES ('a@example.com', 'Al')"}`, 409, "sql_unique_failed", []string{"customers.email", "ON CONFLICT"}},
		{"not null", "execute", `{"sql":"INSERT INTO customers (email) VALUES ('b@example.com')"}`, 400, "sql_not_null_failed", []string{"customers.name"}},
		{"check", "execute", `{"sql":"INSERT INTO customers (name, age) VALUES ('Cy', -1)"}`, 400, "sql_check_failed", []string{"age > 0"}},
		{"foreign key", "execute", `{"sql":"INSERT INTO customers (name, parent_id) VALUES ('Di', 99)"}`, 400, "sql_foreign_key_failed", []string{"customers.parent_id", "parents.id"}},
		{"value count", "execute", `{"sql":"INSERT INTO customers VALUES (1)"}`, 400, "sql_error", []string{"5 columns"}},
		{"bad body", "query", `{"sql":`, 400, "sql_request", nil},
		{"comment only", "query", `{"sql":"-- nothing"}`, 400, "sql_request", nil},
		{"runtime error", "query", `{"sql":"SELECT json_extract('x', '$')"}`, 400, "sql_error", []string{"malformed JSON"}},
	}
	for _, tc := range cases {
		r := a.at(t, "POST", pcSiteDomain, base+"/sqlite/db/"+tc.route, tc.body, key)
		if r.status != tc.status {
			t.Errorf("%s: status %d, want %d: %s", tc.name, r.status, tc.status, r.body)
			continue
		}
		if tc.code == "" {
			continue
		}
		j := r.json(t)
		msg, _ := j["error"].(string)
		if j["code"] != tc.code {
			t.Errorf("%s: code %v, want %s: %s", tc.name, j["code"], tc.code, msg)
		}
		for _, w := range tc.want {
			if !strings.Contains(msg, w) {
				t.Errorf("%s: %q does not say %q", tc.name, msg, w)
			}
		}
		if strings.Contains(msg, "/runtime/") || strings.Contains(msg, ".sqlite") || strings.Contains(msg, "sqlite3:") {
			t.Errorf("%s: internal detail in %q", tc.name, msg)
		}
	}
	// A foreign key refused on delete points at the rows that still refer to it.
	for _, sql := range []string{"INSERT INTO parents (id) VALUES (1)", "INSERT INTO customers (name, parent_id) VALUES ('Ed', 1)"} {
		body, _ := json.Marshal(map[string]any{"sql": sql})
		if r := a.at(t, "POST", pcSiteDomain, base+"/sqlite/db/execute", string(body), key); r.status != 200 {
			t.Fatalf("seed fk %d %s", r.status, r.body)
		}
	}
	if r := a.at(t, "POST", pcSiteDomain, base+"/sqlite/db/execute", `{"sql":"DELETE FROM parents WHERE id = 1"}`, key); r.status != 400 || r.json(t)["code"] != "sql_foreign_key_failed" {
		t.Fatalf("fk delete %d %s", r.status, r.body)
	}
}
