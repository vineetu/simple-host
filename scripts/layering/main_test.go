package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestMatch(t *testing.T) {
	for _, tc := range []struct {
		pattern, path string
		want          bool
	}{
		{"app/{sites,storage}", "app/sites", true},
		{"app/{sites,storage}", "app/hack", false},
		{"repository/**", "repository", true},
		{"repository/**", "repository/sites/nested", true},
		{"repository/**", "repository2", false},
		{"/**", "@/cmd/server", false},
	} {
		if got := match(tc.pattern, tc.path); got != tc.want {
			t.Errorf("match(%q, %q) = %v", tc.pattern, tc.path, got)
		}
	}
}
func TestNormalize(t *testing.T) {
	for _, tc := range []struct {
		in   pkg
		want string
	}{
		{pkg{ImportPath: "internal/a [internal/a.test]", ForTest: "internal/a"}, "internal/a"},
		{pkg{ImportPath: "internal/a_test [internal/a.test]"}, "internal/a"},
		{pkg{ImportPath: "cmd/review.test", Name: "main", Match: []string{"./..."}}, "cmd/review.test"},
		{pkg{ImportPath: "internal/a.test", Name: "a"}, "internal/a.test"},
		{pkg{ImportPath: "internal/a_test"}, "internal/a_test"},
	} {
		if got := normalize(tc.in); got != tc.want {
			t.Errorf("normalize(%+v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestExclusions(t *testing.T) {
	rs := []rule{{"@/cmd/server", []string{"/**", "!testdb", "!geoip/geoiptest", "@/db"}}}
	for _, tc := range []struct {
		to   string
		want bool
	}{{"handler", true}, {"testdb", false}, {"geoip/geoiptest", false}, {"@/db", true}} {
		if got := allowed(rs, "@/cmd/server", tc.to); got != tc.want {
			t.Errorf("allowed(%q) = %v", tc.to, got)
		}
	}
	if got := local("github.com/vsriram/simple-host"); got != "@/" {
		t.Errorf("module root = %q", got)
	}
}

func TestLocalAndDisplay(t *testing.T) {
	for _, tc := range []struct{ path, localPath, displayed string }{
		{module + "products/hack/app/events", "@/products/hack/app/events", "products/hack/app/events"},
		{module + "internal/model", "model", "internal/model"},
		{module + "cmd/server", "@/cmd/server", "cmd/server"},
		{module + "db", "@/db", "db"},
		{strings.TrimSuffix(module, "/"), "@/", ""},
	} {
		if got := local(tc.path); got != tc.localPath {
			t.Errorf("local(%q) = %q, want %q", tc.path, got, tc.localPath)
		}
		if got := display(tc.localPath); got != tc.displayed {
			t.Errorf("display(%q) = %q, want %q", tc.localPath, got, tc.displayed)
		}
	}
	if got := local("example.com/other/products/hack"); got != "" {
		t.Errorf("external local = %q", got)
	}
}

func TestProductContract(t *testing.T) {
	const final = "[final]\n@/products/hack/** -> /** @/products/hack/** @/db @/db/migrations\n"
	platformEdge := edge{"handler", "@/products/hack/app/teams", "production"}
	testEdge := edge{"handler", "@/products/hack/app/teams", "test"}
	const transition = "[transitional]\nhandler -> @/products/hack/app/teams production # removal step D2-S4b\n"
	detail := "internal/handler imports products/hack/app/teams (production), removal step D2-S4b"
	stale := "transitional edge no longer needed: " + detail + "; delete this line from rules.txt"
	for _, tc := range []struct {
		name, rules string
		edges       []edge
		wantIssues  int
		wantLines   []string
	}{
		{"product imports platform and itself", final, []edge{
			{"@/products/hack/app/events", "model", "production"},
			{"@/products/hack/app/events", "@/products/hack/app/teams", "production"},
			{"@/products/hack/app/events", "@/db", "production"},
			{"@/products/hack/app/events", "@/db/migrations", "production"},
		}, 0, nil},
		{"platform imports product", final, []edge{platformEdge}, 1, nil},
		{"transitional import", final + transition, []edge{platformEdge}, 0, []string{"transitional edge in use: " + detail}},
		{"stale transition", final + transition, nil, 1, []string{stale}},
		{"final makes transition redundant", final + "handler -> @/products/hack/app/teams\n" + transition, []edge{platformEdge}, 1, []string{stale}},
		{"test makes transition redundant", final + "[test]\nhandler -> @/products/hack/app/teams\n" + strings.ReplaceAll(transition, "production", "test"), []edge{testEdge}, 1, []string{strings.ReplaceAll(stale, "production", "test")}},
		{"test permission does not allow production", final + "[test]\nhandler -> @/products/hack/app/teams\n" + transition, []edge{platformEdge}, 0, []string{"transitional edge in use: " + detail}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rules, trans, err := readRules(strings.NewReader(tc.rules))
			if err != nil {
				t.Fatal(err)
			}
			edges := map[edge]bool{}
			for _, e := range tc.edges {
				edges[e] = true
			}
			issues, lines := evaluate(rules, trans, edges)
			if len(issues) != tc.wantIssues {
				t.Errorf("issues = %v, want %d", issues, tc.wantIssues)
			}
			if !reflect.DeepEqual(lines, tc.wantLines) {
				t.Errorf("lines = %v, want %v", lines, tc.wantLines)
			}
			if tc.name == "platform imports product" && (len(issues) != 1 || !strings.Contains(issues[0], "internal/handler imports products/hack/app/teams (production) — not in")) {
				t.Errorf("missing violation: %v", issues)
			}
		})
	}
}

func TestRealRules(t *testing.T) {
	f, err := os.Open("rules.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	rules, _, err := readRules(f)
	if err != nil {
		t.Fatal(err)
	}
	if !allowed(rules["final"], "@/products/hack/app/events", "model") || allowed(rules["final"], "handler", "@/products/hack/app/events") {
		t.Fatal("rules.txt must let a product import the platform and must not let the platform import a product")
	}
}

func TestReadRulesEmptyRemovalStep(t *testing.T) {
	_, _, err := readRules(strings.NewReader("[transitional]\nhandler -> @/products/hack/app/teams production # removal step \n"))
	if err == nil || !strings.Contains(err.Error(), "invalid transitional rule") {
		t.Fatalf("error = %v, want invalid transitional rule", err)
	}
}

func TestCheckerOnFixtureModule(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go binary is not on PATH")
	}
	t.Setenv("GOFLAGS", "-mod=mod")
	t.Setenv("GOPROXY", "off")
	t.Setenv("GOTOOLCHAIN", "local")
	t.Setenv("GOWORK", "off")
	mod, err := os.ReadFile("../../go.mod")
	if err != nil {
		t.Fatal(err)
	}
	var goLine string
	for _, line := range strings.Split(string(mod), "\n") {
		if strings.HasPrefix(line, "go ") {
			goLine = line
			break
		}
	}
	if goLine == "" {
		t.Fatal("repo go.mod has no go line")
	}
	const final = "[final]\n@/products/hack/** -> /** @/products/hack/**\n"
	const transition = "[transitional]\nhandler -> @/products/hack/app/teams production # removal step D2-S4b\n"
	const detail = "internal/handler imports products/hack/app/teams (production), removal step D2-S4b"
	for _, tc := range []struct {
		name, handlerImport, testImport, transition, wantLine string
		wantErr                                               bool
		edges                                                 int
	}{
		{name: "product imports platform and product", edges: 3},
		{name: "platform imports product", handlerImport: "import _ \"github.com/vsriram/simple-host/products/hack/app/teams\"", wantLine: "internal/handler imports products/hack/app/teams (production) — not in", wantErr: true},
		{name: "transitional import", handlerImport: "import _ \"github.com/vsriram/simple-host/products/hack/app/teams\"", transition: transition, wantLine: "transitional edge in use: " + detail, edges: 4},
		{name: "stale transition", transition: transition, wantLine: "transitional edge no longer needed: " + detail + "; delete this line from rules.txt", wantErr: true},
		{name: "production transition does not excuse test", testImport: "import _ \"github.com/vsriram/simple-host/products/hack/app/teams\"", transition: transition, wantLine: "internal/handler imports products/hack/app/teams (test) — not in", wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			files := map[string]string{
				"go.mod":                           "module github.com/vsriram/simple-host\n\n" + goLine + "\n",
				"internal/model/model.go":          "package model\n",
				"internal/handler/handler.go":      "package handler\n" + tc.handlerImport + "\n",
				"products/hack/app/teams/teams.go": "package teams\n",
				"products/hack/app/events/events.go": `package events
import (
 _ "github.com/vsriram/simple-host/internal/model"
 _ "github.com/vsriram/simple-host/products/hack/app/teams"
)
`,
				"products/hack/app/events/events_test.go":   "package events\nimport _ \"github.com/vsriram/simple-host/internal/model\"\n",
				"products/hack/app/events/external_test.go": "package events_test\nimport _ \"github.com/vsriram/simple-host/internal/model\"\n",
				rulesFile: final + tc.transition,
			}
			if tc.testImport != "" {
				files["internal/handler/handler_test.go"] = "package handler\n" + tc.testImport + "\n"
			}
			for name, content := range files {
				path := filepath.Join(dir, name)
				if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(content), 0644); err != nil {
					t.Fatal(err)
				}
			}
			var out bytes.Buffer
			err := run(dir, &out)
			if tc.wantErr {
				if err == nil || err.Error() != "layering violations" {
					t.Fatalf("error = %v, want layering violations; output:\n%s", err, &out)
				}
			} else {
				if err != nil {
					t.Fatalf("run: %v; output:\n%s", err, &out)
				}
				want := fmt.Sprintf("layering ok: 4 packages, %d edges checked, %d transitional edges remaining\n", tc.edges, strings.Count(tc.transition, "# removal step"))
				if !strings.HasSuffix(out.String(), want) {
					t.Errorf("output = %q, want suffix %q", out.String(), want)
				}
			}
			if tc.wantLine != "" && !strings.Contains(out.String(), tc.wantLine) {
				t.Errorf("output = %q, want %q", out.String(), tc.wantLine)
			}
		})
	}
}
