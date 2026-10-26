package handler

import (
	"encoding/json"
	"strings"
	"testing"
)

// Every storage SQL refusal names what happened and what to do, under a
// stable code, without paths or another table's rows.
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
		{"incomplete", "query", `{"sql":"SELECT * FROM customers WHERE"}`, 400, "sql_syntax", []string{"ends too early"}},
		{"two statements", "query", `{"sql":"SELECT 1; SELECT 2"}`, 400, "sql_multiple_statements", []string{"each statement as its own call", "SELECT 2"}},
		{"trailing semicolon and comment are fine", "query", `{"sql":"SELECT 1; -- done"}`, 200, "", nil},
		{"placeholders", "query", `{"sql":"SELECT * FROM customers WHERE id = ? AND name = ?","params":[1]}`, 400, "sql_param_count", []string{"2 placeholders but 1 param"}},
		{"pragma", "query", `{"sql":"PRAGMA table_info(customers)"}`, 400, "sql_not_available", []string{"PRAGMA is not available", "sqlite_schema"}},
		{"pragma function", "query", `{"sql":"SELECT * FROM pragma_table_info('customers')"}`, 400, "sql_not_available", []string{"PRAGMA"}},
		{"attach", "execute", `{"sql":"ATTACH DATABASE 'x.db' AS other"}`, 400, "sql_not_available", []string{"ATTACH"}},
		{"temp table", "schema", `{"sql":"CREATE TEMP TABLE scratch (x)"}`, 400, "sql_not_available", []string{"temp tables"}},
		{"virtual table", "schema", `{"sql":"CREATE VIRTUAL TABLE docs USING fts5(body)"}`, 400, "sql_not_available", []string{"virtual tables"}},
		{"transaction", "execute", `{"sql":"BEGIN"}`, 400, "sql_not_available", []string{"own transaction"}},
		{"load_extension", "query", `{"sql":"SELECT load_extension('x')"}`, 400, "sql_no_such_function", []string{"load_extension() is not available"}},
		{"unknown function", "query", `{"sql":"SELECT NOW()"}`, 400, "sql_no_such_function", []string{"NOW() is not available", "datetime('now')"}},
		{"write on query", "query", `{"sql":"INSERT INTO customers (name) VALUES ('Bo')"}`, 400, "sql_use_execute", []string{"storage_sql_execute"}},
		{"read on execute", "execute", `{"sql":"SELECT * FROM customers"}`, 400, "sql_use_query", []string{"storage_sql_query"}},
		{"schema on execute", "execute", `{"sql":"CREATE TABLE x (a)"}`, 400, "sql_use_schema", []string{"storage_sql_schema"}},
		{"schema on query", "query", `{"sql":"DROP TABLE customers"}`, 400, "sql_use_schema", []string{"storage_sql_schema"}},
		{"write on schema", "schema", `{"sql":"DELETE FROM customers"}`, 400, "sql_use_execute", []string{"storage_sql_execute"}},
		{"no table", "query", `{"sql":"SELECT * FROM orders"}`, 400, "sql_no_such_table", []string{"table orders does not exist", "Tables here: customers, parents"}},
		{"no column", "query", `{"sql":"SELECT phone FROM customers"}`, 400, "sql_no_such_column", []string{"column phone does not exist", "Columns in customers: id, email, name, age, parent_id"}},
		{"no column named", "execute", `{"sql":"INSERT INTO customers (phone) VALUES ('1')"}`, 400, "sql_no_such_column", []string{"column phone does not exist in customers", "Columns in customers: id, email"}},
		{"unique", "execute", `{"sql":"INSERT INTO customers (email, name) VALUES ('a@example.com', 'Al')"}`, 409, "sql_unique_failed", []string{"customers.email already exists", "ON CONFLICT"}},
		{"not null", "execute", `{"sql":"INSERT INTO customers (email) VALUES ('b@example.com')"}`, 400, "sql_not_null_failed", []string{"customers.name needs a value"}},
		{"check", "execute", `{"sql":"INSERT INTO customers (name, age) VALUES ('Cy', -1)"}`, 400, "sql_check_failed", []string{"age > 0"}},
		{"foreign key", "execute", `{"sql":"INSERT INTO customers (name, parent_id) VALUES ('Di', 99)"}`, 400, "sql_foreign_key_failed", []string{"customers.parent_id must match parents.id"}},
		{"value count", "execute", `{"sql":"INSERT INTO customers VALUES (1)"}`, 400, "sql_error", []string{"has 5 columns but 1 values", "name the columns"}},
		{"empty sql", "query", `{"sql":"  "}`, 400, "sql_request", []string{"sql is empty"}},
		{"bad body", "query", `{"sql":`, 400, "sql_request", []string{"one JSON object"}},
		{"comment only", "query", `{"sql":"-- nothing"}`, 400, "sql_request", []string{"no statement"}},
		{"object param", "query", `{"sql":"SELECT ?","params":[{"a":1}]}`, 400, "sql_params", []string{"params[0]", "JSON string"}},
		{"runtime error", "query", `{"sql":"SELECT json_extract('x', '$')"}`, 400, "sql_error", []string{"SQLite: malformed JSON"}},
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
	if r := a.at(t, "POST", pcSiteDomain, base+"/sqlite/db/execute", `{"sql":"DELETE FROM parents WHERE id = 1"}`, key); r.status != 400 || r.json(t)["code"] != "sql_foreign_key_failed" || !strings.Contains(string(r.body), "still point at this row") {
		t.Fatalf("fk delete %d %s", r.status, r.body)
	}
}

func TestSQLiteTextDropsPathsAndPrefix(t *testing.T) {
	for in, want := range map[string]string{
		"unable to open /srv/simple-host/sites/u/s/runtime/sqlite/db.sqlite": "unable to open [path]",
		"cannot use db.sqlite-wal here":                                      "cannot use [path] here",
		`near "/": syntax error`:                                             `near "/": syntax error`,
	} {
		if got := sqlPathPattern.ReplaceAllString(in, "[path]"); got != want {
			t.Errorf("%q: got %q, want %q", in, got, want)
		}
	}
	if got := sqlSecondStatement(" ; /* x */ -- y\n  SELECT 2"); got != "SELECT 2" {
		t.Errorf("second statement %q", got)
	}
	if got := sqlSecondStatement(" ;\n-- trailing"); got != "" {
		t.Errorf("comment counted as statement: %q", got)
	}
}

// A visitor on a legacy full-mode database gets SQLite's words about their own
// statement and the rule that refused it, never the owner's table or column
// lists.
func TestSiteStorageSQLErrorsVisitorSeesNoSchema(t *testing.T) {
	s := newPresetSite(t)
	insertLegacyStorage(t, s.a.database, s.sid, "old", "sqlite", "owner", "anyone", "full", "")
	s.must(t, "POST", "/sqlite/old/schema", map[string]string{"sql": "CREATE TABLE secret_notes (id INTEGER PRIMARY KEY, body TEXT UNIQUE)"}, s.key, 200)
	s.must(t, "POST", "/sqlite/old/schema", map[string]string{"sql": "CREATE TABLE hidden (id INTEGER PRIMARY KEY, body TEXT, hidden_col TEXT NOT NULL, hidden_num INTEGER CHECK (hidden_num > 0) DEFAULT 1, strict_col INTEGER, UNIQUE (body, hidden_num))"}, s.key, 200)
	s.must(t, "POST", "/sqlite/old/schema", map[string]string{"sql": "CREATE TABLE strict_hidden (id INTEGER PRIMARY KEY, body TEXT, strict_hidden_col INTEGER DEFAULT 'oops') STRICT"}, s.key, 200)
	s.must(t, "POST", "/sqlite/old/execute", map[string]string{"sql": "INSERT INTO hidden (body, hidden_col) VALUES ('dup', 'x')"}, s.key, 200)
	s.must(t, "POST", "/sqlite/old/execute", map[string]string{"sql": "INSERT INTO secret_notes (body) VALUES ('hidden row')"}, s.key, 200)
	anon := s.callers["anon"]
	for _, tc := range []struct {
		sql, code, want string
	}{
		{"INSERT INTO nope (x) VALUES (1)", "sql_no_such_table", "table nope does not exist"},
		{"INSERT INTO secret_notes (zz) VALUES (1)", "sql_no_such_column", "column zz does not exist"},
		{"UPDATE secret_notes SET zz = 1", "sql_no_such_column", "column zz does not exist"},
		{"INSERT INTO secret_notes (body) SELECT body FROM secret_notes", "sql_visitor_rule", "read rule is owner"},
		{"INSERT INTO secret_notes (body) VALUES ('hidden row')", "sql_unique_failed", "same unique value already exists"},
		{"INSERT INTO hidden (body) VALUES ('x')", "sql_not_null_failed", "required column got no value"},
		{"INSERT INTO hidden (body, hidden_col) VALUES ('dup', 'y')", "sql_unique_failed", "same unique value already exists"},
		{"INSERT INTO hidden (body, hidden_col, hidden_num) VALUES ('z', 'y', -1)", "sql_check_failed", "CHECK rules"},
		{"INSERT INTO strict_hidden (body) VALUES ('x')", "sql_constraint_failed", "a table rule refused the change"},
	} {
		r := s.do(t, "POST", "/sqlite/old/execute", map[string]string{"sql": tc.sql}, anon)
		j := r.json(t)
		msg, _ := j["error"].(string)
		if j["code"] != tc.code || !strings.Contains(msg, tc.want) {
			t.Errorf("%s: %d %s", tc.sql, r.status, r.body)
		}
		if strings.Contains(msg, "Tables here") || strings.Contains(msg, "Columns in") || strings.Contains(msg, "id, body") || strings.Contains(msg, "hidden_") || strings.Contains(msg, "secret_notes.") || strings.Contains(msg, "strict_") {
			t.Errorf("%s: schema leaked to a visitor: %s", tc.sql, msg)
		}
	}
}
