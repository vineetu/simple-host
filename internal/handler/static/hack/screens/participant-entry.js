import { errText, isBlockedCode, showImage } from './_partB-util.js'

export function title () { return 'Entry' }

// Features on this screen: get-own-entry, edit-entry-fields,
// entry-edit-conflict-resolution, upload-entry-screenshot,
// remove-entry-screenshot, view-entry-read-only.

const FIELDS = ['title', 'tagline', 'description', 'video_url', 'code_url']
const LABEL = { title: 'Title', tagline: 'Tagline', description: 'Description', video_url: 'Video link', code_url: 'Code link' }
const PLAIN = { title: 'title', tagline: 'tagline', description: 'description', video_url: 'video link', code_url: 'code link', screenshot: 'screenshot' }
const CLOSED_TEXT = {
  submissions_closed: 'Entries are closed',
  team_site_taken_down: 'The organiser has taken your team’s site down.',
  event_taken_down: 'This event has been taken down.',
  event_closed: 'This event has ended.'
}

export async function render (ctx) {
  const { main, api: client, ui, event: detail } = ctx
  const api = client.api
  const esc = ui.esc
  const ev = detail.event

  main.innerHTML = '<div class="ph"><div class="t"><h1>Entry</h1><p>' + esc(ev.title || '') + '</p></div></div><p class="note">Loading…</p>'

  let r
  try { r = await api('GET', '/v1/hack/events/' + encodeURIComponent(ev.slug) + '/entry') } catch (e) { if (e.message === 'signed out') return; r = { ok: false, data: {} } }
  if (!r.ok) {
    if (r.data && r.data.code === 'no_team') {
      main.innerHTML = '<div class="ph"><div class="t"><h1>Entry</h1><p>' + esc(ev.title || '') + '</p></div></div><div class="locked"><h2>Join or start a team first</h2><p><a class="btn" href="/e/' + esc(encodeURIComponent(ev.slug)) + '/team">Go to Team</a></p></div>'
      return
    }
    main.innerHTML = '<div class="ph"><div class="t"><h1>Entry</h1><p>' + esc(ev.title || '') + '</p></div></div><div class="note bad" role="alert">' + esc(errText(r, 'Could not load your entry.')) + '</div>'
    return
  }

  paint(ctx, ev, r.data)
}

function paint (ctx, ev, data) {
  const { main, api: client, ui } = ctx
  const api = client.api
  const esc = ui.esc
  const required = Array.isArray(data.required) ? data.required : []
  const editable = data.editable !== false
  const entry = data.entry || {}

  const head = '<div class="ph"><div class="t"><h1>Entry</h1><p>' + esc(ev.title || '') + '</p></div></div>'

  if (!editable) {
    const closedText = CLOSED_TEXT[data.refusal] || CLOSED_TEXT.submissions_closed
    const hasAnything = FIELDS.some(f => String(entry[f] || '').trim()) || entry.has_screenshot
    let body = '<section class="card"><h2>Your entry</h2>'
    if (!hasAnything) {
      body += '<p class="muted">' + (data.refusal === 'submissions_closed' ? 'Nothing was entered by the deadline.' : 'No entry.') + '</p>'
    } else {
      FIELDS.forEach(f => {
        const val = String(entry[f] || '')
        if (!val.trim()) return
        body += '<div class="field"><span class="lab">' + esc(LABEL[f]) + '</span><p>' + esc(val) + '</p></div>'
      })
      if (entry.has_screenshot) {
        body += '<div class="field"><span class="lab">Screenshot</span><div class="site-frame" id="en-shot-view"></div></div>'
      }
    }
    body += '</section>'
    main.innerHTML = head + '<div class="note info">' + esc(closedText) + '</div>' + body
    if (entry.has_screenshot) {
      const frame = main.querySelector('#en-shot-view')
      const img = document.createElement('img')
      img.alt = 'Screenshot'
      img.style.maxWidth = '100%'
      img.hidden = true
      frame.append(img)
      showImage(ctx, img, '/v1/hack/events/' + encodeURIComponent(ev.slug) + '/entry/screenshot')
    }
    return
  }

  const missing = Array.isArray(data.missing) ? data.missing : []
  const statusHTML = data.complete
    ? '<div class="note ok">Complete</div>'
    : (missing.length ? '<div class="note">Still needed: ' + esc(missing.map(f => PLAIN[f] || f).join(', ')) + '</div>' : '')

  function fieldHTML (f, type) {
    const req = required.indexOf(f) >= 0
    const val = esc(entry[f] || '')
    const labelText = esc(LABEL[f]) + (req ? ' (required)' : '')
    if (f === 'description') {
      return '<div class="field"><label for="en-' + f + '">' + labelText + '</label><textarea class="input" id="en-' + f + '" maxlength="5000">' + val + '</textarea><p class="help" id="en-' + f + '-err"></p></div>'
    }
    return '<div class="field"><label for="en-' + f + '">' + labelText + '</label><input class="input" id="en-' + f + '"' + (type ? ' type="' + type + '"' : '') + ' value="' + val + '" maxlength="' + (f === 'title' ? 80 : f === 'tagline' ? 160 : 500) + '"><p class="help" id="en-' + f + '-err"></p></div>'
  }

  const shotRequired = required.indexOf('screenshot') >= 0
  main.innerHTML = head + statusHTML +
    '<section class="card"><h2>Your entry</h2>' +
    fieldHTML('title') + fieldHTML('tagline') + fieldHTML('description') + fieldHTML('video_url', 'url') + fieldHTML('code_url', 'url') +
    '<div class="field"><label for="en-shot">Screenshot (saved immediately)' + (shotRequired ? ' (required)' : '') + '</label>' +
    '<div class="site-frame" id="en-shot-frame"' + (entry.has_screenshot ? '' : ' hidden') + '><img id="en-shot-img" alt="Screenshot" style="max-width:100%" hidden></div>' +
    '<input class="input" type="file" id="en-shot" accept="image/png,image/jpeg,image/webp">' +
    '<p class="help" id="en-screenshot-err"></p>' +
    '<div class="danger-zone"><h2>Screenshot</h2><div class="setting"><div class="t"><span>Uploading or removing a screenshot saves immediately.</span></div><button type="button" class="btn danger" id="en-shot-remove"' + (entry.has_screenshot ? '' : ' hidden') + '>Remove screenshot</button></div></div>' +
    '</div>' +
    '</section>' +
    '<div class="sticky-foot"><button type="button" class="btn primary" id="en-save">Save</button><span class="small muted">Title, tagline, description and links save together.</span></div>'

  if (entry.has_screenshot) showImage(ctx, main.querySelector('#en-shot-img'), '/v1/hack/events/' + encodeURIComponent(ev.slug) + '/entry/screenshot')

  const $ = id => main.querySelector('#' + id)
  const path = '/v1/hack/events/' + encodeURIComponent(ev.slug) + '/entry'

  // A write that finds the team frozen or taken down meanwhile reloads the
  // page so it shows that state instead of a stale form (matches
  // hack-app.html's blockedWrite).
  async function blockedRefresh (resp) {
    const code = resp && resp.data && resp.data.code
    if (!isBlockedCode(code)) return false
    ui.toast(errText(resp), main, true)
    await render(ctx)
    return true
  }

  function clearErrors () { FIELDS.concat('screenshot').forEach(f => { const n = $('en-' + f + '-err'); if (n) n.textContent = '' }) }
  function fieldErr (f, msg) { const n = $('en-' + f + '-err'); if (n) n.textContent = msg || '' }
  function showFieldError (code, msg, sent) {
    if (code === 'invalid_url') {
      const which = ['video_url', 'code_url'].filter(f => sent && Object.prototype.hasOwnProperty.call(sent, f))
      which.forEach(f => fieldErr(f, msg))
      return which.length > 0
    }
    if (code === 'image_too_large' || code === 'unsupported_image') { fieldErr('screenshot', msg); return true }
    const field = FIELDS.find(f => code === f || code === 'invalid_' + f)
    if (!field) return false
    fieldErr(field, msg)
    return true
  }

  $('en-save').addEventListener('click', async () => {
    clearErrors()
    const now = {}
    FIELDS.forEach(f => { now[f] = $('en-' + f).value })
    const body = {}
    FIELDS.forEach(f => { if ((now[f] || '') !== (entry[f] || '')) body[f] = f === 'video_url' || f === 'code_url' ? now[f].trim() : now[f] })
    if (!Object.keys(body).length) { ui.toast('Saved', $('en-save')); return }
    body.updated_at = entry.updated_at || ''
    $('en-save').disabled = true
    try {
      const resp = await api('PUT', path, body)
      if (!resp.ok && resp.data && resp.data.code === 'entry_changed') {
        $('en-save').disabled = false
        await showConflict(ctx, ev, data, body, resp.data.entry)
        return
      }
      if (!resp.ok) {
        if (await blockedRefresh(resp)) return
        const msg = errText(resp)
        if (!showFieldError(resp.data && resp.data.code, msg, body)) ui.toast(msg, $('en-save'), true)
        $('en-save').disabled = false
        return
      }
      ui.toast('Saved', $('en-save'))
      paint(ctx, ev, resp.data || {})
    } catch (e) {
      if (e.message === 'signed out') return
      ui.toast('Cannot reach the server.', $('en-save'), true)
      $('en-save').disabled = false
    }
  })

  $('en-shot').addEventListener('change', async () => {
    const input = $('en-shot')
    const file = input.files && input.files[0]
    if (!file) return
    clearErrors()
    if (file.size > 2 * 1024 * 1024 || (file.type && ['image/png', 'image/jpeg', 'image/webp'].indexOf(file.type) < 0)) {
      fieldErr('screenshot', 'Use a PNG, JPEG or WebP image, up to 2 MB.')
      input.value = ''
      return
    }
    try {
      const resp = await client.apiBytes('PUT', '/v1/hack/events/' + encodeURIComponent(ev.slug) + '/entry/screenshot', file, file.type)
      input.value = ''
      if (!resp.ok) {
        if (await blockedRefresh(resp)) return
        const msg = errText(resp)
        const code = resp.data && resp.data.code
        if (code === 'image_too_large' || code === 'unsupported_image') fieldErr('screenshot', msg)
        else ui.toast(msg, input, true)
        return
      }
      ui.toast('Screenshot saved', input)
      paint(ctx, ev, resp.data || {})
    } catch (e) {
      input.value = ''
      if (e.message !== 'signed out') ui.toast('Cannot reach the server.', input, true)
    }
  })

  const shotRemove = $('en-shot-remove')
  if (shotRemove) shotRemove.addEventListener('click', async () => {
    const ok = await ui.confirm({ title: 'Remove the screenshot?', body: 'It comes off your entry.', confirmLabel: 'Remove', danger: true })
    if (!ok) return
    shotRemove.disabled = true
    try {
      const resp = await api('DELETE', '/v1/hack/events/' + encodeURIComponent(ev.slug) + '/entry/screenshot')
      if (!resp.ok) {
        if (await blockedRefresh(resp)) return
        ui.toast(errText(resp), shotRemove, true); shotRemove.disabled = false; return
      }
      ui.toast('Screenshot removed', main.querySelector('.card'))
      paint(ctx, ev, resp.data || {})
    } catch (e) {
      if (e.message !== 'signed out') { ui.toast('Cannot reach the server.', shotRemove, true); shotRemove.disabled = false }
    }
  })
}

// A teammate saved over this entry while it was open here. Optimistic
// concurrency (spec: entry-edit-conflict-resolution) -- a real choice, not a
// toast, with both versions shown side by side.
async function showConflict (ctx, ev, data, myBody, newer) {
  const { ui } = ctx
  const esc = ui.esc
  const changed = FIELDS.filter(f => Object.prototype.hasOwnProperty.call(myBody, f) && (myBody[f] || '') !== (newer[f] || ''))
  const fields = changed.length ? changed : FIELDS
  return new Promise(resolve => {
    const previous = document.activeElement
    const scrim = document.createElement('div')
    scrim.className = 'scrim'
    const cards = fields.map(f => {
      const mine = Object.prototype.hasOwnProperty.call(myBody, f) ? myBody[f] : (data.entry && data.entry[f]) || ''
      return '<div class="card"><h2>Your ' + esc((LABEL[f] || f).toLowerCase()) + '</h2><p>' + esc(mine || '(empty)') + '</p></div>' +
        '<div class="card"><h2>Their ' + esc((LABEL[f] || f).toLowerCase()) + '</h2><p>' + esc(newer[f] || '(empty)') + '</p></div>'
    }).join('')
    scrim.innerHTML = '<div class="dialog" role="dialog" aria-modal="true" aria-labelledby="conflict-title">' +
      '<h2 id="conflict-title">A teammate changed this entry while you were editing</h2>' +
      '<div class="grid2">' + cards + '</div>' +
      '<div class="acts"><button class="btn" data-take>Take theirs</button><button class="btn primary" data-keep>Keep mine</button></div>' +
      '</div>'
    function done (keep) { scrim.remove(); previous && previous.focus && previous.focus(); resolve(keep) }
    scrim.querySelector('[data-keep]').addEventListener('click', () => done(true))
    scrim.querySelector('[data-take]').addEventListener('click', () => done(false))
    scrim.addEventListener('click', e => { if (e.target === scrim) done(false) })
    document.body.append(scrim)
    scrim.querySelector('[data-keep]').focus()
  }).then(async keep => {
    const api = ctx.api.api
    const path = '/v1/hack/events/' + encodeURIComponent(ev.slug) + '/entry'
    if (!keep) {
      // Take theirs: drop the local edit and reload the current entry.
      const r = await api('GET', path)
      if (r.ok) paint(ctx, ev, r.data || {})
      return
    }
    // Keep mine: resubmit on top of the newer version.
    const body = Object.assign({}, myBody, { updated_at: newer.updated_at || '' })
    try {
      const r = await api('PUT', path, body)
      if (!r.ok) { ctx.ui.toast(errText(r, 'Could not save. Try again.'), ctx.main, true); return }
      ctx.ui.toast('Saved', ctx.main)
      paint(ctx, ev, r.data || {})
    } catch (e) { if (e.message !== 'signed out') ctx.ui.toast('Cannot reach the server.', ctx.main, true) }
  })
}
