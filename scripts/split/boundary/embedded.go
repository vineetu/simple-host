package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Go's embed inventory includes hidden files selected by all:, unexported
// embed.FS variables, and string/byte embeds in every platform package.
func embedded(root string) ([]finding, error) {
	var data []byte
	locations := []struct{ dir, pattern string }{{root, "./host/..."}}
	// The rules engine is the one nested module; the root pattern excludes it.
	engine := filepath.Join(root, "host/storage/rules")
	if _, err := os.Stat(filepath.Join(engine, "go.mod")); err == nil {
		locations = append(locations, struct{ dir, pattern string }{engine, "./..."})
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	for _, location := range locations {
		cmd := exec.Command("go", "list", "-json", location.pattern)
		cmd.Dir = location.dir
		b, err := cmd.Output()
		if err != nil {
			return nil, fmt.Errorf("list platform embeds: %w", err)
		}
		data = append(data, b...)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	var rows []finding
	for {
		var p struct {
			Dir        string
			EmbedFiles []string
		}
		if err := dec.Decode(&p); err == io.EOF {
			break
		} else if err != nil {
			return nil, err
		}
		for _, name := range p.EmbedFiles {
			path := filepath.Join(p.Dir, name)
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return nil, err
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return nil, err
			}
			found, err := assetFindings(filepath.ToSlash(rel), data)
			if err != nil {
				return nil, err
			}
			rows = append(rows, found...)
		}
	}
	return rows, nil
}

// Archive member names remain in the logical path, so an exception to an
// existing member never allows a different member or an entire archive.
func assetNameFindings(path string) []finding {
	var rows []finding
	for _, name := range productText.FindAllString(path, -1) {
		rows = append(rows, finding{path, "filename", name, 0})
	}
	return rows
}

func assetFindings(path string, data []byte) ([]finding, error) {
	rows, err := assetContentFindings(path, data)
	return append(assetNameFindings(path), rows...), err
}

func assetContentFindings(path string, data []byte) ([]finding, error) {
	var rows []finding
	child := func(name string, data []byte) error {
		found, err := assetFindings(path+"!"+name, data)
		rows = append(rows, found...)
		return err
	}
	switch {
	case bytes.HasPrefix(data, []byte("PK\x03\x04")) || strings.HasSuffix(path, ".zip"):
		z, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		for _, f := range z.File {
			rows = append(rows, assetNameFindings(path+"!"+f.Name)...)
			if f.FileInfo().IsDir() {
				continue
			}
			r, err := f.Open()
			if err != nil {
				return nil, err
			}
			b, err := io.ReadAll(r)
			r.Close()
			if err != nil {
				return nil, err
			}
			found, err := assetContentFindings(path+"!"+f.Name, b)
			rows = append(rows, found...)
			if err != nil {
				return nil, err
			}
		}
	case bytes.HasPrefix(data, []byte{0x1f, 0x8b}) || strings.HasSuffix(path, ".gz"):
		// Iterate streams explicitly to preserve each gzip member's name.
		r := bytes.NewReader(data)
		for r.Len() > 0 {
			g, err := gzip.NewReader(r)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", path, err)
			}
			g.Multistream(false)
			name := g.Name
			if name == "" {
				name = strings.TrimSuffix(filepath.Base(path), ".gz")
			}
			b, err := io.ReadAll(g)
			g.Close()
			if err != nil {
				return nil, err
			}
			if err := child(name, b); err != nil {
				return nil, err
			}
		}
	case len(data) > 262 && string(data[257:262]) == "ustar":
		r := tar.NewReader(bytes.NewReader(data))
		for {
			h, err := r.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				return nil, err
			}
			rows = append(rows, assetNameFindings(path+"!"+h.Name)...)
			if h.Typeflag != tar.TypeReg {
				continue
			}
			b, err := io.ReadAll(r)
			if err != nil {
				return nil, err
			}
			found, err := assetContentFindings(path+"!"+h.Name, b)
			rows = append(rows, found...)
			if err != nil {
				return nil, err
			}
		}
	default:
		rows = append(rows, textFindings(path, data)...)
	}
	return rows, nil
}

// Counts are exact per file (including archive member paths), never globs.
// Missing entries and stale entries both fail. Embedded-only runs validate
// only host/ entries; a full public-text run also validates text/host/ entries.
func checkCounts(file, scope string, rows []finding) error {
	b, err := os.ReadFile(file)
	if err != nil {
		return err
	}
	actual := map[string]int{}
	for _, r := range rows {
		actual[r.path]++
	}
	var issues []string
	seen := map[string]bool{}
	for i, line := range strings.Split(string(b), "\n") {
		if strings.TrimSpace(line) == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, " | ", 3)
		if len(parts) != 3 || strings.TrimSpace(parts[2]) == "" {
			return fmt.Errorf("%s:%d: expected exact path | count | reason", file, i+1)
		}
		n, err := strconv.Atoi(parts[1])
		if err != nil || n < 1 || seen[parts[0]] {
			return fmt.Errorf("%s:%d: invalid or duplicate count entry", file, i+1)
		}
		seen[parts[0]] = true
		if !strings.HasPrefix(parts[0], scope) {
			continue
		}
		if actual[parts[0]] != n {
			issues = append(issues, fmt.Sprintf("%s: got %d matches, allowlist has %d (%s)", parts[0], actual[parts[0]], n, parts[2]))
		}
		delete(actual, parts[0])
	}
	for path, n := range actual {
		issues = append(issues, fmt.Sprintf("%s: %d unallowlisted product-name matches", path, n))
	}
	sort.Strings(issues)
	if len(issues) > 0 {
		return fmt.Errorf("%s boundary failed:\n%s", file, strings.Join(issues, "\n"))
	}
	fmt.Printf("boundary ok: %s (%d matches, scope %q)\n", file, len(rows), scope)
	return nil
}
