package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	sqlite3 "github.com/ncruces/go-sqlite3"
	ruleengine "github.com/vsriram/simple-host/host/storage/rules"
	"github.com/vsriram/simple-host/internal/model"
	runtime "github.com/vsriram/simple-host/internal/storage"
)

// These are saved resources, not the pure permission/row matrices in R2-S6.
// Every accepted matrix is exercised through PutStorageResource and the public
// service operations. Table overrides are separate resource configurations.
type equivalenceConfig struct {
	name, kind string
	matrix     model.AccessMatrix
	hold       bool
	tables     map[string]ResourceTableInput
}

func equivalenceConfigs(kind string) []equivalenceConfig {
	values := []string{model.WhoNobody, model.WhoOwner, model.WhoOwn, model.WhoSignedIn, model.WhoChecked, model.WhoAnyone}
	var out []equivalenceConfig
	for _, read := range values {
		for _, add := range values {
			for _, edit := range values {
				for _, del := range values {
					m := model.AccessMatrix{Read: read, Add: add, Edit: edit, Delete: del}
					if code, _ := m.Check(); code != "" {
						continue
					}
					name := strings.Join([]string{read, add, edit, del}, "/")
					out = append(out, equivalenceConfig{name: name, kind: kind, matrix: m})
					if kind == "sqlite" && add == model.WhoChecked {
						out = append(out, equivalenceConfig{name: name + "/approve-first", kind: kind, matrix: m, hold: true})
					}
				}
			}
		}
	}
	if kind == "sqlite" {
		for _, tc := range []struct {
			name, base, override string
			hold                 bool
		}{
			{"override-narrower", "public", "private", false},
			{"override-wider", "private", "public", false},
			{"override-equal", "public", "public", false},
			{"override-checked-hold", "public", "comments", true},
		} {
			m, _ := model.PresetMatrix(tc.base)
			out = append(out, equivalenceConfig{name: tc.name, kind: kind, matrix: m, hold: tc.hold, tables: map[string]ResourceTableInput{"overridden": {AccessRequest: model.AccessRequest{Preset: tc.override}}}})
		}
	}
	return out
}

// What the save must refuse, so that the list above is "every configuration
// PutStorageResource accepts" by the save's own answer and not only by
// AccessMatrix.Check: every matrix Check rejects, with Check's code as the
// refusal's rule, and (for
// tables) approve_first on every accepted matrix whose add is not checked, and
// approve_first on any key-value namespace.
func equivalenceSaveRefusals(t *testing.T, twin equivalenceTwin, kind string) {
	t.Helper()
	values := []string{model.WhoNobody, model.WhoOwner, model.WhoOwn, model.WhoSignedIn, model.WhoChecked, model.WhoAnyone}
	refused := 0
	for _, read := range values {
		for _, add := range values {
			for _, edit := range values {
				for _, del := range values {
					m := model.AccessMatrix{Read: read, Add: add, Edit: edit, Delete: del}
					rule, _ := m.Check()
					hold := false
					if rule == "" {
						if kind == "sqlite" && add == model.WhoChecked {
							continue
						}
						hold = true
					}
					in := ResourceInput{Call: twin.call, Name: twin.call.ResourceName, Kind: kind, ApproveFirst: &hold,
						AccessRequest: model.AccessRequest{Preset: "custom", Read: read, Add: add, Edit: edit, Delete: del}}
					if kind == "sqlite" {
						in.Tables = &map[string]ResourceTableInput{}
					}
					_, err := twin.service.PutStorageResource(context.Background(), in)
					e, ok := err.(*Error)
					if !ok || e == nil || e.Code != "invalid_access" || e.Rule != rule {
						t.Fatalf("save %s/%s/%s/%s approve_first=%v: got %#v, want invalid_access with rule %q", read, add, edit, del, hold, err, rule)
					}
					refused++
				}
			}
		}
	}
	if refused == 0 {
		t.Fatal("no configuration was refused")
	}
}

type equivalenceTwin struct {
	service *Service
	call    Call
	conn    *sqlite3.Conn
	events  *[]equivalenceEvent
}

// One outward effect of an operation: a notification handed to deps.Notify
// (its name and its fields), or a call of the committed or changed hook.
type equivalenceEvent struct {
	Name   string
	Fields any
}

// The first refusal, with everything the transport branches on: its code and
// message, whether the data is served all the same (Allowed), whether a retry
// header is withheld, and the codes of the ordered refusals. The preset path
// always lists its refusals and the rules path lists none for a single one,
// so a missing list counts as a list holding only the first code.
type equivalenceRefusal struct {
	Code, Message         string
	Allowed, NoRetryAfter bool
	Refusals              string
}

// A refusal is the service's own *Error. Anything else (a missing table, a
// broken fixture) and the service's own internal failures stop the test: the
// same breakage on both twins must never count as a match.
func equivalenceRefused(t *testing.T, result TableResult, err error) equivalenceRefusal {
	t.Helper()
	if err == nil {
		if result.Authorization == nil {
			return equivalenceRefusal{}
		}
		err = result.Authorization
	}
	e, ok := err.(*Error)
	if !ok {
		t.Fatalf("not a refusal: %T %v", err, err)
	}
	if e == nil {
		return equivalenceRefusal{}
	}
	if e.Code == "" || e.Code == "internal_error" || e.Code == "storage_busy" {
		t.Fatalf("internal failure, not a refusal: %q %q", e.Code, e.Message)
	}
	codes := []string{e.Code}
	if len(e.Refusals) != 0 {
		codes = codes[:0]
		for _, r := range e.Refusals {
			codes = append(codes, r.Code)
		}
	}
	return equivalenceRefusal{Code: e.Code, Message: e.Message, Allowed: e.Allowed, NoRetryAfter: e.NoRetryAfter, Refusals: strings.Join(codes, ",")}
}

func equivalenceFixture(t *testing.T, kind string) equivalenceTwin {
	t.Helper()
	s, c := kvFixture(t, "sqlite", false)
	s.deps.RulesEnabled, s.deps.SpamCheckAvailable = true, true
	s.deps.SpamCheckedMaxBytes, s.deps.SpamCheckedMaxLinks = 4096, 10
	s.deps.PasscodeAllows = func() bool { return true }
	s.deps.ViewersAllow = func() bool { return true }
	var path string
	if kind == "sqlite" {
		c.ResourceName = "rows"
		path = filepath.Join(c.RuntimeDir, "sqlite", "rows.sqlite")
		s.deps.SQLitePath = path
		// The KV resource created by the shared fixture stays empty.
		// Row requests use a separate owner-queryable SQLite file.
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
	} else {
		path = runtime.RuntimeKVPath(c.RuntimeDir, c.ResourceName)
	}
	conn, err := sqlite3.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := conn.Close(); err != nil {
			t.Error(err)
		}
	})
	if kind == "sqlite" {
		for _, table := range []string{"entries", "overridden"} {
			if err := conn.Exec(`CREATE TABLE ` + table + `(id INTEGER PRIMARY KEY, body TEXT, visitor_id TEXT, written_by TEXT, created_at TEXT, updated_at TEXT, approved_at TEXT)`); err != nil {
				t.Fatal(err)
			}
		}
	}
	events := &[]equivalenceEvent{}
	s.deps.Notify = func(_ Call, name string, fields map[string]any) {
		data := equivalenceBody(t, fields).(map[string]any)
		// A write's own fresh timestamps are the only fields that vary.
		for _, m := range []any{data, data["fields"]} {
			if m, ok := m.(map[string]any); ok {
				for _, k := range []string{"created_at", "updated_at", "approved_at"} {
					if v, ok := m[k]; ok {
						m[k] = equivalenceTime(v)
					}
				}
			}
		}
		*events = append(*events, equivalenceEvent{name, data})
	}
	// The service's other outward effects, in the order they happen: the
	// committed hook (usage and change accounting hang off it in the server)
	// and the changed mark.
	s.deps.Committed = func(context.Context) { *events = append(*events, equivalenceEvent{Name: "<committed>"}) }
	s.deps.MarkChanged = func() { *events = append(*events, equivalenceEvent{Name: "<changed>"}) }
	return equivalenceTwin{s, c, conn, events}
}

func equivalenceSave(t *testing.T, twin *equivalenceTwin, cfg equivalenceConfig, converted bool) {
	t.Helper()
	in := ResourceInput{Call: twin.call, Name: twin.call.ResourceName, Kind: cfg.kind, ApproveFirst: &cfg.hold,
		AccessRequest: model.AccessRequest{Preset: "custom", Read: cfg.matrix.Read, Add: cfg.matrix.Add, Edit: cfg.matrix.Edit, Delete: cfg.matrix.Delete}}
	tables := map[string]ResourceTableInput{}
	for name, table := range cfg.tables {
		tables[name] = table
	}
	in.Tables = &tables
	if cfg.kind == "kv" {
		in.Tables = nil
	}
	if converted {
		matrices := map[string]ruleengine.Matrix{}
		for name, table := range cfg.tables {
			m, _, code, _ := table.Matrix()
			if code != "" {
				t.Fatal(code)
			}
			matrices[name] = ruleengine.Matrix(m)
		}
		out := ruleengine.Convert(ruleengine.ConvertInput{Kind: cfg.kind, Default: ruleengine.Matrix(cfg.matrix), Tables: matrices, ApproveFirst: cfg.hold})
		if len(out.Manual) != 0 {
			t.Fatal(out.Manual)
		}
		raw := json.RawMessage(ruleengine.EncodeSet(out.Default))
		in.Rules, in.AccessRequest, in.ApproveFirst = &raw, model.AccessRequest{}, nil
		for name, set := range out.Tables {
			raw := json.RawMessage(ruleengine.EncodeSet(set))
			tables[name] = ResourceTableInput{Rules: &raw}
		}
	}
	saved, err := twin.service.PutStorageResource(context.Background(), in)
	if err != nil {
		t.Fatalf("save converted=%v: %v", converted, err)
	}
	twin.call.Resource, err = twin.service.LoadResource(context.Background(), ResourceReadInput{SiteID: twin.call.SiteID, Name: in.Name})
	if err != nil {
		t.Fatal(err)
	}
	if saved.Resource.RulesStatus() != twin.call.Resource.RulesStatus() {
		t.Fatal("save/load status differs")
	}
	want := "preset"
	if converted {
		want = "rules"
	}
	if twin.call.Resource.RulesStatus() != want {
		t.Fatalf("saved %s, want %s", twin.call.Resource.RulesStatus(), want)
	}
}

const equivalenceSeedTime = "2020-01-01T00:00:00Z"

func equivalenceExec(t *testing.T, conn *sqlite3.Conn, sql string) {
	t.Helper()
	if err := conn.Exec(sql); err != nil {
		t.Fatal(err)
	}
}

func equivalenceReset(t *testing.T, twin equivalenceTwin, kind, table string) {
	t.Helper()
	if kind == "sqlite" {
		equivalenceExec(t, twin.conn, `DELETE FROM `+table+`; INSERT INTO `+table+` VALUES
   (1,'own','author','','2020-01-01T00:00:00Z',NULL,'2020-01-01T00:00:00Z'),
   (2,'other','another','','2020-01-01T00:00:00Z',NULL,'2020-01-01T00:00:00Z'),
   (3,'no author',NULL,'','2020-01-01T00:00:00Z',NULL,'2020-01-01T00:00:00Z'),
   (4,'held','author','','2020-01-01T00:00:00Z',NULL,NULL)`)
	} else {
		equivalenceExec(t, twin.conn, `DELETE FROM kv; INSERT INTO kv VALUES
   ('1','"own"','author','2020-01-01T00:00:00Z','2020-01-01T00:00:00Z'),
   ('2','"other"','another','2020-01-01T00:00:00Z','2020-01-01T00:00:00Z'),
   ('3','"no author"','','2020-01-01T00:00:00Z','2020-01-01T00:00:00Z'),
   ('4','"held"','author','2020-01-01T00:00:00Z','2020-01-01T00:00:00Z');
   UPDATE kv_meta SET entries=4, logical_bytes=(SELECT sum(length(CAST(key AS BLOB))+length(CAST(value AS BLOB))) FROM kv)`)
	}
}

// Only freshly generated server timestamps vary between sequential twins. Keep
// seed times, NULLs, types and every other byte, including unredacted authors.
func equivalenceTime(v any) any {
	if s, ok := v.(string); ok && s != equivalenceSeedTime {
		if _, err := time.Parse(time.RFC3339Nano, s); err == nil {
			return "<server-time>"
		}
	}
	return v
}

// The database change is the SQLite file: row and key-value operations issue
// no Postgres write in this package (the only writes in
// internal/repository/storage are the resource save and the file index). What
// the server writes to Postgres after an operation (usage counters, people
// rows) is done by the transport from the committed and changed hooks, which
// are compared as events here; those rows themselves are R3-S5b's, over HTTP.
func equivalenceSnapshot(t *testing.T, twin equivalenceTwin, kind, table string) any {
	t.Helper()
	query := `SELECT * FROM ` + table + ` ORDER BY id`
	if kind == "kv" {
		query = `SELECT * FROM kv ORDER BY key`
	}
	stmt, _, err := twin.conn.Prepare(query)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	rows := [][]any{}
	for stmt.Step() {
		row := make([]any, stmt.ColumnCount())
		for i := range row {
			row[i] = runtime.StorageColumn(stmt, i)
			switch stmt.ColumnName(i) {
			case "created_at", "updated_at", "approved_at":
				row[i] = equivalenceTime(row[i])
			}
		}
		rows = append(rows, row)
	}
	if err := stmt.Err(); err != nil {
		t.Fatal(err)
	}
	if kind == "kv" {
		meta, err := runtime.RuntimeKVMeta(twin.conn)
		if err != nil {
			t.Fatal(err)
		}
		return []any{rows, meta}
	}
	return rows
}

func equivalenceBody(t *testing.T, body any) any {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	var out any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	if obj, ok := out.(map[string]any); ok {
		if cols, ok := obj["columns"].([]any); ok {
			normalize := func(row []any) {
				for i, col := range cols {
					if col == "created_at" || col == "updated_at" || col == "approved_at" {
						row[i] = equivalenceTime(row[i])
					}
				}
			}
			if row, ok := obj["row"].([]any); ok {
				normalize(row)
			}
			if rows, ok := obj["rows"].([]any); ok {
				for _, row := range rows {
					normalize(row.([]any))
				}
			}
		}
	}
	return out
}

type equivalenceCaller struct {
	name      string
	call      Call
	suspended bool
	only      string
}
type equivalenceOperation struct {
	name, action string
	id           int64
	query        url.Values
}

func equivalenceOperations() []equivalenceOperation {
	out := []equivalenceOperation{{name: "list", action: "read", query: url.Values{}}, {name: "add", action: "add"}}
	for _, id := range []int64{1, 2, 3, 4, 99} {
		for _, action := range []string{"get", "edit", "delete"} {
			op := action
			if action == "get" {
				op = "read"
			}
			out = append(out, equivalenceOperation{name: action, action: op, id: id})
		}
	}
	return out
}

func equivalenceRun(t *testing.T, twin equivalenceTwin, c Call, kind, table string, op equivalenceOperation) (any, equivalenceRefusal) {
	t.Helper()
	ctx := context.Background()
	c.SiteID, c.OwnerID, c.SiteName, c.ResourceName, c.RuntimeDir, c.Resource = twin.call.SiteID, twin.call.OwnerID, twin.call.SiteName, twin.call.ResourceName, twin.call.RuntimeDir, twin.call.Resource
	c.CSRFOK = true
	s := twin.service
	if c.OwnerOnSite {
		c.VisitorID = twin.call.OwnerID
	}
	if kind == "sqlite" {
		values := map[string]json.RawMessage{"body": json.RawMessage(`"changed"`)}
		if c.OwnerOnSite && op.name == "edit" && op.id == 4 {
			values = map[string]json.RawMessage{"approved_at": json.RawMessage(`"2020-01-01T00:00:00Z"`)}
		}
		in := TableInput{Call: c, Table: table, ID: op.id, Values: values, Query: op.query}
		var out TableResult
		var err error
		switch op.name {
		case "list":
			out, err = s.TableList(ctx, in)
		case "get":
			out, err = s.TableGet(ctx, in)
		default:
			in.Authorized = s.AuthorizeTableWrite(ctx, in, op.action)
			switch op.name {
			case "add":
				out, err = s.TableAdd(ctx, in)
			case "edit":
				out, err = s.TableEdit(ctx, in)
			case "delete":
				out, err = s.TableRemove(ctx, in)
			}
		}
		// The transport fires a successful write's notification.
		if err == nil && out.Notify != nil {
			out.Notify()
		}
		return equivalenceBody(t, out.Body), equivalenceRefused(t, out, err)
	}
	key := fmt.Sprint(op.id)
	switch op.name {
	case "list":
		out, err := s.ListKV(ctx, KVListInput{Call: c, Limit: 100, ValidLimit: true})
		return equivalenceBody(t, struct {
			Items []VisibleKVItem
			Next  string
		}{out.Items, out.Next}), equivalenceRefused(t, TableResult{Authorization: out.Authorization}, err)
	case "get":
		out, err := s.GetKV(ctx, KVReadInput{Call: c, Key: key})
		return equivalenceBody(t, out.Item), equivalenceRefused(t, TableResult{Authorization: out.Authorization}, err)
	case "add", "edit":
		if op.name == "add" {
			key = "5"
		}
		requested := ""
		if c.VisitorAI {
			requested = op.name
		}
		value, auth, err := s.PutKV(ctx, KVPutInput{Call: c, Key: key, Requested: requested, Body: strings.NewReader(`{"value":"changed"}`)})
		return equivalenceBody(t, value), equivalenceRefused(t, TableResult{Authorization: auth}, err)
	default:
		auth, err := s.DeleteKV(ctx, KVDeleteInput{Call: c, Key: key})
		return nil, equivalenceRefused(t, TableResult{Authorization: auth}, err)
	}
}

// Approved wording differences (R3-S5a ruling 1, approved 2026-11-15): a
// converted resource keeps the rules wording already live, it does not imitate
// the preset wording. The code, every other field of the refusal, the body,
// the database change and the notifications must still agree; only the
// human-readable message may differ, and only in these two pinned ways. They are counted in the summary line and named in
// scripts/golden/README.md. Any other message difference fails.
//
//   - forbidden: the preset names the action's own value, "... here (owner)"
//     or "... here (nobody)" (access.go); rules have no preset value to name.
//   - for_requires_own: the preset names the table's read value
//     (rows.go); rules describe the read rules instead.
func equivalenceWordingApproved(preset, converted equivalenceRefusal, value, read string) bool {
	message := preset.Message
	preset.Message = converted.Message
	if preset != converted {
		return false
	}
	switch preset.Code {
	case "forbidden":
		return (value == model.WhoOwner || value == model.WhoNobody) && message == converted.Message+" ("+value+")"
	case "for_requires_own":
		return message == "for only adds a row on a table whose read is own (the records or personal presets); this table's read is "+read &&
			converted.Message == "for needs read rules limited to the row's visitor or owner"
	}
	return false
}

// What one run of the comparison counted, for the summary line.
type equivalenceTally struct {
	comparisons, wording int
	successes            map[string]int
	callerNames          map[string]bool
}

func equivalenceCallers(kind, suspended string) []equivalenceCaller {
	callers := []equivalenceCaller{
		{name: "anonymous"},
		{name: "signed-in-non-author", call: Call{VisitorID: "non-author"}},
		{name: "author", call: Call{VisitorID: "author"}},
		{name: "suspended", call: Call{VisitorID: "author", VisitorSessionUserID: suspended}, suspended: true},
		{name: "owner-on-site", call: Call{VisitorID: "site-owner", OwnerOnSite: true}},
		{name: "visitor-AI-author", call: Call{VisitorID: "author", VisitorAI: true}},
		{name: "visitor-AI-non-author", call: Call{VisitorID: "non-author", VisitorAI: true}},
		{name: "anonymous-checked", call: Call{SpamCheck: "ok"}},
		{name: "anonymous-failed-check", call: Call{SpamCheck: "spam_check_failed"}},
		{name: "signed-in-checked", call: Call{VisitorID: "author", SpamCheck: "ok"}},
		{name: "owner-tools", call: Call{Owner: true}},
	}
	if kind == "sqlite" {
		callers = append(callers, equivalenceCaller{name: "owner-on-site-for", call: Call{VisitorID: "site-owner", OwnerOnSite: true, OwnerFor: "author"}, only: "add"},
			equivalenceCaller{name: "owner-tools-for", call: Call{Owner: true, OwnerFor: "author"}, only: "add"})
	}
	return callers
}

// equivalenceCompare runs every caller and operation against both twins and
// fails on the first answer that differs. exact allows no approved
// difference at all: it is for two twins that are both on presets (a
// resource converted by migrate-rules and put back, R4-S1).
func equivalenceCompare(t *testing.T, preset, converted equivalenceTwin, cfg equivalenceConfig, kind string, callers []equivalenceCaller, exact bool, tally *equivalenceTally) {
	t.Helper()
	tables := []string{"entries"}
	if len(cfg.tables) != 0 {
		tables = append(tables, "overridden")
	}
	for _, table := range tables {
		for _, caller := range callers {
			for _, op := range equivalenceOperations() {
				if caller.only != "" && caller.only != op.name {
					continue
				}
				tally.callerNames[caller.name] = true
				c := caller.call
				if !caller.suspended {
					c.ActiveChecked = true
				}
				equivalenceReset(t, preset, kind, table)
				equivalenceReset(t, converted, kind, table)
				*preset.events, *converted.events = nil, nil
				oldBody, oldRefusal := equivalenceRun(t, preset, c, kind, table, op)
				newBody, newRefusal := equivalenceRun(t, converted, c, kind, table, op)
				oldCode, newCode := oldRefusal.Code, newRefusal.Code
				oldEvents, newEvents := *preset.events, *converted.events
				oldState := equivalenceSnapshot(t, preset, kind, table)
				newState := equivalenceSnapshot(t, converted, kind, table)
				tally.comparisons++
				if oldCode == "" {
					tally.successes[kind+"/preset/"+op.name]++
				}
				if newCode == "" {
					tally.successes[kind+"/converted/"+op.name]++
				}
				// The entire approved difference: both refuse, and only a suspended
				// non-owner's owner-valued action may change its first refusal code.
				m := preset.call.Resource.TableMatrix(table)
				action := op.action
				// An ordinary KV set of a missing key is an add,
				// including the edit/missing probe. AI tools instead
				// send their explicit add/edit action above.
				if kind == "kv" && op.name == "edit" && op.id == 99 && !c.VisitorAI {
					action = "add"
				}
				approved := !exact && caller.suspended && m.Value(action) == model.WhoOwner && (oldCode == "forbidden" || oldCode == "add_only") && newCode == "account_suspended"
				// Under the approved difference the two refusals are different
				// refusals, so their wording differs with the code.
				sameRefusal := oldRefusal == newRefusal
				if !exact && !sameRefusal && !approved && equivalenceWordingApproved(oldRefusal, newRefusal, m.Value(action), m.Read) {
					sameRefusal = true
					tally.wording++
				}
				sameEvents := reflect.DeepEqual(oldEvents, newEvents)
				if (!approved && !sameRefusal) || !reflect.DeepEqual(oldBody, newBody) || !reflect.DeepEqual(oldState, newState) || !sameEvents {
					t.Fatalf("table=%s caller=%s operation=%s id=%d: preset refusal=%+v body=%#v state=%#v events=%#v; converted refusal=%+v body=%#v state=%#v events=%#v", table, caller.name, op.name, op.id, oldRefusal, oldBody, oldState, oldEvents, newRefusal, newBody, newState, newEvents)
				}
			}
		}
	}
}

func TestPresetServiceEquivalence(t *testing.T) {
	db := admissionDB(t)
	var suspended string
	if err := db.QueryRow(`INSERT INTO users(username,suspended_at) VALUES(gen_random_uuid()::text,now()) RETURNING id`).Scan(&suspended); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Exec(`DELETE FROM users WHERE id=$1`, suspended) })
	totalConfigs, totalComparisons, totalWording := 0, 0, 0
	// Operations that succeeded, per kind, twin and operation: a fixture in
	// which nothing ever works must not pass as "both refused alike".
	totalSuccesses := map[string]int{}
	totalCallerNames := map[string]bool{}
	// The two halves share nothing but the suspended account (read only) and
	// these totals, so they run side by side; the group returns when both end.
	var mu sync.Mutex
	t.Run("kinds", func(t *testing.T) {
		for _, kind := range []string{"sqlite", "kv"} {
			t.Run(kind, func(t *testing.T) {
				t.Parallel()
				configs := 0
				tally := equivalenceTally{successes: map[string]int{}, callerNames: map[string]bool{}}
				defer func() {
					mu.Lock()
					defer mu.Unlock()
					totalConfigs, totalComparisons, totalWording = totalConfigs+configs, totalComparisons+tally.comparisons, totalWording+tally.wording
					for k, v := range tally.successes {
						totalSuccesses[k] += v
					}
					for k := range tally.callerNames {
						totalCallerNames[k] = true
					}
				}()
				preset, converted := equivalenceFixture(t, kind), equivalenceFixture(t, kind)
				equivalenceSaveRefusals(t, preset, kind)
				// Both sites belong to the same real account, so refreshed owner
				// authority and the stored attribution use identical identities.
				if _, err := db.Exec(`UPDATE sites SET user_id=$1,name='equivalence-converted' WHERE id=$2`, preset.call.OwnerID, converted.call.SiteID); err != nil {
					t.Fatal(err)
				}
				converted.call.OwnerID, converted.call.SiteName = preset.call.OwnerID, "equivalence-converted"
				callers := equivalenceCallers(kind, suspended)
				for _, cfg := range equivalenceConfigs(kind) {
					t.Run(cfg.name, func(t *testing.T) {
						equivalenceSave(t, &preset, cfg, false)
						equivalenceSave(t, &converted, cfg, true)
						configs++
						equivalenceCompare(t, preset, converted, cfg, kind, callers, false, &tally)
					})
					if t.Failed() {
						return
					}
				}
			})
		}
	})
	if t.Failed() {
		return
	}
	for _, kind := range []string{"sqlite", "kv"} {
		for _, twin := range []string{"preset", "converted"} {
			for _, op := range []string{"list", "get", "add", "edit", "delete"} {
				if totalSuccesses[kind+"/"+twin+"/"+op] == 0 {
					t.Fatalf("%s %s: %s never succeeded for any configuration or caller", kind, twin, op)
				}
			}
		}
	}
	// "gate-summary:" lines of passing tests are printed by
	// scripts/lib/summarize-go-test.py, so this line is in the gate's record.
	t.Logf("gate-summary: R3-S5a configurations=%d callers=%d comparisons=%d mismatches=0 refusal_wording_approved=%d", totalConfigs, len(totalCallerNames), totalComparisons, totalWording)
}

// The golden `converted` scenario (scripts/golden/run.mjs) saves its converted
// twin with a literal copy of Convert(public). This keeps the copy honest.
func TestGoldenConvertedScenarioRules(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("..", "..", "..", "scripts", "golden", "run.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	const start, end = "// golden-converted-rules:begin (JSON)\n", "// golden-converted-rules:end\n"
	_, rest, ok := strings.Cut(string(src), start)
	literal, _, ok2 := strings.Cut(rest, end)
	if !ok || !ok2 {
		t.Fatal("run.mjs: the converted scenario's rules markers are missing")
	}
	literal = strings.TrimSuffix(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(literal), "const rules =")), ";")
	var got, want any
	if err := json.Unmarshal([]byte(literal), &got); err != nil {
		t.Fatalf("run.mjs: the converted scenario's rules are not JSON: %v", err)
	}
	m, _ := model.PresetMatrix("public")
	for _, kind := range []string{"sqlite", "kv"} {
		out := ruleengine.Convert(ruleengine.ConvertInput{Kind: kind, Default: ruleengine.Matrix(m)})
		if len(out.Manual) != 0 {
			t.Fatal(out.Manual)
		}
		if err := json.Unmarshal(ruleengine.EncodeSet(out.Default), &want); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%s: run.mjs saves %s, Convert(public) is %s", kind, literal, ruleengine.EncodeSet(out.Default))
		}
	}
}
