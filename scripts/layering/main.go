package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"go/parser"
	"go/token"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

const module = "github.com/vsriram/simple-host/"
const rulesFile = "scripts/layering/rules.txt"

type pkg struct {
	ImportPath, Name, Dir                        string
	Imports, TestImports, XTestImports           []string
	GoFiles, CgoFiles, TestGoFiles, XTestGoFiles []string
	ForTest                                      string
	Match                                        []string
}
type rule struct {
	from string
	to   []string
}
type edge struct{ from, to, kind string }

func normalize(p pkg) string {
	if p.ForTest != "" {
		return p.ForTest
	}
	if i := strings.LastIndex(p.ImportPath, " ["); i >= 0 && strings.HasSuffix(p.ImportPath, ".test]") {
		return strings.TrimSuffix(p.ImportPath[i+2:], ".test]")
	}
	return p.ImportPath
}
func local(s string) string {
	s = normalize(pkg{ImportPath: s})
	if s == strings.TrimSuffix(module, "/") {
		return "@/"
	}
	if !strings.HasPrefix(s, module) {
		return ""
	}
	s = strings.TrimPrefix(s, module)
	if strings.HasPrefix(s, "internal/") {
		return strings.TrimPrefix(s, "internal/")
	}
	return "@/" + s
}
func display(s string) string {
	if strings.HasPrefix(s, "@/") {
		return strings.TrimPrefix(s, "@/")
	}
	return "internal/" + s
}
func expand(s string) []string {
	i := strings.IndexByte(s, '{')
	if i < 0 {
		return []string{s}
	}
	j := strings.IndexByte(s[i:], '}')
	if j < 0 {
		return []string{s}
	}
	j += i
	var out []string
	for _, x := range strings.Split(s[i+1:j], ",") {
		out = append(out, expand(s[:i]+x+s[j+1:])...)
	}
	return out
}
func match(pattern, path string) bool {
	for _, p := range expand(pattern) {
		if p == "/**" {
			if !strings.HasPrefix(path, "@/") {
				return true
			}
			continue
		}
		if strings.HasSuffix(p, "/**") {
			base := strings.TrimSuffix(p, "/**")
			if path == base || strings.HasPrefix(path, base+"/") {
				return true
			}
		} else if p == path {
			return true
		}
	}
	return false
}
func allowed(rs []rule, from, to string) bool {
	for _, r := range rs {
		if !match(r.from, from) {
			continue
		}
		included, excluded := false, false
		for _, p := range r.to {
			if strings.HasPrefix(p, "!") {
				excluded = excluded || match(strings.TrimPrefix(p, "!"), to)
			} else {
				included = included || match(p, to)
			}
		}
		if included && !excluded {
			return true
		}
	}
	return false
}
func readRules(reader io.Reader) (map[string][]rule, map[edge]string, error) {
	sections := map[string][]rule{}
	trans := map[edge]string{}
	section := ""
	scanner := bufio.NewScanner(reader)
	line := 0
	for scanner.Scan() {
		line++
		raw := scanner.Text()
		s := strings.TrimSpace(strings.SplitN(raw, "#", 2)[0])
		if s == "" {
			continue
		}
		if strings.HasPrefix(s, "[") && strings.HasSuffix(s, "]") {
			section = strings.Trim(s, "[]")
			continue
		}
		parts := strings.Split(s, " -> ")
		if len(parts) != 2 {
			return nil, nil, fmt.Errorf("%s:%d: invalid rule", rulesFile, line)
		}
		tos := strings.Fields(parts[1])
		if len(tos) == 0 {
			return nil, nil, fmt.Errorf("%s:%d: missing destination", rulesFile, line)
		}
		if section == "transitional" {
			if len(tos) != 2 || (tos[1] != "production" && tos[1] != "test") || (!strings.Contains(raw, "# removal step ") || strings.TrimSpace(strings.SplitN(raw, "# removal step ", 2)[1]) == "") {
				return nil, nil, fmt.Errorf("%s:%d: invalid transitional rule", rulesFile, line)
			}
			e := edge{parts[0], tos[0], tos[1]}
			if _, exists := trans[e]; exists {
				return nil, nil, fmt.Errorf("%s:%d: duplicate transitional rule", rulesFile, line)
			}
			trans[e] = strings.TrimSpace(strings.SplitN(raw, "# removal step ", 2)[1])
		} else if section == "final" || section == "test" {
			sections[section] = append(sections[section], rule{parts[0], tos})
		} else {
			return nil, nil, fmt.Errorf("%s:%d: unknown section", rulesFile, line)
		}
	}
	return sections, trans, scanner.Err()
}

// evaluate applies the import contract independently of package discovery.
func evaluate(rules map[string][]rule, trans map[edge]string, edges map[edge]bool) (issues, lines []string) {
	normal := func(e edge) bool {
		return allowed(rules["final"], e.from, e.to) ||
			(e.kind == "test" && allowed(rules["test"], e.from, e.to))
	}
	for e := range edges {
		_, transitional := trans[e]
		if !normal(e) && !transitional {
			issues = append(issues, fmt.Sprintf("%s imports %s (%s) — not in %s; see docs/architecture/target.md §2", display(e.from), display(e.to), e.kind, rulesFile))
		}
	}
	for e, step := range trans {
		detail := fmt.Sprintf("%s imports %s (%s), removal step %s", display(e.from), display(e.to), e.kind, step)
		if !edges[e] || normal(e) {
			line := "transitional edge no longer needed: " + detail + "; delete this line from rules.txt"
			lines = append(lines, line)
			issues = append(issues, line)
		} else {
			lines = append(lines, "transitional edge in use: "+detail)
		}
	}
	sort.Strings(issues)
	sort.Strings(lines)
	return issues, lines
}

func run(dir string, out io.Writer) error {
	f, err := os.Open(filepath.Join(dir, rulesFile))
	if err != nil {
		return err
	}
	defer f.Close()
	rules, trans, err := readRules(f)
	if err != nil {
		return err
	}
	cmd := exec.Command("go", "list", "-test", "-json", "./...")
	cmd.Dir = dir
	cmd.Stderr = os.Stderr
	data, err := cmd.Output()
	if err != nil {
		return err
	}
	dec := json.NewDecoder(strings.NewReader(string(data)))
	packages := map[string]bool{}
	edges := map[edge]bool{}
	for {
		var p pkg
		err = dec.Decode(&p)
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		// Test variants carry ForTest; generated test mains match no pattern.
		if p.ForTest != "" || len(p.Match) == 0 {
			continue
		}
		from := local(normalize(p))
		if from == "" {
			continue
		}
		packages[from] = true
		for _, files := range [][]string{p.GoFiles, p.CgoFiles, p.TestGoFiles, p.XTestGoFiles} {
			for _, file := range files {
				if _, err := parser.ParseFile(token.NewFileSet(), filepath.Join(p.Dir, file), nil, parser.AllErrors); err != nil {
					return err
				}
			}
		}
		for _, group := range []struct {
			kind    string
			imports []string
		}{{"production", p.Imports}, {"test", append(p.TestImports, p.XTestImports...)}} {
			for _, imp := range group.imports {
				to := local(imp)
				if to != "" && (group.kind != "test" || to != from) {
					edges[edge{from, to, group.kind}] = true
				}
			}
		}
	}
	issues, lines := evaluate(rules, trans, edges)
	for _, line := range lines {
		fmt.Fprintln(out, line)
	}
	sort.Strings(issues)
	if len(issues) > 0 {
		for _, s := range issues {
			if !strings.HasPrefix(s, "transitional edge no longer needed: ") {
				fmt.Fprintln(out, s)
			}
		}
		return fmt.Errorf("layering violations")
	}
	fmt.Fprintf(out, "layering ok: %d packages, %d edges checked, %d transitional edges remaining\n", len(packages), len(edges), len(trans))
	return nil
}
func main() {
	if err := run(".", os.Stdout); err != nil {
		if err.Error() != "layering violations" {
			fmt.Fprintln(os.Stderr, err)
		}
		os.Exit(1)
	}
}
