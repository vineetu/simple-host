// Entry point for the judge MCP App's bundled script (hack-judge-
// bundle.js), built by esbuild from internal/mcp/apps/bundlegen
// (`go generate ./internal/mcp/apps/...`, directive in
// internal/mcp/hack_apps.go). Pulls in the real website screens,
// unmodified except the platform adapter calls each one uses in place of
// the browser behaviour that does not hold inside this frame (see
// internal/handler/static/hack/platform.js and this frame's own
// internal/mcp/apps/frame/platform.js). nav.js and tour.js are swapped for
// this frame's own internal/mcp/apps/frame/judge-nav.js and tour.js by the
// esbuild OnResolve plugin (bundlegen's framePlugin) -- nothing here asks
// for them directly.
import * as ui from '../../../handler/static/hack/ui.js'
import * as publicJudge from '../../../handler/static/hack/screens/public-judge.js'
import * as queue from '../../../handler/static/hack/screens/judge-queue.js'
import * as scoring from '../../../handler/static/hack/screens/judge-scoring.js'
import * as rubric from '../../../handler/static/hack/screens/judge-rubric.js'

window.__HACKJ = { ui, screens: { invite: publicJudge, queue, score: scoring, rubric } }
