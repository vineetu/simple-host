package config

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// Every environment variable the server reads is in the registry, and the
// registry holds nothing it does not read.
func TestSettingsCoverEveryEnvRead(t *testing.T) {
	read := map[string]bool{}
	for _, k := range Knobs() {
		read[k.Env] = true
	}
	// Only server sources belong here. Skip cmd/ip-country-load (its only
	// env read is the already registered DB_DSN) and cmd/analytics-rebuild
	// (an operator CLI with no env reads).
	// testdb and geoiptest contain test-only database/GeoIP fixture helpers.
	// Product extensions are part of the server and share its settings registry.
	for _, root := range []string{"..", "../../cmd/server", "../../products"} {
		err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				if entry.Name() == "testdb" || entry.Name() == "geoiptest" || entry.Name() == "testdata" {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
			if err != nil {
				return err
			}
			ast.Inspect(file, func(node ast.Node) bool {
				var literal ast.Expr
				switch n := node.(type) {
				case *ast.CallExpr:
					if len(n.Args) == 0 {
						break
					}
					switch f := n.Fun.(type) {
					case *ast.SelectorExpr:
						if pkg, ok := f.X.(*ast.Ident); ok && pkg.Name == "os" && (f.Sel.Name == "Getenv" || f.Sel.Name == "LookupEnv") {
							literal = n.Args[0]
						}
					case *ast.Ident:
						// Literal calls through the getenv-style helpers used by the server.
						// LoadLimits(os.Getenv) reads Knobs(), already included above.
						if strings.Contains(strings.ToLower(f.Name), "getenv") || f.Name == "photoLimit" || f.Name == "appLimit" {
							literal = n.Args[0]
						}
					}
				case *ast.KeyValueExpr:
					// appScreen stores its environment key in Flag, read indirectly.
					if key, ok := n.Key.(*ast.Ident); ok && key.Name == "Flag" {
						literal = n.Value
					}
				}
				if value, ok := literal.(*ast.BasicLit); ok && value.Kind == token.STRING {
					name, err := strconv.Unquote(value.Value)
					if err == nil {
						read[name] = true
					}
				}
				return true
			})
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	listed := map[string]bool{}
	for _, s := range Settings() {
		if listed[s.Name] {
			t.Errorf("%s is listed twice", s.Name)
		}
		listed[s.Name] = true
		if s.Description == "" || s.Type == "" {
			t.Errorf("%s needs a description and a type", s.Name)
		}
	}
	var missing, extra []string
	for n := range read {
		if !listed[n] {
			missing = append(missing, n)
		}
	}
	for n := range listed {
		if !read[n] {
			extra = append(extra, n)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	if len(missing) > 0 {
		t.Errorf("read by the server but not in the settings registry (internal/config/settings.go): %v", missing)
	}
	if len(extra) > 0 {
		t.Errorf("in the settings registry but never read: %v", extra)
	}
	for n := range knobDocs {
		if !read[n] {
			t.Errorf("knobDocs has %s, which is not a knob", n)
		}
	}
}

// docs/advanced/settings.json is exactly what `simple-host settings --json`
// prints. Refresh it with scripts/sync-settings.sh.
func TestSettingsJSONMatchesDocs(t *testing.T) {
	want, err := SettingsJSON()
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile("../../docs/advanced/settings.json")
	if err != nil {
		t.Fatalf("%v (run scripts/sync-settings.sh)", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatal("docs/advanced/settings.json differs from the settings in code: run scripts/sync-settings.sh")
	}
}

// Every security-sensitive switch or choice says which of its values is
// strictest (the setup form follows it), naming exactly the
// values it allows.
func TestSettingsStrictOrder(t *testing.T) {
	for _, s := range Settings() {
		if s.StrictOrder == nil {
			if s.Security && (s.Type == "bool" || s.Type == "enum") {
				t.Errorf("%s is security-sensitive but has no strict_order", s.Name)
			}
			continue
		}
		a, b := append([]string(nil), s.Allowed...), append([]string(nil), s.StrictOrder...)
		sort.Strings(a)
		sort.Strings(b)
		if !s.Security || !slices.Equal(a, b) {
			t.Errorf("%s: strict_order %v does not match allowed %v (security %v)", s.Name, s.StrictOrder, s.Allowed, s.Security)
		}
	}
}
