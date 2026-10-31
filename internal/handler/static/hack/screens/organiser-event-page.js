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
import { fileAsDataURL, fileAsArrayBuffer, moveItem, rowMenuHTML, wireRowMenus, formDialog, undoToast, setDirtyGuard, clearDirtyGuard } from './_orgA-util.js'

export function title(ctx) {
  const tab = resolveTab(ctx)
  return TAB_LABEL[tab] + ' · Event page'
}

const TABS = ['basics', 'about', 'schedule', 'sponsors', 'faq', 'look', 'website']
const TAB_LABEL = { basics: 'Basics', about: 'About', schedule: 'Schedule', sponsors: 'Sponsors', faq: 'FAQ', look: 'Look', website: 'Website' }
// Old deep links (BUILD.md): #basics/#schedule/#sponsors/#faq -> that tab,
// #appearance -> look, #address -> website; #announcements, or tab=
// announcements, redirects away to ?s=announcements entirely (handled in
// render(), not here).
const HASH_TAB = { basics: 'basics', schedule: 'schedule', sponsors: 'sponsors', faq: 'faq', appearance: 'look', address: 'website' }
function resolveTab(ctx) {
  const t = ctx.params.get('tab') || ''
  return TABS.includes(t) ? t : 'basics'
}

const CRAYONS = [['', 'Automatic'], ['red', 'Red'], ['teal', 'Teal'], ['indigo', 'Indigo'], ['orange', 'Orange'], ['yellow', 'Yellow'], ['grass', 'Grass'], ['blush', 'Blush']]
const SWATCH_HEX = { red: '#d15d48', teal: '#267b79', indigo: '#4a49a8', orange: '#e9a45f', yellow: '#f2c94c', grass: '#86ab74', blush: '#ee8f92' }

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
// the website mode/versions) is cached per event slug across tab switches,
// since BUILD.md/editing_rules say switching tabs must not refetch -- every
// tab render in this module is a brand-new call (nav.js keeps ?s=event-page
// on one screen id; only `tab` changes), so the cache has to live at module
// scope, not inside render().
const cache = { slug: null, content: null, contentError: false, website: null, loaded: false }

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
  const counts = { schedule: cache.content.schedule.length, sponsors: cache.content.sponsors.length, faq: cache.content.faq.length }
  return `<nav class="subnav" aria-label="Event page sections">${TABS.map(t => {
    const n = counts[t]
    const badge = n ? `<span class="n" aria-label="${n} items">${n}</span>` : ''
    return `<a href="?s=event-page&tab=${t}"${t === tab ? ' aria-current="page"' : ''}>${TAB_LABEL[t]}${badge}</a>`
  }).join('')}</nav>`
}

function customSiteNote() {
  return cache.website.mode === 'custom' && cache.website.has_custom_page
    ? '<div class="note info"><b>Your own website is live at your address.</b> This still saves, and shows again if you switch back to the built-in page.</div>' : ''
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
  // never lands back on this screen's old URL (BUILD.md).
  let tab = ctx.params.get('tab') || ''
  if (location.hash) {
    const h = decodeURIComponent(location.hash.slice(1))
    if (h === 'announcements') tab = 'announcements'
    else if (HASH_TAB[h]) { tab = HASH_TAB[h]; history.replaceState(history.state, '', `${location.pathname}?s=event-page&tab=${tab}`) }
  }
  if (tab === 'announcements') {
    history.replaceState(history.state, '', `/e/${encodeURIComponent(ev.slug)}/manage?s=announcements`)
    return ctx.refresh()
  }
  if (!TABS.includes(tab)) {
    tab = 'basics'
    history.replaceState(history.state, '', `${location.pathname}?s=event-page&tab=basics`)
  }

  if (cache.slug !== ev.slug) {
    cache.slug = ev.slug
    cache.content = { sponsors: [], faq: [], schedule: [] }
    cache.contentError = false
    cache.website = { mode: ev.website_mode, has_custom_page: false, versions: [], upload_limits: '' }
    cache.loaded = false
  }
  if (!cache.loaded) {
    try {
      const [c, w] = await Promise.all([api('GET', path + '/content'), api('GET', path + '/website')])
      if (!ctx.alive()) return
      if (c.ok) cache.content = { sponsors: c.data.sponsors || [], faq: c.data.faq || [], schedule: c.data.schedule || [] }
      else cache.contentError = true
      if (w.ok) cache.website = w.data
      cache.loaded = true
    } catch (e) { cache.contentError = true; cache.loaded = true }
    if (!ctx.alive()) return
  }

  // Only Basics/About install the unsaved-changes guard; every other tab
  // clears it (also clears for whoever rendered last, if the person left
  // Event page entirely without saving or discarding -- see
  // _orgA-util.js's setDirtyGuard/clearDirtyGuard for the full contract).
  if (tab !== 'basics' && tab !== 'about') clearDirtyGuard()

  main.innerHTML = `<div class="ph"><div class="t"><h1>Event page</h1><p>What visitors see at <a href="${ui.esc(ev.url)}" target="_blank" rel="noopener">${ui.esc(ev.url.replace(/^https?:\/\//, ''))}</a> before they sign up.</p></div><a class="btn ph-link" href="${ui.esc(ev.url)}" target="_blank" rel="noopener">View public page <svg class="ico" viewBox="0 0 20 20" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M11 4h5v5M16 4l-7 7M14 12v4H4V6h4"/></svg></a></div>
<div class="subnav-split">${subnavHTML(tab, ev)}<section class="sp" aria-labelledby="sp-h" id="sp"></section></div>`
  const sp = main.querySelector('#sp')

  if (admin) {
    const banner = document.createElement('div')
    banner.className = 'note warn'
    banner.setAttribute('role', 'alert')
    banner.textContent = 'Viewing as platform admin. Everything here is read only.'
    main.insertBefore(banner, main.firstChild)
  }
  const locked = admin // read-only treatment per tab decides the rest from `ended`

  const helpers = { ctx, main, sp, ui, api, apiBytes, ev, admin, locked, ended, path, setLive }
  if (tab === 'basics') renderBasics(helpers)
  else if (tab === 'about') renderAbout(helpers)
  else if (tab === 'schedule') renderSchedule(helpers)
  else if (tab === 'sponsors') renderSponsors(helpers)
  else if (tab === 'faq') renderFaq(helpers)
  else if (tab === 'look') renderLook(helpers)
  else if (tab === 'website') renderWebsite(helpers)

  if (locked) sp.querySelectorAll('button, input, textarea, select').forEach(n => { if (!n.hasAttribute('data-admin-live')) n.disabled = true })
  focusHeading(sp)
}

// ---------------------------------------------------------------- Basics
function renderBasics({ ctx, sp, ui, api, ev, locked, ended, path, setLive }) {
  const $ = id => sp.querySelector('#' + id)
  const snapshot = { title: ev.title, tagline: ev.tagline || '', starts: ui.localInputValue(ev.starts_at, ev.time_zone, { dateOnly: true }), ends: ui.localInputValue(ev.ends_at, ev.time_zone, { dateOnly: true }), tz: ev.time_zone }

  if (locked || ended) {
    sp.innerHTML = `<div class="sp-head"><h2 id="sp-h">Basics</h2><p>The name, tagline and dates at the top of your page.</p></div>
${customSiteNote()}
${ended && !locked ? '<div class="note">This event has ended. You can still fix your public page.</div>' : ''}
<div class="card"><dl style="margin:0"><dt>Title</dt><dd>${ui.esc(ev.title)}</dd><dt>Tagline</dt><dd>${ui.esc(ev.tagline || '—')}</dd><dt>Starts</dt><dd>${ui.esc(ui.dateOnly(ev.starts_at, ev))}</dd><dt>Ends</dt><dd>${ui.esc(ui.dateOnly(ev.ends_at, ev))}</dd><dt>Time zone</dt><dd>${ui.esc(ev.time_zone)}</dd></dl></div>`
    return
  }

  sp.innerHTML = `<div class="sp-head"><h2 id="sp-h">Basics</h2><p>The name, tagline and dates at the top of your page.</p></div>
${customSiteNote()}
<form class="card" aria-labelledby="sp-h">
<div class="field"><label for="f-title">Title</label><input class="input" id="f-title" maxlength="120" value="${ui.esc(ev.title)}"></div>
<div class="field"><div class="lab-row"><label for="f-tag">Tagline</label><span class="opt">Optional</span><span class="counter" id="f-tag-ct">${(ev.tagline || '').length} / 160</span></div><input class="input" id="f-tag" maxlength="160" value="${ui.esc(ev.tagline || '')}"><p class="help">One line under the title. Link previews in WhatsApp show it too.</p></div>
<div class="field-row">
<div class="field"><label for="f-start">Starts</label><input class="input" id="f-start" type="date" value="${ui.esc(snapshot.starts)}"></div>
<div class="field"><label for="f-end">Ends</label><input class="input" id="f-end" type="date" value="${ui.esc(snapshot.ends)}"></div>
<div class="field grow"><label for="f-tz">Time zone</label><select class="select" id="f-tz">${zoneOptions(ui, ev.time_zone).map(z => `<option value="${ui.esc(z.zone)}"${z.zone === ev.time_zone ? ' selected' : ''}>${ui.esc(z.label)}</option>`).join('')}</select><p class="help">Every deadline and schedule time shows in this zone.</p></div>
</div>
<div class="card-foot"><span class="hint" id="f-hint">No unsaved changes</span><button class="btn quiet" type="button" id="f-discard" hidden>Discard</button><button class="btn primary" type="button" id="f-save" disabled>Save</button></div>
</form>`

  function isDirty() {
    return $('f-title').value !== snapshot.title || $('f-tag').value !== snapshot.tagline ||
      $('f-start').value !== snapshot.starts || $('f-end').value !== snapshot.ends || $('f-tz').value !== snapshot.tz
  }
  function paintDirty() {
    const dirty = isDirty()
    $('f-hint').textContent = dirty ? 'Unsaved changes' : 'No unsaved changes'
    $('f-hint').classList.toggle('dirty', dirty)
    $('f-discard').hidden = !dirty
    $('f-save').disabled = !dirty
  }
  ;['f-title', 'f-tag', 'f-start', 'f-end', 'f-tz'].forEach(id => $(id).addEventListener('input', paintDirty))
  $('f-tag').addEventListener('input', () => { $('f-tag-ct').textContent = $('f-tag').value.length + ' / 160' })
  $('f-discard').addEventListener('click', () => {
    $('f-title').value = snapshot.title; $('f-tag').value = snapshot.tagline; $('f-start').value = snapshot.starts; $('f-end').value = snapshot.ends; $('f-tz').value = snapshot.tz
    $('f-tag-ct').textContent = snapshot.tagline.length + ' / 160'
    paintDirty()
  })
  $('f-save').addEventListener('click', async () => {
    const btn = $('f-save'); btn.disabled = true
    try {
      const r = await api('PATCH', path, { title: $('f-title').value, tagline: $('f-tag').value, time_zone: $('f-tz').value, starts_at: $('f-start').value, ends_at: $('f-end').value })
      if (!r.ok) { setLive(ui.errText(r), 'err', btn); paintDirty(); return }
      // #10 (kept from the old file): a changed title or time zone reaches
      // the switcher/sidebar/status bar/document.title too, which only a
      // full ctx.refresh() rebuilds; the toast survives that (ui.js's own
      // "keep" timers).
      await ctx.refresh()
      ui.toast('Saved', null)
    } catch (e) { setLive('Cannot reach the server.', 'err', btn); paintDirty() }
  })
  setDirtyGuard(ctx, () => (isDirty() ? { label: 'Basics' } : null))
}

// ------------------------------------------------------------------ About
function renderAbout({ ctx, sp, ui, api, ev, locked, ended, path, setLive }) {
  const $ = id => sp.querySelector('#' + id)
  const snapshot = { about: ev.about || '', prizes: ev.prizes || '', rules: ev.rules || '', coc: ev.coc_text || '' }

  if (locked || ended) {
    sp.innerHTML = `<div class="sp-head"><h2 id="sp-h">About</h2><p>The longer text on your page. A blank line starts a new paragraph.</p></div>
${customSiteNote()}
${ended && !locked ? '<div class="note">This event has ended. You can still fix your public page.</div>' : ''}
<div class="card"><dl style="margin:0"><dt>About</dt><dd>${ui.esc(ev.about || '—')}</dd><dt>Prizes</dt><dd>${ui.esc(ev.prizes || '—')}</dd><dt>Rules</dt><dd>${ui.esc(ev.rules || '—')}</dd><dt>Code of conduct additions</dt><dd>${ui.esc(ev.coc_text || '—')}</dd></dl></div>`
    return
  }

  sp.innerHTML = `<div class="sp-head"><h2 id="sp-h">About</h2><p>The longer text on your page. A blank line starts a new paragraph.</p></div>
${customSiteNote()}
<form class="card" aria-labelledby="sp-h">
<div class="field"><div class="lab-row"><label for="f-about">About</label><span class="counter" id="f-about-ct">${snapshot.about.length} / 10000</span></div><textarea class="input" id="f-about" rows="7" maxlength="10000">${ui.esc(snapshot.about)}</textarea></div>
<div class="field"><div class="lab-row"><label for="f-prizes">Prizes</label><span class="opt">Optional</span></div><textarea class="input" id="f-prizes" rows="3" maxlength="5000">${ui.esc(snapshot.prizes)}</textarea></div>
<div class="field"><div class="lab-row"><label for="f-rules">Rules</label><span class="opt">Optional</span></div><textarea class="input" id="f-rules" rows="3" maxlength="10000">${ui.esc(snapshot.rules)}</textarea></div>
<div class="field-group"><h3>Code of conduct</h3>
<details><summary class="small muted">Read the standard code, which every event shows first</summary><p class="small muted" style="margin:8px 0 0">${ui.esc(ev.coc_default || '')}</p></details>
<div class="field" style="margin-top:14px"><div class="lab-row"><label for="f-coc">Your additions</label><span class="opt">Optional</span></div><textarea class="input" id="f-coc" rows="3" maxlength="10000">${ui.esc(snapshot.coc)}</textarea><p class="help">Shown after the standard code on the join page.</p></div>
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
  setDirtyGuard(ctx, () => (isDirty() ? { label: 'About' } : null))
}

// --------------------------------------------------------------- Schedule
function dayHeader(ui, iso, ev) { return ui.dateOnly(iso, ev) }
function renderSchedule({ ctx, sp, ui, api, ev, admin, locked, ended, path, setLive }) {
  const readOnly = locked || ended // decision: Schedule (unlike the other six) is read-only after End, not just under admin view.
  const items = cache.content.schedule.slice().sort((a, b) => new Date(a.start_at) - new Date(b.start_at))
  const now = Date.now()
  const nowIdx = items.findIndex(it => new Date(it.start_at).getTime() <= now && now < new Date(it.end_at).getTime())
  const nextIdx = items.findIndex(it => new Date(it.start_at).getTime() > now)

  // 12-hour with am/pm, like every other time in the app (owner, 2026-10-30).
  function timeLabel(iso) { return new Intl.DateTimeFormat('en-GB', { hour: 'numeric', minute: '2-digit', hourCycle: 'h12', timeZone: ev.time_zone }).format(new Date(iso)) }
  function endLabel(startIso, endIso) {
    const sameDay = ui.dateOnly(startIso, ev) === ui.dateOnly(endIso, ev)
    return sameDay ? timeLabel(endIso) : ui.dateOnly(endIso, ev).replace(/^(\S+)\s.*/, '$1') + ' ' + timeLabel(endIso)
  }
  function rowHTML(item, i) {
    const pill = i === nowIdx ? '<span class="pill stage">Now</span>' : i === nextIdx ? '<span class="pill">Next</span>' : ''
    const acts = readOnly ? '' : `<div class="row-acts"><button class="btn quiet sm" type="button" data-act="edit" data-idx="${i}" aria-label="Edit ${ui.esc(item.title)}">Edit</button>${rowMenuHTML(item.title, [{ label: 'Edit', act: 'edit' }, { label: 'Remove', act: 'remove', danger: true }])}</div>`
    return `<div class="r sched-row"${readOnly ? '' : ` data-idx="${i}"`}><span class="r-when"><b>${timeLabel(item.start_at)}</b>– ${endLabel(item.start_at, item.end_at)}</span>${readOnly ? `<span class="main"><b>${ui.esc(item.title)}</b>${item.description ? `<span>${ui.esc(item.description)}</span>` : '<span class="faint">No details</span>'}</span>` : `<a class="main" href="#" data-act="edit" data-idx="${i}"><b>${ui.esc(item.title)}</b>${item.description ? `<span>${ui.esc(item.description)}</span>` : '<span class="faint">No details</span>'}</a>`}${pill}${acts}</div>`
  }
  function listHTML() {
    let html = '', lastDay = null
    items.forEach((item, i) => {
      const day = dayHeader(ui, item.start_at, ev)
      if (day !== lastDay) { html += `<div class="lh">${ui.esc(day)}</div>`; lastDay = day }
      html += rowHTML(item, i)
    })
    return html
  }

  sp.innerHTML = `<div class="sp-head"><h2 id="sp-h">Schedule</h2><p>In ${ui.esc(ev.time_zone)}. Your page lists them in time order and marks Now and Next.</p>${readOnly || cache.contentError ? '' : '<button class="btn primary" type="button" id="sched-add"><svg class="ico" viewBox="0 0 20 20" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M10 4v12M4 10h12"/></svg>Add time</button>'}</div>
${ended && !admin ? '<div class="note">This event has ended. Its page can\'t change any more and stays up as a record.</div>' : ''}
${cache.contentError ? `<div class="note bad">Sponsors, FAQ and schedule could not be loaded. Reload to edit them.<button class="btn sm" type="button" id="sched-reload" style="margin-left:8px">Reload</button></div>` :
    items.length ? `<div class="list editable" aria-label="Schedule" id="sched-list">${listHTML()}</div>` :
    (readOnly ? '<p class="muted">Nothing scheduled.</p>' : `<div class="empty-state"><svg class="ico" viewBox="0 0 20 20" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M4 5h12a1 1 0 0 1 1 1v10a1 1 0 0 1-1 1H4a1 1 0 0 1-1-1V6a1 1 0 0 1 1-1zm-1 4h14M7 3v4m6-4v4"/></svg><h3>Nothing scheduled yet</h3><p>Add the kickoff, build time and demos. They show on your page, and participants see what is on now.</p><button class="btn primary" type="button" id="sched-add-empty"><svg class="ico" viewBox="0 0 20 20" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M10 4v12M4 10h12"/></svg>Add time</button></div>`)}`

  const reload = sp.querySelector('#sched-reload')
  if (reload) reload.addEventListener('click', async () => {
    reload.disabled = true
    try {
      const r = await api('GET', path + '/content')
      if (r.ok) { cache.content = { sponsors: r.data.sponsors || [], faq: r.data.faq || [], schedule: r.data.schedule || [] }; cache.contentError = false }
    } catch (e) {}
    renderSchedule({ ctx, sp, ui, api, ev, admin, locked, ended, path, setLive })
  })
  if (readOnly || cache.contentError) return

  async function openDialog(existing, idx) {
    const item = existing ? { ...existing } : { title: '', description: '', start_at: '', end_at: '' }
    const startVal = existing ? ui.localInputValue(existing.start_at, ev.time_zone) : ''
    const endVal = existing ? ui.localInputValue(existing.end_at, ev.time_zone) : ''
    const [sd, st] = startVal ? startVal.split('T') : ['', '']
    const [ed, et] = endVal ? endVal.split('T') : ['', '']
    const body = document.createElement('div')
    body.innerHTML = `<p class="muted" style="margin:0 0 12px">Times are in ${ui.esc(ev.time_zone)}.</p>
<div class="field"><label for="d-title">Title</label><input class="input" id="d-title" maxlength="120" value="${ui.esc(item.title)}" autofocus></div>
<div class="field"><div class="lab-row"><label for="d-desc">Details</label><span class="opt">Optional</span></div><textarea class="input" id="d-desc" rows="2" maxlength="500">${ui.esc(item.description || '')}</textarea></div>
<div class="field"><span class="lab" id="d-start-l">Starts</span><div class="dt-pair" role="group" aria-labelledby="d-start-l"><input class="input" type="date" aria-label="Start date" id="d-start-date" value="${ui.esc(sd)}"><input class="input" type="time" aria-label="Start time" id="d-start-time" value="${ui.esc(st)}"></div></div>
<div class="field" id="d-end-field"><span class="lab" id="d-end-l">Ends</span><div class="dt-pair" role="group" aria-labelledby="d-end-l"><input class="input" type="date" aria-label="End date" id="d-end-date" value="${ui.esc(ed)}"><input class="input" type="time" aria-label="End time" id="d-end-time" value="${ui.esc(et)}"></div></div>`
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
      const schedule = cache.content.schedule.slice()
      if (idx == null) schedule.push(next); else schedule[idx] = next
      try {
        const r = await api('PUT', path + '/content', { ...cache.content, schedule })
        if (!r.ok) { body.insertAdjacentHTML('beforeend', `<p class="err">${ui.esc(ui.errText(r))}</p>`); return false }
        cache.content = { sponsors: r.data.sponsors || [], faq: r.data.faq || [], schedule: r.data.schedule || [] }
        return true
      } catch (e) { body.insertAdjacentHTML('beforeend', '<p class="err">Cannot reach the server.</p>'); return false }
    })
    if (saved) renderSchedule({ ctx, sp, ui, api, ev, admin, locked, ended, path, setLive })
  }

  sp.querySelectorAll('[data-act="edit"]').forEach(el => el.addEventListener('click', e => { e.preventDefault(); openDialog(items[Number(el.dataset.idx)], Number(el.dataset.idx)) }))
  sp.querySelectorAll('[data-act="remove"]').forEach(el => el.addEventListener('click', async () => {
    const i = Number(el.closest('[data-idx]').dataset.idx)
    const removed = items[i]
    const schedule = cache.content.schedule.filter(x => x !== removed)
    try {
      const r = await api('PUT', path + '/content', { ...cache.content, schedule })
      if (!r.ok) { setLive(ui.errText(r), 'err'); return }
      const before = cache.content
      cache.content = { sponsors: r.data.sponsors || [], faq: r.data.faq || [], schedule: r.data.schedule || [] }
      renderSchedule({ ctx, sp, ui, api, ev, admin, locked, ended, path, setLive })
      undoToast(sp, `Removed “${removed.title}”`, async () => {
        try {
          const r2 = await api('PUT', path + '/content', before)
          if (r2.ok) cache.content = { sponsors: r2.data.sponsors || [], faq: r2.data.faq || [], schedule: r2.data.schedule || [] }
        } catch (e) {}
        renderSchedule({ ctx, sp, ui, api, ev, admin, locked, ended, path, setLive })
      })
    } catch (e) { setLive('Cannot reach the server.', 'err') }
  }))
  const add = sp.querySelector('#sched-add') || sp.querySelector('#sched-add-empty')
  if (add) add.addEventListener('click', () => openDialog(null, null))
}

// --------------------------------------------------------------- Sponsors
function renderSponsors({ ctx, sp, ui, api, ev, admin, locked, ended, path, setLive }) {
  const readOnly = locked
  const items = cache.content.sponsors
  function thumbHTML(s) {
    if (s.logo_data) return `<span class="thumb"><img src="${ui.esc(s.logo_data)}" alt=""></span>`
    return `<span class="thumb empty" role="img" aria-label="No logo" title="No logo"><svg viewBox="0 0 20 20" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M3 4h14v12H3zM3 13l4-4 3 3 2-2 5 5M13 8h.01"/></svg></span>`
  }
  function rowHTML(s, i) {
    const host = s.url ? (() => { try { return new URL(s.url).host } catch { return s.url } })() : 'No link'
    const sub = [s.tier, host].filter(Boolean).join(' · ') || 'No link'
    if (readOnly) return `<div class="r has-grip">${thumbHTML(s)}<span class="main"><b>${ui.esc(s.name)}</b><span>${ui.esc(sub)}</span></span></div>`
    const grip = `<button class="grip" type="button" aria-roledescription="drag handle" aria-label="Move ${ui.esc(s.name)}, position ${i + 1} of ${items.length}. Use the up and down arrow keys." draggable="true" data-act="grip" data-idx="${i}"><svg viewBox="0 0 12 16" aria-hidden="true" fill="currentColor"><circle cx="3" cy="3" r="1.4"/><circle cx="9" cy="3" r="1.4"/><circle cx="3" cy="8" r="1.4"/><circle cx="9" cy="8" r="1.4"/><circle cx="3" cy="13" r="1.4"/><circle cx="9" cy="13" r="1.4"/></svg></button>`
    const menu = rowMenuHTML(s.name, [{ label: 'Edit', act: 'edit' }, { label: 'Move up', act: 'up', disabled: i === 0 }, { label: 'Move down', act: 'down', disabled: i === items.length - 1 }, { label: 'Remove', act: 'remove', danger: true }])
    return `<div class="r has-grip" data-idx="${i}" draggable="true" data-act="dragrow">${grip}${thumbHTML(s)}<a class="main" href="#" data-act="edit" data-idx="${i}"><b>${ui.esc(s.name)}</b><span>${ui.esc(sub)}</span></a><div class="row-acts"><button class="btn quiet sm" type="button" data-act="edit" data-idx="${i}" aria-label="Edit ${ui.esc(s.name)}">Edit</button>${menu}</div></div>`
  }

  sp.innerHTML = `<div class="sp-head"><h2 id="sp-h">Sponsors</h2><p>Shown on your page in this order. Drag a row, or use its menu, to move it.</p>${readOnly || cache.contentError ? '' : '<button class="btn primary" type="button" id="sp-add"><svg class="ico" viewBox="0 0 20 20" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M10 4v12M4 10h12"/></svg>Add sponsor</button>'}</div>
${customSiteNote()}
${ended && !admin ? '<div class="note">This event has ended. You can still fix your public page.</div>' : ''}
${cache.contentError ? `<div class="note bad">Sponsors, FAQ and schedule could not be loaded. Reload to edit them.<button class="btn sm" type="button" id="sp-reload" style="margin-left:8px">Reload</button></div>` :
    items.length ? `<div class="list editable" aria-label="Sponsors" id="sp-list">${items.map(rowHTML).join('')}</div><p class="list-foot">${items.length} of 20 sponsors · Logos: PNG, JPEG or WebP, up to 128 KB</p>` :
    (readOnly ? '<p class="muted">No sponsors.</p>' : `<div class="empty-state"><h3>No sponsors yet</h3><p>Logos and names show near the bottom of your page.</p><button class="btn primary" type="button" id="sp-add-empty">Add sponsor</button></div>`)}`

  const reload = sp.querySelector('#sp-reload')
  if (reload) reload.addEventListener('click', async () => {
    reload.disabled = true
    try { const r = await api('GET', path + '/content'); if (r.ok) { cache.content = { sponsors: r.data.sponsors || [], faq: r.data.faq || [], schedule: r.data.schedule || [] }; cache.contentError = false } } catch (e) {}
    renderSponsors({ ctx, sp, ui, api, ev, admin, locked, ended, path, setLive })
  })
  if (readOnly || cache.contentError) return

  async function persist(sponsors, successMsg) {
    try {
      const r = await api('PUT', path + '/content', { ...cache.content, sponsors })
      if (!r.ok) { setLive(ui.errText(r), 'err'); return false }
      cache.content = { sponsors: r.data.sponsors || [], faq: r.data.faq || [], schedule: r.data.schedule || [] }
      return true
    } catch (e) { setLive('Cannot reach the server.', 'err'); return false }
  }
  async function move(i, dir) {
    const sponsors = cache.content.sponsors.slice()
    moveItem(sponsors, i, dir)
    if (await persist(sponsors)) renderSponsors({ ctx, sp, ui, api, ev, admin, locked, ended, path, setLive })
  }

  async function openDialog(existing, idx) {
    const s = existing ? { ...existing } : { name: '', tier: '', url: '', logo_data: '' }
    let logo = s.logo_data || ''
    const body = document.createElement('div')
    function pv() { return logo ? `<img src="${ui.esc(logo)}" alt="">` : '<svg viewBox="0 0 20 20" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M3 4h14v12H3zM3 13l4-4 3 3 2-2 5 5M13 8h.01"/></svg>' }
    body.innerHTML = `<div class="field"><span class="lab">Logo</span><div class="file-pick"><span class="pv" id="d-logo-pv">${pv()}</span><div class="t"><b id="d-logo-name">${logo ? 'Current logo' : 'No logo'}</b></div><div class="pick-acts"><label class="btn sm">${logo ? 'Replace' : 'Choose logo'}<input type="file" accept="image/png,image/jpeg,image/webp" id="d-logo-file" aria-label="${logo ? 'Replace' : 'Choose'} logo"></label><button class="btn quiet sm" type="button" id="d-logo-rm"${logo ? '' : ' disabled'}>Remove</button></div></div><p class="help">PNG, JPEG or WebP, up to 128 KB. Square logos look best.</p></div>
<div class="field"><label for="d-name">Name</label><input class="input" id="d-name" maxlength="100" value="${ui.esc(s.name)}"></div>
<div class="field"><div class="lab-row"><label for="d-tier">Tier</label><span class="opt">Optional</span></div><input class="input" id="d-tier" maxlength="60" value="${ui.esc(s.tier || '')}"><p class="help">For example Gold, Silver or Community partner.</p></div>
<div class="field" id="d-link-field"><div class="lab-row"><label for="d-link">Link</label><span class="opt">Optional</span></div><input class="input" id="d-link" value="${ui.esc(s.url || '')}"></div>`
    const q = id => body.querySelector('#' + id)
    q('d-logo-file').addEventListener('change', async () => {
      const file = q('d-logo-file').files && q('d-logo-file').files[0]
      if (!file) return
      if (file.size > 128 * 1024) { setLive('Logo must be under 128 KB.', 'err'); q('d-logo-file').value = ''; return }
      try { logo = await fileAsDataURL(file) } catch (e) { setLive('Could not read that file.', 'err'); return }
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
      if (idx == null) sponsors.push(next); else sponsors[idx] = next
      return persist(sponsors)
    })
    if (saved) renderSponsors({ ctx, sp, ui, api, ev, admin, locked, ended, path, setLive })
  }

  sp.querySelectorAll('[data-act="edit"]').forEach(el => el.addEventListener('click', e => { e.preventDefault(); openDialog(items[Number(el.dataset.idx)], Number(el.dataset.idx)) }))
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
      undoToast(sp, `Removed ${removed.name}`, async () => { await persist(before.sponsors); renderSponsors({ ctx, sp, ui, api, ev, admin, locked, ended, path, setLive }) })
    }
  }))
  // Drag reorder (desktop): native HTML5 DnD on each row, by the grip's own
  // handle per spec, but the whole row is the drag source (the grip alone
  // can't carry a dragstart listener and still be a focusable button in
  // every browser) -- dropping reorders the same way Move up/down does.
  let dragFrom = null
  sp.querySelectorAll('[data-act="dragrow"]').forEach(row => {
    row.addEventListener('dragstart', e => { dragFrom = Number(row.dataset.idx); e.dataTransfer.effectAllowed = 'move' })
    row.addEventListener('dragover', e => { e.preventDefault() })
    row.addEventListener('drop', async e => {
      e.preventDefault()
      const to = Number(row.dataset.idx)
      if (dragFrom == null || dragFrom === to) return
      const sponsors = cache.content.sponsors.slice()
      const [moved] = sponsors.splice(dragFrom, 1)
      sponsors.splice(to, 0, moved)
      if (await persist(sponsors)) renderSponsors({ ctx, sp, ui, api, ev, admin, locked, ended, path, setLive })
    })
  })
  const add = sp.querySelector('#sp-add') || sp.querySelector('#sp-add-empty')
  if (add) add.addEventListener('click', () => openDialog(null, null))
}

// -------------------------------------------------------------------- FAQ
function renderFaq({ ctx, sp, ui, api, ev, admin, locked, ended, path, setLive }) {
  const readOnly = locked
  const items = cache.content.faq
  function rowHTML(f, i) {
    if (readOnly) return `<div class="r has-grip"><span class="main"><b>${ui.esc(f.question)}</b><span class="clamp2">${ui.esc(f.answer)}</span></span></div>`
    const grip = `<button class="grip" type="button" aria-roledescription="drag handle" aria-label="Move ${ui.esc(f.question)}, position ${i + 1} of ${items.length}. Use the up and down arrow keys." data-act="grip" data-idx="${i}"><svg viewBox="0 0 12 16" aria-hidden="true" fill="currentColor"><circle cx="3" cy="3" r="1.4"/><circle cx="9" cy="3" r="1.4"/><circle cx="3" cy="8" r="1.4"/><circle cx="9" cy="8" r="1.4"/><circle cx="3" cy="13" r="1.4"/><circle cx="9" cy="13" r="1.4"/></svg></button>`
    const menu = rowMenuHTML(f.question, [{ label: 'Edit', act: 'edit' }, { label: 'Move up', act: 'up', disabled: i === 0 }, { label: 'Move down', act: 'down', disabled: i === items.length - 1 }, { label: 'Remove', act: 'remove', danger: true }])
    return `<div class="r has-grip" data-idx="${i}" draggable="true" data-act="dragrow">${grip}<a class="main" href="#" data-act="edit" data-idx="${i}"><b>${ui.esc(f.question)}</b><span class="clamp2">${ui.esc(f.answer)}</span></a><div class="row-acts"><button class="btn quiet sm" type="button" data-act="edit" data-idx="${i}" aria-label="Edit ${ui.esc(f.question)}">Edit</button>${menu}</div></div>`
  }
  sp.innerHTML = `<div class="sp-head"><h2 id="sp-h">FAQ</h2><p>Questions and answers, in this order. Answers start folded on your page.</p>${readOnly || cache.contentError ? '' : '<button class="btn primary" type="button" id="fq-add"><svg class="ico" viewBox="0 0 20 20" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M10 4v12M4 10h12"/></svg>Add question</button>'}</div>
${customSiteNote()}
${ended && !admin ? '<div class="note">This event has ended. You can still fix your public page.</div>' : ''}
${cache.contentError ? `<div class="note bad">Sponsors, FAQ and schedule could not be loaded. Reload to edit them.<button class="btn sm" type="button" id="fq-reload" style="margin-left:8px">Reload</button></div>` :
    items.length ? `<div class="list editable" aria-label="Questions" id="fq-list">${items.map(rowHTML).join('')}</div><p class="list-foot">${items.length} of 30 questions</p>` :
    (readOnly ? '<p class="muted">No questions.</p>' : `<div class="empty-state"><h3>No questions yet</h3><p>Answer what people ask most: who can join, where it happens, what to hand in.</p><button class="btn primary" type="button" id="fq-add-empty">Add question</button></div>`)}`

  const reload = sp.querySelector('#fq-reload')
  if (reload) reload.addEventListener('click', async () => {
    reload.disabled = true
    try { const r = await api('GET', path + '/content'); if (r.ok) { cache.content = { sponsors: r.data.sponsors || [], faq: r.data.faq || [], schedule: r.data.schedule || [] }; cache.contentError = false } } catch (e) {}
    renderFaq({ ctx, sp, ui, api, ev, admin, locked, ended, path, setLive })
  })
  if (readOnly || cache.contentError) return

  async function persist(faq) {
    try {
      const r = await api('PUT', path + '/content', { ...cache.content, faq })
      if (!r.ok) { setLive(ui.errText(r), 'err'); return false }
      cache.content = { sponsors: r.data.sponsors || [], faq: r.data.faq || [], schedule: r.data.schedule || [] }
      return true
    } catch (e) { setLive('Cannot reach the server.', 'err'); return false }
  }
  async function move(i, dir) {
    const faq = cache.content.faq.slice()
    moveItem(faq, i, dir)
    if (await persist(faq)) renderFaq({ ctx, sp, ui, api, ev, admin, locked, ended, path, setLive })
  }
  async function openDialog(existing, idx) {
    const f = existing ? { ...existing } : { question: '', answer: '' }
    const body = document.createElement('div')
    body.innerHTML = `<div class="field"><label for="d-q">Question</label><input class="input" id="d-q" maxlength="200" value="${ui.esc(f.question)}" autofocus></div>
<div class="field"><label for="d-a">Answer</label><textarea class="input" id="d-a" rows="4" maxlength="2000">${ui.esc(f.answer)}</textarea></div>`
    const q = id => body.querySelector('#' + id)
    const saved = await formDialog(ui, { title: existing ? 'Edit question' : 'Add question', body, saveLabel: existing ? 'Save' : 'Add question' }, async () => {
      body.querySelectorAll('.err').forEach(n => n.remove())
      const question = q('d-q').value.trim(), answer = q('d-a').value.trim()
      if (!question) { q('d-q').insertAdjacentHTML('afterend', '<p class="err">Question is required.</p>'); return false }
      if (!answer) { q('d-a').insertAdjacentHTML('afterend', '<p class="err">Answer is required.</p>'); return false }
      const next = { question, answer }
      const faq = cache.content.faq.slice()
      if (idx == null) faq.push(next); else faq[idx] = next
      return persist(faq)
    })
    if (saved) renderFaq({ ctx, sp, ui, api, ev, admin, locked, ended, path, setLive })
  }
  sp.querySelectorAll('[data-act="edit"]').forEach(el => el.addEventListener('click', e => { e.preventDefault(); openDialog(items[Number(el.dataset.idx)], Number(el.dataset.idx)) }))
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
      undoToast(sp, `Removed “${removed.question}”`, async () => { await persist(before.faq); renderFaq({ ctx, sp, ui, api, ev, admin, locked, ended, path, setLive }) })
    }
  }))
  let dragFrom = null
  sp.querySelectorAll('[data-act="dragrow"]').forEach(row => {
    row.addEventListener('dragstart', () => { dragFrom = Number(row.dataset.idx) })
    row.addEventListener('dragover', e => e.preventDefault())
    row.addEventListener('drop', async e => {
      e.preventDefault()
      const to = Number(row.dataset.idx)
      if (dragFrom == null || dragFrom === to) return
      const faq = cache.content.faq.slice()
      const [moved] = faq.splice(dragFrom, 1)
      faq.splice(to, 0, moved)
      if (await persist(faq)) renderFaq({ ctx, sp, ui, api, ev, admin, locked, ended, path, setLive })
    })
  })
  const add = sp.querySelector('#fq-add') || sp.querySelector('#fq-add-empty')
  if (add) add.addEventListener('click', () => openDialog(null, null))
}

// ------------------------------------------------------------------- Look
function renderLook({ sp, ui, api, ctx, ev, admin, locked, ended, path, setLive }) {
  const $ = id => sp.querySelector('#' + id)
  const previewColor = ev.accent_color ? (SWATCH_HEX[ev.accent_color] || ev.color) : (ev.color || '#267b79')
  sp.innerHTML = `<div class="sp-head"><h2 id="sp-h">Look</h2><p>Your icon, colour and whether entries show. Each change saves straight away.</p></div>
${ended && !admin ? '<div class="note">This event has ended. You can still fix your public page.</div>' : ''}
<div class="card"><p class="section-title" style="margin-top:0">Preview</p>
<div class="pub-event preview-head" id="look-preview" style="--ev:${previewColor}"><span class="ev-icon">${ev.icon_media_type ? `<img src="${ui.esc(ev.icon_url)}" alt="" width="52" height="52" style="border-radius:50%">` : ''}</span><div><h3>${ui.esc(ev.title)}</h3><p>${ui.esc(ev.tagline || '')}</p></div></div></div>
<div class="card flat">
<div class="setting wraps"><div class="t"><b>Event icon</b><span>Shown round on your page, in Find a hackathon and on join links. PNG, JPEG or WebP, up to 128 KB.</span></div>
<div class="ctl"><span class="file-pick"><span class="pv round" style="width:44px;height:44px">${ev.icon_media_type ? `<img src="${ui.esc(ev.icon_url)}" alt="">` : ''}</span></span><label class="btn sm">${ev.icon_media_type ? 'Replace' : 'Upload icon'}<input type="file" accept="image/png,image/jpeg,image/webp" id="icon-file" aria-label="${ev.icon_media_type ? 'Replace' : 'Upload'} event icon"></label><button class="btn quiet sm" type="button" id="icon-remove"${ev.icon_media_type ? '' : ' disabled'}>Remove</button></div></div>
<div class="setting stack"><div class="t"><div class="t-row"><b>Colour</b><span class="saved" id="colour-saved" hidden><svg class="ico" viewBox="0 0 20 20" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M4 10.5l4 4 8-9"/></svg>Saved</span></div><span>For the line beside your title and for links and buttons. Automatic picks one from your address.</span></div>
<fieldset class="swatches" aria-label="Event colour">${CRAYONS.map(([v, l]) => {
    const hex = v ? SWATCH_HEX[v] : (ev.color || '#d15d48')
    const ink = ui.markInk(hex)
    return `<label class="swatch${v ? '' : ' auto'}"><input type="radio" name="colour" value="${v}" data-c="${hex}"${(ev.accent_color || '') === v ? ' checked' : ''}><i style="--sw:${hex};--sw-ink:${ink}"><svg class="ico" viewBox="0 0 20 20" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M4 10.5l4 4 8-9"/></svg></i><span>${ui.esc(l)}</span></label>`
  }).join('')}</fieldset></div>
<div class="setting"><div class="t"><div class="t-row"><b>Show entries on the event page</b><span class="saved" id="gallery-saved" hidden><svg class="ico" viewBox="0 0 20 20" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M4 10.5l4 4 8-9"/></svg>Saved</span></div><span>Lists every team whose website is live. People's choice voting needs this on.</span></div><div class="ctl"><button class="toggle${ev.gallery_open ? ' on' : ''}" type="button" role="switch" aria-checked="${!!ev.gallery_open}" id="gallery-toggle" aria-label="Show entries on the event page"></button></div></div>
</div>`

  function flashSaved(id) {
    const el = $(id)
    el.hidden = false
    clearTimeout(el._t)
    el._t = setTimeout(() => { el.hidden = true }, 3000)
  }
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
  sp.querySelectorAll('input[name=colour]').forEach(radio => radio.addEventListener('change', async () => {
    const prev = ev.accent_color || ''
    sp.querySelectorAll('input[name=colour]').forEach(r => r.disabled = true)
    $('look-preview').style.setProperty('--ev', radio.dataset.c)
    try {
      const r = await api('PATCH', path, { accent_color: radio.value })
      if (!r.ok) { setLive(ui.errText(r), 'err'); sp.querySelector(`input[name=colour][value="${prev}"]`).checked = true; sp.querySelectorAll('input[name=colour]').forEach(x => x.disabled = false); return }
      ev.accent_color = radio.value
      flashSaved('colour-saved')
      sp.querySelectorAll('input[name=colour]').forEach(x => x.disabled = false)
    } catch (e) { setLive('Cannot reach the server.', 'err'); sp.querySelectorAll('input[name=colour]').forEach(x => x.disabled = false) }
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
  const w = cache.website
  const customLive = w.mode === 'custom'
  const hasUploads = (w.versions || []).length > 0
  sp.innerHTML = `<div class="sp-head"><h2 id="sp-h">Website</h2><p>What visitors get at your address.</p></div>
${ended && !admin ? '<div class="note">This event has ended. You can still fix your public page.</div>' : ''}
${customLive ? `<div class="note info"><b>Your own website is live at your address.</b> Basics, About, Schedule, Sponsors and FAQ still save, and show again if you switch back to the built-in page.</div>` : ''}
<div class="card">
<span class="lab" style="display:block;font-weight:600;font-size:13.5px;margin:0 0 6px">Your address</span>
<div class="addr"><span class="mono">${ui.esc(ev.url.replace(/^https?:\/\//, ''))}</span><button class="btn sm" type="button" id="addr-copy">Copy</button><a class="btn sm" href="${ui.esc(ev.url)}" target="_blank" rel="noopener">Open</a></div>
<div class="field-group"><div class="t-row" style="margin:0 0 12px"><h3 style="margin:0">What shows there</h3><span class="saved" id="mode-saved" hidden><svg class="ico" viewBox="0 0 20 20" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M4 10.5l4 4 8-9"/></svg>Saved</span></div>
<div class="choices" role="radiogroup" aria-label="What shows at your address"><label class="choice${!customLive ? ' on' : ''}"><input type="radio" name="mode" value="builtin"${!customLive ? ' checked' : ''}><span><b>Built-in event page</b><span class="d">Made from Basics, About, Schedule, Sponsors and FAQ. It updates as you edit.</span></span></label><label class="choice${customLive ? ' on' : hasUploads ? '' : ' off'}"><input type="radio" name="mode" value="custom"${customLive ? ' checked' : ''}${hasUploads ? '' : ' disabled'}><span><b>Your own website</b><span class="d">${hasUploads ? `Version ${w.active_version || (w.versions[w.versions.length - 1] || {}).version_number}, uploaded ${ui.esc(ui.dateOnly((w.versions[w.versions.length - 1] || {}).created_at, ev))}. You can switch back any time. Nothing is deleted.` : 'Upload a zip first'}</span></span></label></div></div>
</div>
<div class="card"><h2>Your own website</h2><p class="sub">A .zip with index.html at the top, up to 25 MB. ${!hasUploads ? 'Your first upload goes live at your address straight away.' : customLive ? 'Each upload goes live straight away.' : 'Each upload becomes a new version, ready to switch to.'}${w.upload_limits ? ' ' + ui.esc(w.upload_limits) : ''}</p>
<div class="drop" id="drop-zone"><svg class="ico" viewBox="0 0 20 20" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M10 13V4M6 8l4-4 4 4M4 14v2h12v-2"/></svg><b>Drop a .zip here</b><span class="small">or</span><label class="btn primary">Choose zip<input type="file" accept=".zip,application/zip" id="site-zip" aria-label="Choose a zip to upload"></label></div>
<p class="section-title">Uploads</p>
<div class="list" id="site-versions" style="margin:0">${(w.versions || []).slice().reverse().map(v => `<div class="r"><div class="main"><b>Version ${v.version_number}</b><span>${ui.esc(ui.dateTime(v.created_at, ev))}</span></div>${customLive && v.version_number === w.active_version ? '<span class="pill ok">Live</span>' : !customLive && v === (w.versions || [])[w.versions.length - 1] ? '<span class="pill">Newest</span>' : ''}</div>`).join('') || '<div class="empty">No uploads yet.</div>'}</div>
<p class="help small muted" style="margin:12px 0 0">An AI app can publish versions here too, and manage your website's own saved data (keys, tables and files).</p>
</div>`

  sp.querySelector('#addr-copy').addEventListener('click', async () => {
    try { await navigator.clipboard.writeText(ev.url); setLive('Copied', 'ok') } catch (e) { setLive('Could not copy. Select the text and copy it.', 'err') }
  })
  if (locked) return

  async function doUpload(file) {
    if (!file) return
    let buf
    try { buf = await fileAsArrayBuffer(file) } catch (e) { setLive('Could not read that file.', 'err'); return }
    try {
      // ?create=1: the server's underlying site record for this event's
      // website is created lazily on the first upload, same as a team
      // site (internal/handler/hack_uploads_test.go's own upload() helper
      // always appends it); the old inline file input never did, so a
      // brand-new event's very first website upload 404'd.
      const r = await apiBytes('PUT', path + '/website?create=1', buf, 'application/zip')
      if (!r.ok) { setLive(archiveErrorText(ui, r), 'err'); return }
      const got = await api('GET', path + '/website')
      if (got.ok) cache.website = got.data
      const latest = (cache.website.versions || []).slice(-1)[0]
      setLive(latest ? `Version ${latest.version_number} is up` : 'Website published', 'ok')
      renderWebsite({ ctx, sp, ui, api, apiBytes, ev, admin, locked, ended, path, setLive })
    } catch (e) { setLive('Cannot reach the server.', 'err') }
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
      if (!r.ok) { setLive(ui.errText(r), 'err'); renderWebsite({ ctx, sp, ui, api, apiBytes, ev, admin, locked, ended, path, setLive }); return }
      cache.website = r.data
      renderWebsite({ ctx, sp, ui, api, apiBytes, ev, admin, locked, ended, path, setLive })
      const tick = sp.querySelector('#mode-saved'); if (tick) { tick.hidden = false; setTimeout(() => { tick.hidden = true }, 3000) }
    } catch (e) { setLive('Cannot reach the server.', 'err'); renderWebsite({ ctx, sp, ui, api, apiBytes, ev, admin, locked, ended, path, setLive }) }
  }))
}
