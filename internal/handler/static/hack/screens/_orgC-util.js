// Shared helpers for the organiser Judging / Assignment grid / Results
// screens (batch orgC). Not a shell module — only these three screens import
// it. Mirrors the equivalent logic in hack-app.html so behaviour (error
// text, CSV/archive downloads, event-time-zone fields) is not lost.
import { apiKey } from '../api.js'

// errText mirrors hack-app.html's errText: prefer the server's own message
// (every hack_judging.go / hack_voting.go refusal already writes a plain
// sentence), fall back only when the body is missing or unreadable.
export function errText(r, fallback) {
  if (r && r.data && typeof r.data.error === 'string' && r.data.error) return r.data.error
  if (r && r.status >= 500) return 'Something went wrong. Try again.'
  return fallback || 'Something went wrong'
}

// downloadFile replicates hack-app.html's downloadJudgingCSV: a plain GET
// can't carry the X-API-Key header from a bare <a href>, so fetch the bytes
// and hand the browser a blob to save instead. Works for CSV and the
// projects.tar.gz archive alike.
export async function downloadFile(path, filename, btn, setLive) {
  if (btn) btn.disabled = true
  const headers = {}
  const k = apiKey()
  if (k) headers['X-API-Key'] = k
  let res
  try { res = await fetch(path, { headers }) }
  catch (e) { setLive('Cannot reach the server.', 'err', btn); if (btn) btn.disabled = false; return }
  if (res.status === 401) { if (btn) btn.disabled = false; return }
  if (!res.ok) {
    let msg = 'Could not download the file.'
    try { const body = await res.json(); if (body && body.error) msg = body.error } catch (e) {}
    setLive(msg, 'err', btn)
    if (btn) btn.disabled = false
    return
  }
  const blob = await res.blob()
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = filename
  document.body.appendChild(a)
  a.click()
  a.remove()
  setTimeout(() => URL.revokeObjectURL(url), 1000)
  if (btn) btn.disabled = false
  setLive('Downloaded ' + filename, 'ok', btn)
}

// Mirrors hack-app.html's writesLocked(): a platform-admin inspection or a
// taken-down event blocks every write; an archived (ended) event blocks the
// same controls with its own explanation, reads stay possible either way.
// Returns '' when writes are fine, else the note to show by the controls.
export function writeLockReason(ev, adminView) {
  if (adminView) return 'You are viewing as a platform admin. Nothing on this page can be changed.'
  if (ev && ev.taken_down) return 'This event has been taken down. Nothing on this page can be changed.'
  if (ev && ev.stage === 'archived') return 'This event has ended. Nothing on this page changes any more.'
  return ''
}

// localInZone turns an API timestamp into the "YYYY-MM-DDTHH:MM" wall-clock
// string a <input type="datetime-local"> needs, read in the event's own
// time zone (same as hack-voting.html's localInZone).
export function localInZone(iso, zone) {
  if (!iso) return ''
  try {
    const parts = new Intl.DateTimeFormat('en-GB', { timeZone: zone || 'UTC', year: 'numeric', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', hourCycle: 'h23' }).formatToParts(new Date(iso))
    const o = {}
    parts.forEach(p => { o[p.type] = p.value })
    return `${o.year}-${o.month}-${o.day}T${o.hour}:${o.minute}`
  } catch (e) { return String(iso).slice(0, 16) }
}
