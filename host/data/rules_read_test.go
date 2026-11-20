package data

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"reflect"
	"strings"
	"testing"

	sqlite3 "github.com/ncruces/go-sqlite3"
	"github.com/vsriram/simple-host/host/model"
	ruleengine "github.com/vsriram/simple-host/host/storage/rules"
)

func readFixture(t *testing.T, schema string, set *json.RawMessage) (*Service, Call, ResourceInput) {
	t.Helper()
	s, c, in := rulesFixture(t, schema)
	s.deps.PasscodeAllows = func() bool { return true }
	s.deps.ViewersAllow = func() bool { return true }
	in.Rules = set
	c.Resource = putRules(t, s, in)
	c.Owner = false
	c.CSRFOK = true
	return s, c, in
}
func execReadFixture(t *testing.T, s *Service, sql string) {
	t.Helper()
	conn, err := sqlite3.Open(s.deps.SQLitePath)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err = conn.Exec(sql); err != nil {
		t.Fatal(err)
	}
}
func readCode(result TableResult, err error) string {
	if err == nil {
		err = result.Authorization
	}
	if err == nil || reflect.ValueOf(err).IsNil() {
		return ""
	}
	if e, ok := err.(*Error); ok {
		return e.Code
	}
	return err.Error()
}
func readGet(t *testing.T, s *Service, c Call, table string, id int64, want string) TableResult {
	t.Helper()
	out, err := s.TableGet(context.Background(), TableInput{Call: c, Table: table, ID: id})
	if code := readCode(out, err); code != want {
		t.Fatalf("get %s/%d: want %s, got %s (%+v, %v)", table, id, want, code, out, err)
	}
	return out
}
func readList(t *testing.T, s *Service, c Call, table string, q url.Values) TableResult {
	t.Helper()
	out, err := s.TableList(context.Background(), TableInput{Call: c, Table: table, Query: q})
	if code := readCode(out, err); code != "" {
		t.Fatalf("list: %s (%v)", code, err)
	}
	return out
}

func TestRulesReadHiddenPagingAndFilters(t *testing.T) {
	for _, full := range []bool{false, true} {
		t.Run(fmt.Sprint(full), func(t *testing.T) {
			set := readSet(`visitor_id == auth.id || auth.role == "owner"`)
			if full {
				// The full personal-style set: own read, edit and delete, signed-in add.
				raw := json.RawMessage(ruleengine.EncodeSet(ruleengine.PolicySet{Version: 1, Policies: []ruleengine.Policy{
					{Name: "read", Command: ruleengine.CmdRead, Using: `visitor_id == auth.id || auth.role == "owner"`},
					{Name: "add", Command: ruleengine.CmdAdd, Check: `auth.signed_in`},
					{Name: "edit", Command: ruleengine.CmdEdit, Using: `visitor_id == auth.id || auth.role == "owner"`},
					{Name: "delete", Command: ruleengine.CmdDelete, Using: `visitor_id == auth.id || auth.role == "owner"`},
				}}))
				set = &raw
			}
			s, c, _ := readFixture(t, `CREATE TABLE entries(id INTEGER PRIMARY KEY, body TEXT, visitor_id TEXT);`, set)
			execReadFixture(t, s, `INSERT INTO entries VALUES(1,'hidden','other'),(2,'visible-a','caller'),(3,'hidden','other'),(4,'visible-b','caller'),(5,'hidden','other');`)
			c.VisitorID, c.ActiveChecked = "caller", true
			hidden, e1 := s.TableGet(context.Background(), TableInput{Call: c, Table: "entries", ID: 1})
			missing, e2 := s.TableGet(context.Background(), TableInput{Call: c, Table: "entries", ID: 99})
			if !reflect.DeepEqual(hidden, missing) || !reflect.DeepEqual(e1, e2) || readCode(hidden, e1) != "row_not_found" {
				t.Fatalf("hidden != missing: %+v %v; %+v %v", hidden, e1, missing, e2)
			}
			out := readList(t, s, c, "entries", url.Values{"limit": {"1"}})
			if rows := out.Body["rows"].([][]any); len(rows) != 1 || rows[0][0] != int64(2) {
				t.Fatal(out.Body)
			}
			next := out.Body["next_after"].(string)
			if next == "" {
				t.Fatal("missing cursor")
			}
			out = readList(t, s, c, "entries", url.Values{"limit": {"1"}, "after": {next}})
			if rows := out.Body["rows"].([][]any); len(rows) != 1 || rows[0][0] != int64(4) || out.Body["next_after"] != "" {
				t.Fatal(out.Body)
			}
			out = readList(t, s, c, "entries", url.Values{"where.body": {"hidden"}, "order": {"body"}, "desc": {"1"}})
			if len(out.Body["rows"].([][]any)) != 0 || out.Body["next_after"] != "" {
				t.Fatal(out.Body)
			}
			readGet(t, s, c, "entries", 1, "row_not_found")
		})
	}
}

func TestRulesReadBinaryAndRowAliases(t *testing.T) {
	for _, schema := range []string{
		`CREATE TABLE entries(body TEXT COLLATE NOCASE);`,
		`CREATE TABLE entries(rowid TEXT, body TEXT COLLATE NOCASE);`,
	} {
		t.Run(schema, func(t *testing.T) {
			s, c, _ := readFixture(t, schema, readSet(`body == "yes"`))
			execReadFixture(t, s, `INSERT INTO entries(body) VALUES('YES'),('yes');`)
			readGet(t, s, c, "entries", 1, "row_not_found")
			readGet(t, s, c, "entries", 2, "")
			out := readList(t, s, c, "entries", nil)
			if len(out.Body["rows"].([][]any)) != 1 {
				t.Fatal(out.Body)
			}
		})
	}
}

func TestRulesReadHeldRowsAndAuthorRedaction(t *testing.T) {
	set := saveSet(ruleengine.Policy{Command: ruleengine.CmdRead, Using: "true"}, ruleengine.Policy{Command: ruleengine.CmdRead, Using: `approved_at is not null || auth.role == "owner"`, Restrictive: true})
	s, c, _ := readFixture(t, `CREATE TABLE entries(body TEXT, visitor_id TEXT);`, set)
	execReadFixture(t, s, `INSERT INTO entries(body,visitor_id,approved_at) VALUES('held','author',NULL),('public','other','now');`)
	for _, id := range []string{"", "author", "other"} {
		c.VisitorID = id
		readGet(t, s, c, "entries", 1, "row_not_found")
		out := readGet(t, s, c, "entries", 2, "")
		row := out.Body["row"].([]any)
		if id != "other" && row[1] != nil {
			t.Fatalf("author leaked: %+v", out.Body)
		}
		if out.Body["mine"] != (id == "other") {
			t.Fatal(out.Body)
		}
	}
	c.VisitorID, c.OwnerOnSite = c.OwnerID, true
	readGet(t, s, c, "entries", 1, "")
}

func TestRulesReadSuspension(t *testing.T) {
	for _, expr := range []string{"true", "auth.signed_in"} {
		t.Run(expr, func(t *testing.T) {
			s, c, _ := readFixture(t, `CREATE TABLE entries(body TEXT); INSERT INTO entries VALUES('ok');`, readSet(expr))
			c.VisitorID, c.VisitorSessionUserID = c.OwnerID, c.OwnerID
			if _, err := s.db.Exec(`UPDATE users SET suspended_at=now() WHERE id=$1`, c.OwnerID); err != nil {
				t.Fatal(err)
			}
			want := ""
			if expr != "true" {
				want = "account_suspended"
			}
			readGet(t, s, c, "entries", 1, want)
		})
	}
}

type readBindings struct {
	NoBindings
	values   func(context.Context, int) (ruleengine.Values, error)
	calls    int
	owner    bool
	ownerErr error
}

func (p *readBindings) Schema() ruleengine.BindingSchema {
	return ruleengine.BindingSchema{"event.member": {Type: ruleengine.TypeText, Nullable: true}}
}
func (p *readBindings) Values(ctx context.Context, c BindingCall, needed []string) (ruleengine.Values, error) {
	p.calls++
	return p.values(ctx, p.calls)
}
func (p *readBindings) OwnerOnSite(context.Context, string, string) (bool, error) {
	return p.owner, p.ownerErr
}

func TestRulesReadFreshBindingsAndFailure(t *testing.T) {
	for _, failure := range []string{"", "first", "second", "missing", "timeout"} {
		t.Run(failure, func(t *testing.T) {
			s, c, in := rulesFixture(t, `CREATE TABLE entries(body TEXT); INSERT INTO entries VALUES('first'),('second');`)
			s.deps.PasscodeAllows = func() bool { return true }
			s.deps.ViewersAllow = func() bool { return true }
			p := &readBindings{}
			p.values = func(ctx context.Context, n int) (ruleengine.Values, error) {
				if failure == "first" || failure == "second" && n == 2 {
					return nil, errors.New("unavailable")
				}
				if failure == "missing" {
					return nil, nil
				}
				if failure == "timeout" {
					<-ctx.Done()
					return nil, ctx.Err()
				}
				text := "first"
				if n == 2 {
					text = "second"
				}
				return ruleengine.Values{"event.member": ruleengine.Text(text)}, nil
			}
			s.deps.Bindings = SnapshotBindings(p)
			in.Rules = readSet("body == event.member")
			c.Resource = putRules(t, s, in)
			c.Owner = false
			c.VisitorID = "caller"
			c.ActiveChecked = true
			prepared := 0
			s.sqliteHook = func(stage string) {
				if stage == "after_prepare" {
					prepared++
				}
			}
			want := ""
			if failure != "" {
				want = "internal_error"
			}
			out := readGet(t, s, c, "entries", 2, want)
			if failure != "" && prepared != 0 {
				t.Fatalf("prepared %d data statements after provider failure", prepared)
			}
			if failure == "" && (p.calls != 2 || out.Body["row"].([]any)[0] != "second") {
				t.Fatalf("stale values: %+v calls=%d", out, p.calls)
			}
		})
	}
}

func TestRulesReadOwnerRecheck(t *testing.T) {
	s, c, _ := readFixture(t, `CREATE TABLE entries(body TEXT); INSERT INTO entries VALUES('private');`, readSet(`auth.role == "owner"`))
	c.OwnerOnSite, c.VisitorID, c.ActiveChecked = true, c.OwnerID, true
	s.deps.Bindings = &readBindings{ownerErr: errors.New("owner unavailable")}
	readGet(t, s, c, "entries", 1, "forbidden")
}

func TestRulesReadInvalidAndOwnerRepair(t *testing.T) {
	for _, broken := range []string{"table", "column", "binding"} {
		t.Run(broken, func(t *testing.T) {
			s, c, in := readFixture(t, `CREATE TABLE entries(body TEXT, other TEXT); INSERT INTO entries VALUES('ok','kept');`, readSet(`body == "ok"`))
			switch broken {
			case "table":
				c.Resource.Tables = map[string]model.TableRule{"entries": {}}
			case "column":
				execReadFixture(t, s, `ALTER TABLE entries DROP COLUMN body;`)
			case "binding":
				s.deps.Bindings = &readBindings{}
				in.Rules = readSet(`body == event.member`)
				c.Resource = putRules(t, s, in)
				s.deps.Bindings = nil
			}
			readGet(t, s, c, "entries", 1, "rules_invalid")
			c.Owner = true
			readGet(t, s, c, "entries", 1, "")
			result, err := s.LegacySQL(context.Background(), LegacySQLInput{Call: c, Mode: "query", ReadBody: func() ([]byte, error) { return []byte(`{"sql":"SELECT * FROM entries"}`), nil }, ResultLimit: 1 << 20})
			if err != nil || len(result.Rows) != 1 {
				t.Fatalf("owner query: %+v %v", result, err)
			}
		})
	}
}

func TestRulesReadRawSQLRefused(t *testing.T) {
	s, c, _ := readFixture(t, `CREATE TABLE entries(body TEXT);`, readSet("true"))
	for _, sql := range []string{"SELECT * FROM entries", "WITH x AS (SELECT * FROM entries) SELECT * FROM x", "SELECT * FROM sqlite_schema", "PRAGMA table_info(entries)", "SELECT * FROM entries UNION SELECT name FROM sqlite_schema", "ATTACH ':memory:' AS other", "UPDATE entries SET body='stolen' RETURNING *", "CREATE VIEW stolen AS SELECT * FROM entries"} {
		for _, mode := range []string{"query", "execute", "schema"} {
			for _, onSite := range []bool{false, true} {
				c.OwnerOnSite = onSite
				_, err := s.LegacySQL(context.Background(), LegacySQLInput{Call: c, Mode: mode, ReadBody: func() ([]byte, error) {
					t.Fatal("visitor SQL body was read")
					return json.Marshal(map[string]string{"sql": sql})
				}})
				if e, ok := err.(*Error); !ok || e.Code != "fixed_routes_required" {
					t.Fatalf("%s %s: %v", mode, sql, err)
				}
			}
		}
	}
}

func TestRulesOwnerSchemaColumnsRenameAndNotes(t *testing.T) {
	s, c, in := readFixture(t, `CREATE TABLE entries(body TEXT);`, saveSet(ruleengine.Policy{Command: ruleengine.CmdRead, Using: "true"}, ruleengine.Policy{Command: ruleengine.CmdAdd, Check: "auth.signed_in"}))
	c.Owner = true
	schema := func(sql, want string) LegacySQLResult {
		t.Helper()
		result, err := s.LegacySQL(context.Background(), LegacySQLInput{Call: c, Mode: "schema", ReadBody: func() ([]byte, error) { return json.Marshal(map[string]string{"sql": sql}) }})
		code := ""
		if err != nil {
			code = err.(*Error).Code
		}
		if code != want {
			t.Fatalf("schema %s: want %s, got %v", sql, want, err)
		}
		return result
	}
	schema(`CREATE TABLE more(body TEXT)`, "")
	err := s.Inspect(context.Background(), c, false, func(i *Inspection) error {
		cols, _, err := i.TableInfo("more")
		if cols["visitor_id"] != "TEXT" {
			t.Fatal(cols)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	tables := map[string]ResourceTableInput{"entries": {Rules: readSet(`body == "ok"`)}}
	in.Tables = &tables
	c.Resource = putRules(t, s, in)
	schema(`ALTER TABLE entries RENAME TO renamed`, "table_preset_rename")
	if err := s.Inspect(context.Background(), c, false, func(i *Inspection) error {
		if _, _, err := i.TableInfo("entries"); err != nil {
			return fmt.Errorf("refused rename removed the original table: %w", err)
		}
		if _, _, err := i.TableInfo("renamed"); err == nil {
			t.Fatal("refused rename kept the new table")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	tables["renamed"] = tables["entries"]
	c.Resource = putRules(t, s, in)
	result := schema(`ALTER TABLE entries RENAME TO renamed`, "")
	if !strings.Contains(strings.Join(result.Notes, " "), "rules") {
		t.Fatal(result.Notes)
	}
	result = schema(`ALTER TABLE renamed DROP COLUMN body`, "")
	if !strings.Contains(strings.Join(result.Notes, " "), "rules_invalid") {
		t.Fatal(result.Notes)
	}
	c.Owner = false
	readGet(t, s, c, "renamed", 1, "rules_invalid")
	c.Owner = true
	schema(`DROP TABLE renamed`, "")
}

func TestRulesReadOwnerQueryOptionsRechecked(t *testing.T) {
	for _, failure := range []bool{false, true} {
		for _, option := range []string{"order", "where"} {
			for _, column := range []string{"visitor_id", "VISITOR_ID"} {
				t.Run(fmt.Sprintf("failure=%t/%s/%s", failure, option, column), func(t *testing.T) {
					s, c, _ := readFixture(t, `CREATE TABLE entries(body TEXT, `+column+` TEXT);`, readSet("true"))
					execReadFixture(t, s, `INSERT INTO entries VALUES('one','secret-a'),('two','secret-b');`)
					c.OwnerOnSite, c.VisitorID, c.ActiveChecked = true, c.OwnerID, true
					provider := &readBindings{}
					if failure {
						provider.ownerErr = errors.New("owner unavailable")
					}
					s.deps.Bindings = provider
					q := url.Values{"limit": {"1"}}
					if option == "order" {
						q.Set("order", column)
					} else {
						q.Set("where."+column, "secret-a")
					}
					prepared := 0
					s.sqliteHook = func(stage string) {
						if stage == "after_prepare" {
							prepared++
						}
					}
					out, err := s.TableList(context.Background(), TableInput{Call: c, Table: "entries", Query: q})
					if readCode(out, err) != "invalid_rows" || out.Body != nil || prepared != 0 {
						t.Fatalf("owner query survived refresh: %+v, %v, prepares=%d", out, err, prepared)
					}
					// Ordinary reads still succeed, with author IDs redacted.
					out = readList(t, s, c, "entries", url.Values{"limit": {"1"}})
					if out.Body["rows"].([][]any)[0][1] != nil {
						t.Fatal(out.Body)
					}
				})
			}
		}
	}
}

func TestRulesOwnerSchemaRequiredColumnsSurvive(t *testing.T) {
	for _, removedBinding := range []bool{false, true} {
		t.Run(fmt.Sprint(removedBinding), func(t *testing.T) {
			s, c, in := rulesFixture(t, `CREATE TABLE entries(body TEXT);`)
			s.deps.PasscodeAllows = func() bool { return true }
			s.deps.ViewersAllow = func() bool { return true }
			read, add := "true", "auth.signed_in"
			if removedBinding {
				s.deps.Bindings = &readBindings{}
				read, add = `event.member == "yes"`, `event.member == "yes"`
			}
			in.Rules = saveSet(ruleengine.Policy{Command: ruleengine.CmdRead, Using: read}, ruleengine.Policy{Command: ruleengine.CmdRead, Using: `approved_at is not null || auth.role == "owner"`, Restrictive: true}, ruleengine.Policy{Command: ruleengine.CmdAdd, Check: add})
			c.Resource = putRules(t, s, in)
			execReadFixture(t, s, `INSERT INTO entries(body,visitor_id,approved_at) VALUES('held','author',NULL);`)
			if removedBinding {
				s.deps.Bindings = nil
			}
			schema := func(sql string) (LegacySQLResult, error) {
				return s.LegacySQL(context.Background(), LegacySQLInput{Call: c, Mode: "schema", ReadBody: func() ([]byte, error) { return json.Marshal(map[string]string{"sql": sql}) }})
			}
			result, err := schema(`CREATE TABLE more(body TEXT)`)
			if err != nil {
				t.Fatal(err)
			}
			if removedBinding && !strings.Contains(strings.Join(result.Notes, " "), "rules_invalid") {
				t.Fatal(result.Notes)
			}
			if err := s.Inspect(context.Background(), c, false, func(i *Inspection) error {
				cols, _, err := i.TableInfo("more")
				if cols["visitor_id"] != "TEXT" || cols["approved_at"] != "TEXT" {
					t.Fatal(cols)
				}
				return err
			}); err != nil {
				t.Fatal(err)
			}
			// Approval removal/rename must roll back, including held values.
			for _, sql := range []string{`ALTER TABLE entries DROP COLUMN approved_at`, `ALTER TABLE entries RENAME COLUMN approved_at TO old_approval`, `ALTER TABLE entries RENAME COLUMN visitor_id TO old_author`} {
				if _, err := schema(sql); err == nil {
					t.Fatalf("schema change passed: %s", sql)
				}
			}
			// Drop the index separately so SQLite itself does not mask the guard.
			if _, err := schema(`DROP INDEX sh_visitor_entries`); err != nil {
				t.Fatal(err)
			}
			// Safeguards recreate that index; use the fixture connection to remove it.
			execReadFixture(t, s, `DROP INDEX sh_visitor_entries`)
			if _, err := schema(`ALTER TABLE entries DROP COLUMN visitor_id`); err == nil {
				t.Fatal("author removal passed")
			}
			if removedBinding {
				c.Owner = false
				readGet(t, s, c, "entries", 1, "rules_invalid")
				// Restoring the provider exposes whether DDL approved the held row.
				s.deps.Bindings = &readBindings{values: func(context.Context, int) (ruleengine.Values, error) {
					return ruleengine.Values{"event.member": ruleengine.Text("yes")}, nil
				}}
				c.VisitorID, c.ActiveChecked = "author", true
			}
			c.Owner = false
			readGet(t, s, c, "entries", 1, "row_not_found")
		})
	}
}
