// Package bundlegen builds the judge and organiser MCP Apps' bundled
// script and stylesheet from the real static/hack web app files, at
// build time, from internal/mcp/apps/entry/{judge,organiser}.js, each a
// small hand-written entry module that imports the screens it needs and
// sets window.__HACKJ / window.__HACKO for the hand-written glue
// (hack-{judge,organiser}-view.js) to drive.
//
// Bundling itself is github.com/evanw/esbuild (pinned in go.mod; see
// esbuildVersion below), called through its Go API -- no Node, no
// floating binary, no network fetch at build or generation time; the
// module is fetched once into the module cache like any other dependency
// and the server binary never links it (gentool and the tests here are
// the only importers; `go list -deps ./cmd/server | grep esbuild` is
// empty). Earlier revisions of this package hand-rolled a regex "bundler"
// instead, because a pinned `go run github.com/evanw/esbuild/...` needed
// to fetch and execute a third-party module, which this box's build
// sandbox refused at the time (astra-review problem 8; target.md §7).
// That restriction no longer holds (owner-approved 2026-11-02): this
// version calls esbuild's own Bundle/parse/link/print pipeline instead of
// a line-oriented regex pass, so it understands actual JavaScript rather
// than the fixed, narrow import/export shapes the old transform could.
//
// The screens are reused byte-for-byte from disk except for calls to a
// small platform adapter (internal/handler/static/hack/platform.js on the
// website side, internal/mcp/apps/frame/platform.js on this side) where
// the real browser environment (a real pathname, a same-origin document,
// a page the browser can navigate to, a <form> that can submit) does not
// hold inside an MCP App frame (an isolated, sandboxed iframe with its
// own, unrelated location, that the host -- not this app -- owns
// navigation for, and whose sandbox="allow-scripts" has no allow-forms).
// Those calls are ordinary source in the real screens, not a generation-
// time patch: both implementations are checked-in files, selected by
// framePlugin below. nav.js and tour.js are swapped the same way, for
// in-frame routing and a no-op guided tour.
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

// esbuildVersion names the pinned release (go.mod) for the generated
// header comment, kept as a string literal rather than read back out of
// go.mod so a generation run never needs to parse it.
const esbuildVersion = "esbuild v0.28.2"

// moduleRoot locates this repo's root (the directory holding go.mod).
// runtime.Caller gives this source file's own path, but a -trimpath build
// (this repo's default, go env GOFLAGS) rewrites that to the module path,
// not a filesystem one, so that only works untrimmed. Either way, fall
// back to walking up from the working directory to the nearest go.mod,
// which holds regardless of how the binary was built or where
// `go run`/`go test` was invoked from inside the repo. Used as esbuild's
// AbsWorkingDir, so every module-wrapper comment esbuild emits in the
// bundle is a path relative to the repo root (e.g. "internal/handler/
// static/hack/ui.js"), never an absolute one -- the same bytes from any
// checkout directory, which is what the freshness tests (TestHackJudge
// BundleFresh, TestHackOrganiserBundleFresh, internal/mcp) depend on.
func moduleRoot() (string, error) {
	if _, self, _, ok := runtime.Caller(0); ok && filepath.IsAbs(self) {
		root := filepath.Join(filepath.Dir(self), "..", "..", "..", "..")
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

func webRoot(root string) string   { return filepath.Join(root, "internal", "handler", "static", "hack") }
func frameRoot(root string) string { return filepath.Join(root, "internal", "mcp", "apps", "frame") }
func entryRoot(root string) string { return filepath.Join(root, "internal", "mcp", "apps", "entry") }

// checkRoot confirms root actually is this repo (kit.css is the same
// landmark the old webRoot() checked), so a wrong guess from moduleRoot's
// getwd fallback fails loudly instead of esbuild reporting a confusing
// "file not found" deep in a resolve error.
func checkRoot(root string) error {
	if _, err := os.Stat(filepath.Join(webRoot(root), "kit.css")); err != nil {
		return fmt.Errorf("bundlegen: internal/handler/static/hack not found under module root %s", root)
	}
	return nil
}

// framePlugin resolves the website's nav.js, tour.js and platform.js
// imports to this frame's own implementations under
// internal/mcp/apps/frame, for exactly one app (judge or organiser) at a
// time: navFile names that app's own nav.js stand-in (judge-nav.js or
// organiser-nav.js); tour.js and platform.js are shared by both apps.
// Matched by the resolved filesystem path, not by the literal import
// text, so it catches every import of these three files regardless of
// how many directories separate the importer from internal/handler/
// static/hack (ui.js's own './nav.js' and a screen's '../nav.js' both
// land here) -- and, just as importantly, never touches an unrelated
// file that happens to also be named nav.js, tour.js or platform.js
// somewhere outside that one directory.
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
					// Not one of the three files this frame answers
					// differently: fall through to esbuild's own default
					// resolution (every other import -- _judge-util.js,
					// _orgA-util.js, schedule.js, the screens themselves).
					return api.OnResolveResult{}, nil
				})
		},
	}
}

// buildBundle runs esbuild once, for one entry file and one frame's nav.js
// stand-in. Bundle:true + Write:false asks for esbuild's own module
// resolution, linking and printing with no files written to disk; Format
// IIFE wraps the whole graph in one `(() => { ... })()` with no module
// loader at runtime, which is what a classic <script> (no type="module",
// this frame's CSP allows no external script src at all) needs. No
// minification and LegalCommentsNone keep the generated file readable and
// diff-able; neither changes behaviour.
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

// GenerateJS builds the judge app's single bundled script: ui.js and the
// four judge screens (public-judge, judge-queue, judge-scoring,
// judge-rubric), reached from internal/mcp/apps/entry/judge.js, which
// also sets `window.__HACKJ = { ui, screens: { invite, queue, score,
// rubric } }` for the hand-written glue (hack-judge-view.js) to drive --
// that assignment is part of the bundled entry file, not added here.
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
	// The judge frame has no real-website fallback (every section it asks
	// nav.js for is answered in-frame), so unlike the organiser bundle
	// below it must never carry the organiser frame's __ORIGIN__
	// placeholder -- readHackApp (internal/mcp/hack_apps.go) only
	// substitutes that token for the organiser screen.
	if strings.Contains(body, "__ORIGIN__") {
		return "", fmt.Errorf("bundlegen: judge bundle unexpectedly carries __ORIGIN__ (that placeholder belongs to the organiser frame only; see frame/organiser-nav.js)")
	}
	return generatedHeader + body, nil
}

const generatedHeader = "// GENERATED by internal/mcp/apps/bundlegen (go generate ./internal/mcp/apps/gentool), via " + esbuildVersion + ". Do not edit by hand.\n" +
	"// Entry: internal/mcp/apps/entry/judge.js, bundling internal/handler/static/hack/{ui.js,screens/_judge-util.js,screens/public-judge.js,screens/judge-queue.js,screens/judge-scoring.js,screens/judge-rubric.js} with internal/mcp/apps/frame/{judge-nav.js,tour.js,platform.js} swapped in for nav.js/tour.js/platform.js.\n"

// GenerateOrganiserJS builds the organiser app's single bundled script:
// ui.js, schedule.js and the three Phase 2 organiser screens
// (organiser-overview, organiser-people, organiser-judging), reached from
// internal/mcp/apps/entry/organiser.js, which also sets `window.__HACKO =
// { ui, screens: { overview, people, judging } }` for the hand-written
// glue (hack-organiser-view.js) to drive. Teams, entries, results and the
// event page (and their own _orgB-util.js) are later phases; this bundle
// only ever builds the three screens above.
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
	// frame/organiser-nav.js carries exactly one __ORIGIN__ placeholder,
	// substituted by readHackApp (internal/mcp/hack_apps.go) at serve time
	// -- counted, not just checked for presence, so a future edit that
	// duplicates it (or a screen that coincidentally contains the same
	// literal text) fails generation loudly instead of leaving an
	// un-substituted or wrongly-substituted token in a served page (same
	// reasoning patches.go's import-anchoring used to apply to its own
	// exact-text replacements, Grok security review, SIM-344).
	if n := strings.Count(body, "__ORIGIN__"); n != 1 {
		return "", fmt.Errorf("bundlegen: organiser bundle carries __ORIGIN__ %d times, want exactly 1 (see frame/organiser-nav.js and hack_apps.go's readHackApp)", n)
	}
	return organiserGeneratedHeader + body, nil
}

const organiserGeneratedHeader = "// GENERATED by internal/mcp/apps/bundlegen (go generate ./internal/mcp/apps/gentool), via " + esbuildVersion + ". Do not edit by hand.\n" +
	"// Entry: internal/mcp/apps/entry/organiser.js, bundling internal/handler/static/hack/{ui.js,schedule.js,screens/_orgA-util.js,screens/organiser-overview.js,screens/organiser-people.js,screens/organiser-judging.js} with internal/mcp/apps/frame/{organiser-nav.js,tour.js,platform.js} swapped in for nav.js/tour.js/platform.js. Carries one __ORIGIN__ placeholder (frame/organiser-nav.js), substituted at serve time.\n"

// GenerateCSS inlines kit.css verbatim except for its one @font-face rule:
// the hand font is reserved for the shell's own wordmark, which no judge
// or organiser view renders, and the CSP here allows no remote
// resourceDomains at all, so the font must never be requested.
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
	return "/* GENERATED by internal/mcp/apps/bundlegen from internal/handler/static/hack/kit.css (its @font-face stripped). Do not edit by hand. */\n" + css, nil
}

var fontFaceRe = regexp.MustCompile(`@font-face\{[^}]*\}`)
