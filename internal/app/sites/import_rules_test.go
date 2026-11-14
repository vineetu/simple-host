package sites

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	ruleengine "github.com/vsriram/simple-host/host/storage/rules"
	appstorage "github.com/vsriram/simple-host/internal/app/storage"
	"github.com/vsriram/simple-host/internal/wpimport"
)

func importAdviceFixture() *ImportManager {
	m := NewImportManager(1)
	features := []wpimport.Feature{}
	for _, kind := range []string{"contact_form", "subscribe", "comments", "guestbook", "forum"} {
		features = append(features, wpimport.Feature{Kind: kind, Status: wpimport.StatusNeedsSetup, Pages: []string{"/"}, Use: "comments preset", Setup: []wpimport.ToolCall{{Tool: "storage_set_resource", Args: map[string]any{"site": "test", "name": kind, "body": map[string]any{"kind": "sqlite", "preset": "comments", "approve_first": true}}}}})
	}
	m.Records["test"] = &ImportRecord{Owner: "owner", Site: "test", Report: &wpimport.Report{Features: features}}
	return m
}

func TestImportRulesAdvicePreservesModeration(t *testing.T) {
	t.Setenv("STORAGE_RULES", "on")
	for _, kind := range []string{wpimport.FeatureComments, wpimport.FeatureGuestbook} {
		for _, moderated := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/moderated=%t", kind, moderated), func(t *testing.T) {
				m := importAdviceFixture()
				var feature wpimport.Feature
				for _, f := range m.Records["test"].Report.Features {
					if f.Kind == kind {
						feature = f
					}
				}
				feature.Moderated = moderated
				feature.Setup[0].Args["body"].(map[string]any)["approve_first"] = moderated
				m.Records["test"].Report.Features = []wpimport.Feature{feature}
				out, err := m.Report("owner", "test")
				if err != nil {
					t.Fatal(err)
				}
				report := out["report"].(*wpimport.Report)
				body := report.Features[0].Setup[0].Args["body"].(map[string]any)
				for _, field := range []string{"preset", "approve_first"} {
					if _, ok := body[field]; ok {
						t.Fatalf("emitted setup retains %s", field)
					}
				}
				raw, err := json.Marshal(body["rules"])
				if err != nil {
					t.Fatal(err)
				}
				set, errs := ruleengine.DecodeSet(raw)
				if len(errs) != 0 {
					t.Fatal(errs)
				}
				schema := appstorage.PlatformSchema(true)
				if errs := ruleengine.ValidateSyntax(set, schema, ruleengine.DefaultLimits()); len(errs) != 0 {
					t.Fatal(errs)
				}
				restrictiveReads := 0
				for _, policy := range set.Policies {
					if policy.Command == ruleengine.CmdRead && policy.Restrictive {
						restrictiveReads++
					}
				}
				wantRestrictive := 0
				if moderated {
					wantRestrictive = 1
				}
				if restrictiveReads != wantRestrictive {
					t.Fatalf("restrictive read policies = %d, want %d", restrictiveReads, wantRestrictive)
				}
				plan, errs := ruleengine.Compile(set, ruleengine.Table{
					Name: "posts", RowKey: "rowid", Columns: []ruleengine.Column{{Name: "visitor_id"}, {Name: "approved_at"}},
				}, schema, ruleengine.DefaultLimits())
				if len(errs) != 0 {
					t.Fatal(errs)
				}
				for _, actor := range []struct{ id, role string }{{"", "visitor"}, {"author", "visitor"}, {"other", "visitor"}, {"owner", "owner"}} {
					for _, approved := range []bool{false, true} {
						stamp := ruleengine.Null()
						if approved {
							stamp = ruleengine.Text("2026-11-14T00:00:00Z")
						}
						id := ruleengine.Null()
						if actor.id != "" {
							id = ruleengine.Text(actor.id)
						}
						visible, err := plan.EvalUsing(ruleengine.OpRead, ruleengine.Values{
							"auth.id": id, "auth.role": ruleengine.Text(actor.role), "auth.signed_in": ruleengine.Bool(actor.id != ""),
						}, ruleengine.Row{"visitor_id": ruleengine.Text("author"), "approved_at": stamp})
						want := !moderated || approved || actor.role == "owner"
						if err != nil || visible != want {
							t.Fatalf("actor=%s approved=%t: visible=%t, want %t, err=%v", actor.id, approved, visible, want, err)
						}
					}
				}
			})
		}
	}
}
func TestImportAdviceFlagOffHash(t *testing.T) {
	t.Setenv("STORAGE_RULES", "off")
	out, err := importAdviceFixture().Report("owner", "test")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(out)
	if got := fmt.Sprintf("%x", sha256.Sum256(b)); got != "bc9385a8b5e6859d9aa195c99c1f08c16d68d47e2555666703513e8dcdf94f55" {
		t.Fatalf("flag-off import advice changed: %s", got)
	}
}

func TestImportRulesAdviceAtRequestTime(t *testing.T) {
	m := importAdviceFixture()
	original, _ := json.Marshal(m.Records["test"].Report)
	t.Setenv("STORAGE_RULES", "off")
	before, err := m.Report("owner", "test")
	if err != nil {
		t.Fatal(err)
	}
	old, _ := json.Marshal(before)
	t.Setenv("STORAGE_RULES", "on")
	after, err := m.Report("owner", "test")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(after)
	for _, bad := range []string{`"preset"`, `comments preset`, `\"approve_first\":true`} {
		if strings.Contains(string(b), bad) {
			t.Errorf("rules advice contains %s", bad)
		}
	}
	if !strings.Contains(string(b), `\"rules\"`) || !strings.Contains(string(b), "restrictive read policy") {
		t.Errorf("missing rule advice: %s", b)
	}
	stored, _ := json.Marshal(m.Records["test"].Report)
	if string(stored) != string(original) {
		t.Fatal("stored report changed")
	}
	t.Setenv("STORAGE_RULES", "off")
	restored, _ := m.Report("owner", "test")
	b, _ = json.Marshal(restored)
	if string(b) != string(old) {
		t.Fatal("flag-off advice changed after flag toggled")
	}
}
