package main

import (
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

func writeFixture(t *testing.T, root, name, content string) {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestNamesScope(t *testing.T) {
	root := t.TempDir()
	for name, content := range map[string]string{
		"internal/app/hAcK.go":                        "package app\n// HackComment\nvar HackThing = `HackString`\n",
		"internal/app/plain_test.go":                  "package app\nvar HackTest = 1\n",
		"internal/app/testdata/data.go":               "package fixture\nvar HackFixture = 1\n",
		"internal/server/servertest/a.go":             "package servertest\nvar HackHelper = 1\n",
		"host/runtime.go":                             "package host\nvar HackHost = 1\n",
		"db/db.go":                                    "package db\nvar HackDB = 1\n",
		"db/migrations/testdata/hack-schema.sql":      "-- historical fixture, still a test input",
		"host/db/migrations/testdata/hack-schema.sql": "-- historical fixture, still a test input",
		"db/migrations/hack.sql":                      "-- HackHistory",
		"db/migrations/migrations.go":                 "package migrations\nvar HackMigration = 1\n",
		"db/migrations/one_name_test.go":              "package migrations\nfunc TestHackOneNameBackfill() {}\n",
		"host/db/migrations/hack.sql":                 "-- HackHistory",
		"host/db/migrations/migrations.go":            "package migrations\nvar HackMigration = 1\n",
		"host/db/migrations/one_name_test.go":         "package migrations\nfunc TestHackOneNameBackfill() {}\n",
		"products/hack/product.go":                    "package hack\nvar HackProduct = 1\n",
		"docs/generated/hack.go":                      "package generated\nvar HackInventory = 1\n",
		"docs/history/hack.go":                        "package history\nvar HackHistory = 1\n",
		"CHANGELOG.md":                                "Hack history",
		"PARITY.md":                                   "Hack history",
	} {
		writeFixture(t, root, name, content)
	}
	runtime, tests, err := names(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	section(&out, "Runtime", runtime)
	got := out.String()
	for _, want := range []string{"hAcK.go:0 | filename", "identifier | HackThing", "identifier | HackHost", "identifier | HackDB", "db/migrations/migrations.go:2 | identifier | HackMigration", "host/db/migrations/migrations.go:2 | identifier | HackMigration", "Findings: 6"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %s", want, got)
		}
	}
	for _, excluded := range []string{"HackComment", "HackString", "HackTest", "HackFixture", "HackHelper", "HackHistory", "HackInventory", "HackProduct"} {
		if strings.Contains(got, excluded) {
			t.Errorf("runtime includes %s", excluded)
		}
	}
	if len(tests) != 7 {
		t.Fatalf("test/fixture findings: %v", tests)
	}
	out.Reset()
	section(&out, "Tests", tests)
	for _, path := range []string{"db/migrations/one_name_test.go", "host/db/migrations/one_name_test.go"} {
		if !strings.Contains(out.String(), path+":2 | identifier | TestHackOneNameBackfill") {
			t.Errorf("missing migration test %s in %s", path, out.String())
		}
		fixture := filepath.ToSlash(filepath.Join(filepath.Dir(path), "testdata/hack-schema.sql"))
		if !strings.Contains(out.String(), fixture+":0 | filename | hack-schema.sql") {
			t.Errorf("missing migration fixture %s in %s", fixture, out.String())
		}
	}
}

func TestPublicTextPatternsAndExceptions(t *testing.T) {
	allow := []exception{{"text/host/*.txt", "configured domain", regexp.MustCompile(`"domain":\s*"simple-hack\.app"`)}}
	b := []byte("Simple Hack simple-hack SIMPLEHACK hAcKaThOn\nSIM-123 HACK_SETTING hack\n" +
		`{"domain": "simple-hack.app", "title": "Simple Hack"}` + "\n")
	rows := textFindings("text/host/domains.txt", b, allow)
	if len(rows) != 5 || rows[4].value != "Simple Hack" || rows[4].line != 3 {
		t.Fatalf("expected four product matches and the unmasked title: %v", rows)
	}
	if got := textFindings("internal/webassets/page.txt", b, allow); len(got) != 6 {
		t.Fatalf("domain exception leaked to authored assets: %v", got)
	}
	if got := textFindings("image.bin", []byte("\x00Simple Hack"), nil); len(got) != 0 {
		t.Fatal("binary scanned as prose")
	}
}

func TestPublicTextInputsAndEmbeds(t *testing.T) {
	for _, platform := range []string{"internal", "host"} {
		t.Run(platform, func(t *testing.T) {
			root := t.TempDir()
			writeFixture(t, root, "go.mod", "module example.com/boundaryfixture\n\ngo 1.24\n")
			writeFixture(t, root, platform+"/webassets/assets.go", "package webassets\nimport \"embed\"\n//go:embed all:static\nvar Files embed.FS\n")
			writeFixture(t, root, platform+"/webassets/static/page.html", "Simple Hack")
			writeFixture(t, root, platform+"/webassets/static/.hidden.txt", "Hackathon")
			writeFixture(t, root, platform+"/webassets/unembedded.txt", "Simple Hack")
			writeFixture(t, root, platform+"/mcp/apps.go", "package mcp\nimport \"embed\"\n//go:embed apps/*.js recipes/*.md\nvar Files embed.FS\n")
			writeFixture(t, root, platform+"/mcp/apps/bundle.js", "hackathon")
			writeFixture(t, root, platform+"/mcp/recipes/example.md", "Simple Hack")
			writeFixture(t, root, platform+"/mcp/unembedded.txt", "Simple Hack")
			writeFixture(t, root, "recording/text/host/page.txt", "Simple Hack")
			writeFixture(t, root, "recording/text/hack/page.txt", "Hackathon Simple Hack")
			textDir := filepath.Join(root, "recording/text/host")
			recorded, assets, err := publicText(root, textDir, nil)
			if err != nil {
				t.Fatal(err)
			}
			if len(recorded) != 1 || recorded[0].path != "text/host/page.txt" || len(assets) != 4 {
				t.Fatalf("wrong inputs: recorded=%v assets=%v", recorded, assets)
			}
			wantAssets := []finding{
				{platform + "/webassets/static/.hidden.txt", "text", "Hackathon", 1},
				{platform + "/webassets/static/page.html", "text", "Simple Hack", 1},
				{platform + "/mcp/apps/bundle.js", "text", "hackathon", 1},
				{platform + "/mcp/recipes/example.md", "text", "Simple Hack", 1},
			}
			if !reflect.DeepEqual(assets, wantAssets) {
				t.Fatalf("embedded findings: got %v, want %v", assets, wantAssets)
			}
			for _, dir := range []string{filepath.Join(root, "missing"), t.TempDir()} {
				if _, _, err := publicText(root, dir, nil); err == nil {
					t.Errorf("accepted missing/empty recording: %s", dir)
				}
			}
			writeFixture(t, root, "scripts/allow/public-text.txt", "# no exceptions\n")
			if err := run(root, "public-text", textDir); err != nil {
				t.Fatalf("findings should not fail: %v", err)
			}
			report := filepath.Join(root, "scripts/split/reports/public-text.txt")
			first, err := os.ReadFile(report)
			if err != nil {
				t.Fatal(err)
			}
			if err := run(root, "public-text", textDir); err != nil {
				t.Fatal(err)
			}
			second, err := os.ReadFile(report)
			if err != nil || !reflect.DeepEqual(first, second) {
				t.Fatal("report regeneration differs", err)
			}
		})
	}
}

func TestAllowlistRequiresReasonAndMasksOnlyNamedIdentifier(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "allow.txt")
	writeFixture(t, root, "allow.txt", "internal/app/*.go | ^HackAllowed$ | transition\n")
	allow, err := readAllow(path)
	if err != nil {
		t.Fatal(err)
	}
	if mask("internal/app/a.go", "HackAllowed", allow) != "           " || mask("internal/app/a.go", "HackOther", allow) != "HackOther" {
		t.Fatal("exception did not match exactly")
	}
	writeFixture(t, root, "allow.txt", "internal/app/*.go | HackAllowed | \n")
	if _, err := readAllow(path); err == nil {
		t.Fatal("accepted an exception without a reason")
	}
}
