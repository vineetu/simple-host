package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fixture(t *testing.T) (string, []object, schemaInfo) {
	t.Helper()
	root, e := filepath.Abs("testdata/fixture")
	if e != nil {
		t.Fatal(e)
	}
	o, e := readObjects(filepath.Join(root, "hack-objects.txt"))
	if e != nil {
		t.Fatal(e)
	}
	b, e := os.ReadFile(filepath.Join(root, "schema.sql"))
	if e != nil {
		t.Fatal(e)
	}
	return root, o, parseSchema(string(b))
}
func TestScan(t *testing.T) {
	root, objs, z := fixture(t)
	o := options{Objects: "hack-objects.txt", Schema: "schema.sql", Warn: true}
	r, e := scan(root, o, objs, z)
	if e != nil {
		t.Fatal(e)
	}
	text := render(r, o)
	fixtureSource, err := os.ReadFile(filepath.Join(root, "queries.go"))
	if err != nil {
		t.Fatal(err)
	}
	for section, decl := range map[int]string{0: "JoinedConstant", 4: "JoinedAmbiguous", 6: "JoinedProse"} {
		prefix := "var " + decl + " = "
		offset := strings.Index(string(fixtureSource), prefix)
		wantLine := 1 + strings.Count(string(fixtureSource[:offset]), "\n")
		for _, f := range r.Sections[section] {
			if f.Decl == decl && f.Line != wantLine {
				t.Errorf("%s line %d want %d", decl, f.Line, wantLine)
			}
		}
	}
	has := func(section int, decl, part string) {
		t.Helper()
		for _, f := range r.Sections[section-1] {
			if f.Decl == decl && strings.Contains(f.Text, part) {
				return
			}
		}
		t.Errorf("section %d missing %s %q\n%s", section, decl, part, text)
	}
	has(1, "Plain", "event_teams")
	has(1, "OldProductPath", "old-product-path")
	has(1, "UpperQuery", "FROM EVENTS")
	has(1, "MixedQuery", "FROM Events")
	has(1, "JoinedConstant", "SELECT * FROM events")
	has(5, "JoinedAmbiguous", "SET events =$1")
	has(7, "JoinedProse", "about event_teams")
	for section, findings := range r.Sections {
		for _, f := range findings {
			if strings.HasPrefix(f.Decl, "Joined") && (f.Decl == "JoinedConstant" || f.Decl == "JoinedAmbiguous" || f.Decl == "JoinedProse") {
				if section == 5 {
					t.Errorf("constant Join leaked into list: %+v", f)
				}
				if f.Line < 1 || strings.Contains(f.Text, "‹?") {
					t.Errorf("constant Join was not evaluated: %+v", f)
				}
			}
		}
	}
	for _, decl := range []string{"TwoBuilders", "FieldBuilders"} {
		var got []finding
		for _, f := range r.Sections[3] {
			if f.Decl == decl+" (strings.Builder)" {
				got = append(got, f)
			}
		}
		if len(got) != 2 {
			t.Errorf("%s: want two builders, got %+v", decl, got)
			continue
		}
		for i, want := range []struct{ text, operand string }{{"SELECT * FROM widgets", "first"}, {"SELECT * FROM gadgets", "second"}} {
			if got[i].Text != want.text || strings.Join(got[i].Operands, ",") != want.operand || got[i].Line != got[0].Line+i {
				t.Errorf("%s builder %d: %+v", decl, i, got[i])
			}
		}
	}
	has(1, "SchemaQuery", `FROM "public"."events"`)
	has(1, "UnresolvedColumn", "g.selected_team_id")
	has(1, "NamedQuery", "FROM events")
	has(1, "StructQueries", "SELECT count(*) FROM event_teams")
	has(1, "CrossPackage", "id, name")
	has(2, "Append", "‹?s› FROM event_teams")
	has(2, "JoinedHack", "event_teams ‹?suffix›")
	has(3, "go:embed", "g.selected_team_id")
	has(4, "Joined", "SELECT * FROM ‹?table›")
	has(4, "Built (strings.Builder)", "SELECT * FROM widgets")
	has(4, "Buffered (strings.Builder)", "SELECT * FROM widgets")
	has(6, "StructList", "event_teams")
	for _, f := range r.Sections[5] {
		if f.Decl == "StructList" && f.Text == "events" {
			t.Error("struct field classified as list")
		}
	}
	for _, f := range r.Sections[4] {
		if f.Decl == "Route" || f.Decl == "HackRoute" {
			t.Error("route classified as SQL")
		}
	}
	for _, f := range r.Sections[3] {
		if f.Decl == "Built (strings.Builder)" && strings.Join(f.Operands, ",") != "id,suffix" {
			t.Error(f.Operands)
		}
	}
	if !strings.Contains(text, "shared/columns.go:3 Columns") {
		t.Error("missing other-package provenance", text)
	}
	has(1, "OwnedColumn", "g.selected_team_id")
	has(1, "InnerQueries", "event_teams")
	has(1, "LocalConstants", "event_teams")
	has(1, "NestedConstant", "event_teams")
	if !strings.Contains(text, " localColumns") {
		t.Error("missing transitive local constant")
	}
	has(1, "Combined", "FROM events")
	has(1, "Raw", "JOIN events")
	has(2, "Runtime", "‹?suffix›")
	has(2, "Formatted", "event_teams")
	has(4, "Unknown", "‹?table›")
	has(5, "Ambiguous", "SET events")
	has(6, "hackColumns", "selected_team_id")
	has(7, "NamedProse", "event_teams")
	if !strings.Contains(text, "built from: columns.go:3 columns") {
		t.Error("missing constant provenance", text)
	}
	if !strings.Contains(text, "name lines:") {
		t.Error("missing raw name lines")
	}
	if !strings.Contains(text, "oauth_grants.selected_team_id") {
		t.Error("column context missing")
	}
	if !strings.Contains(text, "also the platform column site_notifications.events") {
		t.Error("ambiguous list annotation")
	}
	for _, no := range []string{"NotHack", "event_domains", "event_account", "aliasOnly", "jsonObject", "skipped-product", "TestQuery", "events happen every day", "IgnoreError"} {
		if strings.Contains(text, no) {
			t.Errorf("unexpected %q", no)
		}
	}
	if r.ProseCounts["events"] != 4 {
		t.Errorf("prose count: %v", r.ProseCounts)
	}
	again, e := scan(root, o, objs, z)
	if e != nil {
		t.Fatal(e)
	}
	if render(again, o) != text {
		t.Fatal("nondeterministic report")
	}
	o.Tests = true
	o.Prose = true
	tests, e := scan(root, o, objs, z)
	if e != nil {
		t.Fatal(e)
	}
	with := render(tests, o)
	for _, route := range []string{"Route", "HackRoute"} {
		found := false
		for _, f := range tests.Sections[6] {
			if f.Decl == route && strings.Contains(f.Text, "DELETE /v1/") {
				found = true
			}
		}
		if !found {
			t.Error("route missing from prose", route)
		}
	}
	if !strings.Contains(with, "TestQuery") || !strings.Contains(with, "events happen every day") {
		t.Fatal(with)
	}
	if len(r.Sections[2]) != 2 || r.Sections[2][0].File != "assets/query.sql" {
		t.Fatal(r.Sections[2])
	}
	if scanCode(r, false) != 1 || scanCode(r, true) != 0 || scanCode(scanResult{}, false) != 0 {
		t.Fatal("exit behavior")
	}
}
func TestCLI(t *testing.T) {
	root, _, _ := fixture(t)
	for _, warn := range []bool{false, true} {
		var out, err bytes.Buffer
		args := []string{"-objects", "hack-objects.txt", "-schema", "schema.sql", "-report", "-"}
		if warn {
			args = append(args, "-warn")
		}
		code := run(root, args, &out, &err)
		want := 1
		if warn {
			want = 0
		}
		if code != want {
			t.Fatalf("code %d: %s", code, err.String())
		}
		if !strings.Contains(out.String(), "Plain") {
			t.Fatal(out.String())
		}
	}
}

func TestPlatformTypeErrorsExitTwo(t *testing.T) {
	fixtureRoot, _, _ := fixture(t)
	root := t.TempDir()
	for name, contents := range map[string]string{"go.mod": "module example.test/broken\n\ngo 1.24\n", "broken.go": "package broken\nvar broken = unknownName\n"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(contents), 0644); err != nil {
			t.Fatal(err)
		}
	}
	var out, errs bytes.Buffer
	code := run(root, []string{"-schema", filepath.Join(fixtureRoot, "schema.sql"), "-objects", filepath.Join(fixtureRoot, "hack-objects.txt"), "-report", "-"}, &out, &errs)
	if code != 2 || !strings.Contains(errs.String(), "load platform package") {
		t.Fatalf("code %d, %s", code, errs.String())
	}
}
