import { test } from 'node:test'
import assert from 'node:assert/strict'
import fs from 'node:fs'
import vm from 'node:vm'

const navigation = JSON.parse(fs.readFileSync('products/hack/rules/hackevents/navigation.json', 'utf8'))
// The old website's navigation, kept for the MCP Apps (products/hack/mcp/apps/legacy).
// The web app's own routing is tested in web/hack (resolve.test.ts, signin.test.ts).

test('fixed schedule rows distinguish voting settings from results publication', () => {
 const source = fs.readFileSync('products/hack/mcp/apps/legacy/schedule.js', 'utf8').replace(/^import .*\n/gm, '').replaceAll('export ', '')
 const scope = vm.createContext({ navigation, encodeURIComponent, href: (role, slug, section) => `/e/${slug}/manage?s=${section}` })
 const resolver = fs.readFileSync('products/hack/mcp/apps/legacy/navigation.js', 'utf8').replace(/^import .*\n/gm, '').replaceAll('export ', '')
 vm.runInContext(resolver, scope)
 vm.runInContext(source + ';globalThis.link = setInLink', scope)
 assert.equal(scope.link('results','demo','voting').href, '/e/demo/manage?s=settings&tab=results#voting')
 assert.equal(scope.link('results','demo','results_published').href, '/e/demo/manage?s=results')
})

test('public event target keeps the authoritative hosted or self-hosted URL', () => {
 const scope=vm.createContext({navigation,encodeURIComponent})
 const source=fs.readFileSync('products/hack/mcp/apps/legacy/navigation.js','utf8').replace(/^import .*\n/gm,'').replaceAll('export ','')
 vm.runInContext(source+';globalThis.target=resolve',scope)
 for(const url of ['https://demo.simple-hack.app/','https://hack.example.org/e/demo?view=public','https://demo.events.example.org/']) assert.equal(scope.target('o.public-page','demo',{event_url:url}).href,url)
})

test('no-next-action copy distinguishes archive, inspection and takedown', () => {
 const source=fs.readFileSync('products/hack/mcp/apps/legacy/screens/organiser-overview.js','utf8')
 const begin=source.indexOf('function nextStepContent(')
 const end=source.indexOf('\nexport async function render',begin)
 const scope=vm.createContext({})
 vm.runInContext(source.slice(begin,end)+';globalThis.content=nextStepContent',scope)
 for(const [event,label] of [[{stage:'archived'},'This event has ended'],[{stage:'building'},'Read-only view'],[{stage:'building',taken_down:true},'This event is taken down']]) assert.equal(scope.content(event,{kind:'none'}).h2,label)
})
