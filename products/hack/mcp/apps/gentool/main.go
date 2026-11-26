// Command gentool regenerates the judge and organiser MCP Apps' bundled scripts
// and stylesheets from products/hack/mcp/apps/legacy (and the directory page's
// static/hack/kit.css). Run it with
// `go generate ./products/hack/mcp/...` after editing a screen or kit.css; the
// TestHack*BundleFresh tests fail if someone forgets.
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/vsriram/simple-host/products/hack/mcp/apps/bundlegen"
)

// moduleRoot walks up from the working directory to the nearest go.mod. A
// -trimpath build makes runtime.Caller's path a module path, so run this with
// `go run` from inside the repo, not as a built binary.
func moduleRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("no go.mod found above %s", dir)
		}
		dir = parent
	}
}

func main() {
	root, err := moduleRoot()
	if err != nil {
		fmt.Fprintln(os.Stderr, "gentool:", err)
		os.Exit(1)
	}
	appsDir := filepath.Join(root, "products", "hack", "mcp", "apps")

	js, err := bundlegen.GenerateJS()
	if err != nil {
		fmt.Fprintln(os.Stderr, "gentool:", err)
		os.Exit(1)
	}
	if err := os.WriteFile(filepath.Join(appsDir, "hack-judge-bundle.js"), []byte(js), 0644); err != nil {
		fmt.Fprintln(os.Stderr, "gentool:", err)
		os.Exit(1)
	}

	css, err := bundlegen.GenerateCSS()
	if err != nil {
		fmt.Fprintln(os.Stderr, "gentool:", err)
		os.Exit(1)
	}
	if err := os.WriteFile(filepath.Join(appsDir, "hack-judge-kit.css"), []byte(css), 0644); err != nil {
		fmt.Fprintln(os.Stderr, "gentool:", err)
		os.Exit(1)
	}

	orgJS, err := bundlegen.GenerateOrganiserJS()
	if err != nil {
		fmt.Fprintln(os.Stderr, "gentool:", err)
		os.Exit(1)
	}
	if err := os.WriteFile(filepath.Join(appsDir, "hack-organiser-bundle.js"), []byte(orgJS), 0644); err != nil {
		fmt.Fprintln(os.Stderr, "gentool:", err)
		os.Exit(1)
	}
	// Same kit.css as the judge bundle, written to its own file because each
	// screen's asset names derive from its screen.ID.
	if err := os.WriteFile(filepath.Join(appsDir, "hack-organiser-kit.css"), []byte(css), 0644); err != nil {
		fmt.Fprintln(os.Stderr, "gentool:", err)
		os.Exit(1)
	}
	fmt.Println("gentool: wrote hack-judge-bundle.js, hack-judge-kit.css, hack-organiser-bundle.js and hack-organiser-kit.css")
}
