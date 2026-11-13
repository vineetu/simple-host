import { test } from 'node:test'
import assert from 'node:assert/strict'
import fs from 'node:fs'
import vm from 'node:vm'

const navigation = JSON.parse(fs.readFileSync('products/hack/rules/hackevents/navigation.json', 'utf8'))
const navSource = fs.readFileSync('internal/webassets/static/hack/nav.js', 'utf8')
  .replace(/^import .*\n/gm, '').replace(/^export \{.*\n/gm, '').replaceAll('export ', '')
const ctx = vm.createContext({ navigation, URL, URLSearchParams, location: { origin: 'https://simple-hack.test' } })
vm.runInContext(navSource + ';globalThis.normalize = normalizeURL;globalThis.route = resolveRoute', ctx)

test('every compatibility alias preserves unrelated parameters', () => {
  for (const alias of navigation.aliases) {
    const url = new URL('https://simple-hack.test/e/demo/manage')
    url.searchParams.set('s', alias.section)
    url.searchParams.set('keep', 'a & b')
    if (alias.tab) url.searchParams.set('tab', alias.tab)
    if (alias.anchor) url.hash = alias.anchor
    const resolved = ctx.normalize(url)
    const target = navigation.targets.find(t => t.id === alias.target)
    assert.equal(resolved.searchParams.get('s'), target.section)
    assert.equal(resolved.searchParams.get('tab'), target.tab)
    assert.equal(resolved.hash, target.anchor ? '#' + target.anchor : '')
    assert.equal(resolved.searchParams.get('keep'), 'a & b')
  }
})

test('canonical Settings and Event page tabs outrank legacy fragments', () => {
  for (const path of ['?s=settings&tab=judging#set-name', '?s=event-page&tab=about#schedule', '?s=event-page&tab=tracks#announcements']) {
    const url = new URL('https://simple-hack.test/e/demo/manage' + path)
    assert.equal(ctx.normalize(url).href, url.href)
  }
  for (const hash of ['#%zz', '#__proto__', '#not-a-section']) {
    const url = ctx.normalize('https://simple-hack.test/e/demo/manage?s=unknown' + hash)
    assert.equal(ctx.route(url).section, 'overview')
  }
})

test('sign-in storage distinguishes an absent return from explicit global returns', () => {
  const source = fs.readFileSync('internal/webassets/static/hack/app.js', 'utf8')
  const funcs = source.slice(source.indexOf('  function safeNext'), source.indexOf('  function goSignIn'))
  function storage() { const data = new Map(); return { getItem: k => data.get(k), setItem: (k, v) => data.set(k, v), removeItem: k => data.delete(k) } }
  const auth = vm.createContext({ URL, URLSearchParams, Date, sessionStorage: storage(), localStorage: storage(), location: { origin: 'https://simple-hack.test', search: '' } })
  vm.runInContext("const NEXT_KEY='next';" + funcs + ';globalThis.read = readNext;globalThis.consume = consumeNext', auth)
  assert.equal(auth.read(), '')
  assert.equal(auth.consume(), '')
  for (const next of ['/events', '/account', '/join/code', '/judge/code', '/organiser/code', '/team/code']) {
    auth.location.search = '?next=' + encodeURIComponent(next)
    assert.equal(auth.read(), next)
    auth.location.search = '' // OAuth and magic-link returns carry no explicit next.
    assert.equal(auth.consume(), next)
    assert.equal(auth.consume(), '')
  }
  auth.location.search = '?next=' + encodeURIComponent('https://elsewhere.test/')
  assert.equal(auth.consume(), '')
})


test('fixed schedule rows distinguish voting settings from results publication', () => {
 const source = fs.readFileSync('internal/webassets/static/hack/schedule.js', 'utf8').replace(/^import .*\n/gm, '').replaceAll('export ', '')
 const scope = vm.createContext({ navigation, encodeURIComponent, href: (role, slug, section) => `/e/${slug}/manage?s=${section}` })
 const resolver = fs.readFileSync('internal/webassets/static/hack/navigation.js', 'utf8').replace(/^import .*\n/gm, '').replaceAll('export ', '')
 vm.runInContext(resolver, scope)
 vm.runInContext(source + ';globalThis.link = setInLink', scope)
 assert.equal(scope.link('results','demo','voting').href, '/e/demo/manage?s=settings&tab=results#voting')
 assert.equal(scope.link('results','demo','results_published').href, '/e/demo/manage?s=results')
})

test('public event target keeps the authoritative hosted or self-hosted URL', () => {
 const scope=vm.createContext({navigation,encodeURIComponent})
 const source=fs.readFileSync('internal/webassets/static/hack/navigation.js','utf8').replace(/^import .*\n/gm,'').replaceAll('export ','')
 vm.runInContext(source+';globalThis.target=resolve',scope)
 for(const url of ['https://demo.simple-hack.app/','https://hack.example.org/e/demo?view=public','https://demo.events.example.org/']) assert.equal(scope.target('o.public-page','demo',{event_url:url}).href,url)
})

test('no-next-action copy distinguishes archive, inspection and takedown', () => {
 const source=fs.readFileSync('internal/webassets/static/hack/screens/organiser-overview.js','utf8')
 const begin=source.indexOf('function nextStepContent(')
 const end=source.indexOf('\nexport async function render',begin)
 const scope=vm.createContext({})
 vm.runInContext(source.slice(begin,end)+';globalThis.content=nextStepContent',scope)
 for(const [event,label] of [[{stage:'archived'},'This event has ended'],[{stage:'building'},'Read-only view'],[{stage:'building',taken_down:true},'This event is taken down']]) assert.equal(scope.content(event,{kind:'none'}).h2,label)
})
