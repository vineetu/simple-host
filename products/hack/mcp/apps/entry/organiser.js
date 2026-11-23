// Entry point for the organiser MCP App's bundled script (hack-organiser-
// bundle.js), built by esbuild from products/hack/mcp/apps/bundlegen
// (`go generate ./products/hack/mcp/...`, directive in
// products/hack/mcp/hack_apps_organiser.go). Pulls in the real website
// screens, unmodified except the platform adapter calls each one uses in
// place of the browser behaviour that does not hold inside this frame
// (see products/hack/webassets/static/hack/platform.js and this frame's own
// products/hack/mcp/apps/frame/platform.js). nav.js and tour.js are swapped for
// this frame's own products/hack/mcp/apps/frame/organiser-nav.js and tour.js by
// the esbuild OnResolve plugin (bundlegen's framePlugin) -- nothing here
// asks for them directly. schedule.js (Overview's next-step cue wording)
// comes in transitively through organiser-overview.js's own import.
//
// Phase 2 only: overview, people, judging -- teams, entries, results and
// the event page are later phases (see GenerateOrganiserJS).
import { resolve } from '../frame/organiser-nav.js'
import * as ui from '../../../webassets/static/hack/ui.js'
import * as overview from '../../../webassets/static/hack/screens/organiser-overview.js'
import * as people from '../../../webassets/static/hack/screens/organiser-people.js'
import * as judging from '../../../webassets/static/hack/screens/organiser-judging.js'

window.__HACKO = { ui, resolve, screens: { overview, people, judging } }
