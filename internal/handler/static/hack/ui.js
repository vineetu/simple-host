export const esc = value => String(value ?? '').replace(/[&<>"']/g, c => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]))
export const stages = { draft: 'Not open yet', open: 'Sign-up open', building: 'Building', closed: 'Entries closed', judging: 'Judging', results: 'Results', archived: 'Ended', ended: 'Ended' }
export const stageLabel = stage => stages[stage] || stage || ''
export const roleLabel = role => ({organiser:'Organiser',participant:'Participant',judge:'Judge'}[role] || '')
// A7: split on whitespace, but only count words that have an actual letter
// or number -- "Mock · Voting" otherwise takes the "·" as its second "word"
// and returns "M·" instead of "MV".
export const initials = name => String(name || '').split(/\s+/).filter(w => /[\p{L}\p{N}]/u.test(w)).slice(0,2).map(w => w.match(/[\p{L}\p{N}]/u)[0]).join('').toUpperCase()
export function classicURL() { const u = new URL(location.href); u.searchParams.set('ui', 'classic'); return u.pathname + u.search + u.hash }
// The one date/time formatter every screen uses (A19; owner decision
// 2026-10-30 overrides the audit's 24-hour pick): 12-hour with am/pm and the
// weekday, in the event's own zone, never seconds -- "Wed 28 Oct, 6:27 pm".
// `event` is the event object (its time_zone is used) or a bare IANA zone
// string; leave it out to use the browser's own zone (screens with no
// event, e.g. account). `options.dateOnly` drops the time ("Wed 28 Oct");
// the `{ timeStyle: undefined }` some screens already passed for the same
// thing (the old dateStyle/timeStyle API's idiom) keeps working unchanged.
// `options.zone` appends a short zone label ("GMT+9") for the few places
// that need to say which zone a time is in.
// The year to compare against "this year" when deciding whether to show one
// -- done in the event's own zone (not the browser's), so a date that's
// already tomorrow in Tokyo while still today in UTC gets the right year,
// and a 31 Dec/1 Jan boundary doesn't silently drop the year it needs.
function yearInZone(d, zone) {
  if (!zone || !zoneKnown(zone)) return d.getFullYear()
  try { return Number(new Intl.DateTimeFormat('en-US', { timeZone: zone, year: 'numeric' }).format(d)) }
  catch { return d.getFullYear() }
}
export function dateTime(value, event, options = {}) {
  if (!value || Number.isNaN(new Date(value).getTime())) return ''
  const d = new Date(value)
  const zone = typeof event === 'string' ? event : event?.time_zone
  const sameYear = yearInZone(d, zone) === yearInZone(new Date(), zone)
  // A date-only value (an all-day date like "2026-11-06", no time part) never
  // gets a time appended, even if a caller forgot to pass `dateOnly` --
  // there is no real time to show, and showing "12:00 am" claims there is.
  const valueIsDateOnly = typeof value === 'string' && /^\d{4}-\d{2}-\d{2}$/.test(value)
  const dateOnlyWanted = valueIsDateOnly || options.dateOnly === true || ('timeStyle' in options && options.timeStyle === undefined)
  const o = {
    weekday: 'short', day: 'numeric', month: 'short',
    ...(sameYear ? {} : { year: 'numeric' }),
    ...(dateOnlyWanted ? {} : { hour: 'numeric', minute: '2-digit', hourCycle: 'h12' }),
    ...(zone ? { timeZone: zone } : {}),
    ...(options.zone ? { timeZoneName: 'short' } : {})
  }
  // en-GB inserts a comma right after the weekday once `year` is also in the
  // field list ("Sat, 2 Jan 2027"), unlike the same-year form ("Fri 6 Nov").
  // Strip that stray one so every date reads "Xxx D Mon[ YYYY][, h:mm am]"
  // the same way regardless of year -- the comma before the time is kept.
  return new Intl.DateTimeFormat('en-GB', o).format(d).replace(/^(\S+),\s/, '$1 ')
}
// Date only, no time -- "Wed 28 Oct" (or "Wed 28 Oct 2027" once the year
// isn't this one). Same zone rules as dateTime; a plain shortcut for the
// screens that only ever want the date half.
export function dateOnly(value, event) {
  return dateTime(value, event, { dateOnly: true })
}
export function nextDeadline(event) {
  return [['Starts', event?.starts_at], ['Entries close', event?.submission_deadline], ['Ends', event?.ends_at]]
    .filter(([, value]) => value && new Date(value) > new Date()).sort((a,b) => new Date(a[1]) - new Date(b[1]))[0]
}
// C2: a toast lands where it will actually be seen.
// - Next to a real control (`near` is some element inside the page, not the
//   page itself): after that control's own row -- the closest
//   .row-flex/.field/.setting/.card/.sticky-foot/.ph/.acts container --
//   never spliced between two buttons inside a flex row.
// - With no real control to anchor to (`near` is <main> itself, or left
//   out): at the very top of the page content, right after the .ph header
//   if the screen has one (first child of main otherwise), inside the
//   page's own padding, and scrolled into view -- so a note on a long page
//   can't end up below the fold or outside .page's padded column.
// Either way it replaces any toast already sitting in that same spot
// instead of stacking under it. Errors never auto-hide -- they stay until
// the person dismisses them or another toast replaces them in the same
// spot; a "Saved"/ok toast still clears itself after a few seconds.
const TOAST_ROW = '.row-flex, .field, .setting, .card, .sticky-foot, .ph, .acts'
function placeToast(note, near) {
  const main = document.querySelector('main')
  const pageLevel = !near || near === main || near === document.body || (main != null && typeof near.contains === 'function' && near.contains(main))
  if (!pageLevel && near.parentNode) {
    const row = near.closest(TOAST_ROW) || near
    const existing = row.nextElementSibling
    if (existing && existing.matches('[data-toast]')) existing.remove()
    row.insertAdjacentElement('afterend', note)
    return
  }
  const host = (near && near.nodeType === 1 && near.matches('main')) ? near : (main || document.getElementById('app'))
  host.querySelectorAll('[data-toast]').forEach(n => n.remove())
  const ph = host.querySelector('.ph')
  if (ph) ph.insertAdjacentElement('afterend', note)
  else host.insertBefore(note, host.firstChild)
  note.scrollIntoView({ block: 'nearest' })
}
export function toast(text, near, bad = false) {
  if (!text) return
  const note = document.createElement('p')
  note.className = 'note toast ' + (bad ? 'bad' : 'ok')
  note.setAttribute('data-toast', '')
  note.setAttribute('role', bad ? 'alert' : 'status')
  const span = document.createElement('span')
  span.textContent = text
  note.append(span)
  if (bad) {
    const dismiss = document.createElement('button')
    dismiss.type = 'button'
    dismiss.className = 'btn quiet sm'
    dismiss.textContent = 'Dismiss'
    dismiss.setAttribute('aria-label', 'Dismiss')
    dismiss.addEventListener('click', () => note.remove())
    note.append(dismiss)
  }
  placeToast(note, near)
  if (!bad) setTimeout(() => note.remove(), 6000)
}
export const saved = near => toast('Saved', near)
// One status/toast line per screen: `const setLive = ui.live(main)` replaces
// every screen's own `setLive` one-liner. Accepts either calling convention
// those one-liners used -- `setLive(msg, 'err')` and `setLive(msg, true)`
// both mean "bad" -- plus an optional element to show the note near instead
// of `main`.
export function live(main) {
  return (msg, kind, near) => { if (msg) toast(msg, near || main, kind === 'err' || kind === true) }
}
// Error codes shared by every screen that passes `codes` to errText(), merged
// from the judge, participant (_partB-util) and public-join screens' own
// maps. Where two groups disagreed on the copy for the same code
// (`team_not_found`: a judge's missing assignment vs. a mistyped join code),
// this keeps the judge-screens wording as the default and the participant
// screens carry their own override (see screens/_partB-util.js) -- every
// message either group had is kept, nothing was dropped.
export const ERROR_MESSAGES = {
  coc_required: 'Please agree to the code of conduct.',
  invalid_display_name: 'Enter your name on one line, without special formatting.',
  already_member: 'You are already in this event.',
  admin_cannot_join: 'The platform admin cannot join an event.',
  judging_closed: 'Judging is closed for this event.',
  event_taken_down: 'This event has been taken down.',
  invalid_code: 'This link does not work any more.',
  event_closed: 'This event has ended.',
  scores_locked: 'Judging is locked.',
  scoring_not_open: 'Scoring opens when entries close.',
  team_not_found: 'That team was not found.',
  judge_not_found: 'That judge was not found.',
  conflict_not_found: "That conflict wasn't found. It may already be gone.",
  criterion_not_found: 'That criterion is not in the event’s current rubric.',
  invalid_request: "Something in that request wasn't right. Reload and try again.",
  rate_limited: 'Too many changes at once. Wait a moment and try again.',
  team_finding_off: 'Team finding is off.',
  team_action_unavailable: 'This team action is not available.',
  invalid_looking_for: 'Keep the looking-for note to one line, up to 120 characters.',
  invalid_intro: 'Keep your intro to one line, up to 120 characters.',
  invalid_title: 'The title must be one line, up to 80 characters.',
  invalid_tagline: 'The tagline must be one line, up to 160 characters.',
  invalid_description: "The description is too long or has characters we can't keep.",
  invalid_team_name: 'Give the team a name of up to 80 characters, on one line.',
  invalid_track: 'Choose a listed track.',
  already_in_team: 'You are already on a team.',
  team_full: 'That team is full.',
  team_name_taken: 'There is already a team with that name.',
  teams_locked: 'Teams are fixed now.',
  invalid_url: 'Enter a full link starting with https://',
  no_team: 'Join or start a team first.',
  submissions_closed: 'Entries are closed for your team. Ask the organiser if you need more time.',
  team_sites_not_ready: "Your team's address is still being set up. Try again in a few minutes.",
  image_too_large: 'The screenshot must be 2 MB or smaller.',
  unsupported_image: 'The screenshot must be a PNG, JPEG or WebP image.',
  team_site_taken_down: "The organiser has taken your team's site down.",
  deleted: 'That no longer exists.',
  not_found: 'That no longer exists.',
  invalid_events: 'Choose at least one notification event.',
  too_many_notifications: 'This site already has as many notifications as it can hold.',
  notification_access_ended: 'Notifications for this site are no longer available.',
  notifications_not_enabled: 'Notifications are unavailable on this server.',
  joining_closed: 'Sign-up has closed for this event.',
  answer_required: 'Answer every required question.',
  invalid_answers: "Something in that request wasn't right. Reload and try again."
}
// errText mirrors hack-app.html's errText: without `codes`, the server's own
// message (every hack.go / hack_*.go refusal already writes a plain
// sentence) always wins, since it is usually more specific than anything a
// client could guess. Pass `codes` (a code -> copy map, usually
// ui.ERROR_MESSAGES or a screen-group's override of it) when some of those
// codes need friendlier client-side copy instead -- then a code match wins
// over the server's own text.
// A 5xx is different: writeInternal's own body is just `{"error":"internal
// server error"}`, lowercase server jargon with nothing a person can act on
// -- so a 5xx always gets the generic "try again" copy, checked before the
// server text (but after a `codes` match, which can still offer its own
// friendlier line for a specific code even on a 5xx).
export function errText(r, { codes, fallback } = {}) {
  const code = r && r.data && r.data.code
  if (codes && code && codes[code]) return codes[code]
  if (r && r.status >= 500) return 'Something went wrong. Try again.'
  if (r && r.data && typeof r.data.error === 'string' && r.data.error) return r.data.error
  return fallback || 'Something went wrong.'
}
// Mirrors hack-app.html's writesLocked(): a platform-admin inspection or a
// taken-down event blocks every write; an archived (ended) event blocks the
// same controls with its own explanation; reads stay possible either way.
// Returns '' when writes are fine, else the note to show by the controls.
export function writeLockReason(ev, adminView) {
  if (adminView) return 'You are viewing as a platform admin. Nothing on this page can be changed.'
  if (ev && ev.taken_down) return 'This event has been taken down. Nothing on this page can be changed.'
  if (ev && ev.stage === 'archived') return 'This event has ended. Nothing on this page changes any more.'
  return ''
}
// The plain six-line DOM builder four screens each wrote their own copy of:
// create tag, special-case attrs.text as textContent, otherwise setAttribute.
export function el(tag, attrs = {}) {
  const node = document.createElement(tag)
  for (const [key, value] of Object.entries(attrs)) {
    if (key === 'text') node.textContent = value
    else node.setAttribute(key, value)
  }
  return node
}
function zoneKnown(tz) {
  try { new Intl.DateTimeFormat('en-US', { timeZone: tz || 'UTC' }).format(0); return true } catch { return false }
}
// The wall-clock parts of an ISO instant in the event's own time zone -- the
// same clock a date/datetime-local field for that event shows and sends,
// with no UTC conversion (the server parses "YYYY-MM-DD"/"YYYY-MM-DDTHH:MM"
// as a wall-clock time in the event's own zone, so reading the field's raw
// .value back on save needs no inverse of this function).
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
  // Some engines format local midnight as "24" instead of "00" with
  // hourCycle: 'h23'; only one of the three original implementations
  // (organiser-entries.js's localInputValue) guarded against this.
  return { y: o.year, m: o.month, d: o.day, h: o.hour === '24' ? '00' : o.hour, min: o.minute }
}
// The value a <input type="date"> or <input type="datetime-local"> should
// show for an ISO instant, in the given zone ("" when there is none).
// `eventOrZone` is either an event object (its time_zone is used) or a bare
// IANA zone string. There is no "from" direction: saving reads the field's
// own wall-clock .value back unchanged, as the comment on zonedParts above
// explains -- do not add an inverse of this function.
export function localInputValue(iso, eventOrZone, { dateOnly = false } = {}) {
  const zone = typeof eventOrZone === 'string' ? eventOrZone : eventOrZone?.time_zone
  const p = zonedParts(iso, zone)
  if (!p) return ''
  return dateOnly ? `${p.y}-${p.m}-${p.d}` : `${p.y}-${p.m}-${p.d}T${p.h}:${p.min}`
}
// Authenticated binary GET (a screenshot, an export archive) needs the
// X-API-Key header by hand: api.js's api()/apiBytes() both expect a JSON
// body back, not bytes. A 401 refreshes through the shell (ctx.refresh),
// which runs the real sign-out flow.
// Grok B3: the CSP's img-src allows only 'self' and data:, never blob: --
// a blob: URL silently never shows. `dataURL` is read via FileReader
// (there is no synchronous Blob -> data: URL conversion); `blob` is kept
// too for a caller that wants the raw bytes (e.g. to check size).
export async function fetchBinary(ctx, path) {
  const headers = {}
  const key = ctx.api.apiKey()
  if (key) headers['X-API-Key'] = key
  let res
  try { res = await fetch(path, { headers }) } catch { return { ok: false, status: 0 } }
  if (res.status === 401) { await ctx.refresh(); return { ok: false, status: 401, signedOut: true } }
  if (!res.ok) {
    let data = {}
    try { data = await res.json() } catch {}
    return { ok: false, status: res.status, data }
  }
  const blob = await res.blob()
  const dataURL = await new Promise(resolve => {
    const reader = new FileReader()
    reader.onload = () => resolve(typeof reader.result === 'string' ? reader.result : '')
    reader.onerror = () => resolve('')
    reader.readAsDataURL(blob)
  })
  return { ok: true, status: res.status, blob, dataURL }
}
// Shows a private image (a screenshot) that needs the API key to fetch, into
// an already-present <img>, as a data: URL (Grok B3 -- never blob:, which
// the CSP blocks).
export async function showImage(ctx, img, path) {
  const r = await fetchBinary(ctx, path)
  if (r.signedOut || !r.ok || !r.dataURL) { img.hidden = true; return }
  img.src = r.dataURL
  img.hidden = false
}
// A7/A16: a bright mark colour (e.g. the mock-judging yellow) makes white
// ink unreadable -- pick white or the kit's own dark ink by relative
// luminance instead of always white. `color` is a '#rrggbb' hex string.
// Was duplicated in shell.js (the switcher/event marks) and
// landing-your-events.js (its own event marks); this is the one shared
// version. Other screens still carry their own copy until they're switched
// over -- this export doesn't remove those.
export function markInk(color) {
  const n = parseInt(color.slice(1), 16)
  const f = v => { v /= 255; return v <= .03928 ? v / 12.92 : ((v + .055) / 1.055) ** 2.4 }
  const lum = .2126 * f(n >> 16) + .7152 * f((n >> 8) & 255) + .0722 * f(n & 255)
  return lum > 0.45 ? '#2b2925' : '#fff'
}
// Only an http(s) link is safe to use as a real <a href> or open in a new
// tab -- javascript:, data: and other schemes never pass. Was duplicated in
// screens/_partB-util.js and screens/_judge-util.js; this is the one shared
// version. Those screens still carry their own copy until they're switched
// over -- this export doesn't remove those.
export function safeHref(url) {
  const s = String(url || '').trim()
  return /^https?:\/\//i.test(s) ? s : ''
}
export function copyButton(text, label = 'Copy') {
  const button = document.createElement('button')
  button.type = 'button'
  button.className = 'btn sm'
  button.textContent = label
  button.addEventListener('click', async () => {
    try { await navigator.clipboard.writeText(text); button.textContent = 'Copied'; setTimeout(() => button.textContent = label, 2000) }
    catch { toast('Could not copy. Select the text and copy it.', button, true) }
  })
  return button
}
// The one scrim/dialog/focus-trap/Escape/Tab-cycle modal primitive. Screens
// never build their own -- ui.confirm (below) is the common two-button case
// built on this; a screen with a genuinely different shape (a dropdown plus
// a warning, several buttons) calls ui.dialog directly instead of hand-
// rolling the scrim wiring again.
// `body` is a string (escaped into one <p>), a Node, or an array of Nodes.
// `buttons` is [{ label, value, className, attrs }], in DOM/tab order;
// clicking one resolves the dialog with its `value`. `attrs` (e.g.
// `{'data-confirm': ''}`) is for a caller or test that needs to find a
// specific button, same as the atlas prototypes' own confirm dialogs.
// Escape and a backdrop click resolve with `escapeValue`. `focus` is the
// index of the button focused on open.
export function dialog({ title, body, buttons, escapeValue = false, focus = 0 }) {
  return new Promise(resolve => {
    const previous = document.activeElement
    const scrim = document.createElement('div')
    scrim.className = 'scrim'
    const box = document.createElement('div')
    box.className = 'dialog'
    box.setAttribute('role', 'dialog')
    box.setAttribute('aria-modal', 'true')
    const h2 = document.createElement('h2')
    h2.id = 'dialog-title'
    h2.textContent = title
    box.setAttribute('aria-labelledby', 'dialog-title')
    box.append(h2)
    const nodes = body == null ? []
      : Array.isArray(body) ? body
      : body instanceof Node ? [body]
      : [Object.assign(document.createElement('p'), { id: 'dialog-body', textContent: body })]
    if (!Array.isArray(body) && !(body instanceof Node) && body != null) box.setAttribute('aria-describedby', 'dialog-body')
    box.append(...nodes)
    const acts = document.createElement('div')
    acts.className = 'acts'
    const els = buttons.map(b => {
      const btn = document.createElement('button')
      btn.type = 'button'
      btn.className = b.className || 'btn'
      btn.textContent = b.label
      for (const [k, v] of Object.entries(b.attrs || {})) btn.setAttribute(k, v)
      btn.addEventListener('click', () => done(b.value))
      return btn
    })
    acts.append(...els)
    box.append(acts)
    scrim.append(box)
    function done(value) { document.removeEventListener('keydown', keydown, true); scrim.remove(); previous?.focus(); resolve(value) }
    function keydown(e) {
      if (e.key === 'Escape') { e.preventDefault(); done(escapeValue) }
      if (e.key === 'Tab') {
        e.preventDefault()
        const index = els.indexOf(document.activeElement)
        els[(index + (e.shiftKey ? els.length - 1 : 1)) % els.length].focus()
      }
    }
    scrim.addEventListener('click', e => { if (e.target === scrim) done(escapeValue) })
    document.addEventListener('keydown', keydown, true)
    document.body.append(scrim)
    els[focus].focus()
  })
}
// In-page confirm dialog; screens call ui.confirm. Never the browser's native
// one. `body` accepts a Node or Node[] (e.g. a list the person is confirming
// against) as well as a plain string. `focus: 'confirm'` opens with the
// confirm button focused instead of cancel -- the safe default is cancel,
// as for any destructive action, but a screen presenting two neutral choices
// (e.g. "keep mine" / "take theirs") can ask for the other default.
// Defined under a different name and re-exported below, the same trick the
// old ask()/`export { ask as confirm }` used -- declaring it directly under
// this module's own export name would read, to the static check that bans
// the browser's native dialogs, as a call to that native one.
function askConfirm({ title, body, confirmLabel = 'Continue', cancelLabel = 'Cancel', danger = false, focus = 'cancel' }) {
  return dialog({
    title, body,
    buttons: [
      { label: cancelLabel, value: false, attrs: { 'data-cancel': '' } },
      { label: confirmLabel, value: true, className: 'btn ' + (danger ? 'danger' : 'primary'), attrs: { 'data-confirm': '' } }
    ],
    escapeValue: false,
    focus: focus === 'confirm' ? 1 : 0
  })
}
export { askConfirm as confirm }
