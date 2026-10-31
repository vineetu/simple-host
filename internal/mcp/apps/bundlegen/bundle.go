// Package bundlegen hand-assembles the judge MCP App's script and
// stylesheet from the real static/hack web app files, at build time, with
// no bundler and no new go.mod dependency.
//
// Why hand-assembly rather than esbuild: the plan's first choice was a
// go:generate step running a pinned esbuild via `go run
// github.com/evanw/esbuild/cmd/esbuild@<version>`. On this box that command
// is blocked (fetching and executing a third-party module is refused by the
// sandbox), and the generated artifact must still be reproducible by anyone
// building this repo. So this package plays the one part of a bundler the
// judge screens actually need: turning a handful of ES modules, each
// `import`/`export`, into one script with no module loader, by wrapping
// each source file in its own closure (a tiny hand-rolled CommonJS: each
// module gets a local `exports` object; an import becomes a local alias
// pointing at the already-built dependency's exports). The transform is
// line-oriented regex, not a JS parser, and only ever sees the fixed, known
// set of files below.
//
// The screens are reused byte-for-byte from disk except for two named,
// documented patches (patches.go) where the real browser environment
// (location.pathname naming the real page, a sandboxed iframe's opaque
// origin) does not hold inside the MCP App frame. Everything else --
// render(ctx), the markup, the escaping, the wording -- is the website's
// own code, unmodified.
package bundlegen

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
)

// webRoot locates internal/handler/static/hack. runtime.Caller gives this
// source file's own path, but a -trimpath build (this repo's default,
// go env GOFLAGS) rewrites that to the module path, not a filesystem one,
// so that only works untrimmed. Either way, fall back to walking up from
// the working directory to the nearest go.mod, which holds regardless of
// how the binary was built or where `go run`/`go test` was invoked from
// inside the repo.
func webRoot() (string, error) {
	if _, self, _, ok := runtime.Caller(0); ok && filepath.IsAbs(self) {
		root := filepath.Join(filepath.Dir(self), "..", "..", "..", "handler", "static", "hack")
		if _, err := os.Stat(filepath.Join(root, "kit.css")); err == nil {
			return root, nil
		}
	}
	dir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("bundlegen: %w", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			root := filepath.Join(dir, "internal", "handler", "static", "hack")
			if _, err := os.Stat(filepath.Join(root, "kit.css")); err == nil {
				return root, nil
			}
			return "", fmt.Errorf("bundlegen: static/hack not found under module root %s", dir)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("bundlegen: no go.mod found above %s", dir)
		}
		dir = parent
	}
}

// module describes one source file folded into the bundle's IIFE.
type module struct {
	// varName is this module's exports object, __M_<varName>.
	varName string
	// srcPath is relative to webRoot (screens/ is a subdirectory).
	srcPath string
	// stub is extra code emitted at the top of this module's closure,
	// before its own body -- used for the nav.js/tour.js replacements
	// (see shims.go) that real screens import but which this frame
	// answers differently (in-frame routing, no guided tour).
	stub string
	// imports are literal lines (exact, after TrimSpace) deleted from the
	// body, each paired with the replacement local-alias line emitted in
	// their place, immediately after the stub.
	imports []importRewrite
}

type importRewrite struct {
	original    string
	replacement string
}

// exportRe matches a top-level `export function f`, `export async function
// f`, `export const f` or `export let f` declaration, capturing the name.
var exportRe = regexp.MustCompile(`(?m)^export (?:async function|function|const|let) ([A-Za-z0-9_]+)`)

// exportAliasRe matches `export { a as b }`.
var exportAliasRe = regexp.MustCompile(`(?m)^export \{\s*([A-Za-z0-9_]+)\s+as\s+([A-Za-z0-9_]+)\s*\}\s*$`)

// stripExportRe removes the leading "export " keyword from a declaration
// line, once exportRe/exportAliasRe have already recorded its name(s).
var stripExportRe = regexp.MustCompile(`(?m)^export (async function|function|const|let)\b`)

// unsupportedRe flags the JS constructs this hand-rolled transform does not
// understand, so a future edit to one of the real screens fails generation
// loudly instead of silently mis-bundling (e.g. a stray `import`/`export`
// keyword left in the output, or a default export this transform has no
// exports.default convention for since nothing here imports one). Checked
// against the raw source before any rewriting.
var (
	defaultExportRe = regexp.MustCompile(`(?m)^export default\b`)
	starExportRe    = regexp.MustCompile(`(?m)^export \*`)
	// A supported import is a single line: `import { a, b } from '...'` or
	// `import * as x from '...'`, both ending the line with the closing
	// quote. Anything that opens an import but is not closed on the same
	// line (a multi-line import, broken across a trailing comma) does not
	// match this and is reported as unsupported instead of silently
	// producing a stray `import` keyword in the generated script.
	supportedImportLineRe   = regexp.MustCompile(`(?m)^import (?:\{[^{}\n]*\}|\* as [A-Za-z0-9_]+) from '[^'\n]+'\s*$`)
	anyImportKeywordLineRe  = regexp.MustCompile(`(?m)^\s*import\b`)
	strayKeywordLineAfterRe = regexp.MustCompile(`(?m)^\s*(?:import|export)\b`)
)

// checkSupported rejects, with a specific reason, any construct in raw this
// transform cannot handle. Called on the untouched source, before patches
// or import rewriting, so the line numbers and text in the error match
// what is actually checked out in internal/handler/static/hack.
func checkSupported(srcPath, raw string) error {
	if m := defaultExportRe.FindString(raw); m != "" {
		return fmt.Errorf("bundlegen: %s: has a default export, which this hand-rolled transform has no convention for (no module here imports one); teach buildModule about it or avoid it in the screen", srcPath)
	}
	if m := starExportRe.FindString(raw); m != "" {
		return fmt.Errorf("bundlegen: %s: has %q, a re-export this transform does not resolve", srcPath, m)
	}
	// Every line beginning an import must be fully matched by the
	// single-line form; a multi-line import (the statement continuing past
	// the first newline) starts a line that anyImportKeywordLineRe finds
	// but supportedImportLineRe's $ anchor will not, since the closing
	// `from '...'` is on a later line.
	starts := anyImportKeywordLineRe.FindAllStringIndex(raw, -1)
	complete := supportedImportLineRe.FindAllStringIndex(raw, -1)
	if len(starts) != len(complete) {
		return fmt.Errorf("bundlegen: %s: has an import this transform cannot parse as one line (a multi-line import, or a shape other than `import { a, b } from '...'` / `import * as x from '...'`)", srcPath)
	}
	return nil
}

func buildModule(root string, m module) (string, []string, error) {
	raw, err := os.ReadFile(filepath.Join(root, m.srcPath))
	if err != nil {
		return "", nil, fmt.Errorf("bundlegen: reading %s: %w", m.srcPath, err)
	}
	if err := checkSupported(m.srcPath, string(raw)); err != nil {
		return "", nil, err
	}
	body, err := applyPatches(m.srcPath, string(raw))
	if err != nil {
		return "", nil, err
	}

	for _, imp := range m.imports {
		// Anchored to a full line (start of line through the end of line,
		// consuming the trailing newline), not a bare substring match:
		// Grok security review, SIM-344, on the risk of imp.original's
		// exact text appearing somewhere other than the import line it
		// names -- inside a comment or a string literal elsewhere in the
		// same file -- which a plain strings.Replace would delete on
		// sight, silently, wherever it happened to match first.
		pattern := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(imp.original) + `\n?`)
		if !pattern.MatchString(body) {
			return "", nil, fmt.Errorf("bundlegen: %s: expected import line not found: %q", m.srcPath, imp.original)
		}
		body = pattern.ReplaceAllString(body, "")
	}

	names := map[string]bool{}
	for _, match := range exportRe.FindAllStringSubmatch(body, -1) {
		names[match[1]] = true
	}
	var aliasLines []string
	for _, match := range exportAliasRe.FindAllStringSubmatch(body, -1) {
		orig, alias := match[1], match[2]
		aliasLines = append(aliasLines, fmt.Sprintf("exports.%s = %s;", alias, orig))
	}
	body = exportAliasRe.ReplaceAllString(body, "")
	body = stripExportRe.ReplaceAllString(body, "$1")

	// Catches a new import this module's hand-maintained judgeModules()
	// entry was never told about (so it was never deleted above), or any
	// other export shape stripExportRe/exportAliasRe don't cover: either
	// would otherwise leave a bare `import`/`export` keyword sitting
	// inside the IIFE, which is a syntax error the moment the bundle is
	// loaded in a classic (non-module) <script> -- caught here, at
	// generation time, instead.
	if loc := strayKeywordLineAfterRe.FindString(body); loc != "" {
		return "", nil, fmt.Errorf("bundlegen: %s: left a bare %q after the transform -- a new import/export this module's judgeModules() entry does not account for", m.srcPath, strings.TrimSpace(loc))
	}

	exportNames := make([]string, 0, len(names))
	for name := range names {
		exportNames = append(exportNames, name)
	}
	sort.Strings(exportNames)

	var out strings.Builder
	fmt.Fprintf(&out, "var __M_%s = (function(){\n'use strict';\nvar exports = {};\n", m.varName)
	if m.stub != "" {
		out.WriteString(m.stub)
		out.WriteString("\n")
	}
	for _, imp := range m.imports {
		out.WriteString(imp.replacement)
		out.WriteString("\n")
	}
	out.WriteString(body)
	out.WriteString("\n")
	for _, name := range exportNames {
		fmt.Fprintf(&out, "exports.%s = %s;\n", name, name)
	}
	for _, line := range aliasLines {
		out.WriteString(line)
		out.WriteString("\n")
	}
	out.WriteString("return exports;\n})();\n")
	return out.String(), exportNames, nil
}

// GenerateJS builds the judge app's single bundled script: the shared
// helpers (ui.js, screens/_judge-util.js) and the four judge screens
// (public-judge, judge-queue, judge-scoring, judge-rubric), each its own
// module, concatenated inside one IIFE that exposes the result as
// `window.__HACKJ = { ui, screens: { invite, queue, score, rubric } }` for
// the hand-written glue (hack-judge-view.js) to drive.
func GenerateJS() (string, error) {
	root, err := webRoot()
	if err != nil {
		return "", err
	}
	var out strings.Builder
	out.WriteString(generatedHeader)
	out.WriteString("(function(){\n'use strict';\n")
	for _, m := range judgeModules() {
		code, _, err := buildModule(root, m)
		if err != nil {
			return "", err
		}
		out.WriteString(code)
		out.WriteString("\n")
	}
	out.WriteString(`window.__HACKJ = { ui: __M_ui, screens: { invite: __M_publicJudge, queue: __M_queue, score: __M_scoring, rubric: __M_rubric } };` + "\n")
	out.WriteString("})();\n")
	return out.String(), nil
}

const generatedHeader = "// GENERATED by internal/mcp/apps/bundlegen (go generate ./internal/mcp/apps/gentool). Do not edit by hand.\n" +
	"// Source: internal/handler/static/hack/{ui.js,screens/_judge-util.js,screens/public-judge.js,screens/judge-queue.js,screens/judge-scoring.js,screens/judge-rubric.js}\n"

// judgeModules lists every module the judge bundle needs, in dependency
// order (each only references __M_* vars already built above it).
func judgeModules() []module {
	ui := module{varName: "ui", srcPath: "ui.js", stub: navStub, imports: []importRewrite{
		{"import { isAppRoute } from './nav.js'", "var isAppRoute = __hackIsAppRoute;"},
	}}
	judgeutil := module{varName: "judgeutil", srcPath: "screens/_judge-util.js", imports: []importRewrite{
		{"import { esc } from '../ui.js'", "var esc = __M_ui.esc;"},
		{"import * as ui from '../ui.js'", "var ui = __M_ui;"},
	}}
	navImport := importRewrite{"import { href } from '../nav.js'", "var href = __hackHref;"}

	publicJudge := module{varName: "publicJudge", srcPath: "screens/public-judge.js", imports: []importRewrite{
		{"import { errText, dateRange } from './_judge-util.js'", "var errText = __M_judgeutil.errText, dateRange = __M_judgeutil.dateRange;"},
	}}
	queue := module{varName: "queue", srcPath: "screens/judge-queue.js", imports: []importRewrite{
		navImport,
		{"import { errText, newTabLink, siteHref, scoreTotals } from './_judge-util.js'", "var errText = __M_judgeutil.errText, newTabLink = __M_judgeutil.newTabLink, siteHref = __M_judgeutil.siteHref, scoreTotals = __M_judgeutil.scoreTotals;"},
	}}
	scoring := module{varName: "scoring", srcPath: "screens/judge-scoring.js", imports: []importRewrite{
		navImport,
		{"import { errText, newTabLink, siteHref } from './_judge-util.js'", "var errText = __M_judgeutil.errText, newTabLink = __M_judgeutil.newTabLink, siteHref = __M_judgeutil.siteHref;"},
		{"import { startTour } from '../tour.js'", "var startTour = __hackStartTour;"},
	}}
	rubric := module{varName: "rubric", srcPath: "screens/judge-rubric.js", imports: []importRewrite{
		{"import { paragraphs } from './_judge-util.js'", "var paragraphs = __M_judgeutil.paragraphs;"},
	}}
	return []module{ui, judgeutil, publicJudge, queue, scoring, rubric}
}

// navStub stands in for the one function ui.js itself imports from nav.js.
// isAppRoute only gates the full-page dirty-navigation guard (confirming a
// same-document SPA link vs. a real navigation); the judge frame has
// neither, so it is always false.
const navStub = "function __hackIsAppRoute(path) { return false; }"

// GenerateCSS inlines kit.css verbatim except for its one @font-face rule:
// the hand font is reserved for the shell's own wordmark, which no judge
// view renders, and the CSP here allows no remote resourceDomains at all,
// so the font must never be requested.
func GenerateCSS() (string, error) {
	root, err := webRoot()
	if err != nil {
		return "", err
	}
	raw, err := os.ReadFile(filepath.Join(root, "kit.css"))
	if err != nil {
		return "", err
	}
	css := fontFaceRe.ReplaceAllString(string(raw), "")
	return "/* GENERATED by internal/mcp/apps/bundlegen from internal/handler/static/hack/kit.css (its @font-face stripped). Do not edit by hand. */\n" + css, nil
}

var fontFaceRe = regexp.MustCompile(`@font-face\{[^}]*\}`)
