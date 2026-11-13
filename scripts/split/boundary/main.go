// boundary writes the report-only naming and public-text inventories for D1-S5b.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"go/scanner"
	"go/token"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

type exception struct {
	path, reason string
	pattern      *regexp.Regexp
}

type finding struct {
	path, kind, value string
	line              int
}

var productText = regexp.MustCompile(`(?i)simple[ -]?hack|hackathon`)

func readAllow(path string) ([]exception, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var result []exception
	for i, line := range strings.Split(string(b), "\n") {
		if strings.TrimSpace(line) == "" || strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		parts := strings.SplitN(line, " | ", 3)
		if len(parts) != 3 || strings.TrimSpace(parts[2]) == "" {
			return nil, fmt.Errorf("%s:%d: expected path glob | regexp | reason", path, i+1)
		}
		re, err := regexp.Compile(parts[1])
		if err != nil {
			return nil, fmt.Errorf("%s:%d: %w", path, i+1, err)
		}
		if _, err := filepath.Match(parts[0], ""); err != nil {
			return nil, err
		}
		result = append(result, exception{parts[0], parts[2], re})
	}
	return result, nil
}

// Mask only the exempt span, never the rest of a line containing other names.
func mask(path, line string, allow []exception) string {
	for _, a := range allow {
		if ok, _ := filepath.Match(a.path, path); ok {
			line = a.pattern.ReplaceAllStringFunc(line, func(s string) string { return strings.Repeat(" ", len(s)) })
		}
	}
	return line
}

func isTest(path string) bool {
	if strings.HasSuffix(path, "_test.go") {
		return true
	}
	for _, part := range strings.Split(path, "/") {
		switch part {
		case "testdata", "fixtures", "testfixtures", "testdb", "servertest", "geoiptest":
			return true
		}
	}
	return false
}

func names(root string, allow []exception) ([]finding, []finding, error) {
	var runtime, tests []finding
	for _, dir := range []string{"internal", "host", "db"} {
		base := filepath.Join(root, dir)
		if _, err := os.Stat(base); os.IsNotExist(err) {
			continue
		}
		err := filepath.WalkDir(base, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(root, path)
			rel = filepath.ToSlash(rel)
			if entry.IsDir() {
				return nil
			}
			if !entry.Type().IsRegular() {
				return nil
			}
			// Historical SQL scripts are excluded, but migration Go source and
			// tests belong in the runtime and test inventories respectively.
			if dir := filepath.ToSlash(filepath.Dir(rel)); filepath.Ext(path) == ".sql" && (dir == "db/migrations" || dir == "host/db/migrations") {
				return nil
			}
			dest := &runtime
			if isTest(rel) {
				dest = &tests
			}
			name := entry.Name()
			if strings.Contains(strings.ToLower(mask(rel, name, allow)), "hack") {
				*dest = append(*dest, finding{rel, "filename", name, 0})
			}
			if filepath.Ext(path) != ".go" {
				return nil
			}
			b, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			fset := token.NewFileSet()
			file := fset.AddFile(rel, -1, len(b))
			var scan scanner.Scanner
			scan.Init(file, b, nil, 0)
			for {
				pos, tok, literal := scan.Scan()
				if tok == token.EOF {
					break
				}
				if tok == token.IDENT && strings.Contains(strings.ToLower(mask(rel, literal, allow)), "hack") {
					*dest = append(*dest, finding{rel, "identifier", literal, fset.Position(pos).Line})
				}
			}
			return nil
		})
		if err != nil {
			return nil, nil, err
		}
	}
	return runtime, tests, nil
}

func textFindings(path string, b []byte, allow []exception) []finding {
	// Archive contents are covered by golden ZIP text; binary asset inspection
	// is a separate D3-S8 gate. Do not grep compressed bytes as public prose.
	if !utf8.Valid(b) || bytes.ContainsRune(b, 0) {
		return nil
	}
	var result []finding
	for i, line := range strings.Split(string(b), "\n") {
		for _, match := range productText.FindAllString(mask(path, line, allow), -1) {
			result = append(result, finding{path, "text", match, i + 1})
		}
	}
	return result
}

func publicText(root, textDir string, allow []exception) ([]finding, []finding, error) {
	var recorded, assets []finding
	count := 0
	err := filepath.WalkDir(textDir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		if filepath.Ext(path) != ".txt" {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(textDir, path)
		recorded = append(recorded, textFindings("text/host/"+filepath.ToSlash(rel), b, allow)...)
		count++
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	if count == 0 {
		return nil, nil, fmt.Errorf("Host recording contains no .txt files: %s", textDir)
	}
	// Ask Go for the actual embed inventory, including all: hidden files.
	platform := "internal"
	if _, err := os.Stat(filepath.Join(root, platform, "webassets")); os.IsNotExist(err) {
		platform = "host"
	}
	for _, name := range []string{"webassets", "mcp"} {
		assetPackage := platform + "/" + name
		cmd := exec.Command("go", "list", "-json", "./"+assetPackage)
		cmd.Dir = root
		b, err := cmd.Output()
		if err != nil {
			return nil, nil, fmt.Errorf("list embedded assets in %s: %w", assetPackage, err)
		}
		var pkg struct{ EmbedFiles []string }
		if err := json.Unmarshal(b, &pkg); err != nil {
			return nil, nil, err
		}
		for _, name := range pkg.EmbedFiles {
			path := assetPackage + "/" + name
			b, err := os.ReadFile(filepath.Join(root, path))
			if err != nil {
				return nil, nil, err
			}
			assets = append(assets, textFindings(path, b, allow)...)
		}
	}
	return recorded, assets, nil
}

func section(out *strings.Builder, title string, rows []finding) {
	fmt.Fprintf(out, "\n## %s\n", title)
	sort.Slice(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if a.path != b.path {
			return a.path < b.path
		}
		if a.line != b.line {
			return a.line < b.line
		}
		if a.kind != b.kind {
			return a.kind < b.kind
		}
		return a.value < b.value
	})
	last, count := "", 0
	for _, r := range rows {
		line := fmt.Sprintf("%s:%d | %s | %s\n", r.path, r.line, r.kind, r.value)
		if line != last {
			out.WriteString(line)
			count++
		}
		last = line
	}
	fmt.Fprintf(out, "Findings: %d\n", count)
}

func main() {
	mode := flag.String("mode", "names", "names or public-text (findings never fail)")
	root := flag.String("root", ".", "repository root")
	textDir := flag.String("text", "", "golden run's text/host directory; required for public-text")
	flag.Parse()
	err := run(*root, *mode, *textDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
}

func run(root, mode, textDir string) error {
	allowFile, report := "source-names.txt", "names.txt"
	if mode == "public-text" {
		allowFile, report = "public-text.txt", "public-text.txt"
	} else if mode != "names" {
		return fmt.Errorf("unknown report: %s", mode)
	}
	allow, err := readAllow(filepath.Join(root, "scripts/allow", allowFile))
	if err != nil {
		return err
	}
	var out strings.Builder
	fmt.Fprintf(&out, "# %s (report only; findings do not fail)\n", mode)
	fmt.Fprintf(&out, "# Allowlist: scripts/allow/%s; each exception has a reason.\n", allowFile)
	if mode == "names" {
		out.WriteString("# Scope: internal/, host/, db/ filenames and Go identifiers; comments and string literals are not identifiers.\n")
		runtime, tests, err := names(root, allow)
		if err != nil {
			return err
		}
		section(&out, "Runtime source", runtime)
		section(&out, "Test files and fixtures", tests)
		out.WriteString("\n## Generated inventories\nNot scanned: docs/generated/ is the union of both products.\n\n## History\nNot scanned: docs/history/, CHANGELOG.md, PARITY.md, SQL migration scripts in db/migrations/ and host/db/migrations/ (testdata remains in scope).\n")
	} else {
		if textDir == "" {
			return fmt.Errorf("public-text requires -text pointing to the Host run's text/host folder")
		}
		out.WriteString("# Patterns: (?i)simple[ -]?hack and (?i)hackathon only.\n# Recorded paths are relative to text/host; binary assets are not text.\n")
		recorded, assets, err := publicText(root, textDir, allow)
		if err != nil {
			return err
		}
		section(&out, "Host recording", recorded)
		section(&out, "Platform embedded assets", assets)
	}
	path := filepath.Join(root, "scripts/split/reports", report)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(out.String()), 0644); err != nil {
		return err
	}
	fmt.Printf("Wrote scripts/split/reports/%s (report only)\n", report)
	return nil
}
