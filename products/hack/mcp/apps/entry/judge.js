// Entry point for the judge MCP App's bundled script (hack-judge-
// bundle.js), built by esbuild from products/hack/mcp/apps/bundlegen
// (`go generate ./products/hack/mcp/...`, directive in
// products/hack/mcp/hack_apps.go). Pulls in the old website's
// screens (kept in products/hack/mcp/apps/legacy), which call this frame's
// platform adapter (products/hack/mcp/apps/frame/platform.js) in place of the
// browser behaviour that does not hold inside this frame. nav.js and tour.js are swapped for
// this frame's own products/hack/mcp/apps/frame/judge-nav.js and tour.js by the
// esbuild OnResolve plugin (bundlegen's framePlugin) -- nothing here asks
// for them directly.
import * as ui from '../legacy/ui.js'
import * as publicJudge from '../legacy/screens/public-judge.js'
import * as queue from '../legacy/screens/judge-queue.js'
import * as scoring from '../legacy/screens/judge-scoring.js'
import * as rubric from '../legacy/screens/judge-rubric.js'

window.__HACKJ = { ui, screens: { invite: publicJudge, queue, score: scoring, rubric } }
