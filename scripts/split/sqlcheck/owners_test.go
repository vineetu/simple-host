package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckOwners(t *testing.T) {
	root := t.TempDir()
	write := func(path, text string) {
		p := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("host/access/repo/a.go", "package repo\nconst q = `SELECT id FROM users`\n")
	write("host/sites/repo/b.go", "package repo\nconst q = `SELECT s.id FROM sites s\n\tJOIN users u ON u.id = s.user_id`\n")
	write("host/ops/c.go", "package ops\nconst q = `UPDATE users SET x = 1`\n")
	write("host/data/d.go", "package data\nconst q = `DELETE FROM x USING public.users u WHERE true`\nconst r = `SELECT 1 FROM x a, sites s`\nconst t = `TRUNCATE TABLE sites`\n")
	write("host/ops/c_test.go", "package ops\nconst q = `UPDATE users SET x = 1`\n")
	write("scripts/split/table-owners.txt", "users | host/access/repo | people.go\nsites | host/sites/repo | door.go\n")
	write("scripts/split/table-owner-exceptions.txt", "host/ops/ | users | operator report\n")
	z := schemaInfo{Tables: map[string]bool{"users": true, "sites": true}}
	var out bytes.Buffer
	code, err := checkOwners(root, z, false, &out)
	if err != nil {
		t.Fatal(err)
	}
	if code != 1 || strings.Count(out.String(), "queries users") != 2 || strings.Count(out.String(), "host/data/d.go:") != 3 || !strings.Contains(out.String(), "host/sites/repo/b.go:3: queries users, which only host/access/repo reads and writes; call people.go instead") {
		t.Fatalf("code %d, output:\n%s", code, out.String())
	}
	out.Reset()
	if _, err := checkOwners(root, z, true, &out); err != nil {
		t.Fatal(err)
	}
	if out.String() != "" {
		t.Fatalf("proposal:\n%s", out.String())
	}
}
