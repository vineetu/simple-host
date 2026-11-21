// Package envlint holds the one test that parses every server source file.
// It lives apart from package config so that an edit anywhere else reruns only
// this tiny package, not config's own tests.
package envlint

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	platform "github.com/vsriram/simple-host/host/config"
	"github.com/vsriram/simple-host/products/hack/config"
)

// Every server environment read is present in the composed product catalogue.
func TestSettingsCoverEveryEnvRead(t *testing.T) {
	read := map[string]bool{}
	for _, k := range config.Knobs() {
		read[k.Env] = true
	}
	for _, k := range platform.Knobs() {
		read[k.Env] = true
	}
	// Only server sources belong here. Skip cmd/ip-country-load (its only
	// env read is the already registered DB_DSN) and cmd/analytics-rebuild
	// (an operator CLI with no env reads).
	// testdb and geoiptest contain test-only database/GeoIP fixture helpers.
	// Scan product sources as well as the platform, so a new literal env read
	// missing from both Settings and the generated docs still fails.
	for _, root := range []string{"../..", "../../../../host", "../../../../cmd/simple-hack"} {
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
						if strings.Contains(strings.ToLower(f.Name), "getenv") || f.Name == "photoLimit" || f.Name == "envInt" || f.Name == "appLimit" {
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
	data, err := platform.SettingsJSON(config.Settings()...)
	if err != nil {
		t.Fatal(err)
	}
	var catalogue platform.SettingsDoc
	if err := json.Unmarshal(data, &catalogue); err != nil {
		t.Fatal(err)
	}
	listed := map[string]bool{}
	for _, s := range catalogue.Settings {
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
		t.Errorf("read by the server but not in the composed settings registry: %v", missing)
	}
	if len(extra) > 0 {
		t.Errorf("in the settings registry but never read: %v", extra)
	}
}
