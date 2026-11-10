import { render as renderTracks } from './_event-tracks.js'
// Atlas 2026-10-30: Event page is now seven sub-pages (?s=event-page&tab=…),
// one shown at a time, each with its own Save/Add rule -- see
// docs/ux-atlas/spec.json "organiser-event-page" (sub_pages, editing_rules,
// decisions incl. the after-End rule) and BUILD.md's routes table. This
// replaces the old one-long-page-with-a-jump-nav version; every capability
// that file had is kept here (basics, about/prizes/rules, code of conduct
// extra, dates/time zone, schedule, sponsors with logo, FAQ, icon upload/
// remove, colour, gallery toggle, built-in vs own website, zip upload with
// plain errors, versions, read-only states, admin view). Announcements
// moved out to its own screen (screens/organiser-announcements.js).
import { fileAsDataURL, fileAsArrayBuffer, moveItem, rowMenuHTML, wireRowMenus, formDialog, undoToast } from './_orgA-util.js'
import { merge, label as labelRows, MOVES, setInLink, timeOnly } from '../schedule.js'

// R19: title() used to read only ctx.params ('tab='), so an old #schedule
// deep link (no tab= query param, resolved from the hash only once render()
// itself runs) showed the right sub-page under the wrong document title
// ("Basics..."). Both title() and render() now share one hash-aware
// resolver. It also carries the event's own name (spec: '<Sub-page> ·
// Event page · <event>' -- app.js appends ' · Simple Hack' itself).
export function title(ctx) {
  const tab = resolveTab(ctx)
  const name = ctx.event?.event?.title
  return TAB_LABEL[tab] + ' · Event page' + (name ? ' · ' + name : '')
}

const TABS = ['basics', 'about', 'schedule', 'tracks', 'sponsors', 'faq', 'look', 'website']
const TAB_LABEL = { basics: 'Basics', about: 'About', schedule: 'Schedule', tracks: 'Tracks', sponsors: 'Sponsors', faq: 'FAQ', look: 'Look', website: 'Website' }
// Old deep links (BUILD.md): #basics/#schedule/#sponsors/#faq -> that tab,
// #appearance -> look, #address -> website; #announcements, or tab=
// announcements, redirects away to ?s=announcements entirely (handled in
// render(), not here).
const HASH_TAB = { basics: 'basics', schedule: 'schedule', sponsors: 'sponsors', faq: 'faq', appearance: 'look', address: 'website' }
// R11: a malformed hash (`#%E0`, not valid percent-encoding) made
// decodeURIComponent throw a URIError that neither this function nor the
// old render()-only version caught, crashing the whole screen. Wrapped in
// try/catch so a bad hash is just ignored (falls through to ?tab= / the
// Basics default) instead of taking the page down. `Object.hasOwn` (not a
// bare `HASH_TAB[h]`) also stops a hash like `#toString` from resolving to
// the Object.prototype method of that name.
function resolveTab(ctx) {
  let t = ctx.params.get('tab') || ''
  if (!TABS.includes(t) && location.hash) {
    let h = location.hash.slice(1)
    try { h = decodeURIComponent(h) } catch { h = '' }
    if (Object.hasOwn(HASH_TAB, h)) t = HASH_TAB[h]
  }
  return TABS.includes(t) ? t : 'basics'
}

const CRAYONS = [['', 'Automatic'], ['red', 'Red'], ['teal', 'Teal'], ['indigo', 'Indigo'], ['orange', 'Orange'], ['yellow', 'Yellow'], ['grass', 'Grass'], ['blush', 'Blush']]
const SWATCH_HEX = { red: '#d15d48', teal: '#267b79', indigo: '#4a49a8', orange: '#e9a45f', yellow: '#f2c94c', grass: '#86ab74', blush: '#ee8f92' }
// SIM-353: whole-page styles for the public event page. '' (Classic) is
// today's look -- no data-theme, no themes.css link, byte-identical HTML.
// The Go allowlist (hackThemes in internal/hackhttp/hack_themes.go) is the
// one source of truth for which non-empty ids are real; this list mirrors
// it for display order and copy only.
const THEMES = [
  ['', 'Classic', "Today's look"],
  ['poster', 'Poster', 'Bold, for student hacks'],
  ['confetti', 'Confetti', 'Bright and playful'],
  ['boardroom', 'Boardroom', 'Clean, for company events'],
  ['garden', 'Garden', 'Calm and friendly'],
  ['terminal', 'Terminal', 'For developer crowds']
]

// #16 (kept from the old file): this screen's own website-zip upload shows
// the server's raw extractor error in plain English instead of server-debug
// text, same mapping as participant-website.js's archiveErrorText.
function archiveErrorText(ui, r, fallback) {
  const code = r && r.data && r.data.code
  if (code === 'missing_index') return 'Put index.html at the top of the zip, not inside a folder.'
  if (code === 'invalid_archive') {
    const raw = String((r && r.data && r.data.error) || '')
    if (/unsafe path|duplicate (entry|path)/i.test(raw)) {
      return "That zip has file paths Simple Host can't accept (a path outside the archive, or two files at the same path). Fix the archive and try again."
    }
    return "That file isn't a valid .zip."
  }
  return ui.errText(r, { fallback })
}

// Event page's own data (the three list arrays sharing PUT /content, plus
// the website mode/versions) is cached per event slug across tab switches
// (editing_rules.data_loading: "switching tabs does not refetch"). Tab
// switches no longer call render() at all (R19: a subnav click is handled
// locally by paintTab, below, never a full route()), so render() itself now
// runs only on a genuine *entry* to Event page -- first visit, Back/Forward
// from another screen, or ctx.refresh() -- and always reloads /content and
// /website then (R6: a stale copy from before someone else's edit, or
// before an upload made elsewhere, used to sit in this cache indefinitely
// and could overwrite a co-organiser's change on the next list write).
const cache = { slug: null, content: null, fixed: [], contentError: false, website: null, websiteError: false }
// Every /content answer refreshes the three stored lists and the fixed
// schedule rows the server derives from the event's settings (a schedule
// that acts, 2026-11-02). cache.content is exactly what PUT /content sends
// back, so the fixed rows live beside it, never inside it.
function setContent(d) {
  cache.content = { sponsors: d.sponsors || [], faq: d.faq || [], schedule: d.schedule || [] }
  cache.fixed = d.fixed_schedule || []
}
// R7: every assignment to cache.content/cache.website after an await must
// check this first -- a slow request started on event A must never land in
// event B's cache after the organiser has switched events, and a request
// outlived by a newer navigation (ctx.alive() false) must never touch the
// cache at all.
function cacheStale(ctx, ev) { return !ctx.alive() || cache.slug !== ev.slug }

function zoneOffsetLabel(zone, date) {
  try {
    const parts = new Intl.DateTimeFormat('en-US', { timeZone: zone, timeZoneName: 'shortOffset' }).formatToParts(date)
    const raw = (parts.find(p => p.type === 'timeZoneName') || {}).value || ''
    const m = raw.match(/GMT([+-]\d{1,2})(?::?(\d{2}))?/)
    if (!m) return ''
    return 'UTC' + m[1] + (m[2] ? ':' + m[2] : '')
  } catch { return '' }
}
// A select of IANA zones, each showing its abbreviation and offset (spec:
// "Asia/Jakarta · WIB, UTC+7"). Intl.supportedValuesOf is young enough that
// a browser without it falls back to a short curated list (still including
// whatever zone the event is already set to, so saving never loses it).
function zoneOptions(ui, current) {
  let zones
  try { zones = typeof Intl.supportedValuesOf === 'function' ? Intl.supportedValuesOf('timeZone') : null } catch { zones = null }
  if (!zones || !zones.length) {
    zones = ['UTC', 'Asia/Jakarta', 'Asia/Makassar', 'Asia/Jayapura', 'Asia/Tokyo', 'Asia/Seoul', 'Asia/Singapore', 'Asia/Kolkata',
      'Europe/London', 'Europe/Paris', 'Europe/Berlin', 'America/New_York', 'America/Chicago', 'America/Denver', 'America/Los_Angeles', 'Australia/Sydney']
  }
  if (current && !zones.includes(current)) zones = [current, ...zones]
  const now = new Date()
  return zones.slice().sort().map(z => {
    const abbr = ui.zoneAbbr ? ui.zoneAbbr(z, now) : ''
    const off = zoneOffsetLabel(z, now)
    return { zone: z, label: z + (abbr || off ? ' · ' + [abbr, off].filter(Boolean).join(', ') : '') }
  })
}

function focusHeading(sp) {
  const h = sp.querySelector('#sp-h')
  if (!h) return
  h.setAttribute('tabindex', '-1')
  h.focus()
}

function subnavHTML(tab, ev) {
  const counts = { schedule: cache.content.schedule.length + cache.fixed.length, sponsors: cache.content.sponsors.length, faq: cache.content.faq.length }
  return `<nav class="subnav" aria-label="Event page sections">${TABS.map(t => {
    const n = counts[t]
    const badge = n ? `<span class="n" aria-label="${n} items">${n}</span>` : ''
    return `<a href="?s=event-page&tab=${t}"${t === tab ? ' aria-current="page"' : ''}>${TAB_LABEL[t]}${badge}</a>`
  }).join('')}</nav>`
}

// R4/decision 2026-10-31: while a custom website is live, the note on
// Basics/About/Look says their edits still save (true -- PATCH on the event
// itself is never blocked by End); the note on the read-only tabs
// (Sponsors/FAQ/Website, and Schedule once it's read-only too) must not
// make that claim once those stop saving.
function customSiteNote(readOnly) {
  if (!(cache.website.mode === 'custom' && cache.website.has_custom_page)) return ''
  return readOnly
    ? '<div class="note info"><b>Your own website is live at your address.</b></div>'
    : '<div class="note info"><b>Your own website is live at your address.</b> This still saves, and shows again if you switch back to the built-in page.</div>'
}

export async function render(ctx) {
  const { main, api: client, ui, event: data } = ctx
  const api = client.api
  const apiBytes = client.apiBytes
  const ev = data.event
  const admin = !!data.admin_view
  const ended = ev.stage === 'archived'
  const path = '/v1/hack/events/' + encodeURIComponent(ev.slug)
  const setLive = ui.live(main)

  wireRowMenus()

  // Old deep links. #announcements (any tab) and tab=announcements redirect
  // away to the Announcements screen entirely, via replaceState so Back
  // never lands back on this screen's old URL (BUILD.md). resolveTab (R11,
  // R19) handles the rest of the hash/tab resolution, shared with title().
  let tab = ctx.params.get('tab') || ''
  let h = ''
  if (location.hash) { try { h = decodeURIComponent(location.hash.slice(1)) } catch { h = '' } }
  if (tab === 'announcements' || (!TABS.includes(tab) && h === 'announcements')) {
    const to = `/e/${encodeURIComponent(ev.slug)}/manage?s=announcements`
    history.replaceState(history.state, '', to)
    ui.notePath(to)
    return ctx.refresh()
  }
  tab = resolveTab(ctx)
  if (ctx.params.get('tab') !== tab) {
    const next = new URL(location.href); next.searchParams.set('tab', tab)
    const fixed = next.pathname + next.search + next.hash
    history.replaceState(history.state, '', fixed)
    ui.notePath(fixed)
  }

  // R6: this module-scope cache is kept warm only across a *tab switch*
  // (paintTab, below, never calls render() again for one) -- any call to
  // render() itself is a genuine entry (first visit, Back/Forward from a
  // different screen, ctx.refresh()), so it always reloads /content and
  // /website, never trusting a copy that might predate an edit made
  // elsewhere (a co-organiser, the AI app, or an MCP tool).
  if (cache.slug !== ev.slug) {
    cache.slug = ev.slug
    cache.content = { sponsors: [], faq: [], schedule: [] }
    cache.fixed = []
    cache.website = { mode: ev.website_mode, has_custom_page: false, versions: [], upload_limits: '' }
  }
  cache.contentError = false
  cache.websiteError = false
  try {
    const [c, w] = await Promise.all([api('GET', path + '/content'), api('GET', path + '/website')])
    if (cacheStale(ctx, ev)) return
    if (c.ok) setContent(c.data)
    else cache.contentError = true
    // R16: a failed /website GET used to leave the stale "no uploads"
    // default in place with no sign anything went wrong -- the own-website
    // choice then wrongly claimed "Upload a zip first" even with a custom
    // site live. Surfaced the same way as the content failure, with Reload.
    if (w.ok) cache.website = w.data
    else cache.websiteError = true
  } catch (e) { cache.contentError = true; cache.websiteError = true }
  if (cacheStale(ctx, ev)) return

  main.innerHTML = `<div class="ph"><div class="t"><h1>Event page</h1><p>What visitors see at <a href="${ui.esc(ev.url)}" target="_blank" rel="noopener">${ui.esc(ev.url.replace(/^https?:\/\//, ''))}</a> before they sign up.</p></div><a class="btn ph-link" href="${ui.esc(ev.url)}" target="_blank" rel="noopener">View public page <svg class="ico" viewBox="0 0 20 20" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M11 4h5v5M16 4l-7 7M14 12v4H4V6h4"/></svg></a></div>
<div class="subnav-split"></div>`
  const split = main.querySelector('.subnav-split')

  if (admin) {
    const banner = document.createElement('div')
    banner.className = 'note warn'
    banner.setAttribute('role', 'alert')
    banner.textContent = 'Viewing as platform admin. Everything here is read only.'
    main.insertBefore(banner, main.firstChild)
  }
  const locked = admin // read-only treatment per tab decides the rest from `ended`

  // R19: a sub-page tab is "right column only" -- the sub-nav plus the one
  // chosen sub-page repaint, never the shell, the event's own data or the
  // page header. paintTab is also what a subnav click now calls directly
  // (below), instead of a full app.js route() (which used to re-fetch
  // getEvent and remount the whole shell sidebar on every tab click).
  let tabGeneration = 0
  function paintTab(t) {
    const turn = ++tabGeneration
    split.innerHTML = `${subnavHTML(t, ev)}<section class="sp" aria-labelledby="sp-h" id="sp"></section>`
    const sp = split.querySelector('#sp')
    const helpers = { ctx, main, sp, ui, api, apiBytes, ev, admin, locked, ended, path, setLive }
    if (t === 'basics') renderBasics(helpers)
    else if (t === 'about') renderAbout(helpers)
    else if (t === 'schedule') renderSchedule(helpers)
    else if (t === 'tracks') renderTracks({ ...ctx, main: sp, alive: () => ctx.alive() && turn === tabGeneration })
    else if (t === 'sponsors') renderSponsors(helpers)
    else if (t === 'faq') renderFaq(helpers)
    else if (t === 'look') renderLook(helpers)
    else if (t === 'website') renderWebsite(helpers)
    if (locked) sp.querySelectorAll('button, input, textarea, select').forEach(n => { if (!n.hasAttribute('data-admin-live')) n.disabled = true })
    focusHeading(sp)
    const name = ev.title
    document.title = TAB_LABEL[t] + ' · Event page' + (name ? ' · ' + name : '') + ' · Simple Hack'
  }
  paintTab(tab)

  // R19: intercept a subnav link click here instead of letting it fall
  // through to app.js's global handler (which would do a full route()).
  // The dirty-guard's own capture-phase listener (ui.js) still runs first
  // and wins when Basics/About is dirty -- it calls preventDefault +
  // stopPropagation before this bubble-phase listener ever sees the click.
  split.addEventListener('click', e => {
    const a = e.target.closest('.subnav a[href]')
    if (!a || e.defaultPrevented || e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey || a.target) return
    let url
    try { url = new URL(a.href) } catch { return }
    if (url.origin !== location.origin) return
    e.preventDefault()
    if (url.search === location.search) return // already on this tab
    // editing_rules.urls: "a tab click is history.pushState + re-render of
    // the right column only" -- pushState (not replace) so Back/Forward
    // step through tabs one at a time. ctx.params is a snapshot taken when
    // app.js last called route(); it won't reflect this pushState, so the
    // target tab is read straight from the clicked link's own URL instead
    // of through resolveTab(ctx).
    const t = new URLSearchParams(url.search).get('tab')
    history.pushState({}, '', url.pathname + url.search)
    ui.notePath(url.pathname + url.search)
    paintTab(TABS.includes(t) ? t : 'basics')
  })
}

function dateTimeField(ui, prefix, label, date, time, { dateID = prefix + '-date', fieldID = '' } = {}) {
  const name = label === 'Starts' ? 'Start' : 'End'
  return `<div class="field"${fieldID ? ` id="${fieldID}"` : ''}><span class="lab" id="${prefix}-l">${label}</span><div class="dt-pair" role="group" aria-labelledby="${prefix}-l"><input data-help="times" class="input" type="date" aria-label="${name} date" id="${dateID}" value="${ui.esc(date)}"><input data-help="times" class="input" type="time" aria-label="${name} time" id="${prefix}-time" value="${ui.esc(time)}"></div></div>`
}

// ---------------------------------------------------------------- Basics
function renderBasics({ ctx, sp, ui, api, ev, locked, ended, path, setLive }) {
  const $ = id => sp.querySelector('#' + id)
  const start = ui.eventBoundaryInput(ev.starts_at, ev)
  const end = ui.eventBoundaryInput(ev.ends_at, ev)
  const snapshot = { title: ev.title, tagline: ev.tagline || '', starts: start.date, startTime: start.time, ends: end.date, endTime: end.time, tz: ev.time_zone }

  // R4/decision 2026-10-31: Basics stays editable after End (the server's
  // PATCH only ever blocks submission_deadline once archived -- title,
  // tagline, about, prizes, rules, coc_text, dates and time zone all keep
  // working) -- only an admin view (never ended on its own) renders it
  // read-only.
  if (locked) {
    sp.innerHTML = `<div class="sp-head"><h2 id="sp-h">Basics</h2><p>The name, tagline and dates at the top of your page.</p></div>
${customSiteNote(true)}
<div class="card"><dl><dt>Title</dt><dd>${ui.esc(ev.title)}</dd><dt>Tagline</dt><dd>${ui.esc(ev.tagline || '—')}</dd><dt>Starts</dt><dd>${ui.esc(ui.eventBoundaryDate(ev.starts_at, ev))}</dd><dt>Ends</dt><dd>${ui.esc(ui.eventBoundaryDate(ev.ends_at, ev))}</dd><dt>Time zone</dt><dd>${ui.esc(ev.time_zone)}</dd></dl></div>`
    return
  }

  sp.innerHTML = `<div class="sp-head"><h2 id="sp-h">Basics</h2><p>The name, tagline and dates at the top of your page.</p></div>
${customSiteNote(false)}
${ended ? '<div class="note">This event has ended. You can still fix the page text, colour and style.</div>' : ''}
<form class="card" aria-labelledby="sp-h">
<div class="field"><label for="f-title">Title</label><input class="input" id="f-title" maxlength="120" value="${ui.esc(ev.title)}"></div>
<div class="field"><div class="lab-row"><label for="f-tag">Tagline</label><span class="opt">Optional</span><span class="counter" id="f-tag-ct">${(ev.tagline || '').length} / 160</span></div><input class="input" id="f-tag" maxlength="160" value="${ui.esc(ev.tagline || '')}"><p class="help">One line under the title. Link previews in WhatsApp show it too.</p></div>
<div class="field-row">
${dateTimeField(ui, 'f-start', 'Starts', snapshot.starts, snapshot.startTime, { dateID: 'f-start' })}
${dateTimeField(ui, 'f-end', 'Ends', snapshot.ends, snapshot.endTime, { dateID: 'f-end' })}
<div class="field grow"><label for="f-tz">Time zone</label><select class="select" id="f-tz">${zoneOptions(ui, ev.time_zone).map(z => `<option value="${ui.esc(z.zone)}"${z.zone === ev.time_zone ? ' selected' : ''}>${ui.esc(z.label)}</option>`).join('')}</select><p class="help">Every deadline and schedule time shows in this zone.</p></div>
</div>
<div class="card-foot"><span class="hint" id="f-hint">No unsaved changes</span><button class="btn quiet" type="button" id="f-discard" hidden>Discard</button><button class="btn primary" type="button" id="f-save" disabled>Save</button></div>
</form>`

  function isDirty() {
    return $('f-title').value !== snapshot.title || $('f-tag').value !== snapshot.tagline ||
      $('f-start').value !== snapshot.starts || $('f-end').value !== snapshot.ends || $('f-tz').value !== snapshot.tz || $('f-start-time').value !== snapshot.startTime || $('f-end-time').value !== snapshot.endTime
  }
  function paintDirty() {
    const dirty = isDirty()
    $('f-hint').textContent = dirty ? 'Unsaved changes' : 'No unsaved changes'
    $('f-hint').classList.toggle('dirty', dirty)
    $('f-discard').hidden = !dirty
    $('f-save').disabled = !dirty
  }
  ;['f-title', 'f-tag', 'f-start', 'f-end', 'f-tz', 'f-start-time', 'f-end-time'].forEach(id => $(id).addEventListener('input', paintDirty))
  $('f-tag').addEventListener('input', () => { $('f-tag-ct').textContent = $('f-tag').value.length + ' / 160' })
  $('f-discard').addEventListener('click', () => {
    $('f-title').value = snapshot.title; $('f-tag').value = snapshot.tagline; $('f-start').value = snapshot.starts; $('f-end').value = snapshot.ends; $('f-tz').value = snapshot.tz
    $('f-start-time').value = snapshot.startTime; $('f-end-time').value = snapshot.endTime
    $('f-tag-ct').textContent = snapshot.tagline.length + ' / 160'
    paintDirty()
  })
  $('f-save').addEventListener('click', async () => {
    const btn = $('f-save'); btn.disabled = true
    try {
      const body = { title: $('f-title').value, tagline: $('f-tag').value, time_zone: $('f-tz').value }
      // Omitted boundaries retain the server's time-zone-only reanchoring.
      if ($('f-start').value !== snapshot.starts || $('f-start-time').value !== snapshot.startTime) {
        body.starts_at = ui.eventBoundaryValue($('f-start').value, $('f-start-time').value)
      }
      if ($('f-end').value !== snapshot.ends || $('f-end-time').value !== snapshot.endTime) {
        body.ends_at = ui.eventBoundaryValue($('f-end').value, $('f-end-time').value)
      }
      const r = await api('PATCH', path, body)
      if (!r.ok) { setLive(ui.errText(r), 'err', btn); paintDirty(); return }
      // #10 (kept from the old file): a changed title or time zone reaches
      // the switcher/sidebar/status bar/document.title too, which only a
      // full ctx.refresh() rebuilds; the toast survives that (ui.js's own
      // "keep" timers).
      await ctx.refresh()
      ui.toast('Saved', null)
    } catch (e) { setLive('Cannot reach the server.', 'err', btn); paintDirty() }
  })
  ui.trackForm(ctx, sp.querySelector('form'))
}

// ------------------------------------------------------------------ About
function renderAbout({ ctx, sp, ui, api, ev, locked, ended, path, setLive }) {
  const $ = id => sp.querySelector('#' + id)
  const snapshot = { about: ev.about || '', prizes: ev.prizes || '', rules: ev.rules || '', coc: ev.coc_text || '' }

  // R4/decision 2026-10-31: About stays editable after End, same as Basics.
  if (locked) {
    sp.innerHTML = `<div class="sp-head"><h2 id="sp-h">About</h2><p>The longer text on your page. A blank line starts a new paragraph.</p></div>
${customSiteNote(true)}
<div class="card"><dl><dt>About</dt><dd>${ui.esc(ev.about || '—')}</dd><dt>Prizes</dt><dd>${ui.esc(ev.prizes || '—')}</dd><dt>Rules</dt><dd>${ui.esc(ev.rules || '—')}</dd><dt>Code of conduct additions</dt><dd>${ui.esc(ev.coc_text || '—')}</dd></dl></div>`
    return
  }

  sp.innerHTML = `<div class="sp-head"><h2 id="sp-h">About</h2><p>The longer text on your page. A blank line starts a new paragraph.</p></div>
${customSiteNote(false)}
${ended ? '<div class="note">This event has ended. You can still fix the page text, colour and style.</div>' : ''}
<form class="card" aria-labelledby="sp-h">
<div class="field"><div class="lab-row"><label for="f-about">About</label><span class="counter" id="f-about-ct">${snapshot.about.length} / 10000</span></div><textarea class="input" id="f-about" rows="7" maxlength="10000">${ui.esc(snapshot.about)}</textarea></div>
<div class="field" id="prizes"><div class="lab-row"><label for="f-prizes">Prizes</label><span class="opt">Optional</span></div><textarea class="input" id="f-prizes" rows="3" maxlength="5000">${ui.esc(snapshot.prizes)}</textarea></div>
<div class="field" id="rules"><div class="lab-row"><label for="f-rules">Rules</label><span class="opt">Optional</span></div><textarea class="input" id="f-rules" rows="3" maxlength="10000">${ui.esc(snapshot.rules)}</textarea></div>
<div class="field-group"><h3>Code of conduct</h3>
<details><summary class="small muted">Read the standard code, which every event shows first</summary><p class="help">${ui.esc(ev.coc_default || '')}</p></details>
<div class="field" id="conduct"><div class="lab-row"><label for="f-coc">Your additions</label><span class="opt">Optional</span></div><textarea class="input" id="f-coc" rows="3" maxlength="10000">${ui.esc(snapshot.coc)}</textarea><p class="help">Shown after the standard code on the join page.</p></div>
</div>
<div class="card-foot sticky"><span class="hint" id="f-hint">No unsaved changes</span><button class="btn quiet" type="button" id="f-discard" hidden>Discard</button><button class="btn primary" type="button" id="f-save" disabled>Save</button></div>
</form>`

  function isDirty() {
    return $('f-about').value !== snapshot.about || $('f-prizes').value !== snapshot.prizes || $('f-rules').value !== snapshot.rules || $('f-coc').value !== snapshot.coc
  }
  function paintDirty() {
    const dirty = isDirty()
    $('f-hint').textContent = dirty ? 'Unsaved changes' : 'No unsaved changes'
    $('f-hint').classList.toggle('dirty', dirty)
    $('f-discard').hidden = !dirty
    $('f-save').disabled = !dirty
  }
  ;['f-about', 'f-prizes', 'f-rules', 'f-coc'].forEach(id => $(id).addEventListener('input', paintDirty))
  $('f-about').addEventListener('input', () => { $('f-about-ct').textContent = $('f-about').value.length + ' / 10000' })
  $('f-discard').addEventListener('click', () => {
    $('f-about').value = snapshot.about; $('f-prizes').value = snapshot.prizes; $('f-rules').value = snapshot.rules; $('f-coc').value = snapshot.coc
    $('f-about-ct').textContent = snapshot.about.length + ' / 10000'
    paintDirty()
  })
  $('f-save').addEventListener('click', async () => {
    const btn = $('f-save'); btn.disabled = true
    try {
      const r = await api('PATCH', path, { about: $('f-about').value, prizes: $('f-prizes').value, rules: $('f-rules').value, coc_text: $('f-coc').value })
      if (!r.ok) { setLive(ui.errText(r), 'err', btn); paintDirty(); return }
      await ctx.refresh()
      ui.toast('Saved', null)
    } catch (e) { setLive('Cannot reach the server.', 'err', btn); paintDirty() }
  })
  ui.trackForm(ctx, sp.querySelector('form'))
}

// --------------------------------------------------------------- Schedule
// R20: one consistent sentence for every read-only-after-End sub-page
// (Schedule, Sponsors, FAQ, Website) -- the server rejects PUT /content,
// PUT/DELETE /icon and PATCH/PUT /website once archived, so the copy here
// must not promise an edit that would fail (decision 2026-10-31).
function endedReadOnlyNote(what) { return `<div class="note">This event has ended. ${what} can't change any more.</div>` }
// A schedule that acts (owner, 2026-11-02): the event's real times (start
// and end, the entry deadline, the voting window, results) show here as fixed
// rows, merged in time order with the rows typed here. A fixed row has no
// Edit or Remove, only a "Set in Entries"-style link to where it changes, so
// it can never drift from the real setting. A typed row can also prompt a
// forward move ("When this starts, ask to"): Overview then asks, e.g.
// "Kickoff is now. Open sign-up?". It never moves the stage itself.
const LOCK_ICON = '<svg class="ico" viewBox="0 0 20 20" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><rect x="5" y="9" width="10" height="8" rx="1.5"/><path d="M7 9V6.5a3 3 0 0 1 6 0V9"/></svg>'
function renderSchedule({ ctx, sp, ui, api, ev, admin, locked, ended, path, setLive }) {
  const readOnly = locked || ended // decision: Schedule (unlike Basics/About/Look) is read-only after End, not just under admin view -- the server rejects PUT /content once archived.
  const items = labelRows(merge(cache.content.schedule, cache.fixed, ev))

  // 12-hour with am/pm, like every other time in the app (owner, 2026-10-30),
  // through ui.dateTime so every time reads the same way.
  function timeLabel(iso) { return timeOnly(ui, iso, ev) }
  function endLabel(startIso, endIso) {
    const sameDay = ui.dateOnly(startIso, ev) === ui.dateOnly(endIso, ev)
    return sameDay ? timeLabel(endIso) : ui.dateOnly(endIso, ev).replace(/^(\S+)\s.*/, '$1') + ' ' + timeLabel(endIso)
  }
  function whenHTML(item) {
    if (item.all_day) return '<b>All day</b>'
    if (!item.end_at) return `<b>${ui.esc(timeLabel(item.start_at))}</b>`
    return `<b>${ui.esc(timeLabel(item.start_at))}</b>– ${ui.esc(endLabel(item.start_at, item.end_at))}`
  }
  function pillHTML(item) { return item.now ? '<span class="pill stage">Now</span>' : item.next ? '<span class="pill">Next</span>' : '' }
  function promptLine(item) { return item.prompt && MOVES[item.prompt] ? `<span class="faint">Then asks: ${ui.esc(MOVES[item.prompt])}?</span>` : '' }
  function fixedRowHTML(item) {
    const where = setInLink(item.set_in, ev.slug, item.key)
    const setIn = where ? `<span>Set in <a href="${ui.esc(where.href)}">${ui.esc(where.label)}</a></span>` : ''
    return `<div class="r sched-row fixed"><span class="r-when">${whenHTML(item)}</span><span class="main"><b>${ui.esc(item.title)}</b>${item.description ? `<span>${ui.esc(item.description)}</span>` : ''}${setIn}${promptLine(item)}</span><span class="r-end">${pillHTML(item)}<span class="pill" title="Comes from the event's settings">${LOCK_ICON}Fixed</span></span></div>`
  }
  function rowHTML(item, i) {
    if (item.fixed) return fixedRowHTML(item)
    const details = item.description ? `<span>${ui.esc(item.description)}</span>` : '<span class="faint">No details</span>'
    const acts = readOnly ? '' : `<div class="row-acts"><button class="btn quiet sm" type="button" data-act="edit" data-idx="${i}" aria-label="Edit ${ui.esc(item.title)}">Edit</button>${rowMenuHTML(item.title, [{ label: 'Edit', act: 'edit' }, { label: 'Remove', act: 'remove', danger: true }])}</div>`
    return `<div class="r sched-row"${readOnly ? '' : ` data-idx="${i}"`}><span class="r-when">${whenHTML(item)}</span>${readOnly ? `<span class="main"><b>${ui.esc(item.title)}</b>${details}${promptLine(item)}</span>` : `<a class="main" href="#" data-act="edit" data-idx="${i}"><b>${ui.esc(item.title)}</b>${details}${promptLine(item)}</a>`}<span class="r-end">${pillHTML(item)}${acts}</span></div>`
  }
  function listHTML() {
    let html = '', lastDay = null
    items.forEach((item, i) => {
      const day = ui.dateOnly(item.start_at, ev)
      if (day !== lastDay) { html += `<div class="lh">${ui.esc(day)}</div>`; lastDay = day }
      html += rowHTML(item, i)
    })
    return html
  }
  const fixedNote = cache.fixed.length && !cache.contentError ? '<p class="help">Rows marked Fixed come from your event settings, so they always match. Change them where they are set.</p>' : ''

  sp.innerHTML = `<div class="sp-head"><h2 id="sp-h">Schedule</h2><p>In ${ui.esc(ev.time_zone)}. Your page lists them in time order and marks Now and Next.</p>${readOnly || cache.contentError ? '' : '<button class="btn primary" type="button" id="sched-add"><svg class="ico" viewBox="0 0 20 20" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M10 4v12M4 10h12"/></svg>Add time</button>'}</div>
${customSiteNote(readOnly)}
${ended && !admin ? endedReadOnlyNote('Schedule') : ''}
${cache.contentError ? `<div class="note bad">Sponsors, FAQ and schedule could not be loaded. Reload to edit them.<button class="btn sm" type="button" id="sched-reload">Reload</button></div>` :
    items.length ? `<div class="list editable" aria-label="Schedule" id="sched-list">${listHTML()}</div>${fixedNote}` :
    (readOnly ? '<p class="muted">Nothing scheduled.</p>' : `<div class="empty-state"><svg class="ico" viewBox="0 0 20 20" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M4 5h12a1 1 0 0 1 1 1v10a1 1 0 0 1-1 1H4a1 1 0 0 1-1-1V6a1 1 0 0 1 1-1zm-1 4h14M7 3v4m6-4v4"/></svg><h3>Nothing scheduled yet</h3><p>Add the kickoff, build time and demos. They show on your page, and participants see what is on now. Your dates and entry deadline show here on their own once you set them.</p><button class="btn primary" type="button" id="sched-add-empty"><svg class="ico" viewBox="0 0 20 20" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M10 4v12M4 10h12"/></svg>Add time</button></div>`)}`

  const reload = sp.querySelector('#sched-reload')
  if (reload) reload.addEventListener('click', async () => {
    reload.disabled = true
    try {
      const r = await api('GET', path + '/content')
      if (cacheStale(ctx, ev)) return
      if (r.ok) { setContent(r.data); cache.contentError = false }
    } catch (e) { if (cacheStale(ctx, ev)) return }
    renderSchedule({ ctx, sp, ui, api, ev, admin, locked, ended, path, setLive })
  })
  if (readOnly || cache.contentError) return

  // R1 (blocker, silent data loss): `existing` is always the exact stored
  // object (each merged row keeps it as `src`), so the write below finds
  // where that same item actually sits in the server's own (unsorted)
  // order by identity (`indexOf`), never by display position. Fixed rows
  // have no src and are never written: the server derives them.
  async function openDialog(existing) {
    const item = existing ? { ...existing } : { title: '', description: '', start_at: '', end_at: '', prompt: '' }
    const startVal = existing ? ui.localInputValue(existing.start_at, ev.time_zone) : ''
    const endVal = existing ? ui.localInputValue(existing.end_at, ev.time_zone) : ''
    const [sd, st] = startVal ? startVal.split('T') : ['', '']
    const [ed, et] = endVal ? endVal.split('T') : ['', '']
    const promptOptions = [['', 'Nothing'], ...Object.entries(MOVES)].map(([v, l]) => `<option value="${ui.esc(v)}"${(item.prompt || '') === v ? ' selected' : ''}>${ui.esc(l)}</option>`).join('')
    const body = document.createElement('div')
    body.innerHTML = `<p class="form-intro">Times are in ${ui.esc(ev.time_zone)}.</p>
<div class="field"><label for="d-title">Title</label><input class="input" id="d-title" maxlength="120" value="${ui.esc(item.title)}"></div>
<div class="field"><div class="lab-row"><label for="d-desc">Details</label><span class="opt">Optional</span></div><textarea class="input" id="d-desc" rows="2" maxlength="500">${ui.esc(item.description || '')}</textarea></div>
${dateTimeField(ui, 'd-start', 'Starts', sd, st)}
${dateTimeField(ui, 'd-end', 'Ends', ed, et, { fieldID: 'd-end-field' })}
<div class="field"><div class="lab-row"><label for="d-prompt">When this starts, ask to</label><span class="opt">Optional</span></div><select class="select" id="d-prompt">${promptOptions}</select><p class="help">Overview asks you at this time, like “Kickoff is now. Open sign-up?” Nothing changes until you press the button.</p></div>`
    const q = id => body.querySelector('#' + id)
    const saved = await formDialog(ui, { title: existing ? 'Edit time' : 'Add time', body, saveLabel: existing ? 'Save' : 'Add time' }, async () => {
      body.querySelectorAll('.err').forEach(n => n.remove())
      const title = q('d-title').value.trim()
      if (!title) { q('d-title').insertAdjacentHTML('afterend', '<p class="err">Title is required.</p>'); return false }
      const startAt = q('d-start-date').value && q('d-start-time').value ? `${q('d-start-date').value}T${q('d-start-time').value}` : ''
      const endAt = q('d-end-date').value && q('d-end-time').value ? `${q('d-end-date').value}T${q('d-end-time').value}` : ''
      if (!startAt || !endAt) { q('d-end-field').insertAdjacentHTML('beforeend', '<p class="err">Starts and ends are both required.</p>'); return false }
      if (endAt <= startAt) { q('d-end-field').insertAdjacentHTML('beforeend', '<p class="err">Ends must be after it starts.</p>'); return false }
      const next = { title, description: q('d-desc').value, start_at: startAt, end_at: endAt }
      if (q('d-prompt').value) next.prompt = q('d-prompt').value
      const schedule = cache.content.schedule.slice()
      if (!existing) schedule.push(next)
      else {
        const at = schedule.indexOf(existing)
        if (at === -1) { body.insertAdjacentHTML('beforeend', '<p class="err">This item changed elsewhere. Reload and try again.</p>'); return false }
        schedule[at] = next
      }
      try {
        const r = await api('PUT', path + '/content', { ...cache.content, schedule })
        if (cacheStale(ctx, ev)) return false
        if (!r.ok) { body.insertAdjacentHTML('beforeend', `<p class="err">${ui.esc(ui.errText(r))}</p>`); return false }
        setContent(r.data)
        return true
      } catch (e) { if (cacheStale(ctx, ev)) return false; body.insertAdjacentHTML('beforeend', '<p class="err">Cannot reach the server.</p>'); return false }
    })
    if (saved) renderSchedule({ ctx, sp, ui, api, ev, admin, locked, ended, path, setLive })
  }

  // R2: the row's own quick Edit button and the row-link both carry
  // data-idx directly, but the row menu's Edit item doesn't (rowMenuHTML
  // has no index to put there) -- it sits inside the row's own
  // `[data-idx]` container instead, so the fallback climbs to that.
  sp.querySelectorAll('[data-act="edit"]').forEach(el => el.addEventListener('click', e => {
    e.preventDefault()
    const i = Number(el.dataset.idx ?? el.closest('[data-idx]').dataset.idx)
    if (items[i] && items[i].src) openDialog(items[i].src)
  }))
  sp.querySelectorAll('[data-act="remove"]').forEach(el => el.addEventListener('click', async () => {
    const i = Number(el.closest('[data-idx]').dataset.idx)
    const removed = items[i] && items[i].src
    if (!removed) return
    const schedule = cache.content.schedule.filter(x => x !== removed)
    try {
      const r = await api('PUT', path + '/content', { ...cache.content, schedule })
      if (cacheStale(ctx, ev)) return
      if (!r.ok) { setLive(ui.errText(r), 'err'); return }
      const before = cache.content
      setContent(r.data)
      renderSchedule({ ctx, sp, ui, api, ev, admin, locked, ended, path, setLive })
      undoToast(sp, `Removed “${removed.title}”`, async () => {
        try {
          const r2 = await api('PUT', path + '/content', before)
          if (cacheStale(ctx, ev)) return
          // R13: Undo is itself a write that can fail (network, or the
          // server refusing it); it used to fail silently and re-render
          // the list without the restored row, so the organiser believed
          // it had come back when it hadn't.
          if (!r2.ok) { setLive(ui.errText(r2), 'err'); return }
          setContent(r2.data)
        } catch (e) { if (cacheStale(ctx, ev)) return; setLive('Cannot reach the server.', 'err'); return }
        renderSchedule({ ctx, sp, ui, api, ev, admin, locked, ended, path, setLive })
      })
    } catch (e) { if (cacheStale(ctx, ev)) return; setLive('Cannot reach the server.', 'err') }
  }))
  // Both the header button and the empty-state button open the dialog.
  sp.querySelectorAll('#sched-add, #sched-add-empty').forEach(b => b.addEventListener('click', () => openDialog(null)))
}

// R10: one shared helper for the polite "X moved to position N of M" live
// region every reorderable list (Sponsors, FAQ) carries when editable --
// `sp.innerHTML` always includes `#reorder-status` in that case.
function announceReorder(sp, name, pos, total) {
  const el = sp.querySelector('#reorder-status')
  if (el) el.textContent = `${name} moved to position ${pos + 1} of ${total}`
}
// --------------------------------------------------------------- Sponsors
function renderSponsors({ ctx, sp, ui, api, ev, admin, locked, ended, path, setLive }) {
  const readOnly = locked || ended // R4/decision 2026-10-31: the server rejects PUT /content once archived.
  const items = cache.content.sponsors
  function thumbHTML(s) {
    if (s.logo_data) return `<span class="thumb"><img src="${ui.esc(s.logo_data)}" alt=""></span>`
    return `<span class="thumb empty" role="img" aria-label="No logo" title="No logo"><svg viewBox="0 0 20 20" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M3 4h14v12H3zM3 13l4-4 3 3 2-2 5 5M13 8h.01"/></svg></span>`
  }
  function rowHTML(s, i) {
    const host = s.url ? (() => { try { return new URL(s.url).host } catch { return s.url } })() : 'No link'
    const sub = [s.tier, host].filter(Boolean).join(' · ') || 'No link'
    if (readOnly) return `<div class="r has-grip">${thumbHTML(s)}<span class="main"><b>${ui.esc(s.name)}</b><span>${ui.esc(sub)}</span></span></div>`
    const grip = `<button data-help="sponsors" class="grip" type="button" aria-roledescription="drag handle" aria-label="Move ${ui.esc(s.name)}, position ${i + 1} of ${items.length}. Use the up and down arrow keys." draggable="true" data-act="grip" data-idx="${i}"><svg viewBox="0 0 12 16" aria-hidden="true" fill="currentColor"><circle cx="3" cy="3" r="1.4"/><circle cx="9" cy="3" r="1.4"/><circle cx="3" cy="8" r="1.4"/><circle cx="9" cy="8" r="1.4"/><circle cx="3" cy="13" r="1.4"/><circle cx="9" cy="13" r="1.4"/></svg></button>`
    const menu = rowMenuHTML(s.name, [{ label: 'Edit', act: 'edit' }, { label: 'Move up', act: 'up', disabled: i === 0 }, { label: 'Move down', act: 'down', disabled: i === items.length - 1 }, { label: 'Remove', act: 'remove', danger: true }])
    return `<div class="r has-grip" data-idx="${i}" draggable="true" data-act="dragrow">${grip}${thumbHTML(s)}<a class="main" href="#" data-act="edit" data-idx="${i}"><b>${ui.esc(s.name)}</b><span>${ui.esc(sub)}</span></a><div class="row-acts"><button class="btn quiet sm" type="button" data-act="edit" data-idx="${i}" aria-label="Edit ${ui.esc(s.name)}">Edit</button>${menu}</div></div>`
  }

  sp.innerHTML = `<div class="sp-head"><h2 id="sp-h">Sponsors</h2><p>Shown on your page in this order. Drag a row, or use its menu, to move it.</p>${readOnly || cache.contentError ? '' : '<button class="btn primary" type="button" id="sp-add"><svg class="ico" viewBox="0 0 20 20" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M10 4v12M4 10h12"/></svg>Add sponsor</button>'}</div>
${customSiteNote(readOnly)}
${ended && !admin ? endedReadOnlyNote('Sponsors') : ''}
${readOnly || cache.contentError ? '' : '<p class="sr-only" id="reorder-status" role="status" aria-live="polite"></p>'}
${cache.contentError ? `<div class="note bad">Sponsors, FAQ and schedule could not be loaded. Reload to edit them.<button class="btn sm" type="button" id="sp-reload">Reload</button></div>` :
    items.length ? `<div class="list editable" aria-label="Sponsors" id="sp-list">${items.map(rowHTML).join('')}</div><p class="list-foot">${items.length} of 20 sponsors · Logos: PNG, JPEG or WebP, up to 128 KB</p>` :
    (readOnly ? '<p class="muted">No sponsors.</p>' : `<div class="empty-state"><h3>No sponsors yet</h3><p>Logos and names show near the bottom of your page.</p><button class="btn primary" type="button" id="sp-add-empty">Add sponsor</button></div>`)}`

  const reload = sp.querySelector('#sp-reload')
  if (reload) reload.addEventListener('click', async () => {
    reload.disabled = true
    try {
      const r = await api('GET', path + '/content')
      if (cacheStale(ctx, ev)) return
      if (r.ok) { setContent(r.data); cache.contentError = false }
    } catch (e) { if (cacheStale(ctx, ev)) return }
    renderSponsors({ ctx, sp, ui, api, ev, admin, locked, ended, path, setLive })
  })
  if (readOnly || cache.contentError) return

  async function persist(sponsors) {
    try {
      const r = await api('PUT', path + '/content', { ...cache.content, sponsors })
      if (cacheStale(ctx, ev)) return false
      if (!r.ok) { setLive(ui.errText(r), 'err'); return false }
      setContent(r.data)
      return true
    } catch (e) { if (cacheStale(ctx, ev)) return false; setLive('Cannot reach the server.', 'err'); return false }
  }
  // R10: `moving` ignores a second arrow press (or menu click) while the
  // first move's PUT is still in flight -- two concurrent writes from the
  // same stale `cache.content.sponsors` base would otherwise race, and
  // the second could silently drop the first. After a successful move,
  // focus follows the row to its new position (focus used to fall to
  // <body> on every re-render) and the live region announces it.
  let moving = false
  async function move(i, dir) {
    if (moving) return
    const j = i + dir
    if (j < 0 || j >= items.length) return
    moving = true
    const movedName = items[i].name
    const sponsors = cache.content.sponsors.slice()
    moveItem(sponsors, i, dir)
    const ok = await persist(sponsors)
    moving = false
    if (!ok) return
    renderSponsors({ ctx, sp, ui, api, ev, admin, locked, ended, path, setLive })
    announceReorder(sp, movedName, j, sponsors.length)
    sp.querySelector(`.grip[data-idx="${j}"]`)?.focus()
  }

  async function openDialog(existing) {
    const s = existing ? { ...existing } : { name: '', tier: '', url: '', logo_data: '' }
    let logo = s.logo_data || ''
    const body = document.createElement('div')
    function pv() { return logo ? `<img src="${ui.esc(logo)}" alt="">` : '<svg viewBox="0 0 20 20" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M3 4h14v12H3zM3 13l4-4 3 3 2-2 5 5M13 8h.01"/></svg>' }
    body.innerHTML = `<div class="field" id="d-logo-field"><span class="lab">Logo</span><div class="file-pick"><span class="pv" id="d-logo-pv">${pv()}</span><div class="t"><b id="d-logo-name">${logo ? 'Current logo' : 'No logo'}</b></div><div class="pick-acts"><label class="btn sm">${logo ? 'Replace' : 'Choose logo'}<input type="file" accept="image/png,image/jpeg,image/webp" id="d-logo-file" aria-label="${logo ? 'Replace' : 'Choose'} logo"></label><button class="btn quiet sm" type="button" id="d-logo-rm"${logo ? '' : ' disabled'}>Remove</button></div></div><p class="help">PNG, JPEG or WebP, up to 128 KB. Square logos look best.</p></div>
<div class="field"><label for="d-name">Name</label><input class="input" id="d-name" maxlength="100" value="${ui.esc(s.name)}"></div>
<div class="field"><div class="lab-row"><label for="d-tier">Tier</label><span class="opt">Optional</span></div><input class="input" id="d-tier" maxlength="60" value="${ui.esc(s.tier || '')}"><p class="help">For example Gold, Silver or Community partner.</p></div>
<div class="field" id="d-link-field"><div class="lab-row"><label for="d-link">Link</label><span class="opt">Optional</span></div><input class="input" id="d-link" value="${ui.esc(s.url || '')}"></div>`
    const q = id => body.querySelector('#' + id)
    // R22: logo errors used to show as a toast that could land behind the
    // modal's own scrim -- shown inline under the Logo field instead, like
    // every other field error in this dialog. The file type is also
    // checked client-side now rather than only after an upload attempt
    // (the server checks it again regardless -- cleanLogo).
    q('d-logo-file').addEventListener('change', async () => {
      const field = q('d-logo-field')
      field.querySelectorAll('.err').forEach(n => n.remove())
      const file = q('d-logo-file').files && q('d-logo-file').files[0]
      if (!file) return
      if (!['image/png', 'image/jpeg', 'image/webp'].includes(file.type)) {
        field.insertAdjacentHTML('beforeend', '<p class="err">Use a PNG, JPEG or WebP image.</p>')
        q('d-logo-file').value = ''
        return
      }
      if (file.size > 128 * 1024) {
        field.insertAdjacentHTML('beforeend', '<p class="err">Logo must be under 128 KB.</p>')
        q('d-logo-file').value = ''
        return
      }
      try { logo = await fileAsDataURL(file) } catch (e) { field.insertAdjacentHTML('beforeend', '<p class="err">Could not read that file.</p>'); return }
      q('d-logo-pv').innerHTML = pv()
      q('d-logo-name').textContent = file.name + ' · not saved yet'
      q('d-logo-rm').disabled = false
    })
    q('d-logo-rm').addEventListener('click', () => { logo = ''; q('d-logo-pv').innerHTML = pv(); q('d-logo-name').textContent = 'No logo'; q('d-logo-rm').disabled = true })
    const saved = await formDialog(ui, { title: existing ? 'Edit sponsor' : 'Add sponsor', body, saveLabel: existing ? 'Save sponsor' : 'Add sponsor' }, async () => {
      body.querySelectorAll('.err').forEach(n => n.remove())
      q('d-link-field').classList.remove('has-error')
      const name = q('d-name').value.trim()
      if (!name) { q('d-name').insertAdjacentHTML('afterend', '<p class="err">Name is required.</p>'); return false }
      const url = q('d-link').value.trim()
      if (url && !/^https:\/\//i.test(url)) { q('d-link-field').classList.add('has-error'); q('d-link-field').insertAdjacentHTML('beforeend', '<p class="err">Use a full address that starts with https://</p>'); return false }
      const next = { name, tier: q('d-tier').value, url, logo_data: logo }
      const sponsors = cache.content.sponsors.slice()
      if (!existing) sponsors.push(next)
      else {
        const at = sponsors.indexOf(existing)
        if (at === -1) { body.insertAdjacentHTML('beforeend', '<p class="err">This item changed elsewhere. Reload and try again.</p>'); return false }
        sponsors[at] = next
      }
      return persist(sponsors)
    })
    if (saved) renderSponsors({ ctx, sp, ui, api, ev, admin, locked, ended, path, setLive })
  }

  // R2: fall back to the row's own [data-idx] ancestor for the row menu's
  // Edit item, which carries no index of its own (rowMenuHTML has no idx
  // parameter) -- without it, Number(undefined) is NaN and Save silently
  // writes to arr[NaN], which JSON drops.
  sp.querySelectorAll('[data-act="edit"]').forEach(el => el.addEventListener('click', e => {
    e.preventDefault()
    const i = Number(el.dataset.idx ?? el.closest('[data-idx]').dataset.idx)
    openDialog(items[i])
  }))
  sp.querySelectorAll('[data-act="up"]').forEach(el => el.addEventListener('click', () => move(Number(el.closest('[data-idx]').dataset.idx), -1)))
  sp.querySelectorAll('[data-act="down"]').forEach(el => el.addEventListener('click', () => move(Number(el.closest('[data-idx]').dataset.idx), 1)))
  sp.querySelectorAll('.grip').forEach(el => el.addEventListener('keydown', e => {
    if (e.key === 'ArrowUp') { e.preventDefault(); move(Number(el.dataset.idx), -1) }
    else if (e.key === 'ArrowDown') { e.preventDefault(); move(Number(el.dataset.idx), 1) }
  }))
  sp.querySelectorAll('[data-act="remove"]').forEach(el => el.addEventListener('click', async () => {
    const i = Number(el.closest('[data-idx]').dataset.idx)
    const removed = items[i]
    const before = cache.content
    const sponsors = cache.content.sponsors.filter(x => x !== removed)
    if (await persist(sponsors)) {
      renderSponsors({ ctx, sp, ui, api, ev, admin, locked, ended, path, setLive })
      // R23: focus the row now sitting at the removed one's position (the
      // next row, shifted up), or Add when the list is now empty, instead
      // of letting focus drop to <body>.
      const rows = Array.from(sp.querySelectorAll('.r[data-idx]'))
      const next = rows.find(row => Number(row.dataset.idx) === i) || rows[rows.length - 1]
      const focusEl = (next && next.querySelector('.row-acts [data-act="edit"]')) || sp.querySelector('#sp-add') || sp.querySelector('#sp-add-empty')
      focusEl?.focus()
      undoToast(sp, `Removed ${removed.name}`, async () => { if (await persist(before.sponsors)) renderSponsors({ ctx, sp, ui, api, ev, admin, locked, ended, path, setLive }) })
    }
  }))
  // Drag reorder (desktop): native HTML5 DnD on each row, by the grip's own
  // handle per spec, but the whole row is the drag source (the grip alone
  // can't carry a dragstart listener and still be a focusable button in
  // every browser) -- dropping reorders the same way Move up/down does.
  let dragFrom = null
  sp.querySelectorAll('[data-act="dragrow"]').forEach(row => {
    row.addEventListener('dragstart', e => { dragFrom = Number(row.dataset.idx); e.dataTransfer.effectAllowed = 'move' })
    // R18: dragFrom was never reset, so a file dragged in from the desktop
    // and dropped on a row (or a drag that ends without a drop, e.g. Esc)
    // could reorder later using a stale index from a previous drag.
    row.addEventListener('dragend', () => { dragFrom = null })
    row.addEventListener('dragover', e => { e.preventDefault() })
    row.addEventListener('drop', async e => {
      e.preventDefault()
      if (e.dataTransfer && e.dataTransfer.files && e.dataTransfer.files.length) { dragFrom = null; return }
      const to = Number(row.dataset.idx)
      const from = dragFrom
      dragFrom = null
      if (from == null || from === to) return
      const sponsors = cache.content.sponsors.slice()
      const [moved] = sponsors.splice(from, 1)
      sponsors.splice(to, 0, moved)
      if (await persist(sponsors)) renderSponsors({ ctx, sp, ui, api, ev, admin, locked, ended, path, setLive })
    })
  })
  // Both the header button and the empty-state button open the dialog.
  sp.querySelectorAll('#sp-add, #sp-add-empty').forEach(b => b.addEventListener('click', () => openDialog(null)))
}

// -------------------------------------------------------------------- FAQ
function renderFaq({ ctx, sp, ui, api, ev, admin, locked, ended, path, setLive }) {
  const readOnly = locked || ended // R4/decision 2026-10-31: the server rejects PUT /content once archived.
  const items = cache.content.faq
  function rowHTML(f, i) {
    if (readOnly) return `<div class="r has-grip"><span class="main"><b>${ui.esc(f.question)}</b><span class="clamp2">${ui.esc(f.answer)}</span></span></div>`
    const grip = `<button data-help="faq" class="grip" type="button" aria-roledescription="drag handle" aria-label="Move ${ui.esc(f.question)}, position ${i + 1} of ${items.length}. Use the up and down arrow keys." data-act="grip" data-idx="${i}"><svg viewBox="0 0 12 16" aria-hidden="true" fill="currentColor"><circle cx="3" cy="3" r="1.4"/><circle cx="9" cy="3" r="1.4"/><circle cx="3" cy="8" r="1.4"/><circle cx="9" cy="8" r="1.4"/><circle cx="3" cy="13" r="1.4"/><circle cx="9" cy="13" r="1.4"/></svg></button>`
    const menu = rowMenuHTML(f.question, [{ label: 'Edit', act: 'edit' }, { label: 'Move up', act: 'up', disabled: i === 0 }, { label: 'Move down', act: 'down', disabled: i === items.length - 1 }, { label: 'Remove', act: 'remove', danger: true }])
    return `<div class="r has-grip" data-idx="${i}" draggable="true" data-act="dragrow">${grip}<a class="main" href="#" data-act="edit" data-idx="${i}"><b>${ui.esc(f.question)}</b><span class="clamp2">${ui.esc(f.answer)}</span></a><div class="row-acts"><button class="btn quiet sm" type="button" data-act="edit" data-idx="${i}" aria-label="Edit ${ui.esc(f.question)}">Edit</button>${menu}</div></div>`
  }
  sp.innerHTML = `<div class="sp-head"><h2 id="sp-h">FAQ</h2><p>Questions and answers, in this order. Answers start folded on your page.</p>${readOnly || cache.contentError ? '' : '<button class="btn primary" type="button" id="fq-add"><svg class="ico" viewBox="0 0 20 20" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M10 4v12M4 10h12"/></svg>Add question</button>'}</div>
${customSiteNote(readOnly)}
${ended && !admin ? endedReadOnlyNote('FAQ') : ''}
${readOnly || cache.contentError ? '' : '<p class="sr-only" id="reorder-status" role="status" aria-live="polite"></p>'}
${cache.contentError ? `<div class="note bad">Sponsors, FAQ and schedule could not be loaded. Reload to edit them.<button class="btn sm" type="button" id="fq-reload">Reload</button></div>` :
    items.length ? `<div class="list editable" aria-label="Questions" id="fq-list">${items.map(rowHTML).join('')}</div><p class="list-foot">${items.length} of 30 questions</p>` :
    (readOnly ? '<p class="muted">No questions.</p>' : `<div class="empty-state"><h3>No questions yet</h3><p>Answer what people ask most: who can join, where it happens, what to hand in.</p><button class="btn primary" type="button" id="fq-add-empty">Add question</button></div>`)}`

  const reload = sp.querySelector('#fq-reload')
  if (reload) reload.addEventListener('click', async () => {
    reload.disabled = true
    try {
      const r = await api('GET', path + '/content')
      if (cacheStale(ctx, ev)) return
      if (r.ok) { setContent(r.data); cache.contentError = false }
    } catch (e) { if (cacheStale(ctx, ev)) return }
    renderFaq({ ctx, sp, ui, api, ev, admin, locked, ended, path, setLive })
  })
  if (readOnly || cache.contentError) return

  async function persist(faq) {
    try {
      const r = await api('PUT', path + '/content', { ...cache.content, faq })
      if (cacheStale(ctx, ev)) return false
      if (!r.ok) { setLive(ui.errText(r), 'err'); return false }
      setContent(r.data)
      return true
    } catch (e) { if (cacheStale(ctx, ev)) return false; setLive('Cannot reach the server.', 'err'); return false }
  }
  // R10: see Sponsors' own move() for the focus/live-region/in-flight notes -- same shape here.
  let moving = false
  async function move(i, dir) {
    if (moving) return
    const j = i + dir
    if (j < 0 || j >= items.length) return
    moving = true
    const movedName = items[i].question
    const faq = cache.content.faq.slice()
    moveItem(faq, i, dir)
    const ok = await persist(faq)
    moving = false
    if (!ok) return
    renderFaq({ ctx, sp, ui, api, ev, admin, locked, ended, path, setLive })
    announceReorder(sp, movedName, j, faq.length)
    sp.querySelector(`.grip[data-idx="${j}"]`)?.focus()
  }
  async function openDialog(existing) {
    const f = existing ? { ...existing } : { question: '', answer: '' }
    const body = document.createElement('div')
    body.innerHTML = `<div class="field"><label for="d-q">Question</label><input class="input" id="d-q" maxlength="200" value="${ui.esc(f.question)}"></div>
<div class="field"><label for="d-a">Answer</label><textarea class="input" id="d-a" rows="4" maxlength="2000">${ui.esc(f.answer)}</textarea></div>`
    const q = id => body.querySelector('#' + id)
    const saved = await formDialog(ui, { title: existing ? 'Edit question' : 'Add question', body, saveLabel: existing ? 'Save' : 'Add question' }, async () => {
      body.querySelectorAll('.err').forEach(n => n.remove())
      const question = q('d-q').value.trim(), answer = q('d-a').value.trim()
      if (!question) { q('d-q').insertAdjacentHTML('afterend', '<p class="err">Question is required.</p>'); return false }
      if (!answer) { q('d-a').insertAdjacentHTML('afterend', '<p class="err">Answer is required.</p>'); return false }
      const next = { question, answer }
      const faq = cache.content.faq.slice()
      if (!existing) faq.push(next)
      else {
        const at = faq.indexOf(existing)
        if (at === -1) { body.insertAdjacentHTML('beforeend', '<p class="err">This item changed elsewhere. Reload and try again.</p>'); return false }
        faq[at] = next
      }
      return persist(faq)
    })
    if (saved) renderFaq({ ctx, sp, ui, api, ev, admin, locked, ended, path, setLive })
  }
  sp.querySelectorAll('[data-act="edit"]').forEach(el => el.addEventListener('click', e => {
    e.preventDefault()
    const i = Number(el.dataset.idx ?? el.closest('[data-idx]').dataset.idx)
    openDialog(items[i])
  }))
  sp.querySelectorAll('[data-act="up"]').forEach(el => el.addEventListener('click', () => move(Number(el.closest('[data-idx]').dataset.idx), -1)))
  sp.querySelectorAll('[data-act="down"]').forEach(el => el.addEventListener('click', () => move(Number(el.closest('[data-idx]').dataset.idx), 1)))
  sp.querySelectorAll('.grip').forEach(el => el.addEventListener('keydown', e => {
    if (e.key === 'ArrowUp') { e.preventDefault(); move(Number(el.dataset.idx), -1) }
    else if (e.key === 'ArrowDown') { e.preventDefault(); move(Number(el.dataset.idx), 1) }
  }))
  sp.querySelectorAll('[data-act="remove"]').forEach(el => el.addEventListener('click', async () => {
    const i = Number(el.closest('[data-idx]').dataset.idx)
    const removed = items[i]
    const before = cache.content
    const faq = cache.content.faq.filter(x => x !== removed)
    if (await persist(faq)) {
      renderFaq({ ctx, sp, ui, api, ev, admin, locked, ended, path, setLive })
      const rows = Array.from(sp.querySelectorAll('.r[data-idx]'))
      const next = rows.find(row => Number(row.dataset.idx) === i) || rows[rows.length - 1]
      const focusEl = (next && next.querySelector('.row-acts [data-act="edit"]')) || sp.querySelector('#fq-add') || sp.querySelector('#fq-add-empty')
      focusEl?.focus()
      undoToast(sp, `Removed “${removed.question}”`, async () => { if (await persist(before.faq)) renderFaq({ ctx, sp, ui, api, ev, admin, locked, ended, path, setLive }) })
    }
  }))
  let dragFrom = null
  sp.querySelectorAll('[data-act="dragrow"]').forEach(row => {
    row.addEventListener('dragstart', e => { dragFrom = Number(row.dataset.idx); e.dataTransfer.effectAllowed = 'move' })
    row.addEventListener('dragend', () => { dragFrom = null })
    row.addEventListener('dragover', e => e.preventDefault())
    row.addEventListener('drop', async e => {
      e.preventDefault()
      if (e.dataTransfer && e.dataTransfer.files && e.dataTransfer.files.length) { dragFrom = null; return }
      const to = Number(row.dataset.idx)
      const from = dragFrom
      dragFrom = null
      if (from == null || from === to) return
      const faq = cache.content.faq.slice()
      const [moved] = faq.splice(from, 1)
      faq.splice(to, 0, moved)
      if (await persist(faq)) renderFaq({ ctx, sp, ui, api, ev, admin, locked, ended, path, setLive })
    })
  })
  // Both the header button and the empty-state button open the dialog.
  sp.querySelectorAll('#fq-add, #fq-add-empty').forEach(b => b.addEventListener('click', () => openDialog(null)))
}

// ------------------------------------------------------------------- Look
function renderLook({ sp, ui, api, ctx, ev, admin, locked, ended, path, setLive }) {
  const $ = id => sp.querySelector('#' + id)
  const previewColor = ev.accent_color ? (SWATCH_HEX[ev.accent_color] || ev.color) : (ev.color || '#267b79')
  // R4/decision 2026-10-31: colour and the Show entries switch keep working
  // after End (PATCH on the event itself), but the icon does not -- PUT/
  // DELETE /icon both answer 409 once archived -- so only the icon row
  // loses its controls; everything else on this tab stays live.
  const iconLocked = ended // (admin already disables everything generically, see paintTab)
  const theme = ev.theme || ''
  sp.innerHTML = `<div class="sp-head"><h2 id="sp-h">Look</h2><p>Your icon, colour, page style and whether entries show. Each change saves straight away.</p></div>
${ended && !admin ? '<div class="note">This event has ended. You can still fix the page text, colour and style.</div>' : ''}
<div class="card"><p class="section-title">Preview</p>
<div class="pub-event preview-head" id="look-preview" style="--ev:${previewColor}"${theme ? ` data-theme="${ui.esc(theme)}"` : ''}><span class="ev-icon">${ev.icon_media_type ? `<img src="${ui.esc(ev.icon_url)}" alt="" width="52" height="52">` : ''}</span><div><h3>${ui.esc(ev.title)}</h3><p>${ui.esc(ev.tagline || '')}</p></div></div></div>
<div class="card flat">
<div class="setting stack"><div class="t"><div class="t-row"><b>Page style</b><button class="btn quiet sm" type="button" id="theme-shuffle">Try another</button><span class="saved" id="theme-saved" hidden><svg class="ico" viewBox="0 0 20 20" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M4 10.5l4 4 8-9"/></svg>Saved</span></div><span>Background, type and card shapes for your public page.</span></div>
<div class="choices" role="radiogroup" aria-label="Page style">${THEMES.map(([v, l, d]) => `<label class="choice${theme === v ? ' on' : ''}"><input data-help="look.style" type="radio" name="theme" value="${v}"${theme === v ? ' checked' : ''}><span><b>${ui.esc(l)}</b><span class="d">${ui.esc(d)}</span></span></label>`).join('')}</div>
</div>
<div class="setting wraps"><div class="t"><b>Event icon</b><span>Shown round on your page, in Find a hackathon and on join links. PNG, JPEG or WebP, up to 128 KB.${iconLocked ? ' Can’t change after End.' : ''}</span></div>
<div class="ctl">${iconLocked
    ? `<span class="file-pick"><span class="pv round sm">${ev.icon_media_type ? `<img src="${ui.esc(ev.icon_url)}" alt="">` : ''}</span></span>`
    : `<span class="file-pick"><span class="pv round sm">${ev.icon_media_type ? `<img src="${ui.esc(ev.icon_url)}" alt="">` : ''}</span></span><label class="btn sm">${ev.icon_media_type ? 'Replace' : 'Upload icon'}<input type="file" accept="image/png,image/jpeg,image/webp" id="icon-file" aria-label="${ev.icon_media_type ? 'Replace' : 'Upload'} event icon"></label><button class="btn quiet sm" type="button" id="icon-remove"${ev.icon_media_type ? '' : ' disabled'}>Remove</button>`}</div></div>
<div class="setting stack"><div class="t"><div class="t-row"><b>Colour</b><span class="saved" id="colour-saved" hidden><svg class="ico" viewBox="0 0 20 20" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M4 10.5l4 4 8-9"/></svg>Saved</span></div><span>For buttons, links and your page style's highlights. Automatic picks one from your address.</span></div>
<fieldset class="swatches" aria-label="Event colour">${CRAYONS.map(([v, l]) => {
    const hex = v ? SWATCH_HEX[v] : (ev.color || '#d15d48')
    const ink = ui.markInk(hex)
    return `<label class="swatch${v ? '' : ' auto'}"><input data-help="look.colour" type="radio" name="colour" value="${v}" data-c="${hex}"${(ev.accent_color || '') === v ? ' checked' : ''}><i style="--sw:${hex};--sw-ink:${ink}"><svg class="ico" viewBox="0 0 20 20" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M4 10.5l4 4 8-9"/></svg></i><span>${ui.esc(l)}</span></label>`
  }).join('')}</fieldset></div>
<div class="setting" id="gallery"><div class="t"><div class="t-row"><b>Show entries on the event page</b><span class="saved" id="gallery-saved" hidden><svg class="ico" viewBox="0 0 20 20" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M4 10.5l4 4 8-9"/></svg>Saved</span></div><span>Lists every team whose website is live. People's choice voting needs this on.</span></div><div class="ctl"><button class="toggle${ev.gallery_open ? ' on' : ''}" type="button" role="switch" aria-checked="${!!ev.gallery_open}" id="gallery-toggle" aria-label="Show entries on the event page"></button></div></div>
</div>`

  function flashSaved(id) {
    const el = $(id)
    el.hidden = false
    clearTimeout(el._t)
    el._t = setTimeout(() => { el.hidden = true }, 3000)
  }
  // Page style: a radio choice that saves at once, same chained-PATCH/revert
  // shape as Colour below (serialised so a rapid second pick still lands
  // after the first finishes rather than racing it, and the radios stay
  // enabled and focusable throughout). The preview gets data-theme so it
  // updates instantly; themes.css is scoped to .pub-event[data-theme] and
  // the compound .pub-event[data-theme].preview-head for this preview.
  function paintThemeChoices(val) {
    sp.querySelectorAll('input[name=theme]').forEach(r => { r.closest('.choice').classList.toggle('on', r.value === val) })
  }
  function setPreviewTheme(val) {
    if (val) $('look-preview').setAttribute('data-theme', val); else $('look-preview').removeAttribute('data-theme')
  }
  let themeChain = Promise.resolve()
  // lastPickedTheme tracks the most recent click regardless of how far its
  // own PATCH has got. A rapid pick #1 -> pick #2 queues both PATCHes in
  // order, but pick #1's own failure/success handler below only touches
  // the preview/cards/radio when pick #1 is STILL the latest pick -- so an
  // old, now-superseded attempt landing late can no longer stomp on
  // whatever the user has since chosen (review fix: it used to always
  // revert/repaint to its own value, flashing the UI back to a stale pick
  // for a moment even though a newer one was already queued behind it).
  let lastPickedTheme = theme
  function saveTheme(val) {
    lastPickedTheme = val
    setPreviewTheme(val)
    paintThemeChoices(val)
    themeChain = themeChain.then(async () => {
      // prevVal is read here, not at click time, so a second pick made
      // before the first PATCH lands reverts to whatever the chain has
      // actually settled on so far (same timing as Colour's prevHex below).
      const prevVal = ev.theme || ''
      try {
        const r = await api('PATCH', path, { theme: val })
        if (!r.ok) {
          setLive(ui.errText(r), 'err')
          if (val === lastPickedTheme) {
            setPreviewTheme(prevVal)
            paintThemeChoices(prevVal)
            const prevRadio = sp.querySelector(`input[name=theme][value="${prevVal}"]`)
            if (prevRadio) prevRadio.checked = true
          }
          return
        }
        ev.theme = val
        // Re-assert the preview/cards/radio on success too, but only when
        // this PATCH's value is still the latest pick -- an old pick's
        // success landing late should not overwrite a newer one; that
        // newer pick's own chain link will land right after and re-assert
        // itself regardless.
        if (val === lastPickedTheme) {
          setPreviewTheme(val)
          paintThemeChoices(val)
          const radio = sp.querySelector(`input[name=theme][value="${val}"]`)
          if (radio) radio.checked = true
        }
        flashSaved('theme-saved')
      } catch (e) {
        setLive('Cannot reach the server.', 'err')
        if (val === lastPickedTheme) {
          setPreviewTheme(prevVal)
          paintThemeChoices(prevVal)
          const prevRadio = sp.querySelector(`input[name=theme][value="${prevVal}"]`)
          if (prevRadio) prevRadio.checked = true
        }
      }
    })
  }
  sp.querySelectorAll('input[name=theme]').forEach(radio => radio.addEventListener('change', () => saveTheme(radio.value)))
  $('theme-shuffle').addEventListener('click', () => {
    // Exclude whatever is currently checked (the in-progress pick, which
    // may not have saved yet), not just ev.theme's last-committed value --
    // otherwise Try another could re-roll back onto the theme already
    // showing in the radios/preview.
    const checked = sp.querySelector('input[name=theme]:checked')
    const currentVal = checked ? checked.value : (ev.theme || '')
    const opts = THEMES.map(([v]) => v).filter(v => v && v !== currentVal)
    const pick = opts[Math.floor(Math.random() * opts.length)]
    const radio = sp.querySelector(`input[name=theme][value="${pick}"]`)
    if (radio) { radio.checked = true; radio.dispatchEvent(new Event('change')) }
  })
  if (!iconLocked) {
    $('icon-file').addEventListener('change', async () => {
      const input = $('icon-file')
      const file = input.files && input.files[0]
      if (!file) return
      if (!['image/png', 'image/jpeg', 'image/webp'].includes(file.type)) { setLive('Use a PNG, JPEG or WebP image.', 'err', input); input.value = ''; return }
      let buf
      try { buf = await fileAsArrayBuffer(file) } catch (e) { setLive('Could not read that file.', 'err', input); return }
      try {
        const r = await ctx.api.apiBytes('PUT', path + '/icon', buf, file.type)
        if (!r.ok) { setLive(ui.errText(r), 'err', input); input.value = ''; return }
        await ctx.refresh()
      } catch (e) { setLive('Cannot reach the server.', 'err', input); input.value = '' }
    })
    $('icon-remove').addEventListener('click', async () => {
      const btn = $('icon-remove'); btn.disabled = true
      try {
        const r = await api('DELETE', path + '/icon')
        if (!r.ok) { setLive(ui.errText(r), 'err', btn); btn.disabled = false; return }
        await ctx.refresh()
      } catch (e) { setLive('Cannot reach the server.', 'err', btn); btn.disabled = false }
    })
  }
  // R15: the radios used to be disabled for the duration of each PATCH,
  // which drops them out of the tab/arrow-key focus order mid-navigation
  // (a keyboard user arrowing through the swatches lost their place after
  // every step). Serialised through one promise chain instead, so a rapid
  // second choice still lands after the first finishes rather than racing
  // it, and the radios themselves stay enabled and focusable throughout.
  // R14: a successful change now also calls ctx.refresh() (restores the
  // pre-rebuild behaviour) so the sidebar switcher mark and status bar
  // pick up the new colour, and a failed change reverts the live preview
  // --ev (it used to only revert which radio was checked).
  let colourChain = Promise.resolve()
  sp.querySelectorAll('input[name=colour]').forEach(radio => radio.addEventListener('change', () => {
    const chosen = radio.value
    $('look-preview').style.setProperty('--ev', radio.dataset.c)
    colourChain = colourChain.then(async () => {
      try {
        const r = await api('PATCH', path, { accent_color: chosen })
        if (!r.ok) {
          setLive(ui.errText(r), 'err')
          const prevHex = ev.accent_color ? (SWATCH_HEX[ev.accent_color] || ev.color) : (ev.color || '#d15d48')
          $('look-preview').style.setProperty('--ev', prevHex)
          const prevRadio = sp.querySelector(`input[name=colour][value="${ev.accent_color || ''}"]`)
          if (prevRadio) prevRadio.checked = true
          return
        }
        ev.accent_color = chosen
        flashSaved('colour-saved')
        // Update the sidebar's event mark in place: a full refresh would move
        // keyboard focus off the swatches on every arrow step.
        const hex = radio.dataset.c
        document.querySelectorAll('.switcher .ev-mark').forEach(m => { m.style.setProperty('--ev', hex); m.style.color = ui.markInk(hex) })
      } catch (e) {
        setLive('Cannot reach the server.', 'err')
        const prevHex = ev.accent_color ? (SWATCH_HEX[ev.accent_color] || ev.color) : (ev.color || '#d15d48')
        $('look-preview').style.setProperty('--ev', prevHex)
        const prevRadio = sp.querySelector(`input[name=colour][value="${ev.accent_color || ''}"]`)
        if (prevRadio) prevRadio.checked = true
      }
    })
  }))
  $('gallery-toggle').addEventListener('click', async () => {
    const btn = $('gallery-toggle')
    const want = !btn.classList.contains('on')
    btn.disabled = true
    try {
      const r = await api('PATCH', path, { gallery_open: want })
      if (!r.ok) { setLive(ui.errText(r), 'err', btn); btn.disabled = false; return }
      ev.gallery_open = want
      btn.classList.toggle('on', want)
      btn.setAttribute('aria-checked', String(want))
      flashSaved('gallery-saved')
      btn.disabled = false
    } catch (e) { setLive('Cannot reach the server.', 'err', btn); btn.disabled = false }
  })
}

// --------------------------------------------------------------- Website
function renderWebsite({ ctx, sp, ui, api, apiBytes, ev, admin, locked, ended, path, setLive }) {
  const readOnly = locked || ended // R4/decision 2026-10-31: PATCH/PUT /website both answer 409 once archived.
  const w = cache.website
  const customLive = w.mode === 'custom'
  const hasUploads = (w.versions || []).length > 0
  // R20: "Version N, uploaded <date>" named the *newest* upload's date even
  // when an earlier version was the one actually active (switching back to
  // an older version isn't offered by the API, but the active version can
  // still be behind the newest if the organiser switched to the built-in
  // page after an upload, then back without uploading again).
  const activeNum = w.active_version || (w.versions[w.versions.length - 1] || {}).version_number
  const activeVersion = (w.versions || []).find(v => v.version_number === activeNum) || w.versions[w.versions.length - 1] || {}
  sp.innerHTML = `<div class="sp-head"><h2 id="sp-h">Website</h2><p>What visitors get at your address.</p></div>
${ended && !admin ? endedReadOnlyNote('Your website') : ''}
${customLive ? `<div class="note info"><b>Your own website is live at your address.</b>${readOnly ? '' : ' Basics, About and Look still save, and show again if you switch back to the built-in page.'}</div>` : ''}
${cache.websiteError ? `<div class="note bad">Your website settings could not be loaded. Reload to edit them.<button class="btn sm" type="button" id="web-reload">Reload</button></div>` : ''}
<div class="card">
<div class="field"><span class="lab">Your address</span>
<div class="addr"><span class="mono">${ui.esc(ev.url.replace(/^https?:\/\//, ''))}</span><button class="btn sm" type="button" id="addr-copy">Copy</button><a class="btn sm" href="${ui.esc(ev.url)}" target="_blank" rel="noopener">Open</a></div></div>
<div class="field-group"><div class="t-row"><h3>What shows there</h3>${readOnly ? '' : '<span class="saved" id="mode-saved" hidden><svg class="ico" viewBox="0 0 20 20" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M4 10.5l4 4 8-9"/></svg>Saved</span>'}</div>
${readOnly ? `<p class="muted">${customLive ? `Your own website -- Version ${ui.esc(String(activeNum))}, uploaded ${ui.esc(ui.dateOnly(activeVersion.created_at, ev))}.` : 'Built-in event page.'}</p>` :
    `<div class="choices" role="radiogroup" aria-label="What shows at your address"><label class="choice${!customLive ? ' on' : ''}"><input data-help="website" type="radio" name="mode" value="builtin"${!customLive ? ' checked' : ''}><span><b>Built-in event page</b><span class="d">Made from Basics, About, Schedule, Sponsors and FAQ. It updates as you edit.</span></span></label><label class="choice${customLive ? ' on' : hasUploads ? '' : ' off'}"><input data-help="website" type="radio" name="mode" value="custom"${customLive ? ' checked' : ''}${hasUploads ? '' : ' disabled'}><span><b>Your own website</b><span class="d">${hasUploads ? `Version ${ui.esc(String(activeNum))}, uploaded ${ui.esc(ui.dateOnly(activeVersion.created_at, ev))}. You can switch back any time. Nothing is deleted.` : 'Upload a zip first'}</span></span></label></div>`}</div>
</div>
<div class="card"><h2>Your own website</h2><p class="sub">A .zip with index.html at the top, up to 25 MB. ${hasUploads ? 'Each upload goes live at your address right away. Earlier uploads stay listed.' : 'Your first upload goes live at your address right away.'}${w.upload_limits ? ' ' + ui.esc(w.upload_limits) : ''}</p>
${readOnly ? '' : '<div class="drop" id="drop-zone"><svg class="ico" viewBox="0 0 20 20" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M10 13V4M6 8l4-4 4 4M4 14v2h12v-2"/></svg><b>Drop a .zip here</b><span class="small">or</span><label class="btn primary">Choose zip<input type="file" accept=".zip,application/zip" id="site-zip" aria-label="Choose a zip to upload"></label></div>'}
<p class="section-title">Uploads</p>
<div class="list" id="site-versions">${(w.versions || []).slice().reverse().map(v => `<div class="r"><div class="main"><b>Version ${v.version_number}</b><span>${ui.esc(ui.dateTime(v.created_at, ev))}</span></div>${customLive && v.version_number === w.active_version ? '<span class="pill ok">Live</span>' : !customLive && v === (w.versions || [])[w.versions.length - 1] ? '<span class="pill">Newest</span>' : ''}</div>`).join('') || '<div class="empty">No uploads yet.</div>'}</div>
<p class="help">An AI app can publish versions here too, and manage your website's own saved data (keys, tables and files).</p>
<p class="help">Your own website updates itself at each stage, results included, when it is built from the “event” page recipe. Made yours earlier? Ask your AI app to add the live event feed from that recipe.</p>
</div>`

  sp.querySelector('#addr-copy').addEventListener('click', async () => {
    try { await navigator.clipboard.writeText(ev.url); setLive('Copied', 'ok') } catch (e) { setLive('Could not copy. Select the text and copy it.', 'err') }
  })
  const reload = sp.querySelector('#web-reload')
  if (reload) reload.addEventListener('click', async () => {
    reload.disabled = true
    try {
      const r = await api('GET', path + '/website')
      if (cacheStale(ctx, ev)) return
      if (r.ok) { cache.website = r.data; cache.websiteError = false }
    } catch (e) { if (cacheStale(ctx, ev)) return }
    renderWebsite({ ctx, sp, ui, api, apiBytes, ev, admin, locked, ended, path, setLive })
  })
  if (readOnly) return

  async function doUpload(file) {
    if (!file) return
    // R5/decision 2026-10-31: an upload always goes live at this address at
    // once (SetEventWebsiteMode(..., "custom") runs after every successful
    // upload, builtin or not) -- while the built-in page is still what's
    // showing, confirm first, so an organiser uploading a work-in-progress
    // zip "to switch to later" isn't surprised that it replaced the public
    // page immediately.
    if (!customLive) {
      const ok = await ctx.ui.confirm({ title: 'Switch to your own website?', body: 'This upload replaces the built-in page at your address right away. You can switch back to the built-in page any time.', confirmLabel: 'Upload and go live' })
      if (!ok) return
    }
    let buf
    try { buf = await fileAsArrayBuffer(file) } catch (e) { setLive('Could not read that file.', 'err'); return }
    try {
      // ?create=1: the server's underlying site record for this event's
      // website is created lazily on the first upload, same as a team
      // site (internal/handler/hack_uploads_test.go's own upload() helper
      // always appends it); the old inline file input never did, so a
      // brand-new event's very first website upload 404'd.
      const r = await apiBytes('PUT', path + '/website?create=1', buf, 'application/zip')
      if (cacheStale(ctx, ev)) return
      if (!r.ok) { setLive(archiveErrorText(ui, r), 'err'); return }
      const got = await api('GET', path + '/website')
      if (cacheStale(ctx, ev)) return
      if (got.ok) { cache.website = got.data; cache.websiteError = false }
      const latest = (cache.website.versions || []).slice(-1)[0]
      setLive(latest ? `Version ${latest.version_number} is up` : 'Website published', 'ok')
      renderWebsite({ ctx, sp, ui, api, apiBytes, ev, admin, locked, ended, path, setLive })
    } catch (e) { if (cacheStale(ctx, ev)) return; setLive('Cannot reach the server.', 'err') }
  }
  sp.querySelector('#site-zip').addEventListener('change', e => doUpload(e.target.files && e.target.files[0]))
  const drop = sp.querySelector('#drop-zone')
  drop.addEventListener('dragover', e => e.preventDefault())
  drop.addEventListener('drop', e => { e.preventDefault(); const f = e.dataTransfer.files && e.dataTransfer.files[0]; if (f) doUpload(f) })

  sp.querySelectorAll('input[name=mode]').forEach(radio => radio.addEventListener('change', async () => {
    const toCustom = radio.value === 'custom'
    if (toCustom) {
      const ok = await ctx.ui.confirm({ title: 'Switch to custom website?', body: 'Your own site replaces the built-in page at this address. You can switch back any time. Nothing is deleted.', confirmLabel: 'Switch' })
      if (!ok) { renderWebsite({ ctx, sp, ui, api, apiBytes, ev, admin, locked, ended, path, setLive }); return }
    }
    sp.querySelectorAll('input[name=mode]').forEach(r => r.disabled = true)
    try {
      const r = await api('PATCH', path + '/website', { mode: toCustom ? 'custom' : 'builtin' })
      if (cacheStale(ctx, ev)) return
      if (!r.ok) { setLive(ui.errText(r), 'err'); renderWebsite({ ctx, sp, ui, api, apiBytes, ev, admin, locked, ended, path, setLive }); return }
      cache.website = r.data
      renderWebsite({ ctx, sp, ui, api, apiBytes, ev, admin, locked, ended, path, setLive })
      const tick = sp.querySelector('#mode-saved'); if (tick) { tick.hidden = false; setTimeout(() => { tick.hidden = true }, 3000) }
    } catch (e) { if (cacheStale(ctx, ev)) return; setLive('Cannot reach the server.', 'err'); renderWebsite({ ctx, sp, ui, api, apiBytes, ev, admin, locked, ended, path, setLive }) }
  }))
}
