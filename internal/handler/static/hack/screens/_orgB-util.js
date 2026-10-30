// Shared helpers for the organiser-teams / organiser-team-detail /
// organiser-entries / organiser-entry-detail screens. Not a shell/kit file:
// screen-local, like BUILD.md allows for a module more than one of these
// screens needs.

export const FIELD_LABEL = { title: 'Title', tagline: 'Tagline', description: 'Description', video_url: 'Video link', code_url: 'Code link', screenshot: 'Screenshot' }

export function labelList(fields) {
  return fields.map(f => FIELD_LABEL[f] || f)
}

// Joins ["Title","Tagline"] as "Title and Tagline", ["A","B","C"] as "A, B and C".
export function joinAnd(items) {
  if (items.length === 0) return ''
  if (items.length === 1) return items[0]
  return items.slice(0, -1).join(', ') + ' and ' + items[items.length - 1]
}

export function fmtBytes(n) {
  n = Number(n) || 0
  if (n < 1024) return n + ' B'
  const units = ['KB', 'MB', 'GB', 'TB']
  let v = n / 1024
  let i = 0
  while (v >= 1024 && i < units.length - 1) { v /= 1024; i++ }
  return (v >= 10 ? Math.round(v) : Math.round(v * 10) / 10) + ' ' + units[i]
}

// slug for a new track/team-ish name: lowercase, dashes, 1-80 chars, never empty.
export function slugify(name) {
  let s = String(name || '').toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-+|-+$/g, '')
  if (s.length > 80) s = s.slice(0, 80).replace(/-+$/, '')
  return s || 'track-' + Math.random().toString(36).slice(2, 8)
}

function zoneKnown(tz) {
  try { new Intl.DateTimeFormat('en-US', { timeZone: tz || 'UTC' }).format(0); return true } catch { return false }
}

// The wall-clock parts of an ISO instant in the event's own time zone — the
// same clock a datetime-local field for that event shows and sends, with no
// UTC conversion (the server parses "YYYY-MM-DDTHH:MM" in the event's zone).
function zonedParts(iso, tz) {
  const d = new Date(iso)
  if (!iso || Number.isNaN(d.getTime())) return null
  const zone = zoneKnown(tz) ? (tz || 'UTC') : 'UTC'
  const o = {}
  try {
    new Intl.DateTimeFormat('en-US', { timeZone: zone, year: 'numeric', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', hourCycle: 'h23' })
      .formatToParts(d).forEach(p => { o[p.type] = p.value })
  } catch { return null }
  if (!o.year || !o.month || !o.day || !o.hour || !o.minute) return null
  return { y: o.year, m: o.month, d: o.day, h: o.hour === '24' ? '00' : o.hour, min: o.minute }
}

// The value a <input type="datetime-local"> should show for an ISO instant,
// in the event's zone ("" when there is none).
export function localInputValue(iso, event) {
  const p = zonedParts(iso, event && event.time_zone)
  return p ? `${p.y}-${p.m}-${p.d}T${p.h}:${p.min}` : ''
}

// Downloads a CSV (or any file) the same way the classic app does: fetch
// with the API key header, then save the blob — a plain <a href> cannot
// carry that header.
export async function downloadFile(client, path, filename, btn, ui) {
  if (btn) btn.disabled = true
  try {
    const headers = {}
    const k = client.apiKey()
    if (k) headers['X-API-Key'] = k
    const res = await fetch(path, { headers })
    if (res.status === 401) return
    if (!res.ok) {
      let msg = 'Could not download the file.'
      try { const body = await res.json(); if (body && body.error) msg = body.error } catch {}
      ui.toast(msg, btn && btn.closest('section'), true)
      return
    }
    const blob = await res.blob()
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = filename
    document.body.append(a)
    a.click()
    a.remove()
    setTimeout(() => URL.revokeObjectURL(url), 1000)
  } catch {
    ui.toast('Cannot reach the server.', btn && btn.closest('section'), true)
  } finally {
    if (btn) btn.disabled = false
  }
}

// Fetches an authenticated image (a team's entry screenshot) as a data URL
// for an <img src>, since the endpoint needs the X-API-Key header.
export async function fetchImageDataURL(client, path) {
  const headers = {}
  const k = client.apiKey()
  if (k) headers['X-API-Key'] = k
  let res
  try { res = await fetch(path, { headers }) } catch { return '' }
  if (!res.ok) return ''
  const blob = await res.blob()
  return await new Promise(resolve => {
    const reader = new FileReader()
    reader.onload = () => resolve(typeof reader.result === 'string' ? reader.result : '')
    reader.onerror = () => resolve('')
    reader.readAsDataURL(blob)
  })
}

// A richer confirm dialog than ui.confirm: a title, then arbitrary DOM nodes
// for the body (the fill-teams plan needs a list, not one line of text).
export function richConfirm({ title, bodyNodes, confirmLabel = 'Continue', cancelLabel = 'Cancel', danger = false }) {
  return new Promise(resolve => {
    const previous = document.activeElement
    const scrim = document.createElement('div')
    scrim.className = 'scrim'
    const dialog = document.createElement('div')
    dialog.className = 'dialog'
    dialog.setAttribute('role', 'dialog')
    dialog.setAttribute('aria-modal', 'true')
    const h2 = document.createElement('h2')
    h2.textContent = title
    dialog.append(h2)
    for (const node of bodyNodes) dialog.append(node)
    const acts = document.createElement('div')
    acts.className = 'acts'
    const cancelBtn = document.createElement('button')
    cancelBtn.className = 'btn'
    cancelBtn.textContent = cancelLabel
    const okBtn = document.createElement('button')
    okBtn.className = 'btn ' + (danger ? 'danger' : 'primary')
    okBtn.textContent = confirmLabel
    acts.append(cancelBtn, okBtn)
    dialog.append(acts)
    scrim.append(dialog)
    const buttons = [cancelBtn, okBtn]
    function done(value) { document.removeEventListener('keydown', keydown, true); scrim.remove(); previous?.focus(); resolve(value) }
    function keydown(e) {
      if (e.key === 'Escape') { e.preventDefault(); done(false) }
      if (e.key === 'Tab') {
        e.preventDefault()
        const i = buttons.indexOf(document.activeElement)
        buttons[(i + (e.shiftKey ? buttons.length - 1 : 1)) % buttons.length].focus()
      }
    }
    cancelBtn.addEventListener('click', () => done(false))
    okBtn.addEventListener('click', () => done(true))
    scrim.addEventListener('click', e => { if (e.target === scrim) done(false) })
    document.addEventListener('keydown', keydown, true)
    document.body.append(scrim)
    cancelBtn.focus()
  })
}
