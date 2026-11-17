package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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
		{"@/host/storage/rules", "storage/rules", true},
		{"@/host/app/**", "app/sites", true},
		{"@/host/app", "app/sites", false},
		{"@/host/**", "model", true},
		{"@/host/**", "@/cmd/server", false},
		{"@/products/hack/**", "@/products/hack/app/events", true},
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
		{pkg{ImportPath: "host/a [host/a.test]", ForTest: "host/a"}, "host/a"},
		{pkg{ImportPath: "host/a_test [host/a.test]"}, "host/a"},
		{pkg{ImportPath: "cmd/review.test", Name: "main", Match: []string{"./..."}}, "cmd/review.test"},
		{pkg{ImportPath: "host/a.test", Name: "a"}, "host/a.test"},
		{pkg{ImportPath: "host/a_test"}, "host/a_test"},
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
		{module + "host/model", "model", "host/model"},
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

func TestAreaAndFineContract(t *testing.T) {
	f, err := os.Open("rules.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	rules, trans, err := readRules(f)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		from, to string
		want     bool
	}{
		{"handler", "app/sites", true},
		{"handler", "@/products/hack/app/teams", false},
		{"repository/sites", "handler", false},
		{"server", "extension", true},
		{"server/servertest", "extension", true},
		{"handler", "extension", false},
		{"@/cmd/simple-host", "extension", false},
		{"@/cmd/simple-hack", "extension", true},
		{"@/products/hack", "extension", true},
		{"@/cmd/simple-host", "@/products/hack", false},
		{"@/products/hack", "@/products/other", false},
	} {
		for _, kind := range []string{"production", "test"} {
			issues, _ := evaluate(rules, trans, map[edge]bool{{tc.from, tc.to, kind}: true})
			if (len(issues) == 0) != tc.want {
				t.Errorf("%s -> %s (%s): %v", tc.from, tc.to, kind, issues)
			}
		}
	}
	// A fine allow row cannot open the platform-to-product boundary.
	rules["final"] = append(rules["final"], rule{"handler", []string{"@/products/hack"}})
	if issues, _ := evaluate(rules, trans, map[edge]bool{{"handler", "@/products/hack", "production"}: true}); len(issues) == 0 {
		t.Fatal("fine row bypassed area check")
	}
	trans[edge{"handler", "model", "production"}] = "D3-S8"
	if issues, _ := evaluate(rules, trans, nil); len(issues) == 0 {
		t.Fatal("accepted nonempty transitional section")
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
	const final = "[areas]\n/** -> /** !extension\n@/products/hack/** -> /** @/products/hack/**\n[final]\n@/products/hack/** -> /** @/products/hack/**\n"
	const transition = "[transitional]\nhandler -> @/products/hack/app/teams production # removal step D2-S4b\n"
	const detail = "transitional edges are forbidden: host/handler imports products/hack/app/teams (production)"
	for _, tc := range []struct {
		name, handlerImport, testImport, repositoryImport, transition, wantLine string
		wantErr                                                                 bool
		edges                                                                   int
	}{
		{name: "product imports platform and product", edges: 3},
		{name: "repository imports handler", repositoryImport: "import _ \"github.com/vsriram/simple-host/host/handler\"", wantLine: "host/repository/sites imports host/handler (production) — not in", wantErr: true},
		{name: "platform imports product", handlerImport: "import _ \"github.com/vsriram/simple-host/products/hack/app/teams\"", wantLine: "host/handler imports products/hack/app/teams (production) — not in", wantErr: true},
		{name: "transitional import", handlerImport: "import _ \"github.com/vsriram/simple-host/products/hack/app/teams\"", transition: transition, wantLine: detail, wantErr: true},
		{name: "stale transition", transition: transition, wantLine: detail, wantErr: true},
		{name: "production transition does not excuse test", testImport: "import _ \"github.com/vsriram/simple-host/products/hack/app/teams\"", transition: transition, wantLine: "host/handler imports products/hack/app/teams (test) — not in", wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			files := map[string]string{
				"go.mod":                           "module github.com/vsriram/simple-host\n\n" + goLine + "\n",
				"host/model/model.go":              "package model\n",
				"host/handler/handler.go":          "package handler\n" + tc.handlerImport + "\n",
				"products/hack/app/teams/teams.go": "package teams\n",
				"products/hack/app/events/events.go": `package events
import (
 _ "github.com/vsriram/simple-host/host/model"
 _ "github.com/vsriram/simple-host/products/hack/app/teams"
)
`,
				"products/hack/app/events/events_test.go":   "package events\nimport _ \"github.com/vsriram/simple-host/host/model\"\n",
				"products/hack/app/events/external_test.go": "package events_test\nimport _ \"github.com/vsriram/simple-host/host/model\"\n",
				rulesFile: final + tc.transition,
			}
			if tc.repositoryImport != "" {
				files["host/repository/sites/sites.go"] = "package sites\n" + tc.repositoryImport + "\n"
			}
			if tc.testImport != "" {
				files["host/handler/handler_test.go"] = "package handler\n" + tc.testImport + "\n"
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
