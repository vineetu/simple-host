package data

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"testing"
	"time"

	ruleengine "github.com/vsriram/simple-host/host/storage/rules"
)

func writeSet(read, add, using, check string) *json.RawMessage {
	return saveSet(ruleengine.Policy{Command: ruleengine.CmdRead, Using: read}, ruleengine.Policy{Command: ruleengine.CmdAdd, Check: add}, ruleengine.Policy{Command: ruleengine.CmdEdit, Using: using, Check: check}, ruleengine.Policy{Command: ruleengine.CmdDelete, Using: using})
}
func ruleWrite(t *testing.T, s *Service, c Call, action string, id int64, body string) (TableResult, error) {
	t.Helper()
	in := TableInput{Call: c, Table: "entries", ID: id}
	if body != "" {
		if err := json.Unmarshal([]byte(body), &in.Values); err != nil {
			t.Fatal(err)
		}
	}
	in.Authorized = s.AuthorizeTableWrite(context.Background(), in, action)
	if !in.Authorized.Allowed() {
		return TableResult{Authorization: in.Authorized.Authorization}, nil
	}
	switch action {
	case "add":
		return s.TableAdd(context.Background(), in)
	case "edit":
		return s.TableEdit(context.Background(), in)
	default:
		return s.TableRemove(context.Background(), in)
	}
}
func mustRuleWrite(t *testing.T, s *Service, c Call, action string, id int64, body, want string) TableResult {
	t.Helper()
	out, err := ruleWrite(t, s, c, action, id, body)
	if code := readCode(out, err); code != want {
		t.Fatalf("%s: want %s got %s (%v)", action, want, code, err)
	}
	if want == "" && out.Body == nil {
		t.Fatal("successful write returned no result")
	}
	if want != "" && (out.Body != nil || out.Notify != nil) {
		t.Fatal("refused write returned data or notification")
	}
	return out
}
func ruleDBBytes(t *testing.T, s *Service) []byte {
	t.Helper()
	// Production opens databases in WAL mode. Establish that file-format
	// transition before taking either snapshot; it is not a row mutation.
	execReadFixture(t, s, "PRAGMA journal_mode=WAL")
	b, err := os.ReadFile(s.deps.SQLitePath)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestRulesWriteCheckRollback(t *testing.T) {
	s, c, _ := readFixture(t, `CREATE TABLE entries(id INTEGER PRIMARY KEY, body TEXT, status TEXT DEFAULT 'draft', generated TEXT GENERATED ALWAYS AS (body || ':' || status) STORED); INSERT INTO entries(body) VALUES('old');`, writeSet("true", `status == "approved"`, `auth.signed_in`, `generated == "new:approved"`))
	c.VisitorID, c.ActiveChecked = "caller", true
	before := ruleDBBytes(t, s)
	failed, e1 := ruleWrite(t, s, c, "edit", 1, `{"body":"new"}`)
	missing, e2 := ruleWrite(t, s, c, "edit", 99, `{"body":"new"}`)
	if readCode(failed, e1) != "row_not_found" || !reflect.DeepEqual(failed, missing) || !reflect.DeepEqual(e1, e2) {
		t.Fatalf("failed check differs from missing: %+v %v / %+v %v", failed, e1, missing, e2)
	}
	if !bytes.Equal(before, ruleDBBytes(t, s)) {
		t.Fatal("failed edit changed database bytes")
	}
	mustRuleWrite(t, s, c, "add", 0, `{"body":"default"}`, "forbidden")
	if !bytes.Equal(before, ruleDBBytes(t, s)) {
		t.Fatal("failed add retained default/generated effects")
	}
	// The same generated column participates in a successful stored-row check.
	mustRuleWrite(t, s, c, "edit", 1, `{"body":"new","status":"approved"}`, "")
	mustRuleWrite(t, s, c, "add", 0, `{"body":"accepted","status":"approved"}`, "")
}

func TestRulesWriteAuthorizerSideEffects(t *testing.T) {
	for _, action := range []string{"add", "edit", "delete"} {
		for _, target := range []string{"entries", "audit"} {
			t.Run(action+"/"+target, func(t *testing.T) {
				s, c, _ := readFixture(t, `CREATE TABLE entries(id INTEGER PRIMARY KEY, body TEXT); CREATE TABLE audit(body TEXT); INSERT INTO entries VALUES(1,'old');`, writeSet("true", "true", "auth.signed_in", "true"))
				c.VisitorID, c.ActiveChecked = "caller", true
				event := map[string]string{"add": "INSERT", "edit": "UPDATE", "delete": "DELETE"}[action]
				execReadFixture(t, s, fmt.Sprintf(`CREATE TRIGGER side_effect AFTER %s ON entries BEGIN INSERT INTO %s(body) VALUES('effect'); END`, event, target))
				before := ruleDBBytes(t, s)
				mustRuleWrite(t, s, c, action, 1, `{"body":"new"}`, "forbidden")
				if !bytes.Equal(before, ruleDBBytes(t, s)) {
					t.Fatal("trigger refusal changed bytes")
				}
			})
		}
	}
	// The existing authorizer refuses a cascading foreign-key write to another
	// table at preparation, so it belongs to group (a), not admitted effects (b).
	s, c, _ := readFixture(t, `CREATE TABLE entries(id INTEGER PRIMARY KEY, code TEXT UNIQUE, body TEXT); CREATE TABLE child(parent TEXT REFERENCES entries(code) ON UPDATE CASCADE ON DELETE CASCADE); INSERT INTO entries VALUES(1,'old','old'); INSERT INTO child VALUES('old');`, writeSet("true", "true", "auth.signed_in", "false"))
	c.VisitorID, c.ActiveChecked = "caller", true
	before := ruleDBBytes(t, s)
	mustRuleWrite(t, s, c, "edit", 1, `{"code":"new"}`, "forbidden")
	mustRuleWrite(t, s, c, "delete", 1, "", "forbidden")
	if !bytes.Equal(before, ruleDBBytes(t, s)) {
		t.Fatal("foreign-key action changed bytes")
	}
	t.Log("foreign-key CASCADE rejected by row authorizer; defaults and generated columns are admitted and tested separately")
}

func TestRulesWriteConflictAbort(t *testing.T) {
	s, c, _ := readFixture(t, `CREATE TABLE entries(id INTEGER PRIMARY KEY, body TEXT UNIQUE ON CONFLICT REPLACE); INSERT INTO entries VALUES(1,'first'),(2,'second');`, writeSet("true", "true", "auth.signed_in", "true"))
	c.VisitorID, c.ActiveChecked = "caller", true
	before := ruleDBBytes(t, s)
	mustRuleWrite(t, s, c, "add", 0, `{"body":"first"}`, "row_conflict")
	mustRuleWrite(t, s, c, "edit", 2, `{"body":"first"}`, "row_conflict")
	if !bytes.Equal(before, ruleDBBytes(t, s)) {
		t.Fatal("conflict deleted or changed rows")
	}
}

func TestRulesOneEntryPerPerson(t *testing.T) {
	// Rules and a unique index enforce one entry per person even when two
	// tabs both saw an empty list.
	s, c, _ := readFixture(t, `CREATE TABLE entries(id INTEGER PRIMARY KEY, body TEXT);`, saveSet(
		ruleengine.Policy{Name: "own", Command: ruleengine.CmdAll, Using: "visitor_id == auth.id"},
		ruleengine.Policy{Name: "add", Command: ruleengine.CmdAdd, Check: "auth.signed_in"},
		ruleengine.Policy{Name: "owner", Command: ruleengine.CmdAll, Using: `auth.role == "owner"`},
	))
	owner := c
	owner.Owner = true
	_, err := s.LegacySQL(context.Background(), LegacySQLInput{Call: owner, Mode: "schema", ReadBody: func() ([]byte, error) {
		return json.Marshal(map[string]string{"sql": "CREATE UNIQUE INDEX entries_one_per_person ON entries(visitor_id);"})
	}})
	if err != nil {
		t.Fatal(err)
	}
	c.VisitorID, c.ActiveChecked = "first-person", true
	for tab := 0; tab < 2; tab++ {
		if rows := readList(t, s, c, "entries", nil).Body["rows"].([][]any); len(rows) != 0 {
			t.Fatalf("tab %d: expected no existing answer, got %v", tab, rows)
		}
	}
	added := mustRuleWrite(t, s, c, "add", 0, `{"body":"first answer"}`, "")
	id := added.Body["last_insert_id"].(int64)
	for attempt := 0; attempt < 2; attempt++ {
		mustRuleWrite(t, s, c, "add", 0, `{"body":"duplicate"}`, "row_conflict")
		rows := readList(t, s, c, "entries", nil).Body["rows"].([][]any)
		if len(rows) != 1 || rows[0][0] != id || rows[0][1] != "first answer" {
			t.Fatalf("conflict changed or duplicated the answer: %v", rows)
		}
	}
	// Recover from the conflict by fetching and editing the stored row.
	mustRuleWrite(t, s, c, "edit", id, `{"body":"updated answer"}`, "")
	if row := readGet(t, s, c, "entries", id, "").Body["row"].([]any); row[1] != "updated answer" {
		t.Fatal(row)
	}
	other := c
	other.VisitorID = "second-person"
	if rows := readList(t, s, other, "entries", nil).Body["rows"].([][]any); len(rows) != 0 {
		t.Fatalf("second person can see the first answer: %v", rows)
	}
	mustRuleWrite(t, s, other, "add", 0, `{"body":"second answer"}`, "")
	if rows := readList(t, s, owner, "entries", nil).Body["rows"].([][]any); len(rows) != 2 {
		t.Fatalf("expected one answer per person: %v", rows)
	}
}

func TestRulesWriteServerColumnsAndApproval(t *testing.T) {
	s, c, in := rulesFixture(t, `CREATE TABLE entries(id INTEGER PRIMARY KEY, body TEXT, created_at TEXT, updated_at TEXT, written_by TEXT);`)
	s.deps.SpamCheckAvailable = true
	comments := json.RawMessage(ruleengine.EncodeSet(ruleengine.PolicySet{Version: 1, Policies: []ruleengine.Policy{
		{Name: "read", Command: ruleengine.CmdRead, Using: `true`},
		{Name: "add", Command: ruleengine.CmdAdd, Check: `auth.checked || auth.ai || auth.role == "owner"`},
		{Name: "edit", Command: ruleengine.CmdEdit, Using: `auth.role == "owner"`},
		{Name: "delete", Command: ruleengine.CmdDelete, Using: `auth.role == "owner"`},
		{Name: "approve_first", Command: ruleengine.CmdRead, Using: `auth.role == "owner" || approved_at is not null`, Restrictive: true},
	}}))
	in.Rules = &comments
	c.Resource = putRules(t, s, in)
	c.Owner = false
	c.CSRFOK = true
	c.PasscodeOK, c.ViewersOK = true, true
	c.VisitorID = "caller"
	c.ActiveChecked = true
	c.SpamCheck = "ok"
	added := mustRuleWrite(t, s, c, "add", 0, `{"body":"held","visitor_id":"forged","written_by":"forged","created_at":"forged","updated_at":"forged","approved_at":"forged"}`, "")
	id := added.Body["last_insert_id"].(int64)
	owner := c
	owner.Owner = true
	row := readGet(t, s, owner, "entries", id, "").Body
	cols, vals := row["columns"].([]string), row["row"].([]any)
	for i, name := range cols {
		switch name {
		case "visitor_id":
			if vals[i] != "caller" {
				t.Fatal(row)
			}
		case "approved_at", "updated_at":
			if vals[i] != nil {
				t.Fatal(row)
			}
		case "written_by":
			if vals[i] != "" {
				t.Fatal(row)
			}
		case "created_at":
			if vals[i] == nil || vals[i] == "forged" {
				t.Fatal(row)
			}
		}
	}
	readGet(t, s, c, "entries", id, "row_not_found")
	// Use an edit policy that permits the caller so column refusals are reached.
	in.Rules = writeSet("true", "true", "auth.signed_in", "true")
	set, _ := ruleengine.DecodeSet(*in.Rules)
	set.Policies = append(set.Policies, ruleengine.Policy{Command: ruleengine.CmdRead, Using: `approved_at is not null || auth.role == "owner"`, Restrictive: true})
	raw := json.RawMessage(ruleengine.EncodeSet(set))
	in.Rules = &raw
	c.Resource = putRules(t, s, in)
	for _, name := range []string{"approved_at", "id", "visitor_id", "written_by", "created_at", "updated_at", "rowid", "_rowid_", "oid"} {
		mustRuleWrite(t, s, c, "edit", id, fmt.Sprintf(`{%q:"forged"}`, name), "server_column")
	}
	mustRuleWrite(t, s, c, "add", 0, `{"id":42,"body":"id"}`, "invalid_rows")
	// The comments-style set itself retains owner-on-site approval.
	in.Rules = &comments
	c.Resource = putRules(t, s, in)
	onSite := c
	onSite.OwnerOnSite = true
	onSite.VisitorID = c.OwnerID
	mustRuleWrite(t, s, onSite, "edit", id, `{"approved_at":"now"}`, "")
	readGet(t, s, c, "entries", id, "")
	added = mustRuleWrite(t, s, onSite, "add", 0, `{"body":"owner","approved_at":"forged"}`, "")
	owner.Resource = c.Resource
	row = readGet(t, s, owner, "entries", added.Body["last_insert_id"].(int64), "").Body
	for i, name := range row["columns"].([]string) {
		if name == "approved_at" && (row["row"].([]any)[i] == nil || row["row"].([]any)[i] == "forged") {
			t.Fatal(row)
		}
	}
	// Removing every approval reference leaves it ordinary editable data.
	in.Rules = writeSet("true", "true", "auth.signed_in", "true")
	c.Resource = putRules(t, s, in)
	added = mustRuleWrite(t, s, c, "add", 0, `{"body":"ordinary","approved_at":"chosen"}`, "")
	mustRuleWrite(t, s, c, "edit", added.Body["last_insert_id"].(int64), `{"approved_at":"edited"}`, "")
	owner.Resource = c.Resource
	row = readGet(t, s, owner, "entries", added.Body["last_insert_id"].(int64), "").Body
	for i, name := range row["columns"].([]string) {
		if name == "approved_at" && row["row"].([]any)[i] != "edited" {
			t.Fatal(row)
		}
	}
}

func TestRulesWriteMixedCaseServerColumns(t *testing.T) {
	s, c, _ := readFixture(t, `CREATE TABLE entries(body TEXT, VISITOR_ID TEXT, Written_By TEXT, Created_At TEXT, UPDATED_AT TEXT);`, writeSet("true", "true", "auth.signed_in", "true"))
	c.VisitorID, c.ActiveChecked = "caller", true
	owner := c
	owner.Owner = true
	for _, body := range []string{
		`{"body":"new","VISITOR_ID":"victim","Written_By":"owner","Created_At":"forged","UPDATED_AT":"forged"}`,
		`{"body":"new","visitor_id":"victim","written_by":"owner","created_at":"forged","updated_at":"forged"}`,
		`{"body":"new","VISITOR_ID":"victim","visitor_id":"other","Written_By":"owner","written_by":"other","Created_At":"forged","created_at":"other","UPDATED_AT":"forged","updated_at":"other"}`,
	} {
		added := mustRuleWrite(t, s, c, "add", 0, body, "")
		id := added.Body["last_insert_id"].(int64)
		row := readGet(t, s, owner, "entries", id, "").Body["row"].([]any)
		if row[1] != "caller" || row[2] != "" || row[4] != nil {
			t.Fatalf("forged server columns survived: %v", row)
		}
		if _, err := time.Parse(time.RFC3339Nano, fmt.Sprint(row[3])); err != nil {
			t.Fatalf("created timestamp: %v", row)
		}
		mustRuleWrite(t, s, c, "edit", id, `{"body":"changed"}`, "")
		row = readGet(t, s, owner, "entries", id, "").Body["row"].([]any)
		if _, err := time.Parse(time.RFC3339Nano, fmt.Sprint(row[4])); err != nil {
			t.Fatalf("updated timestamp: %v", row)
		}
	}
}

func TestRulesWriteMixedCaseApproval(t *testing.T) {
	s, c, in := rulesFixture(t, `CREATE TABLE entries(body TEXT, APPROVED_AT TEXT);`)
	c.PasscodeOK, c.ViewersOK = true, true
	s.deps.SpamCheckAvailable = true
	raw := json.RawMessage(ruleengine.EncodeSet(ruleengine.PolicySet{Version: 1, Policies: []ruleengine.Policy{
		{Name: "read", Command: ruleengine.CmdRead, Using: `true`},
		{Name: "add", Command: ruleengine.CmdAdd, Check: `auth.checked || auth.ai || auth.role == "owner"`},
		{Name: "edit", Command: ruleengine.CmdEdit, Using: `auth.role == "owner"`},
		{Name: "delete", Command: ruleengine.CmdDelete, Using: `auth.role == "owner"`},
		{Name: "approve_first", Command: ruleengine.CmdRead, Using: `auth.role == "owner" || approved_at is not null`, Restrictive: true},
	}}))
	in.Rules = &raw
	c.Resource = putRules(t, s, in)
	c.Owner, c.CSRFOK, c.ActiveChecked = false, true, true
	c.VisitorID, c.SpamCheck = "caller", "ok"
	owner := c
	owner.Owner = true
	onSite := c
	onSite.OwnerOnSite, onSite.VisitorID = true, c.OwnerID
	approval := func(actor Call, id int64) any {
		t.Helper()
		return readGet(t, s, actor, "entries", id, "").Body["row"].([]any)[1]
	}
	for _, body := range []string{
		`{"body":"held","APPROVED_AT":"forged"}`,
		`{"body":"held","approved_at":"forged"}`,
		`{"body":"held","APPROVED_AT":"forged","approved_at":"other"}`,
	} {
		added := mustRuleWrite(t, s, c, "add", 0, body, "")
		id := added.Body["last_insert_id"].(int64)
		if got := approval(owner, id); got != nil {
			t.Fatalf("visitor supplied approval survived: %v", got)
		}
		readGet(t, s, c, "entries", id, "row_not_found")
		mustRuleWrite(t, s, onSite, "edit", id, `{"APPROVED_AT":"approved"}`, "")
		if got := approval(c, id); got != "approved" {
			t.Fatalf("owner-on-site approval: %v", got)
		}
	}
	for _, actor := range []Call{owner, onSite} {
		added := mustRuleWrite(t, s, actor, "add", 0, `{"body":"owner"}`, "")
		if got := approval(c, added.Body["last_insert_id"].(int64)); got == nil {
			t.Fatal("owner add was not approved")
		} else if _, err := time.Parse(time.RFC3339Nano, fmt.Sprint(got)); err != nil {
			t.Fatalf("approval timestamp: %v", got)
		}
		added = mustRuleWrite(t, s, actor, "add", 0, `{"body":"owner","APPROVED_AT":"chosen"}`, "")
		got := approval(c, added.Body["last_insert_id"].(int64))
		if actor.Owner && got != "chosen" || !actor.Owner && (got == nil || got == "chosen") {
			t.Fatalf("owner=%v supplied approval: %v", actor.Owner, got)
		}
	}
	// Permit visitor edits so the server-column refusal is reached.
	in.Rules = writeSet(`approved_at is not null || auth.role == "owner"`, "true", "auth.signed_in", "true")
	c.Resource = putRules(t, s, in)
	for _, name := range []string{"APPROVED_AT", "approved_at", "Approved_At"} {
		mustRuleWrite(t, s, c, "edit", 1, fmt.Sprintf(`{%q:"forged"}`, name), "server_column")
	}
	// Without a rule reference this remains ordinary editable data.
	in.Rules = writeSet("true", "true", "auth.signed_in", "true")
	c.Resource = putRules(t, s, in)
	added := mustRuleWrite(t, s, c, "add", 0, `{"body":"ordinary","APPROVED_AT":"chosen"}`, "")
	id := added.Body["last_insert_id"].(int64)
	if got := approval(c, id); got != "chosen" {
		t.Fatalf("ordinary approval column stamped: %v", got)
	}
	mustRuleWrite(t, s, c, "edit", id, `{"APPROVED_AT":"edited"}`, "")
	if got := approval(c, id); got != "edited" {
		t.Fatalf("ordinary approval column edit: %v", got)
	}
}

func TestRulesWriteReadVisibilityAndAI(t *testing.T) {
	s, c, _ := readFixture(t, `CREATE TABLE entries(body TEXT, visitor_id TEXT); INSERT INTO entries VALUES('visible','caller'),('visible','other'),('hidden','caller');`, writeSet(`body == "visible"`, "true", "auth.signed_in", "true"))
	c.VisitorID, c.ActiveChecked, c.VisitorAI = "caller", true, true
	for _, action := range []string{"edit", "delete"} {
		for _, id := range []int64{2, 3, 99} {
			mustRuleWrite(t, s, c, action, id, `{"body":"changed"}`, "row_not_found")
		}
	}
	// Post-edit visibility is not required; C(edit) alone decides the new row.
	mustRuleWrite(t, s, c, "edit", 1, `{"body":"hidden"}`, "")
	readGet(t, s, c, "entries", 1, "row_not_found")
	execReadFixture(t, s, `UPDATE entries SET body='visible' WHERE rowid=1`)
	mustRuleWrite(t, s, c, "delete", 1, "", "")
}

func TestRulesWriteFreshBindings(t *testing.T) {
	for _, failure := range []string{"changed", "error", "owner"} {
		t.Run(failure, func(t *testing.T) {
			s, c, in := rulesFixture(t, `CREATE TABLE entries(body TEXT);`)
			c.PasscodeOK, c.ViewersOK = true, true
			p := &readBindings{owner: false}
			p.values = func(_ context.Context, n int) (ruleengine.Values, error) {
				if n == 2 && failure == "error" {
					return nil, errors.New("unavailable")
				}
				v := "first"
				if n == 2 {
					v = "second"
				}
				return ruleengine.Values{"event.member": ruleengine.Text(v)}, nil
			}
			s.deps.Bindings = p
			add := `body == event.member`
			if failure == "owner" {
				add = `auth.role == "owner"`
			}
			in.Rules = writeSet("true", add, "auth.signed_in", "true")
			c.Resource = putRules(t, s, in)
			c.Owner = false
			c.CSRFOK = true
			c.VisitorID = "caller"
			c.ActiveChecked = true
			c.OwnerOnSite = failure == "owner"
			prepared := 0
			s.sqliteHook = func(stage string) {
				if stage == "after_prepare" {
					prepared++
				}
			}
			want := "forbidden"
			if failure == "error" {
				want = "internal_error"
			}
			mustRuleWrite(t, s, c, "add", 0, `{"body":"first"}`, want)
			if failure != "changed" && prepared != 0 {
				t.Fatalf("prepared %d writes after binding refusal", prepared)
			}
		})
	}
}

func TestRulesWriteRefusalOrderAndCheckedPost(t *testing.T) {
	s, c, in := readFixture(t, `CREATE TABLE entries(body TEXT);`, writeSet("true", "auth.signed_in", "auth.signed_in", "true"))
	s.deps.SpamCheckAvailable = true
	c.VisitorID, c.VisitorSessionUserID = c.OwnerID, c.OwnerID
	if _, err := s.db.Exec(`UPDATE users SET suspended_at=now() WHERE id=$1`, c.OwnerID); err != nil {
		t.Fatal(err)
	}
	c.CSRFOK = false
	mustRuleWrite(t, s, c, "add", 0, `{}`, "csrf_required")
	c.CSRFOK = true
	mustRuleWrite(t, s, c, "add", 0, `{}`, "account_suspended")
	c.VisitorID = ""
	mustRuleWrite(t, s, c, "add", 0, `{}`, "sign_in_required")
	// Checked-post limits depend on the applying add policies, not whether
	// a restrictive policy mentions checked for some other command.
	for _, tc := range []struct {
		add, spam, want string
		checked         bool
	}{
		{"auth.checked", "", "spam_check_required", false},
		{"auth.checked", "spam_check_failed", "spam_check_failed", false},
		{"auth.checked", "ok", "", true},
		{"true || auth.checked", "ok", "", false},
		{"true", "ok", "", false},
	} {
		in.Rules = writeSet("true", tc.add, "auth.signed_in", "true")
		c.Resource = putRules(t, s, in)
		c.SpamCheck = tc.spam
		auth := s.AuthorizeTableWrite(context.Background(), TableInput{Call: c, Table: "entries"}, "add")
		if got := readCode(TableResult{Authorization: auth.Authorization}, nil); got != tc.want || auth.CheckedPost() != tc.checked {
			t.Fatalf("%+v: %+v checked=%v", tc, auth.Authorization, auth.CheckedPost())
		}
	}
	c.VisitorID = "caller"
	c.ActiveChecked = true
	c.SpamCheck = ""
	// Worked case 1: no edit policy, so add permission does not mean add_only.
	in.Rules = saveSet(ruleengine.Policy{Command: ruleengine.CmdRead, Using: "true"}, ruleengine.Policy{Command: ruleengine.CmdAdd, Check: "true"})
	c.Resource = putRules(t, s, in)
	for _, action := range []string{"edit", "delete"} {
		mustRuleWrite(t, s, c, action, 1, `{"body":"x"}`, "forbidden")
	}
	// Worked case 2: comments count checked add even without a spam token.
	raw := json.RawMessage(ruleengine.EncodeSet(ruleengine.PolicySet{Version: 1, Policies: []ruleengine.Policy{
		{Name: "read", Command: ruleengine.CmdRead, Using: `true`},
		{Name: "add", Command: ruleengine.CmdAdd, Check: `auth.checked || auth.ai || auth.role == "owner"`},
		{Name: "edit", Command: ruleengine.CmdEdit, Using: `auth.role == "owner"`},
		{Name: "delete", Command: ruleengine.CmdDelete, Using: `auth.role == "owner"`},
	}}))
	in.Rules = &raw
	c.Resource = putRules(t, s, in)
	for _, action := range []string{"edit", "delete"} {
		mustRuleWrite(t, s, c, action, 1, `{"body":"x"}`, "add_only")
	}
}

func TestRulesWriteStageTwoFiltersUseFreshValues(t *testing.T) {
	for _, action := range []string{"edit", "delete"} {
		t.Run(action, func(t *testing.T) {
			s, c, in := rulesFixture(t, `CREATE TABLE entries(body TEXT); INSERT INTO entries VALUES('first'),('second');`)
			c.PasscodeOK, c.ViewersOK = true, true
			p := &readBindings{values: func(_ context.Context, n int) (ruleengine.Values, error) {
				v := "first"
				if n > 1 {
					v = "second"
				}
				return ruleengine.Values{"event.member": ruleengine.Text(v)}, nil
			}}
			s.deps.Bindings = p
			in.Rules = writeSet(`body == event.member`, "true", "auth.signed_in", "true")
			c.Resource = putRules(t, s, in)
			c.Owner = false
			c.CSRFOK = true
			c.VisitorID = "caller"
			c.ActiveChecked = true
			mustRuleWrite(t, s, c, action, 2, `{"body":"changed"}`, "")
			if p.calls != 2 {
				t.Fatalf("binding calls=%d", p.calls)
			}
		})
	}
}

func TestRulesWriteCheckUsesStoredAffinity(t *testing.T) {
	s, c, _ := readFixture(t, `CREATE TABLE entries(body TEXT); INSERT INTO entries VALUES('old');`, writeSet("true", "true", "auth.signed_in", `body == "1.0"`))
	c.VisitorID, c.ActiveChecked = "caller", true
	mustRuleWrite(t, s, c, "edit", 1, `{"body":1.0}`, "")
	out := readGet(t, s, c, "entries", 1, "")
	if out.Body["row"].([]any)[0] != "1.0" {
		t.Fatal(out.Body)
	}
	mustRuleWrite(t, s, c, "edit", 1, `{"body":1}`, "row_not_found")
}
