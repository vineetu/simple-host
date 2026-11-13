package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fixtureMigrations(t *testing.T) []migration {
	t.Helper()
	root, o, z := fixture(t)
	order := []string{"01-platform.sql", "02-hack.sql", "03-mixed.sql", "04-platform.sql", "05-data.sql"}
	all, e := proposeMigrations(root, order, o, z)
	if e != nil {
		t.Fatal(e)
	}
	return all
}
func TestMigrationProposal(t *testing.T) {
	all := fixtureMigrations(t)
	for i, want := range []string{"platform", "hack", "mixed?", "platform", "mixed?"} {
		if all[i].Owner != want {
			t.Errorf("%s %s", all[i].Name, all[i].Owner)
		}
	}
	if !strings.Contains(all[4].Reason, "reads events (hack)") {
		t.Error(all[4])
	}
}
func TestMigrationChecks(t *testing.T) {
	all := fixtureMigrations(t)
	confirmed := map[string]migration{}
	var lines []string
	for _, m := range all {
		m.Owner = strings.TrimSuffix(m.Owner, "?")
		confirmed[m.Name] = m
		lines = append(lines, m.Name+" | "+m.Owner+" | reviewed "+m.Reason)
	}
	comment := mixedComment(all[2], all, confirmed, nil)
	if !strings.Contains(comment, "04-platform.sql") || !strings.Contains(comment, "platform_unused") {
		t.Fatal(comment)
	}
	none := mixedComment(all[4], all, confirmed, nil)
	if !strings.HasSuffix(none, "none") {
		t.Fatal(none)
	}
	path := filepath.Join(t.TempDir(), "owners.txt")
	write := func(s string) {
		t.Helper()
		if e := os.WriteFile(path, []byte(s), 0644); e != nil {
			t.Fatal(e)
		}
	}
	base := strings.Join(lines, "\n") + "\n"
	write(base)
	msg, code, e := checkMigrations(path, all, nil, false)
	if e != nil || code != 1 || !strings.Contains(msg, "missing or stale") {
		t.Fatal(msg, code, e)
	}
	_, code, e = checkMigrations(path, all, nil, true)
	if e != nil || code != 0 {
		t.Fatal(code, e)
	}
	_, code, e = checkMigrations(path, all, nil, false)
	if e != nil || code != 0 {
		t.Fatal(code, e)
	}
	write(strings.Join(lines[1:], "\n") + "\nextra.sql | nope | \n")
	msg, code, e = checkMigrations(path, all, nil, false)
	for _, v := range []string{"missing file: 01-platform.sql", "file does not exist: extra.sql", "invalid owner:", "empty reason:"} {
		if e != nil || code != 1 || !strings.Contains(msg, v) {
			t.Fatal(msg, code, e)
		}
	}
	write(base + "#   mixed: stale\n")
	msg, code, e = checkMigrations(path, all, nil, false)
	if e != nil || code != 1 || !strings.Contains(msg, "stale mixed comment") {
		t.Fatal(msg, code, e)
	}
}
func TestMixedBaseline(t *testing.T) {
	all := []migration{{Name: "a", CreatedPlatform: []string{"thing"}}, {Name: "b", Mentions: []string{"thing"}}}
	c := map[string]migration{"b": {Owner: "platform"}}
	s := mixedComment(all[0], all, c, map[string]bool{"a": true, "b": true})
	if !strings.Contains(s, "b") {
		t.Fatal(s)
	}
	all[0], all[1] = all[1], all[0]
	s = mixedComment(all[1], all, c, map[string]bool{"a": true, "b": true})
	if !strings.Contains(s, "b") {
		t.Fatal(s)
	}
}

func TestMissingOwnerFile(t *testing.T) {
	all := fixtureMigrations(t)
	msg, code, err := checkMigrations(filepath.Join(t.TempDir(), "missing.txt"), all, nil, false)
	if err != nil || code != 1 || !strings.Contains(msg, "missing file: 01-platform.sql") {
		t.Fatal(msg, code, err)
	}
}

func TestMigrationColumnIsNotRead(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "db/migrations"), 0755); err != nil {
		t.Fatal(err)
	}
	text := `CREATE TABLE paper_site_footprint (id integer, versions INTEGER, "events" integer, value numeric(10,2), FOREIGN KEY(id) REFERENCES sites(id));`
	if err := os.WriteFile(filepath.Join(root, "db/migrations/metrics.sql"), []byte(text), 0644); err != nil {
		t.Fatal(err)
	}
	z := parseSchema(text + `CREATE TABLE versions(id integer); CREATE TABLE sites(id integer); CREATE TABLE events(id integer);`)
	all, err := proposeMigrations(root, []string{"metrics.sql"}, []object{{Kind: "table", Name: "events"}}, z)
	if err != nil {
		t.Fatal(err)
	}
	if all[0].Owner != "platform" || strings.Contains(all[0].Reason, "versions") || strings.Contains(all[0].Reason, "events") || !strings.Contains(all[0].Reason, "reads sites") {
		t.Fatal(all)
	}
}
