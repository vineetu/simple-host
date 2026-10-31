package bundlegen

import (
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, root, rel, content string) {
	t.Helper()
	full := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

// TestCheckSupportedRejectsUnhandledConstructs guards the guard: a future
// screen edit that adds a default export, a `export * from`, or a
// multi-line import must fail generation loudly (TestHackJudgeBundleFresh,
// internal/mcp, would otherwise just silently bundle something with a
// stray keyword or no exports.default convention) rather than produce a
// bundle that mis-behaves or fails to parse only once loaded in a browser.
func TestCheckSupportedRejectsUnhandledConstructs(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{"default export", "export default function foo() {}\n"},
		{"star re-export", "export * from './other.js'\n"},
		{"multiline import", "import {\n  a, b\n} from './x.js'\nexport const a = 1\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := checkSupported("fake.js", c.src); err == nil {
				t.Fatalf("checkSupported accepted %s", c.name)
			}
		})
	}
	// And the inverse: ordinary, already-supported shapes must still pass.
	ok := "import { a, b } from './x.js'\nimport * as ui from '../ui.js'\nexport function f() {}\nexport const g = 1\nexport { f as h }\n"
	if err := checkSupported("fake.js", ok); err != nil {
		t.Fatalf("checkSupported rejected a supported file: %v", err)
	}
}

// TestBuildModuleCatchesUnaccountedImport is the second half of the guard:
// an import this module's own judgeModules() entry does not list (so it
// is never deleted) must fail generation too, not silently leave a bare
// `import` keyword in the IIFE.
func TestBuildModuleCatchesUnaccountedImport(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "screens/fake.js", "import { x } from './missing.js'\nexport function f() { return x }\n")
	_, _, err := buildModule(root, module{varName: "fake", srcPath: "screens/fake.js"})
	if err == nil {
		t.Fatal("buildModule accepted an import no judgeModules() entry deletes")
	}
}
