package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	sqlite3 "github.com/ncruces/go-sqlite3"
	"github.com/vsriram/simple-host/host/model"
	runtime "github.com/vsriram/simple-host/host/storage"
	ruleengine "github.com/vsriram/simple-host/host/storage/rules"
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

type equivalenceOperation struct {
	name, action string
	id           int64
	query        url.Values
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
