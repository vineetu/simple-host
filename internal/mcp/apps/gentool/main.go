// Command gentool regenerates the judge MCP App's bundled script and
// stylesheet from internal/handler/static/hack. Run it with
// `go generate ./internal/mcp/apps/...` (see the go:generate directive in
// internal/mcp/hack_apps.go) after editing any of the judge screens or
// kit.css; TestHackJudgeBundleFresh fails the build if someone forgets.
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/vsriram/simple-host/internal/mcp/apps/bundlegen"
)

// moduleRoot walks up from the working directory to the nearest go.mod.
// A -trimpath build (this repo's default) makes runtime.Caller's path the
// module path, not a filesystem one, so this command is meant to be run
// with `go run` from inside the repo rather than as a built binary.
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
	appsDir := filepath.Join(root, "internal", "mcp", "apps")

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
	fmt.Println("gentool: wrote hack-judge-bundle.js and hack-judge-kit.css")
}
