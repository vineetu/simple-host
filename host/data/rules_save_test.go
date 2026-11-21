package data

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	sqlite3 "github.com/ncruces/go-sqlite3"
	"github.com/vsriram/simple-host/host/data/repo"
	model "github.com/vsriram/simple-host/host/model"
	runtime "github.com/vsriram/simple-host/host/storage"
	ruleengine "github.com/vsriram/simple-host/host/storage/rules"
)

func rawRules(raw string) *json.RawMessage { x := json.RawMessage(raw); return &x }
func saveSet(policies ...ruleengine.Policy) *json.RawMessage {
	raw := json.RawMessage(ruleengine.EncodeSet(ruleengine.PolicySet{Policies: append([]ruleengine.Policy{}, policies...)}))
	return &raw
}
func readSet(using string) *json.RawMessage {
	return saveSet(ruleengine.Policy{Name: "visible", Command: ruleengine.CmdRead, Using: using})
}
func rulesFixture(t *testing.T, schema string) (*Service, Call, ResourceInput) {
	t.Helper()
	s, c := kvFixture(t, false)
	c.ResourceName = "rows"
	s.deps.SQLitePath = filepath.Join(c.RuntimeDir, "sqlite", "rows.sqlite")
	if schema != "" {
		if err := os.MkdirAll(filepath.Dir(s.deps.SQLitePath), 0700); err != nil {
			t.Fatal(err)
		}
		conn, err := sqlite3.Open(s.deps.SQLitePath)
		if err != nil {
			t.Fatal(err)
		}
		if err := conn.Exec(schema); err != nil {
			t.Fatal(err)
		}
		if err := conn.Close(); err != nil {
			t.Fatal(err)
		}
	}
	return s, c, ResourceInput{Call: c, Name: "rows", Kind: "sqlite", Rules: readSet("true")}
}
func putRules(t *testing.T, s *Service, in ResourceInput) model.Resource {
	t.Helper()
	result, err := s.PutStorageResource(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := s.LoadResource(context.Background(), in.Call.SiteID, in.Name)
	if err != nil {
		t.Fatal(err)
	}
	// PostgreSQL canonicalizes JSON formatting without changing the set.
	canonical := func(raw json.RawMessage) string {
		if raw == nil {
			return "NULL"
		}
		set, errs := ruleengine.DecodeSet(raw)
		if len(errs) != 0 {
			t.Fatal(errs)
		}
		return string(ruleengine.EncodeSet(set))
	}
	if canonical(result.Resource.Rules) != canonical(loaded.Rules) {
		t.Fatal("default rules changed")
	}
	result.Resource.Rules = loaded.Rules
	for name, q := range result.Resource.Tables {
		if canonical(q.Rules) != canonical(loaded.Tables[name].Rules) {
			t.Fatal("table rules changed")
		}
		q.Rules = loaded.Tables[name].Rules
		result.Resource.Tables[name] = q
	}
	if result.Resource.Fingerprint() != loaded.Fingerprint() {
		t.Fatal("save/load differ")
	}

	return loaded
}
func ruleRefusal(t *testing.T, s *Service, in ResourceInput, clause string) *Error {
	t.Helper()
	_, err := s.PutStorageResource(context.Background(), in)
	e, ok := err.(*Error)
	if !ok || e.Code != "invalid_rules" || e.RuleError == nil || e.RuleError.Clause != clause || len(e.RulesErrors) == 0 {
		t.Fatalf("want invalid_rules/%s, got %#v", clause, err)
	}
	if e.RuleError.Message == "" || *e.RuleError != e.RulesErrors[0] {
		t.Fatalf("lost rule_error: %+v", e)
	}
	return e
}

func TestRulesSaveInvalidClauses(t *testing.T) {
	cases := []struct{ name, raw, clause string }{
		{"json", `{"policies":null}`, "json"},
		{"version", `{"version":2,"policies":[]}`, "version"},
		{"name", `{"policies":[{"name":"p","command":"read","using":"true"},{"name":"p","command":"read","using":"true"}]}`, "name"},
		{"command", `{"policies":[{"name":"p","command":"bogus","using":"true"}]}`, "command"},
		{"using", `{"policies":[{"name":"p","command":"read"}]}`, "using"},
		{"check", `{"policies":[{"name":"p","command":"add"}]}`, "check"},
		{"binding", `{"policies":[{"name":"p","command":"read","using":"event.member"}]}`, "binding"},
		{"type", `{"policies":[{"name":"p","command":"read","using":"auth.signed_in == 42"}]}`, "type"},
		{"anonymous_change", `{"policies":[{"name":"p","command":"edit","using":"true"}]}`, "anonymous_change"},
		{"column", `{"policies":[{"name":"p","command":"read","using":"absent == auth.id"}]}`, "column"},
	}
	s, _, in := rulesFixture(t, `CREATE TABLE notes(body TEXT)`)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in.Rules = rawRules(tc.raw)
			e := ruleRefusal(t, s, in, tc.clause)
			if tc.clause == "column" && (e.RuleError.Table != "notes" || e.RuleError.Policy != "p" || e.RuleError.Position != 0) {
				t.Fatalf("fields: %+v", e.RuleError)
			}
		})
	}
	in.Rules = readSet(strings.Repeat(" ", 2049) + "true")
	ruleRefusal(t, s, in, "size")
	// The compiler's aggregate predicate limit is separate from each expression.
	policies := []ruleengine.Policy{}
	for i := 0; i < 32; i++ {
		policies = append(policies, ruleengine.Policy{Name: fmt.Sprint(i), Command: ruleengine.CmdRead, Using: `body in auth.teams`})
	}
	in.Rules = saveSet(policies...)
	ruleRefusal(t, s, in, "composed")
}

func TestRulesSaveRequestSemantics(t *testing.T) {
	s, c, in := rulesFixture(t, "")
	ctx := context.Background()
	empty := map[string]ResourceTableInput{}
	in.Tables = &empty
	x := putRules(t, s, in)
	if x.RulesStatus() != "rules" || len(x.Tables) != 0 {
		t.Fatalf("%+v", x)
	}
	tableSets := map[string]ResourceTableInput{"Notes": {Rules: saveSet()}}
	in.Tables = &tableSets
	x = putRules(t, s, in)
	// A rules save marks the resource as saved after presets.
	var saved int
	if err := s.db.QueryRow(`SELECT count(*) FROM site_storage_resources r JOIN site_storage_tables t ON t.site_id=r.site_id AND t.resource_name=r.name
 WHERE r.site_id=$1 AND r.name=$2 AND r.acc_read='nobody' AND t.table_name='notes'`, c.SiteID, in.Name).Scan(&saved); err != nil || saved != 1 {
		t.Fatalf("saved: %d %v", saved, err)
	}
	in.Tables = nil
	in.Rules = saveSet()
	x = putRules(t, s, in)
	if len(x.Tables) != 1 {
		t.Fatal("omitted tables lost")
	}
	set, _ := ruleengine.DecodeSet(x.Rules)
	if len(set.Policies) != 0 {
		t.Fatal("explicit empty set lost")
	}
	// Null and a passcode-only save keep rules and overrides.
	in.Rules = rawRules("null")
	in.SitePasscode = "off"
	x = putRules(t, s, in)
	if x.RulesStatus() != "rules" || len(x.Tables) != 1 || x.SitePasscode != "off" {
		t.Fatalf("passcode: %+v", x)
	}
	var err error
	// The older columns never decide access; the status reads the rules alone.
	if _, err := s.db.Exec(`UPDATE site_storage_resources SET read_policy='anyone',write_policy='anyone',acc_read='anyone' WHERE site_id=$1 AND name=$2`, c.SiteID, in.Name); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`DELETE FROM site_storage_tables WHERE site_id=$1 AND resource_name=$2`, c.SiteID, in.Name); err != nil {
		t.Fatal(err)
	}
	x, err = repo.LoadResource(ctx, s.db, c.SiteID, in.Name)
	if err != nil || x.RulesStatus() != "rules" || x.Rules == nil {
		t.Fatalf("old save: %+v %v", x, err)
	}
}

func TestRulesSaveShapes(t *testing.T) {
	s, _, in := rulesFixture(t, "")
	// A field of the removed presets is refused on every kind, before any
	// rules are read.
	for _, kind := range []string{"kv", "sqlite", "files"} {
		q := in
		q.Kind, q.Name, q.PresetFieldsPresent = kind, "preset-"+kind, true
		if _, err := s.PutStorageResource(context.Background(), q); readCode(TableResult{}, err) != "invalid_resource" {
			t.Fatalf("%s: want invalid_resource, got %v", kind, err)
		}
	}
	for _, q := range []ResourceTableInput{{}, {Rules: rawRules("null")}} {
		tables := map[string]ResourceTableInput{"notes": q}
		in.Tables = &tables
		ruleRefusal(t, s, in, "shape")
	}
	tables := map[string]ResourceTableInput{"notes": {Rules: saveSet(), PresetFieldsPresent: true}}
	in.Tables = &tables
	if _, err := s.PutStorageResource(context.Background(), in); readCode(TableResult{}, err) != "invalid_resource" {
		t.Fatalf("table preset fields: want invalid_resource, got %v", err)
	}
	in.Tables = nil
	files := in
	files.Kind, files.Name, files.Rules = "files", "uploads", readSet("value == 1")
	ruleRefusal(t, s, files, "column") // files(path, visitor_id) has no value
	in.Rules = saveSet(ruleengine.Policy{Name: "checked", Command: ruleengine.CmdAdd, Check: "auth.checked"})
	ruleRefusal(t, s, in, "binding")
	s.deps.SpamCheckAvailable = true
	putRules(t, s, in)
	files.Rules = in.Rules
	ruleRefusal(t, s, files, "binding") // uploads are never spam-checked
	files.Rules = readSet("visitor_id == auth.id && path != ''")
	putRules(t, s, files)
}

func TestRulesSaveKVAndBindings(t *testing.T) {
	s, c := kvFixture(t, false)
	in := ResourceInput{Call: c, Name: "data", Kind: "kv", Rules: readSet(`visitor_id == auth.id`)}
	x := putRules(t, s, in)
	if x.RulesStatus() != "rules" {
		t.Fatal(x.RulesStatus())
	}
	in.Rules = readSet(`approved_at is not null`)
	ruleRefusal(t, s, in, "column")
}

func TestRulesSavePreparationBeforeMutation(t *testing.T) {
	s, _, in := rulesFixture(t, `CREATE TABLE notes(body TEXT); INSERT INTO notes VALUES ('old')`)
	in.Rules = saveSet(ruleengine.Policy{Name: "add", Command: ruleengine.CmdAdd, Check: "auth.signed_in"}, ruleengine.Policy{Name: "read", Command: ruleengine.CmdRead, Using: `visitor_id == auth.id || approved_at is not null`})
	putRules(t, s, in)
	conn, err := sqlite3.Open(s.deps.SQLitePath)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	cols, err := runtime.TableColumns(conn, "notes")
	if err != nil || cols["visitor_id"] != "TEXT" || cols["approved_at"] != "TEXT" {
		t.Fatalf("columns: %v %v", cols, err)
	}
	stmt, _, err := conn.Prepare(`SELECT approved_at FROM notes`)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	if !stmt.Step() || stmt.ColumnType(0) == sqlite3.NULL {
		t.Fatal("old rows must remain approved")
	}
	stmt.Close()
	for _, column := range []string{"visitor_id", "approved_at"} {
		t.Run(column, func(t *testing.T) {
			s, _, in := rulesFixture(t, `CREATE TABLE notes(body TEXT,`+column+` INTEGER)`)
			ruleRefusal(t, s, in, "column")
		})
	}
	t.Run("all tables before adding columns", func(t *testing.T) {
		s, _, in := rulesFixture(t, `CREATE TABLE a(body TEXT); CREATE TABLE z(other TEXT)`)
		in.Rules = readSet(`body == auth.id || visitor_id == auth.id`)
		ruleRefusal(t, s, in, "column")
		assertNoAuthorColumn(t, s, "a")
	})
	t.Run("statement size before adding columns", func(t *testing.T) {
		defs := []string{}
		for i := 0; i < 100; i++ {
			defs = append(defs, runtime.Quote(fmt.Sprintf("c%d_", i)+strings.Repeat("x", 400))+" TEXT")
		}
		s, _, in := rulesFixture(t, `CREATE TABLE wide (`+strings.Join(defs, ",")+`)`)
		in.Rules = saveSet(ruleengine.Policy{Name: "add", Command: ruleengine.CmdAdd, Check: "auth.signed_in"})
		ruleRefusal(t, s, in, "composed")
		assertNoAuthorColumn(t, s, "wide")
	})
}

func TestRulesSaveGeneratedSystemColumns(t *testing.T) {
	for _, tc := range []struct {
		column, generation, typ string
		referenced              bool
	}{
		{"visitor_id", "VIRTUAL", "INTEGER", false},
		{"approved_at", "STORED", "INTEGER", true},
		{"visitor_id", "STORED", "TEXT", true},
		{"approved_at", "VIRTUAL", "TEXT", false},
	} {
		column, generation, typ, referenced := tc.column, tc.generation, tc.typ, tc.referenced
		{
			{
				{
					t.Run(fmt.Sprintf("%s/%s/%s/referenced=%t", column, generation, typ, referenced), func(t *testing.T) {
						s, c, in := rulesFixture(t, `CREATE TABLE notes(body TEXT, `+column+` `+typ+` GENERATED ALWAYS AS (1) `+generation+`); INSERT INTO notes(body) VALUES ('old')`)
						if referenced {
							in.Rules = readSet(column + ` is not null`)
						}
						if typ == "TEXT" {
							// Preparation must recognize the existing generated column,
							// including when a rule makes it author-tracked or held.
							putRules(t, s, in)
							putRules(t, s, in)
							return
						}
						before, err := os.ReadFile(s.deps.SQLitePath)
						if err != nil {
							t.Fatal(err)
						}
						e := ruleRefusal(t, s, in, "column")
						if e.RuleError.Table != "notes" || e.RuleError.Position != -1 {
							t.Fatalf("column diagnostic: %+v", e.RuleError)
						}
						after, err := os.ReadFile(s.deps.SQLitePath)
						if err != nil || string(before) != string(after) {
							t.Fatalf("rejected save changed SQLite: %v", err)
						}
						resources, err := s.ListResourceRecords(context.Background(), c.SiteID)
						if err != nil {
							t.Fatal(err)
						}
						for _, resource := range resources {
							if resource.Name == in.Name {
								t.Fatalf("rejected save persisted resource: %+v", resource)
							}
						}
					})
				}
			}
		}
	}
}

func TestRulesSaveWideTablePredicates(t *testing.T) {
	for _, tc := range []struct {
		name  string
		width int
		lists bool
	}{
		{"small predicates", 174, false},
		{"maximal bindings fit statement limit", 174, true},
		{"maximal bindings exceed statement limit", 316, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			defs := []string{"body TEXT"}
			for i := 0; i < 100; i++ {
				name := fmt.Sprintf("c%03d_", i) + strings.Repeat("x", tc.width-5)
				defs = append(defs, runtime.Quote(name)+" TEXT")
			}
			s, c, in := rulesFixture(t, `CREATE TABLE wide (`+strings.Join(defs, ",")+`)`)
			if !tc.lists {
				putRules(t, s, in)
				return
			}
			s.deps.BindingSchema, _ = s.rulesSchema()
			s.deps.BindingSchema["auth.teams"] = ruleengine.Binding{Type: ruleengine.TypeTextList}
			in.Rules = saveSet(
				ruleengine.Policy{Name: "read", Command: ruleengine.CmdRead, Using: `auth.role == "owner" || body in auth.teams`},
				ruleengine.Policy{Name: "edit", Command: ruleengine.CmdEdit, Using: `auth.signed_in && body in auth.teams`},
				ruleengine.Policy{Name: "add", Command: ruleengine.CmdAdd, Check: `auth.signed_in`},
			)
			if tc.width == 174 {
				putRules(t, s, in)
				return
			}
			ruleRefusal(t, s, in, "composed")
			assertNoAuthorColumn(t, s, "wide")
			var count int
			if err := s.db.QueryRow(`SELECT count(*) FROM site_storage_resources WHERE site_id=$1 AND name=$2`, c.SiteID, in.Name).Scan(&count); err != nil || count != 0 {
				t.Fatalf("failed save mutated Postgres: count %d, %v", count, err)
			}
		})
	}
}

func TestRulesSaveExistingMixedCaseSystemColumns(t *testing.T) {
	for _, tc := range []struct {
		name, schema, column, using, value string
	}{
		{"approval", `CREATE TABLE notes(body TEXT, APPROVED_AT TEXT); INSERT INTO notes VALUES ('old', 'original')`, "APPROVED_AT", `approved_at is not null`, "original"},
		{"author adds index", `CREATE TABLE notes(body TEXT, VISITOR_ID TEXT); INSERT INTO notes VALUES ('old', 'author')`, "VISITOR_ID", `visitor_id == auth.id`, "author"},
		{"author existing index", `CREATE TABLE notes(body TEXT, Visitor_Id TEXT); CREATE INDEX SH_VISITOR_NOTES ON notes(Visitor_Id); INSERT INTO notes VALUES ('old', 'author')`, "Visitor_Id", `visitor_id == auth.id`, "author"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _, in := rulesFixture(t, tc.schema)
			in.Rules = readSet(tc.using)
			putRules(t, s, in)
			// Re-saving also verifies the index created during the first save.
			putRules(t, s, in)
			conn, err := sqlite3.Open(s.deps.SQLitePath)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			cols, err := runtime.TableColumns(conn, "notes")
			if err != nil || cols[tc.column] != "TEXT" || len(cols) != 2 {
				t.Fatalf("schema spelling changed: %v, %v", cols, err)
			}
			stmt, _, err := conn.Prepare(`SELECT ` + runtime.Quote(tc.column) + ` FROM notes`)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			if !stmt.Step() || stmt.ColumnText(0) != tc.value {
				t.Fatalf("existing value changed: %v", stmt.Err())
			}
			if strings.EqualFold(tc.column, "visitor_id") {
				index, _, err := conn.Prepare(`PRAGMA index_info(sh_visitor_notes)`)
				if err != nil {
					t.Fatal(err)
				}
				defer index.Close()
				if !index.Step() || index.ColumnText(2) != tc.column {
					t.Fatalf("author index lost schema spelling: %v", index.Err())
				}
			}
		})
	}
}

func TestRulesSaveEnvelopeSizeBoundary(t *testing.T) {
	for _, tables := range []string{"populated"} {
		for _, over := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/over=%t", tables, over), func(t *testing.T) {
				s, _, in := rulesFixture(t, "")
				// Construct the envelope independently, preserving raw JSON whitespace.
				suffix := "}"
				entries := map[string]ResourceTableInput{}
				switch tables {
				case "empty":
					in.Tables = &entries
					suffix = `,"tables":{}}`
				case "populated":
					entries["a"] = ResourceTableInput{Rules: saveSet()}
					entries["b"] = ResourceTableInput{Rules: readSet("true")}
					in.Tables = &entries
					suffix = `,"tables":{"a":{"rules":` + string(*entries["a"].Rules) + `},"b":{"rules":` + string(*entries["b"].Rules) + `}}}`
				}
				limit := rulesSaveBytes
				if over {
					limit++
				}
				padding := limit - len(`{"rules":`) - len(*in.Rules) - len(suffix)
				in.Rules = rawRules(strings.Repeat(" ", padding) + string(*in.Rules))
				envelope := `{"rules":` + string(*in.Rules) + suffix
				if len(envelope) != limit || !json.Valid([]byte(envelope)) {
					t.Fatal("invalid boundary fixture")
				}
				if over {
					ruleRefusal(t, s, in, "size")
				} else {
					putRules(t, s, in)
				}
			})
		}
	}
}

func assertNoAuthorColumn(t *testing.T, s *Service, table string) {
	t.Helper()
	conn, err := sqlite3.Open(s.deps.SQLitePath)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	cols, err := runtime.TableColumns(conn, table)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := cols["visitor_id"]; ok {
		t.Fatal("validation failure changed SQLite")
	}
}

func TestRulesSaveServiceSizeBound(t *testing.T) {
	s, _, in := rulesFixture(t, "")
	tables := map[string]ResourceTableInput{}
	// Find the largest legal composed predicate in this family. Each clause
	// also reaches the expression byte bound, without exceeding its node/depth
	// limits; literals stay bound parameters in the composed predicate.
	var maximal *json.RawMessage
	table := ruleengine.Table{Name: "probe", RowKey: "_rowid_", Columns: []ruleengine.Column{{Name: "body"}}}
	var balanced func([]string) string
	balanced = func(terms []string) string {
		if len(terms) == 1 {
			return terms[0]
		}
		mid := len(terms) / 2
		return "(" + balanced(terms[:mid]) + " || " + balanced(terms[mid:]) + ")"
	}
	for count := 1; count <= 64; count++ {
		terms := make([]string, count)
		for i := range terms {
			terms[i] = `body == "x"`
		}
		clause := balanced(terms)
		shortClause := clause
		terms[0] = `body == "` + strings.Repeat("x", 1+2048-len(clause)) + `"`
		clause = balanced(terms)
		raw := saveSet(ruleengine.Policy{Name: "a", Command: ruleengine.CmdRead, Using: clause}, ruleengine.Policy{Name: "b", Command: ruleengine.CmdRead, Using: shortClause}, ruleengine.Policy{Name: "c", Command: ruleengine.CmdRead, Using: shortClause})
		set, _ := ruleengine.DecodeSet(*raw)
		_, errs := ruleengine.Compile(set, table, PlatformSchema(false), ruleengine.DefaultLimits())
		if len(errs) != 0 {
			if errs[0].Clause != "composed" {
				t.Fatal(errs)
			}
			break
		}
		maximal = raw
	}
	if maximal == nil {
		t.Fatal("no legal predicate")
	}
	for i := 0; i < 50; i++ {
		tables[fmt.Sprintf("t%d", i)] = ResourceTableInput{Rules: maximal}
	}

	in.Tables = &tables
	x := putRules(t, s, in)
	if len(x.Tables) != 50 {
		t.Fatalf("lost sets: %d", len(x.Tables))
	}
	in.Rules = rawRules(strings.Repeat(" ", 257<<10) + `{"policies":[]}`)
	ruleRefusal(t, s, in, "size")
}

func TestRulesSaveLegacyProvenance(t *testing.T) {
	for _, tc := range []struct {
		authorRule bool
		column     string
	}{{false, "visitor_id"}, {true, "visitor_id"}, {false, "VISITOR_ID"}, {true, "VISITOR_ID"}} {
		t.Run(fmt.Sprint(tc.authorRule)+"/"+tc.column, func(t *testing.T) {
			s, c, in := rulesFixture(t, `CREATE TABLE notes(body TEXT,`+runtime.Quote(tc.column)+` TEXT); INSERT INTO notes VALUES ('old','forged')`)
			_, err := s.db.Exec(`INSERT INTO site_storage_resources(site_id,name,kind,read_policy,write_policy,write_mode,site_passcode) VALUES($1,$2,'sqlite','anyone','anyone','full','inherit')`, c.SiteID, in.Name)
			if err != nil {
				t.Fatal(err)
			}
			if tc.authorRule {
				in.Rules = readSet(`visitor_id == auth.id`)
				_, err = s.PutStorageResource(context.Background(), in)
				if e, ok := err.(*Error); !ok || e.Code != "visitor_id_required" {
					t.Fatalf("legacy author: %v", err)
				}
				return
			}
			putRules(t, s, in)
			conn, err := sqlite3.Open(s.deps.SQLitePath)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			stmt, _, err := conn.Prepare(`SELECT visitor_id FROM notes`)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			if !stmt.Step() || stmt.ColumnType(0) != sqlite3.NULL {
				t.Fatal("forged author survived")
			}
		})
	}
	// Full-mode KV writers are cleared in their own SQLite transaction too.
	s, c := kvFixture(t, false)
	kvPut(t, s, c, "old", `1`, "")
	if err := s.withKV(context.Background(), c, true, func(_ context.Context, conn *sqlite3.Conn, _ *Admission) error {
		return conn.Exec(`UPDATE kv SET visitor_id='forged' WHERE key='old'`)
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE site_storage_resources SET acc_read=NULL,write_policy='anyone',write_mode='full' WHERE site_id=$1 AND name='data'`, c.SiteID); err != nil {
		t.Fatal(err)
	}
	putRules(t, s, ResourceInput{Call: c, Name: "data", Kind: "kv", Rules: saveSet()})
	var stored string
	err := s.withKV(context.Background(), c, false, func(_ context.Context, conn *sqlite3.Conn, _ *Admission) error {
		stmt, _, err := conn.Prepare(`SELECT visitor_id FROM kv WHERE key='old'`)
		if err != nil {
			return err
		}
		defer stmt.Close()
		if stmt.Step() {
			stored = stmt.ColumnText(0)
		}
		return stmt.Err()
	})
	if err != nil || stored != "" {
		t.Fatalf("KV writer: %q %v", stored, err)
	}
}

func TestRulesResourceListBindingErrorsWithoutSQLite(t *testing.T) {
	s, c, in := rulesFixture(t, "")
	s.deps.BindingSchema = PlatformSchema(false)
	s.deps.BindingSchema["event.member"] = ruleengine.Binding{Type: ruleengine.TypeBool}
	in.Rules = readSet("event.member")
	putRules(t, s, in)
	delete(s.deps.BindingSchema, "event.member")
	entries, err := s.ListStorageResources(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Resource.Name == in.Name {
			if len(entry.RulesErrors) != 1 || entry.RulesErrors[0].Clause != "binding" {
				t.Fatalf("binding errors: %+v", entry)
			}
			return
		}
	}
	t.Fatal("resource missing")
}

func TestRulesPasscodeSaveMissingColumns(t *testing.T) {
	for _, tc := range []struct {
		name, using, alter string
		override           bool
	}{
		{"default", `body == auth.id`, `ALTER TABLE notes DROP COLUMN body`, false},
		{"table override", `body == auth.id`, `ALTER TABLE notes DROP COLUMN body`, true},
		{"author", `visitor_id == auth.id`, `DROP INDEX sh_visitor_notes; ALTER TABLE notes DROP COLUMN visitor_id`, false},
		{"approval", `approved_at is not null`, `ALTER TABLE notes DROP COLUMN approved_at`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _, in := rulesFixture(t, `CREATE TABLE notes(body TEXT, kept TEXT); INSERT INTO notes VALUES ('original', 'kept')`)
			if tc.override {
				tables := map[string]ResourceTableInput{"notes": {Rules: readSet(tc.using)}}
				in.Tables = &tables
			} else {
				in.Rules = readSet(tc.using)
			}
			before := putRules(t, s, in)
			conn, err := sqlite3.Open(s.deps.SQLitePath)
			if err != nil {
				t.Fatal(err)
			}
			if err := conn.Exec(tc.alter); err != nil {
				conn.Close()
				t.Fatal(err)
			}
			conn.Close()
			snapshot, err := os.ReadFile(s.deps.SQLitePath)
			if err != nil {
				t.Fatal(err)
			}
			in.Rules, in.Tables, in.SitePasscode = nil, nil, "off"
			result, err := s.PutStorageResource(context.Background(), in)
			if err != nil {
				t.Fatal(err)
			}
			if len(result.RulesErrors) != 1 {
				t.Fatalf("missing column diagnostics: %+v", result.RulesErrors)
			}
			diagnostic := result.RulesErrors[0]
			if diagnostic.Clause != "column" || diagnostic.Table != "notes" || diagnostic.Policy != "visible" || diagnostic.Position != 0 || diagnostic.Message == "" {
				t.Fatalf("column diagnostic fields: %+v", diagnostic)
			}
			assertPasscodeRulesKept(t, s, in, before, result)
			// Checking retained rules must not re-add dropped server columns or
			// otherwise prepare the file. The catalog and rows stay byte-identical.
			after, err := os.ReadFile(s.deps.SQLitePath)
			if err != nil || string(snapshot) != string(after) {
				t.Fatalf("retained rules validation changed SQLite: %v", err)
			}
		})
	}
}

func TestRulesPasscodeSaveMissingBindings(t *testing.T) {
	for _, kind := range []string{"sqlite", "kv"} {
		t.Run(kind, func(t *testing.T) {
			s, _, in := rulesFixture(t, "")
			in.Kind = kind
			s.deps.BindingSchema = PlatformSchema(false)
			s.deps.BindingSchema["event.member"] = ruleengine.Binding{Type: ruleengine.TypeBool}
			in.Rules = readSet("event.member")
			if kind == "sqlite" {
				tables := map[string]ResourceTableInput{"future": {Rules: readSet("event.member")}}
				in.Tables = &tables
			}
			before := putRules(t, s, in)
			delete(s.deps.BindingSchema, "event.member")
			in.Rules, in.Tables, in.SitePasscode = rawRules("null"), nil, "off"
			result, err := s.PutStorageResource(context.Background(), in)
			if err != nil {
				t.Fatal(err)
			}
			if len(result.RulesErrors) != 1+len(before.Tables) {
				t.Fatalf("missing binding diagnostics: %+v", result.RulesErrors)
			}
			for i, diagnostic := range result.RulesErrors {
				wantTable := ""
				if i > 0 {
					wantTable = "future"
				}
				if diagnostic.Clause != "binding" || diagnostic.Table != wantTable || diagnostic.Policy != "visible" || diagnostic.Position != 0 || diagnostic.Message == "" {
					t.Fatalf("binding diagnostic fields: %+v", diagnostic)
				}
			}
			assertPasscodeRulesKept(t, s, in, before, result)
		})
	}
}

func assertPasscodeRulesKept(t *testing.T, s *Service, in ResourceInput, before model.Resource, result ResourceResult) {
	t.Helper()
	before.SitePasscode = "off"
	loaded, err := s.LoadResource(context.Background(), in.Call.SiteID, in.Name)
	if err != nil {
		t.Fatal(err)
	}
	if result.Created || result.Resource.Fingerprint() != before.Fingerprint() || loaded.Fingerprint() != before.Fingerprint() {
		t.Fatal("passcode-only save changed retained rules or failed to persist the passcode")
	}
}

func TestRulesSaveTableReplacementAndPostPreparationFailure(t *testing.T) {
	t.Run("replacement", func(t *testing.T) {
		s, _, in := rulesFixture(t, `CREATE TABLE a(body TEXT); CREATE TABLE b(body TEXT)`)
		overrides := map[string]ResourceTableInput{"a": {Rules: saveSet()}, "b": {Rules: readSet("true")}}
		in.Tables = &overrides
		putRules(t, s, in)
		delete(overrides, "a")
		x := putRules(t, s, in)
		if len(x.Tables) != 1 || x.Tables["b"].Rules == nil {
			t.Fatal("table replacement carried an omitted set")
		}
	})
	t.Run("post preparation must compile", func(t *testing.T) {
		s, c, in := rulesFixture(t, `CREATE TABLE notes(body TEXT)`)
		in.Rules = readSet(`visitor_id == auth.id`)
		inspections := 0
		s.sqliteHook = func(stage string) {
			if stage != "after_open" {
				return
			}
			inspections++
			if inspections != 2 {
				return
			}
			conn, err := sqlite3.Open(s.deps.SQLitePath)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			if err := conn.Exec(`DROP INDEX sh_visitor_notes; ALTER TABLE notes DROP COLUMN visitor_id`); err != nil {
				t.Fatal(err)
			}
		}
		_, err := s.PutStorageResource(context.Background(), in)
		if e, ok := err.(*Error); !ok || e.Code != "internal_error" {
			t.Fatalf("post preparation: %v", err)
		}
		var count int
		if err := s.db.QueryRow(`SELECT count(*) FROM site_storage_resources WHERE site_id=$1 AND name=$2`, c.SiteID, in.Name).Scan(&count); err != nil || count != 0 {
			t.Fatalf("Postgres changed after failed recompile: %d %v", count, err)
		}
	})
}

// Table rules apply only beside resource rules, whatever the transport.
func TestRulesSaveTableOnly(t *testing.T) {
	s, _, in := rulesFixture(t, "")
	in.Rules = nil
	table := map[string]ResourceTableInput{"notes": {Rules: saveSet()}}
	in.Tables = &table
	ruleRefusal(t, s, in, "shape")
}
