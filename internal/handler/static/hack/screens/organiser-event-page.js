import { fileAsDataURL, fileAsArrayBuffer, moveItem, adminLock } from './_orgA-util.js'

export function title(ctx) { return 'Event page' }

const CRAYONS = [['', 'Automatic'], ['red', 'Red'], ['teal', 'Teal'], ['indigo', 'Indigo'], ['orange', 'Orange'], ['yellow', 'Yellow'], ['grass', 'Grass'], ['blush', 'Blush']]

// B2 fix: a schedule row's start_at/end_at is either a real ISO instant from
// the server (show it via ui.localInputValue, converting from UTC into the
// event's zone) or the raw value of a datetime-local field the organiser
// has touched since the last load/save (item._local, set by collectSchedule
// below) -- that raw string is *already* the event-zone wall clock the
// server will read back on save, exactly as typed, with no conversion. The
// old code always ran it through ui.localInputValue, which re-parsed that
// naive string as browser-local time and reformatted it in the event's
// zone -- a no-op only when the browser and event zones match, and a shift
// by their offset difference otherwise (Grok SIM-346 Broken #2: a Los
// Angeles browser editing a Tokyo event saw rows jump +17h, on every
// Add/Move/Remove before Save). Exported (pure, no DOM) so a plain node
// test can exercise the exact branch -- see
// polish-audit/b2-schedule-tz.test.mjs.
export function scheduleFieldValue(ui, ev, item, field) {
  return item._local ? (item[field] || '') : ui.localInputValue(item[field], ev.time_zone)
}

// #16: this screen's own website-zip upload showed the server's raw
// extractor error ("invalid site archive: create gzip reader: gzip:
// invalid header") instead of plain English, unlike participant-website.js
// (SIM-346 C10), which already translates invalid_archive. Same mapping,
// duplicated locally for now rather than reaching into another role's
// screen module -- round2-shared.md may later give this a shared home.
function archiveErrorText(ui, r, fallback) {
  const code = r && r.data && r.data.code
  if (code === 'invalid_archive') {
    const raw = String((r && r.data && r.data.error) || '')
    if (/unsafe path|duplicate (entry|path)/i.test(raw)) {
      return "That zip has file paths Simple Host can't accept (a path outside the archive, or two files at the same path). Fix the archive and try again."
    }
    return "That file isn't a valid .zip."
  }
  return ui.errText(r, { fallback })
}

export async function render(ctx) {
  const { main, api: client, ui, event: data } = ctx
  const api = client.api
  const apiBytes = client.apiBytes
  const ev = data.event
  const admin = !!data.admin_view
  const path = '/v1/hack/events/' + encodeURIComponent(ev.slug)
  const setLive = ui.live(main)

  // Local, mutable state for the three list-shaped sections and the website
  // mode card. All three content arrays save together (one PUT /content), so
  // they live in one object that every row editor mutates in place.
  let content = { sponsors: [], faq: [], schedule: [] }
  let contentError = false // true when /content failed to load: sponsors/FAQ/schedule saves are refused below so an empty default never replaces what the server actually holds
  let announcements = []
  let website = { mode: ev.website_mode, has_custom_page: false, versions: [], upload_limits: '' }
  try {
    const [c, a, w] = await Promise.all([api('GET', path + '/content'), api('GET', path + '/announcements'), api('GET', path + '/website')])
    if (c.ok) content = { sponsors: c.data.sponsors || [], faq: c.data.faq || [], schedule: c.data.schedule || [] }
    else contentError = true
    if (a.ok) announcements = a.data.announcements || []
    if (w.ok) website = w.data
  } catch (e) { contentError = true }

  // Every section below shares one PUT /content, so a failed initial read must
  // never be papered over with an empty array -- that empty object would then
  // replace whatever sponsors/FAQ/schedule the server actually has. Saves are
  // refused (and the reason shown) until a reload gets a real read.
  function guardContent(btn) {
    if (!contentError) return false
    setLive("Could not load this event's page content earlier. Reload the page before saving here.", 'err', btn)
    return true
  }

  function scheduleRow(item, i) {
    // start_at/end_at: see scheduleFieldValue at the top of this module for
    // why this isn't a plain ui.localInputValue call any more (B2).
    return `<div class="field"><label>Session<input class="input" data-sched-title="${i}" type="text" maxlength="120" value="${ui.esc(item.title || '')}"></label></div>
<div class="field"><label>Details<textarea class="input" data-sched-desc="${i}" maxlength="500">${ui.esc(item.description || '')}</textarea></label></div>
<div class="grid2"><div class="field"><label>Starts<input class="input" data-sched-start="${i}" type="datetime-local" value="${ui.esc(scheduleFieldValue(ui, ev, item, 'start_at'))}"></label></div><div class="field"><label>Ends<input class="input" data-sched-end="${i}" type="datetime-local" value="${ui.esc(scheduleFieldValue(ui, ev, item, 'end_at'))}"></label></div></div>
<div class="row-flex"><button class="btn sm" type="button" data-sched-up="${i}">Move up</button><button class="btn sm" type="button" data-sched-down="${i}">Move down</button><button class="btn danger sm" type="button" data-sched-rm="${i}">Remove</button></div>`
  }
  function sponsorRow(s, i) {
    return `<div class="field"><label>Logo &middot; PNG, JPEG or WebP, up to 128 KB<input class="input" type="file" accept="image/png,image/jpeg,image/webp" data-sp-logo="${i}"></label>${s.logo_data ? `<img src="${ui.esc(s.logo_data)}" alt="" width="40" height="40">` : ''}</div>
<div class="field"><label>Name<input class="input" data-sp-name="${i}" type="text" maxlength="100" value="${ui.esc(s.name || '')}"></label></div>
<div class="field"><label>Tier<input class="input" data-sp-tier="${i}" type="text" maxlength="60" value="${ui.esc(s.tier || '')}"></label></div>
<div class="field"><label>Link (optional)<input class="input" data-sp-url="${i}" type="text" maxlength="500" value="${ui.esc(s.url || '')}"></label></div>
<div class="row-flex"><button class="btn sm" type="button" data-sp-up="${i}">Move up</button><button class="btn sm" type="button" data-sp-down="${i}">Move down</button><button class="btn danger sm" type="button" data-sp-rm="${i}">Remove</button></div>`
  }
  function faqRow(f, i) {
    return `<div class="field"><label>Question<input class="input" data-fq-q="${i}" type="text" maxlength="200" value="${ui.esc(f.question || '')}"></label></div>
<div class="field"><label>Answer<textarea class="input" data-fq-a="${i}" maxlength="2000">${ui.esc(f.answer || '')}</textarea></label></div>
<div class="row-flex"><button class="btn sm" type="button" data-fq-up="${i}">Move up</button><button class="btn sm" type="button" data-fq-down="${i}">Move down</button><button class="btn danger sm" type="button" data-fq-rm="${i}">Remove</button></div>`
  }
  function announcementRow(a) {
    return `<div class="card" data-ann="${ui.esc(a.id)}"><b>${ui.esc(a.title)} &middot; ${ui.esc(ui.dateTime(a.created_at, ev))}</b><p data-ann-body>${ui.esc(a.body)}</p>
<div class="row-flex" data-ann-view><button class="btn" type="button" data-ann-edit>Edit</button><button class="btn danger sm" type="button" data-ann-del title="Removes it from the page and everyone's feed. It already stays sent if you emailed it.">Delete</button></div>
<form data-ann-edit-form hidden><div class="field"><label>Title<input class="input" data-ann-edit-title type="text" maxlength="120" value="${ui.esc(a.title)}"></label></div><div class="field"><label>Message<textarea class="input" data-ann-edit-body maxlength="3000">${ui.esc(a.body)}</textarea></label></div><button class="btn" type="submit">Save</button> <button class="btn" type="button" data-ann-edit-cancel>Cancel</button></form></div>`
  }

  // Every row-level op (add/move/remove/logo pick) repaints the whole section,
  // which would otherwise throw away whatever the organiser has typed into
  // the still-open rows since the last paint. Each collector pulls current
  // input values back into `content` first; call it before any mutate+paint.
  function collectSchedule() {
    content.schedule.forEach((item, i) => {
      const t = main.querySelector(`[data-sched-title="${i}"]`)
      if (!t) return
      item.title = t.value
      item.description = main.querySelector(`[data-sched-desc="${i}"]`).value
      item.start_at = main.querySelector(`[data-sched-start="${i}"]`).value
      item.end_at = main.querySelector(`[data-sched-end="${i}"]`).value
      // B2: these are now raw datetime-local field values, in the event's
      // own zone already (not a UTC instant) -- scheduleFieldValue must not
      // run them through ui.localInputValue again on the next paint.
      item._local = true
    })
  }
  function collectSponsors() {
    content.sponsors.forEach((s, i) => {
      const n = main.querySelector(`[data-sp-name="${i}"]`)
      if (!n) return
      s.name = n.value
      s.tier = main.querySelector(`[data-sp-tier="${i}"]`).value
      s.url = main.querySelector(`[data-sp-url="${i}"]`).value
    })
  }
  function collectFaq() {
    content.faq.forEach((f, i) => {
      const q = main.querySelector(`[data-fq-q="${i}"]`)
      if (!q) return
      f.question = q.value
      f.answer = main.querySelector(`[data-fq-a="${i}"]`).value
    })
  }

  // Repeating rows (schedule, sponsors, FAQ) are separated by kit .item.
  function itemWrap(html) {
    return `<div class="item">${html}</div>`
  }

  function paint() {
    const $ = id => main.querySelector('#' + id)
    const customConnected = website.mode === 'custom' && website.has_custom_page

    main.innerHTML = `
<div class="ph"><div class="t"><h1>Event page</h1><p>What visitors see before they sign up.</p></div><a class="btn" href="${ui.esc(ev.url)}" target="_blank" rel="noopener">View public page</a></div>
<div class="toc-split">
<nav class="toc" aria-label="Jump to section"><a href="#basics">Basics</a><a href="#schedule">Schedule</a><a href="#announcements">Announcements</a><a href="#sponsors">Sponsors</a><a href="#faq">FAQ</a><a href="#appearance">Look</a><a href="#address">Public address</a></nav>
<div style="flex:1;min-width:0">
${customConnected ? `<div class="note info">Your custom website is live. Basics, Schedule, Sponsors and FAQ only show once you switch back to the built-in page.</div>` : ''}
<section class="card" id="basics"${customConnected ? ' hidden' : ''}><h2>Basics</h2>
<div class="field"><label>Title<input class="input" id="pg-title" type="text" maxlength="120" value="${ui.esc(ev.title)}"></label></div>
<div class="field"><label>Tagline<input class="input" id="pg-tagline" type="text" maxlength="160" value="${ui.esc(ev.tagline || '')}"></label></div>
<div class="field"><label>About<textarea class="input" id="pg-about" maxlength="10000">${ui.esc(ev.about || '')}</textarea></label></div>
<div class="field"><label>Rules<textarea class="input" id="pg-rules" maxlength="10000">${ui.esc(ev.rules || '')}</textarea></label></div>
<div class="field"><label>Prizes<textarea class="input" id="pg-prizes" maxlength="5000">${ui.esc(ev.prizes || '')}</textarea></label></div>
<div class="field"><label>Extra code of conduct (optional)<textarea class="input" id="pg-coc" maxlength="10000">${ui.esc(ev.coc_text || '')}</textarea><p class="help">Added after the standard code: “${ui.esc((ev.coc_default || '').slice(0, 160))}${(ev.coc_default || '').length > 160 ? '…' : ''}”</p></label></div>
<div class="field"><label>Starts<input class="input" id="pg-start" type="date" value="${ui.esc(ui.localInputValue(ev.starts_at, ev.time_zone, { dateOnly: true }))}"></label></div>
<div class="field"><label>Ends<input class="input" id="pg-end" type="date" value="${ui.esc(ui.localInputValue(ev.ends_at, ev.time_zone, { dateOnly: true }))}"></label></div>
<div class="field"><label>Time zone<input class="input" id="pg-tz" type="text" value="${ui.esc(ev.time_zone)}"></label><p class="help">For example Asia/Jakarta or Europe/London.</p></div>
<button class="btn primary" type="button" id="pg-save">Save basics</button></section>

${contentError ? '<div class="note bad">Sponsors, FAQ and schedule could not be loaded. Saving in those sections is turned off until you reload the page, so nothing already on the public page gets overwritten with an empty list.</div>' : ''}
<section class="card" id="schedule"${customConnected ? ' hidden' : ''}><h2>Schedule</h2><p class="sub">Times are in ${ui.esc(ev.time_zone)}.</p><div id="sched-rows">${content.schedule.map((s, i) => itemWrap(scheduleRow(s, i), i)).join('')}</div><div class="row-flex"><button class="btn" type="button" id="sched-add"${contentError ? ' disabled' : ''}>Add time</button><button class="btn" type="button" id="sched-save"${contentError ? ' disabled' : ''}>Save schedule</button></div></section>

<section class="card" id="announcements"><h2>Announcements</h2><div id="ann-list">${announcements.map(announcementRow).join('') || '<p class="muted">No announcements yet.</p>'}</div>
<form id="ann-form"><div class="field"><label>Announcement title<input class="input" id="ann-title" type="text" maxlength="120"></label></div><div class="field"><label>Message<textarea class="input" id="ann-body" maxlength="3000"></textarea></label></div><label class="check"><input type="checkbox" id="ann-email"><span>Email every participant too</span></label><button class="btn" type="submit">Post announcement</button></form></section>

<section class="card" id="sponsors"${customConnected ? ' hidden' : ''}><h2>Sponsors</h2><div id="sp-rows">${content.sponsors.map((s, i) => itemWrap(sponsorRow(s, i), i)).join('')}</div><div class="row-flex"><button class="btn" type="button" id="sp-add"${contentError ? ' disabled' : ''}>Add sponsor</button><button class="btn" type="button" id="sp-save"${contentError ? ' disabled' : ''}>Save sponsors</button></div></section>

<section class="card" id="faq"${customConnected ? ' hidden' : ''}><h2>FAQ</h2><p class="sub">Answers are collapsed by default on the public page.</p><div id="fq-rows">${content.faq.map((f, i) => itemWrap(faqRow(f, i), i)).join('')}</div><div class="row-flex"><button class="btn" type="button" id="fq-add"${contentError ? ' disabled' : ''}>Add question</button><button class="btn" type="button" id="fq-save"${contentError ? ' disabled' : ''}>Save FAQ</button></div></section>

<section class="card" id="appearance"><h2>Look</h2>
<div class="field"><label>Event icon (PNG, JPEG or WebP, up to 128 KB)<input class="input" type="file" id="icon-file" accept="image/png,image/jpeg,image/webp"></label></div>
<div class="row-flex">${ev.icon_media_type ? `<img src="${ui.esc(ev.icon_url)}" alt="" width="40" height="40">` : ''}<button class="btn" type="button" id="icon-remove"${ev.icon_media_type ? '' : ' disabled'}>Remove icon</button></div>
<div class="field"><label>Event colour<select class="select" id="pg-color">${CRAYONS.map(([v, l]) => `<option value="${v}"${ev.accent_color === v ? ' selected' : ''}>${ui.esc(l)}</option>`).join('')}</select></label></div>
<label class="check"><input type="checkbox" id="pg-gallery"${ev.gallery_open ? ' checked' : ''}><div><b>Show entries on the event page</b><span>The event page lists teams whose site is live.</span></div></label>
</section>

<section class="card" id="address"><h2>Public address</h2><p class="mono">${ui.esc(ev.url.replace(/^https?:\/\//, ''))}</p><span class="pill ${website.mode === 'custom' ? 'ok' : ''}">${website.mode === 'custom' ? 'Your own website' + (website.active_version ? ' &middot; version ' + website.active_version : '') : 'Built-in page'}</span>
<div class="field"><label>Your own website (zip with index.html at the top, up to 25 MB)<input class="input" type="file" id="site-zip" accept=".zip"></label>${website.upload_limits ? `<p class="help">${ui.esc(website.upload_limits)}</p>` : ''}</div>
<div class="row-flex"><button class="btn" type="button" id="site-upload">Upload a zip</button>${website.has_custom_page ? `<button class="btn" type="button" id="site-switch">${website.mode === 'custom' ? 'Back to built-in page' : 'Switch to custom website'}</button>` : ''}</div>
<h3 class="section-title">Previous uploads</h3><div class="list" id="site-versions">${(website.versions || []).slice().reverse().map(v => `<div class="r"><div class="main"><b>Version ${v.version_number}</b><span>${ui.esc(ui.dateTime(v.created_at, ev))}</span></div></div>`).join('') || '<div class="empty">No uploads yet.</div>'}</div>
</section>
</div></div>`

    adminLock(main, admin)

    // ---- Basics ----
    $('pg-save').addEventListener('click', async () => {
      const btn = $('pg-save'); btn.disabled = true
      try {
        const r = await api('PATCH', path, {
          title: $('pg-title').value, tagline: $('pg-tagline').value, about: $('pg-about').value,
          rules: $('pg-rules').value, prizes: $('pg-prizes').value, coc_text: $('pg-coc').value,
          time_zone: $('pg-tz').value, starts_at: $('pg-start').value, ends_at: $('pg-end').value
        })
        if (!r.ok) { setLive(ui.errText(r), 'err', btn); btn.disabled = false; return }
        await ctx.refresh()
      } catch (e) { setLive('Cannot reach the server.', 'err', btn); btn.disabled = false }
    })

    // ---- Schedule ----
    $('sched-add').addEventListener('click', () => { collectSchedule(); content.schedule.push({ title: '', description: '', start_at: '', end_at: '' }); paint() })
    main.querySelectorAll('[data-sched-up]').forEach(b => b.addEventListener('click', () => { collectSchedule(); moveItem(content.schedule, Number(b.dataset.schedUp), -1); paint() }))
    main.querySelectorAll('[data-sched-down]').forEach(b => b.addEventListener('click', () => { collectSchedule(); moveItem(content.schedule, Number(b.dataset.schedDown), 1); paint() }))
    main.querySelectorAll('[data-sched-rm]').forEach(b => b.addEventListener('click', () => { collectSchedule(); content.schedule.splice(Number(b.dataset.schedRm), 1); paint() }))
    $('sched-save').addEventListener('click', async () => {
      if (guardContent($('sched-save'))) return
      const btn = $('sched-save'); btn.disabled = true
      collectSchedule()
      try {
        const r = await api('PUT', path + '/content', content)
        if (!r.ok) { setLive(ui.errText(r), 'err', btn); btn.disabled = false; return }
        content = { sponsors: r.data.sponsors || [], faq: r.data.faq || [], schedule: r.data.schedule || [] }
        setLive('Saved', 'ok', btn)
        paint()
      } catch (e) { setLive('Cannot reach the server.', 'err', btn); btn.disabled = false }
    })

    // ---- Sponsors ----
    $('sp-add').addEventListener('click', () => { collectSponsors(); content.sponsors.push({ name: '', tier: '', logo_data: '', url: '' }); paint() })
    main.querySelectorAll('[data-sp-up]').forEach(b => b.addEventListener('click', () => { collectSponsors(); moveItem(content.sponsors, Number(b.dataset.spUp), -1); paint() }))
    main.querySelectorAll('[data-sp-down]').forEach(b => b.addEventListener('click', () => { collectSponsors(); moveItem(content.sponsors, Number(b.dataset.spDown), 1); paint() }))
    main.querySelectorAll('[data-sp-rm]').forEach(b => b.addEventListener('click', () => { collectSponsors(); content.sponsors.splice(Number(b.dataset.spRm), 1); paint() }))
    main.querySelectorAll('[data-sp-logo]').forEach(input => input.addEventListener('change', async () => {
      const i = Number(input.dataset.spLogo)
      const file = input.files && input.files[0]
      if (!file) return
      if (file.size > 128 * 1024) { setLive('Logo must be under 128 KB.', 'err', input); input.value = ''; return }
      collectSponsors()
      try { content.sponsors[i].logo_data = await fileAsDataURL(file) } catch (e) { setLive('Could not read that file.', 'err', input); return }
      paint()
    }))
    $('sp-save').addEventListener('click', async () => {
      if (guardContent($('sp-save'))) return
      const btn = $('sp-save'); btn.disabled = true
      collectSponsors()
      try {
        const r = await api('PUT', path + '/content', content)
        if (!r.ok) { setLive(ui.errText(r), 'err', btn); btn.disabled = false; return }
        content = { sponsors: r.data.sponsors || [], faq: r.data.faq || [], schedule: r.data.schedule || [] }
        setLive('Saved', 'ok', btn)
        paint()
      } catch (e) { setLive('Cannot reach the server.', 'err', btn); btn.disabled = false }
    })

    // ---- FAQ ----
    $('fq-add').addEventListener('click', () => { collectFaq(); content.faq.push({ question: '', answer: '' }); paint() })
    main.querySelectorAll('[data-fq-up]').forEach(b => b.addEventListener('click', () => { collectFaq(); moveItem(content.faq, Number(b.dataset.fqUp), -1); paint() }))
    main.querySelectorAll('[data-fq-down]').forEach(b => b.addEventListener('click', () => { collectFaq(); moveItem(content.faq, Number(b.dataset.fqDown), 1); paint() }))
    main.querySelectorAll('[data-fq-rm]').forEach(b => b.addEventListener('click', () => { collectFaq(); content.faq.splice(Number(b.dataset.fqRm), 1); paint() }))
    $('fq-save').addEventListener('click', async () => {
      if (guardContent($('fq-save'))) return
      const btn = $('fq-save'); btn.disabled = true
      collectFaq()
      try {
        const r = await api('PUT', path + '/content', content)
        if (!r.ok) { setLive(ui.errText(r), 'err', btn); btn.disabled = false; return }
        content = { sponsors: r.data.sponsors || [], faq: r.data.faq || [], schedule: r.data.schedule || [] }
        setLive('Saved', 'ok', btn)
        paint()
      } catch (e) { setLive('Cannot reach the server.', 'err', btn); btn.disabled = false }
    })

    // ---- Announcements ----
    $('ann-form').addEventListener('submit', async e => {
      e.preventDefault()
      const btn = $('ann-form').querySelector('button[type=submit]'); btn.disabled = true
      try {
        const r = await api('POST', path + '/announcements', { title: $('ann-title').value, body: $('ann-body').value, email_participants: $('ann-email').checked })
        if (!r.ok) { setLive(ui.errText(r), 'err', btn); btn.disabled = false; return }
        const a2 = await api('GET', path + '/announcements')
        if (a2.ok) announcements = a2.data.announcements || []
        setLive('Posted', 'ok')
        paint()
      } catch (e2) { setLive('Cannot reach the server.', 'err', btn); btn.disabled = false }
    })
    main.querySelectorAll('[data-ann-edit]').forEach(btn => btn.addEventListener('click', () => {
      const card = btn.closest('[data-ann]')
      card.querySelector('[data-ann-view]').hidden = true
      card.querySelector('[data-ann-edit-form]').hidden = false
    }))
    main.querySelectorAll('[data-ann-edit-cancel]').forEach(btn => btn.addEventListener('click', () => {
      const card = btn.closest('[data-ann]')
      card.querySelector('[data-ann-view]').hidden = false
      card.querySelector('[data-ann-edit-form]').hidden = true
    }))
    main.querySelectorAll('[data-ann-edit-form]').forEach(form => form.addEventListener('submit', async e => {
      e.preventDefault()
      const card = form.closest('[data-ann]')
      const id = card.getAttribute('data-ann')
      const btn = form.querySelector('button[type=submit]'); btn.disabled = true
      try {
        const r = await api('PUT', path + '/announcements/' + encodeURIComponent(id), { title: form.querySelector('[data-ann-edit-title]').value, body: form.querySelector('[data-ann-edit-body]').value })
        if (!r.ok) { setLive(ui.errText(r), 'err', btn); btn.disabled = false; return }
        const a2 = await api('GET', path + '/announcements')
        if (a2.ok) announcements = a2.data.announcements || []
        paint()
      } catch (e2) { setLive('Cannot reach the server.', 'err', btn); btn.disabled = false }
    }))
    main.querySelectorAll('[data-ann-del]').forEach(btn => btn.addEventListener('click', async () => {
      const card = btn.closest('[data-ann]')
      const id = card.getAttribute('data-ann')
      const ok = await ui.confirm({ title: 'Delete this announcement?', body: "Removes it from the page and everyone's feed. It already stays sent if you emailed it.", confirmLabel: 'Delete', danger: true })
      if (!ok) return
      btn.disabled = true
      try {
        const r = await api('DELETE', path + '/announcements/' + encodeURIComponent(id))
        if (!r.ok) { setLive(ui.errText(r), 'err', btn); btn.disabled = false; return }
        announcements = announcements.filter(a => a.id !== id)
        paint()
      } catch (e) { setLive('Cannot reach the server.', 'err', btn); btn.disabled = false }
    }))

    // ---- Gallery & appearance ----
    $('icon-file').addEventListener('change', async () => {
      const input = $('icon-file')
      const file = input.files && input.files[0]
      if (!file) return
      if (!['image/png', 'image/jpeg', 'image/webp'].includes(file.type)) { setLive('Use a PNG, JPEG or WebP image.', 'err', input); input.value = ''; return }
      let buf
      try { buf = await fileAsArrayBuffer(file) } catch (e) { setLive('Could not read that file.', 'err', input); return }
      try {
        const r = await apiBytes('PUT', path + '/icon', buf, file.type)
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
    $('pg-color').addEventListener('change', async () => {
      const sel = $('pg-color'); const prev = ev.accent_color || ''; sel.disabled = true
      try {
        const r = await api('PATCH', path, { accent_color: sel.value })
        if (!r.ok) { setLive(ui.errText(r), 'err', sel); sel.value = prev; sel.disabled = false; return }
        await ctx.refresh()
      } catch (e) { setLive('Cannot reach the server.', 'err', sel); sel.value = prev; sel.disabled = false }
    })
    $('pg-gallery').addEventListener('change', async () => {
      const box = $('pg-gallery'); const want = box.checked; box.disabled = true
      try {
        const r = await api('PATCH', path, { gallery_open: want })
        if (!r.ok) { box.checked = !want; setLive(ui.errText(r), 'err', box); box.disabled = false; return }
        ev.gallery_open = want
        setLive(want ? 'Entries now show on the event page' : 'Entries hidden from the event page', 'ok', box.closest('label'))
        box.disabled = false
      } catch (e) { box.checked = !want; setLive('Cannot reach the server.', 'err', box); box.disabled = false }
    })

    // ---- Public address ----
    $('site-upload').addEventListener('click', async () => {
      const input = $('site-zip')
      const file = input.files && input.files[0]
      if (!file) { setLive('Choose a zip file first.', 'err', input); return }
      const btn = $('site-upload'); btn.disabled = true
      let buf
      try { buf = await fileAsArrayBuffer(file) } catch (e) { setLive('Could not read that file.', 'err', input); btn.disabled = false; return }
      try {
        const r = await apiBytes('PUT', path + '/website', buf, 'application/zip')
        if (!r.ok) { setLive(archiveErrorText(ui, r), 'err', btn); btn.disabled = false; return }
        const w = await api('GET', path + '/website')
        if (w.ok) website = w.data
        setLive('Website published', 'ok')
        paint()
      } catch (e) { setLive('Cannot reach the server.', 'err', btn); btn.disabled = false }
    })
    const switchBtn = $('site-switch')
    if (switchBtn) switchBtn.addEventListener('click', async () => {
      const toCustom = website.mode !== 'custom'
      if (toCustom) {
        const ok = await ui.confirm({ title: 'Switch to custom website?', body: 'Your own site replaces the built-in page at this address. You can switch back any time. Nothing is deleted.', confirmLabel: 'Switch' })
        if (!ok) return
      }
      switchBtn.disabled = true
      try {
        const r = await api('PATCH', path + '/website', { mode: toCustom ? 'custom' : 'builtin' })
        if (!r.ok) { setLive(ui.errText(r), 'err', switchBtn); switchBtn.disabled = false; return }
        website = r.data
        paint()
      } catch (e) { setLive('Cannot reach the server.', 'err', switchBtn); switchBtn.disabled = false }
    })
  }

  paint()
}
