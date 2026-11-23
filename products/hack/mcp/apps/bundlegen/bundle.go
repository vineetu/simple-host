// Package bundlegen builds the judge and organiser MCP Apps' bundled script and
// stylesheet from the real static/hack web app files, from the hand-written
// entry modules apps/entry/{judge,organiser}.js (which set window.__HACKJ /
// window.__HACKO for the glue in hack-{judge,organiser}-view.js).
//
// Bundling is github.com/evanw/esbuild (pinned in go.mod, see esbuildVersion)
// through its Go API: no Node, no floating binary, no network fetch at
// generation time. The server binary never links it (only gentool and the
// tests import it).
//
// The screens are reused byte-for-byte except for calls to a small platform
// adapter (static/hack/platform.js on the website side, apps/frame/platform.js
// here), because a real pathname, same-origin document, navigable page and
// submittable <form> do not exist inside a sandboxed MCP App iframe (the host
// owns navigation; sandbox="allow-scripts" has no allow-forms). Both
// implementations are checked-in files selected by framePlugin; nav.js and
// tour.js are swapped the same way, for in-frame routing and a no-op tour.
package bundlegen

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"github.com/evanw/esbuild/pkg/api"
)

// esbuildVersion names the pinned release (go.mod) for the generated header
// comment; a literal so generation never parses go.mod.
const esbuildVersion = "esbuild v0.28.2"

// moduleRoot locates the repo root (the directory holding go.mod). runtime.Caller
// gives this file's path, but a -trimpath build rewrites it to the module path,
// so fall back to walking up from the working directory. It is esbuild's
// AbsWorkingDir, so every module-wrapper comment in the bundle is a repo-relative
// path: the same bytes from any checkout, which the freshness tests
// (TestHackJudgeBundleFresh, TestHackOrganiserBundleFresh) depend on.
func moduleRoot() (string, error) {
	if _, self, _, ok := runtime.Caller(0); ok && filepath.IsAbs(self) {
		root := filepath.Join(filepath.Dir(self), "..", "..", "..", "..", "..")
		if _, err := os.Stat(filepath.Join(root, "go.mod")); err == nil {
			return filepath.Clean(root), nil
		}
	}
	dir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("bundlegen: %w", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("bundlegen: no go.mod found above %s", dir)
		}
		dir = parent
	}
}

func webRoot(root string) string {
	return filepath.Join(root, "products", "hack", "webassets", "static", "hack")
}
func frameRoot(root string) string {
	return filepath.Join(root, "products", "hack", "mcp", "apps", "frame")
}
func entryRoot(root string) string {
	return filepath.Join(root, "products", "hack", "mcp", "apps", "entry")
}

// checkRoot confirms root is this repo (kit.css is the landmark), so a wrong
// getwd fallback fails loudly instead of as a confusing esbuild resolve error.
func checkRoot(root string) error {
	if _, err := os.Stat(filepath.Join(webRoot(root), "kit.css")); err != nil {
		return fmt.Errorf("bundlegen: products/hack/webassets/static/hack not found under module root %s", root)
	}
	return nil
}

// framePlugin resolves the website's nav.js, tour.js and platform.js imports to
// this frame's implementations under apps/frame, for one app at a time: navFile
// names that app's nav.js stand-in; tour.js and platform.js are shared. It
// matches by resolved filesystem path, not import text, so every import depth is
// caught and an unrelated file with the same name elsewhere is never touched.
func framePlugin(root, navFile string) api.Plugin {
	web := webRoot(root)
	frame := frameRoot(root)
	navAbs := filepath.Join(web, "nav.js")
	tourAbs := filepath.Join(web, "tour.js")
	platformAbs := filepath.Join(web, "platform.js")
	return api.Plugin{
		Name: "hack-frame",
		Setup: func(build api.PluginBuild) {
			build.OnResolve(api.OnResolveOptions{Filter: `(^|/)(nav|tour|platform)\.js$`},
				func(args api.OnResolveArgs) (api.OnResolveResult, error) {
					abs := filepath.Clean(filepath.Join(args.ResolveDir, args.Path))
					switch abs {
					case navAbs:
						return api.OnResolveResult{Path: filepath.Join(frame, navFile), Namespace: "file"}, nil
					case tourAbs:
						return api.OnResolveResult{Path: filepath.Join(frame, "tour.js"), Namespace: "file"}, nil
					case platformAbs:
						return api.OnResolveResult{Path: filepath.Join(frame, "platform.js"), Namespace: "file"}, nil
					}
					// Any other import falls through to esbuild's default resolution.
					return api.OnResolveResult{}, nil
				})
		},
	}
}

// buildBundle runs esbuild once for one entry file and one frame's nav.js
// stand-in. Format IIFE wraps the graph in one `(() => { ... })()`, which a
// classic <script> needs (the frame's CSP allows no external script src). No
// minification and LegalCommentsNone keep the output readable and diff-able.
func buildBundle(root, entry, navFile string) (string, error) {
	result := api.Build(api.BuildOptions{
		EntryPoints:   []string{entry},
		Bundle:        true,
		Write:         false,
		Format:        api.FormatIIFE,
		Target:        api.ES2020,
		Platform:      api.PlatformBrowser,
		LegalComments: api.LegalCommentsNone,
		AbsWorkingDir: root,
		Plugins:       []api.Plugin{framePlugin(root, navFile)},
	})
	if len(result.Errors) > 0 {
		msgs := make([]string, 0, len(result.Errors))
		for _, m := range result.Errors {
			msgs = append(msgs, m.Text)
		}
		return "", fmt.Errorf("bundlegen: esbuild: %s", strings.Join(msgs, "; "))
	}
	if len(result.OutputFiles) != 1 {
		return "", fmt.Errorf("bundlegen: esbuild produced %d output files for %s, want exactly 1", len(result.OutputFiles), entry)
	}
	return string(result.OutputFiles[0].Contents), nil
}

// GenerateJS builds the judge app's single bundled script: ui.js and the four
// judge screens (public-judge, judge-queue, judge-scoring, judge-rubric), reached
// from apps/entry/judge.js, which also sets window.__HACKJ.
func GenerateJS() (string, error) {
	root, err := moduleRoot()
	if err != nil {
		return "", err
	}
	if err := checkRoot(root); err != nil {
		return "", err
	}
	body, err := buildBundle(root, filepath.Join(entryRoot(root), "judge.js"), "judge-nav.js")
	if err != nil {
		return "", err
	}
	// The judge frame has no real-website fallback, so unlike the organiser bundle
	// it must never carry the __ORIGIN__ placeholder (readHackApp substitutes it
	// only for the organiser screen).
	if strings.Contains(body, "__ORIGIN__") {
		return "", fmt.Errorf("bundlegen: judge bundle unexpectedly carries __ORIGIN__ (that placeholder belongs to the organiser frame only; see frame/organiser-nav.js)")
	}
	return generatedHeader + body, nil
}

const generatedHeader = "// GENERATED by products/hack/mcp/apps/bundlegen (go generate ./products/hack/mcp/...), via " + esbuildVersion + ". Do not edit by hand.\n" +
	"// Entry: products/hack/mcp/apps/entry/judge.js, bundling products/hack/webassets/static/hack/{ui.js,screens/_judge-util.js,screens/public-judge.js,screens/judge-queue.js,screens/judge-scoring.js,screens/judge-rubric.js} with products/hack/mcp/apps/frame/{judge-nav.js,tour.js,platform.js} swapped in for nav.js/tour.js/platform.js.\n"

// GenerateOrganiserJS builds the organiser app's single bundled script: ui.js,
// schedule.js and the three organiser screens (overview, people, judging),
// reached from apps/entry/organiser.js, which also sets window.__HACKO.
func GenerateOrganiserJS() (string, error) {
	root, err := moduleRoot()
	if err != nil {
		return "", err
	}
	if err := checkRoot(root); err != nil {
		return "", err
	}
	body, err := buildBundle(root, filepath.Join(entryRoot(root), "organiser.js"), "organiser-nav.js")
	if err != nil {
		return "", err
	}
	// frame/organiser-nav.js carries exactly one __ORIGIN__ placeholder, substituted
	// by readHackApp at serve time. Counted, not just checked for presence, so a
	// duplicate (or a screen containing the same literal) fails generation instead
	// of leaving a wrongly substituted token in a served page.
	if n := strings.Count(body, "__ORIGIN__"); n != 1 {
		return "", fmt.Errorf("bundlegen: organiser bundle carries __ORIGIN__ %d times, want exactly 1 (see frame/organiser-nav.js and hack_apps.go's readHackApp)", n)
	}
	return organiserGeneratedHeader + body, nil
}

const organiserGeneratedHeader = "// GENERATED by products/hack/mcp/apps/bundlegen (go generate ./products/hack/mcp/...), via " + esbuildVersion + ". Do not edit by hand.\n" +
	"// Entry: products/hack/mcp/apps/entry/organiser.js, bundling products/hack/webassets/static/hack/{ui.js,schedule.js,screens/_orgA-util.js,screens/organiser-overview.js,screens/organiser-people.js,screens/organiser-judging.js} with products/hack/mcp/apps/frame/{organiser-nav.js,tour.js,platform.js} swapped in for nav.js/tour.js/platform.js. Carries one __ORIGIN__ placeholder (frame/organiser-nav.js), substituted at serve time.\n"

// GenerateCSS inlines kit.css verbatim except its one @font-face rule: the hand
// font is reserved for the shell's wordmark, and the CSP allows no remote
// resourceDomains, so the font must never be requested.
func GenerateCSS() (string, error) {
	root, err := moduleRoot()
	if err != nil {
		return "", err
	}
	if err := checkRoot(root); err != nil {
		return "", err
	}
	raw, err := os.ReadFile(filepath.Join(webRoot(root), "kit.css"))
	if err != nil {
		return "", err
	}
	css := fontFaceRe.ReplaceAllString(string(raw), "")
	return "/* GENERATED by products/hack/mcp/apps/bundlegen from products/hack/webassets/static/hack/kit.css (its @font-face stripped). Do not edit by hand. */\n" + css, nil
}

var fontFaceRe = regexp.MustCompile(`@font-face\{[^}]*\}`)
