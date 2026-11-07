import { test } from 'node:test'
import assert from 'node:assert/strict'
import fs from 'node:fs'
import vm from 'node:vm'

const navigation = JSON.parse(fs.readFileSync('internal/rules/hackevents/navigation.json', 'utf8'))
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
