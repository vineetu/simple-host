// boundary enforces platform naming and public-text boundaries.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"go/scanner"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"
)

type finding struct {
	path, kind, value string
	line              int
}

var productText = regexp.MustCompile(`(?i)simple[ -]?hack|hackathon`)

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

func names(root string) ([]finding, []finding, error) {
	var runtime, tests []finding
	for _, dir := range []string{"host"} {
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
			dest := &runtime
			if isTest(rel) {
				dest = &tests
			}
			name := entry.Name()
			if strings.Contains(strings.ToLower(name), "hack") {
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
				if tok == token.IDENT && strings.Contains(strings.ToLower(literal), "hack") {
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

func textFindings(path string, b []byte) []finding {
	// Binary assets are not prose; archives are opened before this function.
	if !utf8.Valid(b) || bytes.ContainsRune(b, 0) {
		return nil
	}
	var result []finding
	for i, line := range strings.Split(string(b), "\n") {
		for _, match := range productText.FindAllString(line, -1) {
			result = append(result, finding{path, "text", match, i + 1})
		}
	}
	return result
}

func publicText(root, textDir string) ([]finding, []finding, error) {
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
		recorded = append(recorded, textFindings("text/host/"+filepath.ToSlash(rel), b)...)
		count++
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	if count == 0 {
		return nil, nil, fmt.Errorf("Host recording contains no .txt files: %s", textDir)
	}
	assets, err = embedded(root)
	return recorded, assets, err
}

func main() {
	mode := flag.String("mode", "names", "names, embedded, or public-text")
	root := flag.String("root", ".", "repository root")
	textDir := flag.String("text", "", "golden run's text/host directory")
	flag.Parse()
	if err := run(*root, *mode, *textDir); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(root, mode, textDir string) error {
	root, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	var rows []finding
	allowFile := "public-text.txt"
	scope := "host/"
	switch mode {
	case "names":
		allowFile = "source-names.txt"
		scope = ""
		var tests []finding
		rows, tests, err = names(root)
		rows = append(rows, tests...)
	case "embedded":
		rows, err = embedded(root)
	case "public-text":
		if textDir == "" {
			return fmt.Errorf("public-text requires the Host run's text folder")
		}
		var assets []finding
		rows, assets, err = publicText(root, textDir)
		rows = append(rows, assets...)
		scope = ""
	default:
		return fmt.Errorf("unknown boundary mode: %s", mode)
	}
	if err != nil {
		return err
	}
	return checkCounts(filepath.Join(root, "scripts/allow", allowFile), scope, rows)
}
