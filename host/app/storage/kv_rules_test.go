package storage

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	runtime "github.com/vsriram/simple-host/host/storage"
	ruleengine "github.com/vsriram/simple-host/host/storage/rules"
)

func kvRulesFixture(t *testing.T, set *json.RawMessage) (*Service, Call) {
	t.Helper()
	s, c := kvFixture(t, "sqlite", false)
	s.deps.RulesEnabled = true
	s.deps.SpamCheckAvailable = true
	s.deps.PasscodeAllows = func() bool { return true }
	s.deps.ViewersAllow = func() bool { return true }
	c.Resource = putRules(t, s, ResourceInput{Call: c, Name: c.ResourceName, Kind: "kv", Rules: set})
	c.Owner, c.CSRFOK, c.ActiveChecked = false, true, true
	c.VisitorID = "writer"
	s.deps.RulesEnabled = false // Saved rules are enforced even with the save switch off.
	return s, c
}

func kvRuleSet(read, add, edit, check, del string) *json.RawMessage {
	return saveSet(ruleengine.Policy{Command: ruleengine.CmdRead, Using: read}, ruleengine.Policy{Command: ruleengine.CmdAdd, Check: add}, ruleengine.Policy{Command: ruleengine.CmdEdit, Using: edit, Check: check}, ruleengine.Policy{Command: ruleengine.CmdDelete, Using: del})
}

func kvRulesPut(t *testing.T, s *Service, c Call, key, value, requested, want string) {
	t.Helper()
	_, auth, err := s.PutKV(context.Background(), KVPutInput{Call: c, Key: key, Requested: requested, Body: strings.NewReader(`{"value":` + value + `}`)})
	if got := readCode(TableResult{Authorization: auth}, err); got != want {
		t.Fatalf("put %s: got %s want %s (%v)", key, got, want, err)
	}
}

func kvRulesGet(t *testing.T, s *Service, c Call, key, want string) VisibleKVItem {
	t.Helper()
	out, err := s.GetKV(context.Background(), KVReadInput{Call: c, Key: key})
	if got := readCode(TableResult{Authorization: out.Authorization}, err); got != want {
		t.Fatalf("get %s: got %s want %s", key, got, want)
	}
	return out.Item
}

func kvRulesDelete(t *testing.T, s *Service, c Call, key, want string) {
	t.Helper()
	auth, err := s.DeleteKV(context.Background(), KVDeleteInput{Call: c, Key: key})
	if got := readCode(TableResult{Authorization: auth}, err); got != want {
		t.Fatalf("delete %s: got %s want %s", key, got, want)
	}
}

func kvRulesSnapshot(t *testing.T, c Call) ([]runtime.KVSnapshotEntry, runtime.KVMeta) {
	t.Helper()
	rows, meta, err := runtime.ReadKVSnapshot(context.Background(), runtime.RuntimeKVPath(c.RuntimeDir, c.ResourceName))
	if err != nil {
		t.Fatal(err)
	}
	return rows, meta
}

func TestKVRulesKeyValueVisibilityAndPaging(t *testing.T) {
	for _, expr := range []string{`key contains "visible"`, `value contains "public"`} {
		t.Run(expr, func(t *testing.T) {
			s, c := kvRulesFixture(t, kvRuleSet(expr, "true", "auth.signed_in", "auth.signed_in", "auth.signed_in"))
			for _, entry := range []struct{ k, v string }{{"a-hidden", `"private"`}, {"b-visible", `"public"`}, {"c-hidden", `"private"`}, {"d-visible", `"public"`}} {
				kvRulesPut(t, s, c, entry.k, entry.v, "", "")
			}
			kvRulesGet(t, s, c, "a-hidden", "key_not_found")
			kvRulesGet(t, s, c, "missing", "key_not_found")
			c.VisitorID = "other"
			got := kvRulesGet(t, s, c, "b-visible", "")
			if got.VisitorID != "" || got.Mine == nil || *got.Mine {
				t.Fatalf("author leaked: %+v", got)
			}
			out, err := s.ListKV(context.Background(), KVListInput{Call: c, Limit: 1, ValidLimit: true})
			if err != nil || out.Authorization != nil || len(out.Items) != 1 || out.Items[0].Key != "b-visible" || out.Next != "b-visible" {
				t.Fatalf("first page %+v %v", out, err)
			}
			out, err = s.ListKV(context.Background(), KVListInput{Call: c, Limit: 1, ValidLimit: true, After: out.Next})
			if err != nil || len(out.Items) != 1 || out.Items[0].Key != "d-visible" || out.Next != "" {
				t.Fatalf("second page %+v %v", out, err)
			}
			kvRulesPut(t, s, c, "a-hidden", `"public"`, "", "key_exists")
			kvRulesDelete(t, s, c, "a-hidden", "key_not_found")
		})
	}
}

func TestKVRulesLiteralPrefixPaging(t *testing.T) {
	s, c := kvRulesFixture(t, kvRuleSet(`value == "1"`, "true", "auth.signed_in", "auth.signed_in", "auth.signed_in"))
	prefixes := []string{"%", "_", `\`, `x%_\`, "A", "a", "é", "ÿ", "中", "😀", "\U0010ffff"}
	var visible []string
	for _, prefix := range prefixes {
		for _, suffix := range []string{"a", "b", "c"} {
			value := "1"
			if suffix == "b" {
				value = "0"
			} else {
				visible = append(visible, prefix+suffix)
			}
			kvRulesPut(t, s, c, prefix+suffix, value, "", "")
		}
	}
	// These would match wildcard-based implementations of the literal prefixes.
	for _, key := range []string{"xAAone", "z"} {
		kvRulesPut(t, s, c, key, "1", "", "")
		visible = append(visible, key)
	}
	sort.Strings(visible)
	for _, prefix := range append(prefixes, "", "missing") {
		t.Run(prefix, func(t *testing.T) {
			var want []string
			for _, key := range visible {
				if strings.HasPrefix(key, prefix) {
					want = append(want, key)
				}
			}
			var got []string
			after := ""
			for page := 0; ; page++ {
				if page > len(visible) {
					t.Fatal("cursor did not reach the end")
				}
				out, err := s.ListKV(context.Background(), KVListInput{Call: c, Prefix: prefix, After: after, Limit: 1, ValidLimit: true})
				if err != nil || out.Authorization != nil || len(out.Items) > 1 {
					t.Fatalf("page: %+v %v", out, err)
				}
				for _, item := range out.Items {
					got = append(got, item.Key)
				}
				if out.Next == "" {
					break
				}
				if len(out.Items) != 1 || out.Next != out.Items[0].Key || out.Next <= after {
					t.Fatalf("invalid next cursor: %+v", out)
				}
				after = out.Next
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("keys: got %q want %q", got, want)
			}
		})
	}
}

func TestKVRulesListPrefixRangeQueryPlan(t *testing.T) {
	conn, done, err := runtime.OpenRuntimeKV(context.Background(), filepath.Join(t.TempDir(), "list.sqlite"), true, true)
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	predicate := ruleengine.Predicate{SQL: `visitor_id=? OR value=?`, Args: []ruleengine.Value{ruleengine.Text("writer"), ruleengine.Text("1")}}
	for _, tc := range []struct{ prefix, after, lower string }{
		{"", "", "key>''"},
		{"", "z", "key>'z'"},
		{"missing", "", "key>='missing'"},
		{`x%_\`, "", `key>='x%_\'`},
		{"é", "a", "key>='é'"},
		{"é", "é", "key>'é'"},
		{"é", "éa", "key>'éa'"},
		{"é", "中", "key>'中'"},
		{"ÿ", "", "key>='ÿ'"},
		{"\U0010ffff", "", "key>='\U0010ffff'"},
	} {
		t.Run(tc.prefix+"/"+tc.after, func(t *testing.T) {
			query, args := kvRuleListQuery(tc.prefix, tc.after, 100, predicate)
			stmt, err := kvRuleStatement(conn, query, args)
			if err != nil {
				t.Fatal(err)
			}
			expanded := stmt.ExpandedSQL()
			stmt.Close()
			if !strings.Contains(expanded, "WHERE "+tc.lower) {
				t.Fatalf("effective lower bound missing: %s", expanded)
			}
			plan, err := kvRuleStatement(conn, "EXPLAIN QUERY PLAN "+query, args)
			if err != nil {
				t.Fatal(err)
			}
			defer plan.Close()
			var detail string
			for plan.Step() {
				detail += plan.ColumnText(3) + "\n"
			}
			if err := plan.Err(); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(detail, "SEARCH kv USING PRIMARY KEY (key>?") || strings.Contains(detail, "SCAN") || strings.Contains(detail, "TEMP B-TREE") {
				t.Fatalf("expected ordered index range: %s", detail)
			}
			if tc.prefix != "" && !strings.Contains(detail, "AND key<?") {
				t.Fatalf("prefix upper bound missing: %s", detail)
			}
		})
	}
}

func TestKVRulesAnonymousAndEmptyAuthors(t *testing.T) {
	s, c := kvRulesFixture(t, kvRuleSet(`visitor_id == auth.id`, "true", "visitor_id == auth.id", "visitor_id == auth.id", "visitor_id == auth.id"))
	owner := c
	owner.Owner = true
	kvRulesPut(t, s, owner, "moved-empty-author", `1`, "", "")
	kvRulesPut(t, s, c, "own", `1`, "", "")
	c.VisitorID = ""
	kvRulesGet(t, s, c, "moved-empty-author", "sign_in_required")
	out, err := s.ListKV(context.Background(), KVListInput{Call: c, Limit: 10, ValidLimit: true})
	if readCode(TableResult{Authorization: out.Authorization}, err) != "sign_in_required" || len(out.Items) != 0 {
		t.Fatalf("anonymous list %+v %v", out, err)
	}
	kvRulesPut(t, s, c, "own", `2`, "", "sign_in_required")
	kvRulesDelete(t, s, c, "own", "sign_in_required")
	// The guard also applies when edit and delete use signed-in rather than own.
	s, c = kvRulesFixture(t, kvRuleSet("true", "true", "auth.signed_in", "auth.signed_in", "auth.signed_in"))
	kvRulesPut(t, s, c, "key", `1`, "", "")
	c.VisitorID = ""
	// Existing-key fallback precedes the ordinary sign-in refusal.
	kvRulesPut(t, s, c, "key", `2`, "", "key_exists")
	kvRulesDelete(t, s, c, "key", "sign_in_required")
}

func TestKVRulesFailedChecksRollbackAndKeepAuthor(t *testing.T) {
	s, c := kvRulesFixture(t, kvRuleSet("true", `value == "1"`, "auth.signed_in", `value == "2"`, "auth.signed_in"))
	rows, meta := kvRulesSnapshot(t, c)
	kvRulesPut(t, s, c, "bad", `9`, "", "forbidden")
	after, afterMeta := kvRulesSnapshot(t, c)
	if !reflect.DeepEqual(rows, after) || meta != afterMeta {
		t.Fatal("failed insert changed data or kv_meta")
	}
	kvRulesPut(t, s, c, "key", `1`, "", "")
	rows, meta = kvRulesSnapshot(t, c)
	kvRulesPut(t, s, c, "key", `9`, "", "key_exists")
	after, afterMeta = kvRulesSnapshot(t, c)
	if !reflect.DeepEqual(rows, after) || meta != afterMeta {
		t.Fatal("failed edit changed data or kv_meta")
	}
	var event string
	var data map[string]any
	s.deps.Notify = func(_ Call, name string, fields map[string]any) { event, data = name, fields }
	c.VisitorID = "editor"
	kvRulesPut(t, s, c, "key", `2`, "", "")
	after, _ = kvRulesSnapshot(t, c)
	if after[0].Writer != "writer" || event != "record.changed" || data["row_visitor_id"] != "writer" || data["owner_write"] != false {
		t.Fatalf("author/notification changed: %+v %s %+v", after, event, data)
	}
	owner := c
	owner.Owner = true
	kvRulesPut(t, s, owner, "key", `3`, "", "")
	if data["row_visitor_id"] != "writer" || data["owner_write"] != true {
		t.Fatalf("owner attribution %+v", data)
	}
	kvRulesDelete(t, s, c, "key", "")
	if data["row_visitor_id"] != "writer" || data["change"] != "deleted" {
		t.Fatalf("delete attribution %+v", data)
	}
	_, afterMeta = kvRulesSnapshot(t, c)
	if afterMeta.Entries != 0 || afterMeta.LogicalBytes != 0 {
		t.Fatal(afterMeta)
	}
}

func TestKVRulesExistingCommentsBeforeSpamAndSuspension(t *testing.T) {
	converted := ruleengine.Convert(ruleengine.ConvertInput{Kind: "kv", Default: ruleengine.Matrix{Read: "anyone", Add: "checked", Edit: "owner", Delete: "owner"}})
	if len(converted.Manual) > 0 {
		t.Fatal(converted.Manual)
	}
	raw := json.RawMessage(ruleengine.EncodeSet(converted.Default))
	s, c := kvRulesFixture(t, &raw)
	c.SpamCheck = "ok"
	s.deps.SpamCheckedMaxBytes = 100
	s.deps.SpamCheckedMaxLinks = 1
	kvRulesPut(t, s, c, "key", `1`, "", "")
	c.SpamCheck = ""
	kvRulesPut(t, s, c, "key", `2`, "", "key_exists")
	c.ActiveChecked = false
	c.VisitorID = c.OwnerID
	c.VisitorSessionUserID = c.OwnerID
	if _, err := s.db.Exec(`UPDATE users SET suspended_at=now() WHERE id=$1`, c.OwnerID); err != nil {
		t.Fatal(err)
	}
	kvRulesPut(t, s, c, "key", `2`, "", "key_exists")
	kvRulesPut(t, s, c, "new", `2`, "", "account_suspended")
}

func TestKVRulesVisitorAIActionAndOwnConstraint(t *testing.T) {
	s, c := kvRulesFixture(t, kvRuleSet("true", "true", "auth.signed_in", "auth.signed_in", "auth.signed_in"))
	kvRulesPut(t, s, c, "key", `1`, "", "")
	c.VisitorAI = true
	kvRulesPut(t, s, c, "key", `2`, "add", "key_exists")
	kvRulesPut(t, s, c, "missing", `2`, "edit", "key_not_found")
	kvRulesPut(t, s, c, "key", `2`, "edit", "")
	c.VisitorID = "other"
	kvRulesPut(t, s, c, "key", `3`, "edit", "key_exists")
	// The AI author term makes this delete depend on the row, so it answers
	// as today's author-filtered delete does: no change, no event. Before
	// R3-S5a this answered success; the reversal is R3-S5a ruling 2,
	// approved 2026-11-15 (a production change in kv_sqlite.go).
	kvRulesDelete(t, s, c, "key", "key_not_found")
	kvRulesDelete(t, s, c, "absent", "key_not_found")
	owner := c
	owner.Owner = true
	if string(kvRulesGet(t, s, owner, "key", "").Value) != "2" {
		t.Fatal("AI deleted another author's key")
	}
}

func TestKVRulesCheckedPostLimitsAndMissingDelete(t *testing.T) {
	s, c := kvRulesFixture(t, kvRuleSet("true", "auth.checked", "auth.signed_in", "auth.signed_in", "auth.signed_in"))
	c.SpamCheck = "ok"
	s.deps.SpamCheckedMaxBytes = 30
	s.deps.SpamCheckedMaxLinks = 1
	kvRulesPut(t, s, c, "large", `"`+strings.Repeat("a", 30)+`"`, "", "post_too_large")
	kvRulesPut(t, s, c, "links", `"http://a http://b"`, "", "too_many_links")
	kvRulesPut(t, s, c, "ok", `"http://a"`, "", "")
	notifications := 0
	s.deps.Notify = func(Call, string, map[string]any) { notifications++ }
	kvRulesDelete(t, s, c, "missing", "")
	if notifications != 0 {
		t.Fatal("missing delete notified")
	}
	s, c = kvRulesFixture(t, kvRuleSet(`visitor_id == auth.id || auth.role == "owner"`, "true", `visitor_id == auth.id || auth.role == "owner"`, `visitor_id == auth.id || auth.role == "owner"`, `visitor_id == auth.id || auth.role == "owner"`))
	kvRulesDelete(t, s, c, "missing", "key_not_found")
}

// R3-S5a ruling 5: a delete that succeeds without removing anything (a missing
// key, by a caller whose read and delete rights do not depend on the row)
// calls the committed hook once, as the preset path does, and changes nothing.
func TestKVRulesIdleDeleteCallsCommitted(t *testing.T) {
	own := `visitor_id == auth.id || auth.role == "owner"`
	s, c := kvRulesFixture(t, kvRuleSet(own, "true", own, own, own))
	kvRulesPut(t, s, c, "kept", `1`, "", "")
	owner := c
	owner.Owner, owner.VisitorID = true, ""
	rows, meta := kvRulesSnapshot(t, c)
	committed, notifications := 0, 0
	s.deps.Committed = func(context.Context) { committed++ }
	s.deps.Notify = func(Call, string, map[string]any) { notifications++ }
	kvRulesDelete(t, s, owner, "missing", "")
	if committed != 1 || notifications != 0 {
		t.Fatalf("idle delete: committed=%d notifications=%d, want 1 and 0", committed, notifications)
	}
	afterRows, afterMeta := kvRulesSnapshot(t, c)
	if !reflect.DeepEqual(rows, afterRows) || !reflect.DeepEqual(meta, afterMeta) {
		t.Fatalf("idle delete changed the store: before %+v %+v, after %+v %+v", rows, meta, afterRows, afterMeta)
	}
	// A refused idle delete (the right depends on the row) is not a commit.
	kvRulesDelete(t, s, c, "missing", "key_not_found")
	if committed != 1 {
		t.Fatalf("refused delete called committed: %d", committed)
	}
}

func TestKVRulesQuotaAtLimit(t *testing.T) {
	s, c := kvRulesFixture(t, kvRuleSet("true", "true", "auth.signed_in", "auth.signed_in", "auth.signed_in"))
	dir := filepath.Join(c.RuntimeDir, "sqlite")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(filepath.Join(dir, "budget.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	err = f.Truncate(SiteLimitBytes("", false) - 4)
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	kvRulesPut(t, s, c, "key", `10`, "", "site_full")
	kvRulesPut(t, s, c, "key", `1`, "", "")
	kvRulesPut(t, s, c, "key", `10`, "", "site_full")
	hidden := c
	hidden.Resource.Rules = *kvRuleSet(`key == "public"`, "true", "auth.signed_in", "auth.signed_in", "auth.signed_in")
	kvRulesPut(t, s, hidden, "key", `10`, "", "key_exists")
	kvRulesDelete(t, s, c, "key", "")
	_, meta := kvRulesSnapshot(t, c)
	if meta.Entries != 0 || meta.LogicalBytes != 0 {
		t.Fatal(meta)
	}
}

func TestKVRulesPostgresSaveRefused(t *testing.T) {
	s, c := kvFixture(t, "postgres", false)
	s.deps.RulesEnabled = true
	_, err := s.PutStorageResource(context.Background(), ResourceInput{Call: c, Name: c.ResourceName, Kind: "kv", Rules: readSet("true")})
	if e, ok := err.(*Error); !ok || e.Code != "rules_unavailable" {
		t.Fatalf("%v", err)
	}
}

func TestKVRulesRejectAnonymousChangeAtSave(t *testing.T) {
	for _, op := range []ruleengine.Command{ruleengine.CmdEdit, ruleengine.CmdDelete} {
		t.Run(string(op), func(t *testing.T) {
			s, c := kvFixture(t, "sqlite", false)
			s.deps.RulesEnabled = true
			in := ResourceInput{Call: c, Name: c.ResourceName, Kind: "kv", Rules: saveSet(ruleengine.Policy{Command: op, Using: "true"})}
			ruleRefusal(t, s, in, "anonymous_change")
		})
	}
}

func TestKVRulesPostgresMarkerFailsClosed(t *testing.T) {
	s, c := kvRulesFixture(t, kvRuleSet("true", "true", "auth.signed_in", "auth.signed_in", "auth.signed_in"))
	c.Resource.KVStore = "postgres"
	kvRulesGet(t, s, c, "key", "rules_invalid")
	kvRulesPut(t, s, c, "key", `1`, "", "rules_invalid")
	kvRulesDelete(t, s, c, "key", "rules_invalid")
}

func TestKVRulesWriteBindingSnapshot(t *testing.T) {
	s, c := kvRulesFixture(t, kvRuleSet("true", "true", "auth.signed_in", "auth.signed_in", "auth.signed_in"))
	p := &readBindings{values: func(context.Context, int) (ruleengine.Values, error) {
		return ruleengine.Values{"event.member": ruleengine.Text("yes")}, nil
	}}
	s.deps.Bindings = SnapshotBindings(p)
	s.deps.RulesEnabled = true
	owner := c
	owner.Owner = true
	c.Resource = putRules(t, s, ResourceInput{Call: owner, Name: c.ResourceName, Kind: "kv", Rules: kvRuleSet("true", `event.member == "yes"`, `auth.signed_in && event.member == "yes"`, "auth.signed_in", "auth.signed_in")})
	kvRulesPut(t, s, c, "key", `1`, "", "")
	kvRulesPut(t, s, c, "key", `2`, "", "")
	if p.calls != 2 {
		t.Fatalf("bindings read %d times for two writes; each write must use one snapshot", p.calls)
	}
}
