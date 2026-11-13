package storage

import (
	"bytes"
	"encoding/json"
	"fmt"
	"testing"

	ruleengine "github.com/vsriram/simple-host/host/storage/rules"
	"github.com/vsriram/simple-host/internal/model"
	runtime "github.com/vsriram/simple-host/internal/storage"
)

var conversionActions = []struct {
	name string
	op   ruleengine.Op
}{{"read", ruleengine.OpRead}, {"add", ruleengine.OpAdd}, {"edit", ruleengine.OpEdit}, {"delete", ruleengine.OpDelete}}

func conversionValues(c Call) ruleengine.Values {
	id, role := ruleengine.Null(), "anon"
	if c.VisitorID != "" {
		id, role = ruleengine.Text(c.VisitorID), "visitor"
	}
	if c.OwnerOnSite {
		role = "owner"
	}
	return ruleengine.Values{"auth.id": id, "auth.role": ruleengine.Text(role), "auth.signed_in": ruleengine.Bool(c.VisitorID != ""),
		"auth.ai": ruleengine.Bool(c.VisitorAI), "auth.checked": ruleengine.Bool(c.SpamCheck == "ok"), "auth.teams": ruleengine.List()}
}

func conversionPlan(t *testing.T, m model.AccessMatrix, hold bool, kind string) (ruleengine.PolicySet, *ruleengine.Plan) {
	t.Helper()
	out := ruleengine.Convert(ruleengine.ConvertInput{Default: ruleengine.Matrix(m), ApproveFirst: hold, Kind: kind})
	if len(out.Manual) != 0 {
		t.Fatal(out.Manual)
	}
	if kind == "sqlite" {
		// A checked default makes approve_first a legal save even for an
		// override whose add is not checked. The effective override must be
		// exactly the set tested here, independent of the resource default.
		override := ruleengine.Convert(ruleengine.ConvertInput{
			Default: ruleengine.Matrix{Read: "anyone", Add: "checked", Edit: "owner", Delete: "owner"},
			Tables:  map[string]ruleengine.Matrix{"entries": ruleengine.Matrix(m)}, ApproveFirst: hold, Kind: kind,
		})
		if len(override.Manual) != 0 || !bytes.Equal(ruleengine.EncodeSet(out.Default), ruleengine.EncodeSet(override.Tables["entries"])) {
			t.Fatalf("table override differs from the equivalent default: %+v", override)
		}
	}
	table := ruleengine.Table{Name: "entries", RowKey: "rowid", Columns: []ruleengine.Column{{Name: "visitor_id"}, {Name: "approved_at"}}}
	if kind == "kv" {
		table.RowKey = `"key"`
		table.Columns = []ruleengine.Column{{Name: "key"}, {Name: "value"}, {Name: "visitor_id"}}
	}
	p, errs := ruleengine.Compile(out.Default, table, PlatformSchema(true), ruleengine.DefaultLimits())
	if len(errs) != 0 {
		t.Fatal(errs)
	}
	return out.Default, p
}

// conversionRowDecision deliberately composes read visibility, the operation,
// and the same mandatory AI predicate that enforcement adds to its statements.
func conversionRowDecision(t *testing.T, p *ruleengine.Plan, op ruleengine.Op, c Call, row ruleengine.Row) bool {
	t.Helper()
	v := conversionValues(c)
	if op == ruleengine.OpAdd {
		ok, err := p.EvalCheck(op, v, row)
		if err != nil {
			t.Fatal(err)
		}
		return ok
	}
	ok, err := p.EvalUsing(ruleengine.OpRead, v, row)
	if err != nil {
		t.Fatal(err)
	}
	if op != ruleengine.OpRead {
		change, err := p.EvalUsing(op, v, row)
		if err != nil {
			t.Fatal(err)
		}
		ok = ok && change
	}
	ai := ruleAIConstraint(op, c)
	if len(ai.Args) != 0 {
		ok = ok && row["visitor_id"].Kind == ruleengine.KindText && row["visitor_id"].Text == ai.Args[0].Text
	}
	return ok
}

func TestConvertEquivalence(t *testing.T) {
	values := []string{model.WhoNobody, model.WhoOwner, model.WhoOwn, model.WhoSignedIn, model.WhoChecked, model.WhoAnyone}
	callers := []struct {
		name string
		call Call
	}{
		{"anonymous", Call{}},
		{"signed-in-non-author", Call{VisitorID: "other"}},
		{"author", Call{VisitorID: "author"}},
		{"owner-on-site", Call{VisitorID: "owner", OwnerOnSite: true}},
		{"visitor-AI-author", Call{VisitorID: "author", VisitorAI: true}},
		{"visitor-AI-non-author", Call{VisitorID: "other", VisitorAI: true}},
	}
	var matrices, configurations, decisions, rowChecks, mismatches int
	s := &Service{}
	for _, read := range values {
		for _, add := range values {
			for _, edit := range values {
				for _, del := range values {
					m := model.AccessMatrix{Read: read, Add: add, Edit: edit, Delete: del}
					if code, _ := m.Check(); code != "" {
						continue
					}
					matrices++
					for _, kind := range []string{"sqlite", "kv"} {
						for _, hold := range []bool{false, true} {
							if hold && (add != model.WhoChecked || kind != "sqlite") {
								continue
							}
							configurations++
							set, p := conversionPlan(t, m, hold, kind)
							for _, caller := range callers {
								for _, spam := range []string{"", "ok", "spam_check_failed"} {
									for _, action := range conversionActions {
										c := caller.call
										c.ActiveChecked, c.SpamCheck, c.CurrentMatrix = true, spam, m
										c.Resource = model.Resource{Access: &m, Kind: kind, ApproveFirst: hold}
										filter, oldAllowed, refusals := s.allowDecisions(&c, action.name, m.Value(action.name))
										oldCode := ""
										if len(refusals) > 0 {
											oldCode = refusals[0].Code
										}
										newAllowed, newCode := rulePermission(p, set, action.op, conversionValues(c), spam)
										decisions++
										label := fmt.Sprintf("matrix=%+v kind=%s hold=%v caller=%s spam=%q action=%s", m, kind, hold, caller.name, spam, action.name)
										if oldAllowed != newAllowed {
											mismatches++
											t.Errorf("permission %s old=%v new=%v", label, oldAllowed, newAllowed)
										}
										if oldCode != newCode {
											mismatches++
											t.Errorf("refusal %s old=%q new=%q", label, oldCode, newCode)
										}
										rows := []struct {
											name             string
											author, approval ruleengine.Value
										}{
											{"own", ruleengine.Text(c.VisitorID), ruleengine.Text("approved")},
											{"other", ruleengine.Text("another"), ruleengine.Text("approved")},
											{"no-author", ruleengine.Null(), ruleengine.Text("approved")},
											{"held", ruleengine.Text(c.VisitorID), ruleengine.Null()},
										}
										for _, item := range rows {
											rowChecks++
											row := ruleengine.Row{"visitor_id": item.author, "approved_at": item.approval}
											oldVisible := oldAllowed
											if action.op == ruleengine.OpAdd {
												body := map[string]json.RawMessage{"visitor_id": json.RawMessage(`"forged"`)}
												stampInsertValues(map[string]string{"visitor_id": "TEXT", "approved_at": "TEXT"}, body, InsertActor{OwnerOnSite: c.OwnerOnSite, Hold: hold, StampID: c.VisitorID})
												row["visitor_id"] = ruleengine.Null()
												var author string
												if json.Unmarshal(body["visitor_id"], &author) == nil && author != "" {
													row["visitor_id"] = ruleengine.Text(author)
												}
												if hold {
													row["approved_at"] = ruleengine.Null()
													if c.OwnerOnSite {
														row["approved_at"] = ruleengine.Text("approved")
													}
												}
											} else {
												oldVisible = oldVisible && (filter == "" || item.author.Kind == ruleengine.KindText && item.author.Text == filter)
												oldVisible = oldVisible && (!c.Resource.Holds("entries") || c.OwnerOnSite || item.approval.Kind != ruleengine.KindNull)
											}
											newVisible := newAllowed && conversionRowDecision(t, p, action.op, c, row)
											if oldVisible != newVisible {
												mismatches++
												t.Errorf("row %s row=%s old=%v new=%v", label, item.name, oldVisible, newVisible)
											}
										}
									}
								}
							}
						}
					}
				}
			}
		}
	}
	t.Logf("matrices=%d configurations=%d permission/refusal comparisons=%d row comparisons=%d mismatches=%d", matrices, configurations, decisions, rowChecks, mismatches)
}

func TestConvertNamedRefusals(t *testing.T) {
	for _, tc := range []struct {
		name, preset string
		matrix       model.AccessMatrix
		call         Call
		want         string
	}{
		{name: "anyone-add-with-no-change-policy", matrix: model.AccessMatrix{Read: model.WhoAnyone, Add: model.WhoAnyone, Edit: model.WhoNobody, Delete: model.WhoNobody}, want: "forbidden"},
		{name: "comments-without-spam-check", preset: "comments", call: Call{VisitorID: "visitor"}, want: "add_only"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := tc.matrix
			if tc.preset != "" {
				m, _ = model.PresetMatrix(tc.preset)
			}
			set, p := conversionPlan(t, m, false, "sqlite")
			for _, action := range conversionActions[2:] {
				c := tc.call
				c.ActiveChecked, c.CurrentMatrix = true, m
				_, oldAllowed, refusals := (&Service{}).allowDecisions(&c, action.name, m.Value(action.name))
				allowed, code := rulePermission(p, set, action.op, conversionValues(c), "")
				if oldAllowed || allowed || len(refusals) == 0 || refusals[0].Code != tc.want || code != tc.want {
					t.Fatalf("%s: old=%v/%v new=%v/%s want=%s", action.name, oldAllowed, refusals, allowed, code, tc.want)
				}
			}
		})
	}
}

func TestConvertCommentsExistingKeyWithoutSpamCheck(t *testing.T) {
	m, _ := model.PresetMatrix("comments")
	_, p := conversionPlan(t, m, false, "kv")
	for _, id := range []string{"", "visitor"} {
		c := Call{VisitorID: id, Resource: model.Resource{Access: &m, Kind: "kv"}}
		_, oldErr := kvAction(true, kvTaken(c))
		if oldErr == nil || oldErr.(*Error).Code != "key_exists" {
			t.Fatalf("old: %v", oldErr)
		}
		v := conversionValues(c)
		if p.Partial(ruleengine.OpAdd, v) != ruleengine.False {
			t.Fatal("fixture passed spam check")
		}
		checked := cloneRuleValues(v)
		checked["auth.checked"] = ruleengine.Bool(true)
		newCode := ""
		if p.Partial(ruleengine.OpEdit, v) == ruleengine.False && p.Partial(ruleengine.OpAdd, checked) != ruleengine.False {
			newCode = "key_exists"
		}
		if newCode != oldErr.(*Error).Code {
			t.Fatalf("caller=%q old=%v new=%s", id, oldErr, newCode)
		}
	}
}

func TestConvertBoardAIChangeOthers(t *testing.T) {
	m, _ := model.PresetMatrix("board")
	set, p := conversionPlan(t, m, false, "sqlite")
	c := Call{VisitorID: "visitor", VisitorAI: true, ActiveChecked: true, CurrentMatrix: m}
	row := ruleengine.Row{"visitor_id": ruleengine.Text("other"), "approved_at": ruleengine.Text("approved")}
	for _, action := range conversionActions[2:] {
		filter, oldAllowed, _ := (&Service{}).allowDecisions(&c, action.name, m.Value(action.name))
		allowed, _ := rulePermission(p, set, action.op, conversionValues(c), "")
		oldVisible := oldAllowed && (filter == "" || filter == row["visitor_id"].Text)
		newVisible := allowed && conversionRowDecision(t, p, action.op, c, row)
		if oldVisible || newVisible {
			t.Fatalf("%s changed another author's row", action.name)
		}
		if action.op == ruleengine.OpEdit {
			using, err := p.EvalUsing(action.op, conversionValues(c), row)
			if err != nil || !using {
				t.Fatalf("fixture must require AI constraint: %v %v", using, err)
			}
		}
	}
	// Exercise the actual SQL from the shared helper, including delete even
	// though board's owner-only delete refuses this visitor before row selection.
	conn := ruleTestConn(t)
	ruleTestExec(t, conn, `CREATE TABLE entries(visitor_id TEXT); INSERT INTO entries VALUES ('visitor'), ('other'), (NULL)`)
	for _, op := range []ruleengine.Op{ruleengine.OpEdit, ruleengine.OpDelete} {
		constraint := ruleAIConstraint(op, c)
		stmt := ruleTestStmt(t, conn, "SELECT visitor_id FROM entries WHERE "+constraint.SQL, constraint.Args, nil)
		if !stmt.Step() || stmt.ColumnText(0) != "visitor" || stmt.Step() || stmt.Err() != nil {
			t.Fatal("AI predicate did not select only its author")
		}
		if err := stmt.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestConvertAddServerColumnsAndPrimaryKey(t *testing.T) {
	conn := ruleTestConn(t)
	ruleTestExec(t, conn, `CREATE TABLE entries(id INTEGER PRIMARY KEY, visitor_id TEXT, created_at TEXT, updated_at TEXT, body TEXT)`)
	m, _ := model.PresetMatrix("personal")
	set, p := conversionPlan(t, m, false, "sqlite")
	c := Call{VisitorID: "visitor", ActiveChecked: true, CurrentMatrix: m, Resource: model.Resource{Access: &m, Kind: "sqlite"}}
	meta, err := tableMetadata(conn, c, "entries", "")
	if err != nil {
		t.Fatal(err)
	}
	marks := 0
	s := New(nil, Deps{MarkChanged: func() { marks++ }})
	values := map[string]json.RawMessage{"visitor_id": json.RawMessage(`"forged"`), "created_at": json.RawMessage(`"forged"`), "updated_at": json.RawMessage(`"forged"`), "body": json.RawMessage(`"hello"`)}
	allowed, code := rulePermission(p, set, ruleengine.OpAdd, conversionValues(c), "")
	if !allowed || code != "" {
		t.Fatalf("new permission: %v/%s", allowed, code)
	}
	var id int64
	if err := s.addTableRow(conn, c, TableInput{Table: "entries", Values: values}, meta, &Admission{}, &id); err != nil {
		t.Fatal(err)
	}
	stmt := ruleTestStmt(t, conn, `SELECT visitor_id, created_at, updated_at FROM entries WHERE id = ?`, []ruleengine.Value{ruleengine.Int(id)}, nil)
	if !stmt.Step() {
		t.Fatal(stmt.Err())
	}
	row := ruleengine.Row{"visitor_id": ruleTestValue(stmt, 0)}
	if stmt.ColumnText(0) != "visitor" || stmt.ColumnText(1) == "forged" || stmt.ColumnText(1) == "" || ruleTestValue(stmt, 2).Kind != ruleengine.KindNull {
		t.Fatal("server columns were not replaced/dropped")
	}
	if err := stmt.Close(); err != nil {
		t.Fatal(err)
	}
	if !conversionRowDecision(t, p, ruleengine.OpAdd, c, row) {
		t.Fatal("stamped add refused")
	}
	for _, key := range []string{"id", "rowid", "_rowid_", "oid"} {
		body := map[string]json.RawMessage{key: json.RawMessage(`42`)}
		err := s.addTableRow(conn, c, TableInput{Table: "entries", Values: body}, meta, &Admission{}, &id)
		if err == nil || err.(*Error).Code != "invalid_rows" || marks != 1 {
			t.Fatalf("%s: %v marks=%d", key, err, marks)
		}
		// Rules enforcement retains this same server-ID guard before stamping.
		supplied, err := runtime.InsertSuppliesID(conn, "entries", body)
		if err != nil || !supplied {
			t.Fatalf("rules ID guard: %s %v %v", key, supplied, err)
		}
	}
}

func TestConvertLegacyAnonymousChangesAreManual(t *testing.T) {
	for _, kind := range []string{"sqlite", "kv"} {
		for _, read := range []string{model.WhoAnyone, model.WhoSignedIn, model.WhoOwner, model.WhoOwn} {
			resource := model.Resource{Kind: kind, Read: read, Write: model.WhoAnyone, WriteMode: "full"}
			m := resource.Matrix() // Access == nil exercises LegacyMatrix.
			out := ruleengine.Convert(ruleengine.ConvertInput{Default: ruleengine.Matrix(m), Kind: kind})
			manual := m.Edit == model.WhoAnyone || m.Delete == model.WhoAnyone
			if manual != (len(out.Manual) > 0) || manual && (out.Default.Policies != nil || out.Tables != nil) {
				t.Fatalf("%s/%s: %+v", kind, read, out)
			}
		}
	}
}
