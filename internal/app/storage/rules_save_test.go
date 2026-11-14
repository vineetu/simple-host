package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	sqlite3 "github.com/ncruces/go-sqlite3"
	ruleengine "github.com/vsriram/simple-host/host/storage/rules"
	repo "github.com/vsriram/simple-host/internal/repository/storage"
	rules "github.com/vsriram/simple-host/internal/rules/storage"
	runtime "github.com/vsriram/simple-host/internal/storage"
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
	s, c := kvFixture(t, "sqlite", false)
	s.deps.RulesEnabled = true
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
func putRules(t *testing.T, s *Service, in ResourceInput) rules.Resource {
	t.Helper()
	result, err := s.PutStorageResource(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := s.LoadResource(context.Background(), ResourceReadInput{SiteID: in.Call.SiteID, Name: in.Name})
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
	in.Rules = rawRules("null")
	in.AccessRequest = rules.AccessRequest{Preset: "public"}
	presets := map[string]ResourceTableInput{"notes": {AccessRequest: rules.AccessRequest{Preset: "private"}}}
	in.Tables = &presets
	putRules(t, s, in)
	in.AccessRequest = rules.AccessRequest{}
	in.Rules = readSet("true")
	in.Tables = nil
	ruleRefusal(t, s, in, "shape")
	empty := map[string]ResourceTableInput{}
	in.Tables = &empty
	x := putRules(t, s, in)
	if x.RulesStatus() != "rules" || len(x.Tables) != 0 {
		t.Fatalf("%+v", x)
	}
	// Fill denies visitors through the old preset path, including table overrides.
	deny := rules.AccessMatrix{Read: "nobody", Add: "nobody", Edit: "nobody", Delete: "nobody"}
	if x.Matrix() != deny || x.Read != "owner" || x.Write != "owner" || x.WriteMode != "add" {
		t.Fatalf("fill: %+v", x)
	}
	tableSets := map[string]ResourceTableInput{"Notes": {Rules: saveSet()}}
	in.Tables = &tableSets
	x = putRules(t, s, in)
	if x.Tables["notes"].Matrix != deny {
		t.Fatal("table fill")
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
	// Table preset fields also select a return to presets when the default
	// preset is omitted; the usual default remains private.
	in.Rules = nil
	in.Tables = &presets
	x = putRules(t, s, in)
	if x.RulesStatus() != "preset" || len(x.Tables) != 1 {
		t.Fatal("table-only preset save was ignored")
	}
	in.Rules = saveSet()
	in.Tables = &tableSets
	putRules(t, s, in)
	in.Tables = nil
	// Null and a passcode-only save keep rules and overrides even with the flag off.
	s.deps.RulesEnabled = false
	in.Rules = rawRules("null")
	in.SitePasscode = "off"
	x = putRules(t, s, in)
	if x.RulesStatus() != "rules" || len(x.Tables) != 1 || x.SitePasscode != "off" {
		t.Fatalf("passcode: %+v", x)
	}
	in.Rules = readSet("true")
	_, err := s.PutStorageResource(ctx, in)
	if e, ok := err.(*Error); !ok || e.Code != "rules_unavailable" {
		t.Fatalf("flag off: %v", err)
	}
	s.deps.RulesEnabled = true
	// The conversion record survives both manual directions.
	history := `{"preset":"public"}`
	if _, err := s.db.Exec(`UPDATE site_storage_resources SET rules_converted_from=$3 WHERE site_id=$1 AND name=$2`, c.SiteID, in.Name, history); err != nil {
		t.Fatal(err)
	}
	in.Rules = nil
	in.AccessRequest = rules.AccessRequest{Preset: "private"}
	_, err = s.PutStorageResource(ctx, in)
	if e, ok := err.(*Error); !ok || e.Code != "tables_present" {
		t.Fatalf("rules to preset: %v", err)
	}
	in.Tables = &empty
	x = putRules(t, s, in)
	if x.Rules != nil || len(x.Tables) != 0 || x.RulesStatus() != "preset" || x.ConvertedFrom == nil {
		t.Fatalf("preset again: %+v", x)
	}
	in.AccessRequest = rules.AccessRequest{}
	in.Rules = readSet("true")
	x = putRules(t, s, in)
	if x.ConvertedFrom == nil || x.RulesStatus() != "rules" {
		t.Fatalf("rules again: %+v", x)
	}
	// The pre-rules statement overwrites only the fill; a new reader marks it invalid.
	if _, err := s.db.Exec(`INSERT INTO site_storage_resources(site_id,name,kind,read_policy,write_policy,site_passcode,write_mode,acc_read,acc_add,acc_edit,acc_delete,preset_base,approve_first)
 VALUES($1,$2,'sqlite','anyone','owner','inherit','add','anyone','owner','owner','owner','public',false)
 ON CONFLICT(site_id,name) DO UPDATE SET read_policy=EXCLUDED.read_policy,write_policy=EXCLUDED.write_policy,site_passcode=EXCLUDED.site_passcode,write_mode=EXCLUDED.write_mode,acc_read=EXCLUDED.acc_read,acc_add=EXCLUDED.acc_add,acc_edit=EXCLUDED.acc_edit,acc_delete=EXCLUDED.acc_delete,preset_base=EXCLUDED.preset_base,approve_first=EXCLUDED.approve_first`, c.SiteID, in.Name); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`DELETE FROM site_storage_tables WHERE site_id=$1 AND resource_name=$2`, c.SiteID, in.Name); err != nil {
		t.Fatal(err)
	}
	x, err = repo.LoadResource(ctx, s.db, c.SiteID, in.Name)
	if err != nil || x.RulesStatus() != "invalid" {
		t.Fatalf("old save: %+v %v", x, err)
	}
}

func TestRulesSaveShapes(t *testing.T) {
	s, _, in := rulesFixture(t, "")
	for _, field := range []string{"preset", "read", "add", "edit", "delete", "write", "write_mode", "approve_first"} {
		t.Run(field, func(t *testing.T) {
			q := in
			switch field {
			case "preset":
				q.Preset = "public"
			case "read":
				q.Read = "anyone"
			case "add":
				q.Add = "signed-in"
			case "edit":
				q.Edit = "owner"
			case "delete":
				q.Delete = "owner"
			case "write":
				q.Write = "owner"
			case "write_mode":
				q.WriteMode = "add"
			case "approve_first":
				b := false
				q.ApproveFirst = &b
			}
			ruleRefusal(t, s, q, "shape")
		})
	}
	for _, q := range []ResourceTableInput{{}, {Rules: rawRules("null")}, {Rules: saveSet(), Write: "owner"}, {Rules: saveSet(), WriteMode: "add"}, {Rules: saveSet(), PresetFieldsPresent: true}, {AccessRequest: rules.AccessRequest{Preset: "private"}, Rules: saveSet()}} {
		tables := map[string]ResourceTableInput{"notes": q}
		in.Tables = &tables
		ruleRefusal(t, s, in, "shape")
	}
	in.Tables = nil
	in.Kind = "files"
	ruleRefusal(t, s, in, "shape")
	in.Kind = "sqlite"
	in.Rules = saveSet(ruleengine.Policy{Name: "checked", Command: ruleengine.CmdAdd, Check: "auth.checked"})
	ruleRefusal(t, s, in, "binding")
	s.deps.SpamCheckAvailable = true
	putRules(t, s, in)
}

func TestRulesSaveKVStoreAndBindings(t *testing.T) {
	for _, store := range []string{"postgres", "sqlite"} {
		t.Run(store, func(t *testing.T) {
			s, c := kvFixture(t, store, false)
			s.deps.RulesEnabled = true
			in := ResourceInput{Call: c, Name: "data", Kind: "kv", Rules: readSet(`visitor_id == auth.id`)}
			if store == "postgres" {
				_, err := s.PutStorageResource(context.Background(), in)
				if e, ok := err.(*Error); !ok || e.Code != "rules_unavailable" || !strings.Contains(e.Message, "has not been moved yet") {
					t.Fatal(err)
				}
				return
			}
			x := putRules(t, s, in)
			if x.RulesStatus() != "rules" {
				t.Fatal(x.RulesStatus())
			}
			in.Rules = readSet(`approved_at is not null`)
			ruleRefusal(t, s, in, "column")
		})
	}
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
	for _, column := range []string{"visitor_id", "approved_at"} {
		for _, generation := range []string{"VIRTUAL", "STORED"} {
			for _, typ := range []string{"INTEGER", "TEXT"} {
				for _, referenced := range []bool{false, true} {
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
						if e.RuleError.Table != "notes" || e.RuleError.Position != -1 || e.RuleError.Message != column+" must be TEXT" {
							t.Fatalf("column diagnostic: %+v", e.RuleError)
						}
						after, err := os.ReadFile(s.deps.SQLitePath)
						if err != nil || string(before) != string(after) {
							t.Fatalf("rejected save changed SQLite: %v", err)
						}
						resources, err := s.ListResourceRecords(context.Background(), ResourceListInput{SiteID: c.SiteID})
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

func TestPresetSaveMixedCaseSystemColumnsFlagOff(t *testing.T) {
	for _, tc := range []struct {
		name, schema, preset string
		hold                 bool
	}{
		{"wall author", `CREATE TABLE notes(body TEXT, VISITOR_ID TEXT)`, "wall", false},
		{"board author", `CREATE TABLE notes(body TEXT, Visitor_Id TEXT)`, "board", false},
		{"author index", `CREATE TABLE notes(body TEXT, visitor_id TEXT); CREATE INDEX SH_VISITOR_NOTES ON notes(visitor_id)`, "wall", false},
		{"approval", `CREATE TABLE notes(body TEXT, approved_AT TEXT)`, "comments", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, c, in := rulesFixture(t, tc.schema)
			s.deps.RulesEnabled = false
			s.deps.SpamCheckAvailable = true
			s.deps.PasscodeAllows = func() bool { return true }
			s.deps.ViewersAllow = func() bool { return true }
			in.Rules = nil
			in.AccessRequest = rules.AccessRequest{Preset: "private"}
			putRules(t, s, in)
			in.Preset = tc.preset
			in.ApproveFirst = &tc.hold
			_, err := s.PutStorageResource(context.Background(), in)
			if e, ok := err.(*Error); !ok || e.Code != "visitor_id_required" {
				t.Fatalf("mixed-case preset save: %v", err)
			}
			x, err := s.LoadResource(context.Background(), ResourceReadInput{SiteID: c.SiteID, Name: in.Name})
			if err != nil || x.PresetBase != "private" {
				t.Fatalf("rejected save changed preset: %+v, %v", x, err)
			}
			c.Owner, c.VisitorID, c.Resource = false, "visitor", x
			c.CSRFOK, c.ActiveChecked = true, true
			add := TableInput{Call: c, Table: "notes", Values: map[string]json.RawMessage{"body": json.RawMessage(`"forged"`), "VISITOR_ID": json.RawMessage(`"another-person"`)}}
			add.Authorized = s.AuthorizeTableWrite(context.Background(), add, "add")
			out, err := s.TableAdd(context.Background(), add)
			if err != nil || out.Authorization == nil || out.Authorization.Code != "forbidden" || out.Authorization.Allowed {
				t.Fatalf("forgery attempt: %+v, %v", out, err)
			}
			conn, err := sqlite3.Open(s.deps.SQLitePath)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			stmt, _, err := conn.Prepare(`SELECT count(*) FROM notes`)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			if !stmt.Step() || stmt.ColumnInt64(0) != 0 {
				t.Fatalf("forgery inserted a row: %v", stmt.Err())
			}
			cols, err := runtime.TableColumns(conn, "notes")
			if err != nil || len(cols) != 2 {
				t.Fatalf("rejected save changed columns: %v, %v", cols, err)
			}
		})
	}
}

func TestRulesSaveEnvelopeSizeBoundary(t *testing.T) {
	for _, tables := range []string{"omitted", "empty", "populated"} {
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
	s, c := kvFixture(t, "sqlite", false)
	s.deps.RulesEnabled = true
	kvPut(t, s, c, "old", `1`, "")
	if err := s.withKV(context.Background(), c, true, func(_ context.Context, conn *sqlite3.Conn, _ *Admission) error {
		return conn.Exec(`UPDATE kv SET visitor_id='forged' WHERE key='old'`)
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE site_storage_resources SET acc_read=NULL,acc_add=NULL,acc_edit=NULL,acc_delete=NULL,write_policy='anyone',write_mode='full' WHERE site_id=$1 AND name='data'`, c.SiteID); err != nil {
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
			s.deps.KVStore = "sqlite"
			s.deps.BindingSchema = PlatformSchema(false)
			s.deps.BindingSchema["event.member"] = ruleengine.Binding{Type: ruleengine.TypeBool}
			in.Rules = readSet("event.member")
			if kind == "sqlite" {
				tables := map[string]ResourceTableInput{"future": {Rules: readSet("event.member")}}
				in.Tables = &tables
			}
			before := putRules(t, s, in)
			delete(s.deps.BindingSchema, "event.member")
			s.deps.RulesEnabled = false
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

func assertPasscodeRulesKept(t *testing.T, s *Service, in ResourceInput, before rules.Resource, result ResourceResult) {
	t.Helper()
	before.SitePasscode = "off"
	loaded, err := s.LoadResource(context.Background(), ResourceReadInput{SiteID: in.Call.SiteID, Name: in.Name})
	if err != nil {
		t.Fatal(err)
	}
	if result.Created || result.Resource.Fingerprint() != before.Fingerprint() || loaded.Fingerprint() != before.Fingerprint() {
		t.Fatal("passcode-only save changed retained rules or failed to persist the passcode")
	}
}

func TestRulesPreparationHelpers(t *testing.T) {
	schema := PlatformSchema(true)
	table := ruleengine.Table{Name: "notes", RowKey: "_rowid_", Columns: []ruleengine.Column{{Name: "visitor_id"}, {Name: "approved_at"}}}
	set := ruleengine.PolicySet{Policies: []ruleengine.Policy{{Name: "add", Command: ruleengine.CmdAdd, Check: "auth.checked"}, {Name: "read", Command: ruleengine.CmdRead, Using: "approved_at is not null"}}}
	plan, errs := ruleengine.Compile(set, table, schema, ruleengine.DefaultLimits())
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	plans := rulePlans{"notes": plan}
	if !plans.OwnTables()("Notes") || !plans.Holds("NOTES") || !plans.HasChecked() {
		t.Fatal("rules preparation helpers lost a requirement")
	}
	empty, errs := ruleengine.Compile(ruleengine.PolicySet{Policies: []ruleengine.Policy{}}, table, schema, ruleengine.DefaultLimits())
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	plans["notes"] = empty
	if plans.OwnTables()("notes") || plans.Holds("notes") || plans.HasChecked() {
		t.Fatal("leftover columns must not reactivate tracking or holding")
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
