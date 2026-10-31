import { isAppRoute } from './nav.js'
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
    ...(valueIsDateOnly ? { timeZone: 'UTC' } : zone ? { timeZone: zone } : {})
  }
  // en-GB inserts a comma right after the weekday once `year` is also in the
  // field list ("Sat, 2 Jan 2027"), unlike the same-year form ("Fri 6 Nov").
  // Strip that stray one so every date reads "Xxx D Mon[ YYYY][, h:mm am]"
  // the same way regardless of year -- the comma before the time is kept.
  const base = new Intl.DateTimeFormat('en-GB', o).format(d).replace(/^(\S+),\s/, '$1 ')
  // Zone label (owner decision 2026-10-30): appended by hand through
  // zoneAbbr rather than Intl's own `timeZoneName: 'short'` -- Intl's short
  // name for most of the zones this app cares about (Jakarta, Tokyo, Seoul,
  // Singapore, Kolkata, Central Europe) is a bare "GMT+9" with no letters,
  // where the Go public page's own hackZoneAbbr (reading Go's tzdata) says
  // "JST". zoneAbbr closes that gap the same way for every screen.
  if (!options.zone || valueIsDateOnly) return base
  const label = zoneAbbr(zone, d)
  return label ? base + ' ' + label : base
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
const TOAST_ROW = '.r, .row-flex, .field, .setting, .card, .sticky-foot, .ph, .acts'
function placeToast(note, near) {
  const main = document.querySelector('main')
  const pageLevel = !near || near === main || near === document.body || (main != null && typeof near.contains === 'function' && near.contains(main))
  if (!pageLevel && near.parentNode) {
    const row = near.closest(TOAST_ROW) || near
    const existing = row.matches('.sticky-foot') ? row.previousElementSibling : row.nextElementSibling
    if (existing && existing.matches('[data-toast]')) existing.remove()
    // A fixed save bar sits over the page end: put its note just above it.
    row.insertAdjacentElement(row.matches('.sticky-foot') ? 'beforebegin' : 'afterend', note)
    note.scrollIntoView({ block: 'nearest' })
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
  // Many screens repaint right after saving, which wipes a note placed next
  // to the old control. If the note was swept away, show it again at the top
  // of the page so "Saved" and errors are never lost.
  let gone = false
  const keep = () => { if (!gone && !note.isConnected) placeToast(note, null) }
  setTimeout(keep, 0); setTimeout(keep, 400); setTimeout(keep, 1200)
  if (!bad) setTimeout(() => { gone = true; note.remove() }, 6000)
  else note.querySelector('button')?.addEventListener('click', () => { gone = true })
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
  // Cosmetic #1 (retest 3): the event address check (GET /names/{slug} and
  // POST /events) wrote its own lowercase, comma-run sentences straight
  // into the help text -- friendly, capitalised copy for Create event's
  // address field.
  invalid_name: "Use 3 to 39 lowercase letters, numbers or hyphens. It can't start or end with a hyphen.",
  name_reserved: 'That name is reserved.',
  name_taken: 'That name is already in use.',
  name_check_unavailable: 'Could not check whether this name is free. Try again.',
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
// `opts.settings` (S14): the configuration screens -- Event page, People,
// Teams, Results -- read as one settings form rather than a one-off
// action, so their archived note says "Settings can't change any more"
// instead of this helper's plain default wording.
export function writeLockReason(ev, adminView, opts = {}) {
  if (adminView) return 'You are viewing as a platform admin. Nothing on this page can be changed.'
  if (ev && ev.taken_down) return 'This event has been taken down. Nothing on this page can be changed.'
  if (ev && ev.stage === 'archived') return opts.settings ? "This event has ended. Settings can't change any more." : 'This event has ended. Nothing on this page changes any more.'
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
// Zone labels (owner decision 2026-10-30): a short familiar name where one
// exists -- WIB, WITA, WIT, JST, KST, SGT, IST, GMT/BST, CET/CEST, and the
// US ET/CT/MT/PT family with their daylight variants -- else "GMT+N". The
// one helper every screen uses (ui.dateTime({zone:true}) above and the
// Results "Published ..." line both go through it), kept in step with the
// Go public page's own hackZoneAbbr (hack_eventpage.go): that one reads a
// real abbreviation straight off Go's tzdata and falls back to the IANA
// name when tzdata has none. Intl's "short" name is close but not the
// same -- it already gives North America and the UK a real letter code,
// but for Jakarta, Tokyo, Seoul, Singapore, Kolkata and Central Europe it
// is just the bare GMT+N offset with no name at all, so those get a fixed
// lookup instead of trusting Intl.
const ZONE_ABBR = {
  'Asia/Jakarta': 'WIB', 'Asia/Pontianak': 'WIB',
  'Asia/Makassar': 'WITA',
  'Asia/Jayapura': 'WIT',
  'Asia/Tokyo': 'JST',
  'Asia/Seoul': 'KST',
  'Asia/Singapore': 'SGT',
  'Asia/Kolkata': 'IST', 'Asia/Calcutta': 'IST'
}
// Central Europe and London have a real daylight distinction but Intl's
// short name for them is usually just a GMT+N offset too -- these get their
// own DST check instead of a single fixed label.
const AU_ZONES = {
  'Australia/Sydney': ['AEST', 'AEDT'], 'Australia/Melbourne': ['AEST', 'AEDT'], 'Australia/Hobart': ['AEST', 'AEDT'],
  'Australia/Canberra': ['AEST', 'AEDT'], 'Australia/Brisbane': ['AEST'], 'Australia/Adelaide': ['ACST', 'ACDT'],
  'Australia/Darwin': ['ACST'], 'Australia/Perth': ['AWST']
}
const CET_ZONES = ['Europe/Paris', 'Europe/Berlin', 'Europe/Madrid', 'Europe/Rome', 'Europe/Amsterdam', 'Europe/Brussels', 'Europe/Vienna', 'Europe/Prague', 'Europe/Warsaw', 'Europe/Zurich', 'Europe/Stockholm', 'Europe/Copenhagen', 'Europe/Budapest', 'Europe/Luxembourg']
function offsetMinutes(zone, date) {
  try {
    const parts = new Intl.DateTimeFormat('en-US', { timeZone: zone, timeZoneName: 'shortOffset' }).formatToParts(date)
    const raw = (parts.find(p => p.type === 'timeZoneName') || {}).value || ''
    const m = raw.match(/GMT([+-]\d{1,2})(?::?(\d{2}))?/)
    if (!m) return null
    const sign = m[1].startsWith('-') ? -1 : 1
    return sign * (Math.abs(Number(m[1])) * 60 + Number(m[2] || 0))
  } catch { return null }
}
function gmtOffsetLabel(zone, date) {
  const mins = offsetMinutes(zone, date)
  // shortOffset itself isn't supported (Safari before 15.4, older Chromium):
  // there's no offset to build "GMT+N" from, so this returns '' and the
  // caller falls back to whatever Intl's plain 'short' name gave instead of
  // wrongly claiming "GMT" for a zone that isn't one.
  if (mins == null) return ''
  if (mins === 0) return 'GMT'
  const sign = mins < 0 ? '-' : '+'
  const abs = Math.abs(mins)
  const h = Math.floor(abs / 60), m = abs % 60
  return 'GMT' + sign + h + (m ? ':' + String(m).padStart(2, '0') : '')
}
// Daylight is whichever offset differs from January's -- true for every
// zone this helper's table covers (all northern hemisphere).
function inDaylight(zone, date) {
  const jan = offsetMinutes(zone, new Date(date.getFullYear(), 0, 1, 12))
  const cur = offsetMinutes(zone, date)
  return jan != null && cur != null && cur !== jan
}
export function zoneAbbr(zone, date = new Date()) {
  zone = zone || Intl.DateTimeFormat().resolvedOptions().timeZone
  if (!zoneKnown(zone)) return ''
  let raw = ''
  try {
    const parts = new Intl.DateTimeFormat('en-US', { timeZone: zone, timeZoneName: 'short' }).formatToParts(date)
    raw = ((parts.find(p => p.type === 'timeZoneName') || {}).value || '').trim()
    // A real letter name (EST, EDT, PST, GMT...) -- trust Intl, it already
    // gets North America and London right. "GMT+9" and friends are a bare
    // offset, not a name, even though the digits sit next to the letters
    // "GMT".
    if (raw && !/^(GMT|UTC)?[+-]\d/.test(raw)) return raw
  } catch {}
  if (ZONE_ABBR[zone]) return ZONE_ABBR[zone]
  if (zone === 'Europe/London' || zone === 'Europe/Dublin') return inDaylight(zone, date) ? (zone === 'Europe/Dublin' ? 'IST' : 'BST') : 'GMT'
  if (CET_ZONES.includes(zone)) return inDaylight(zone, date) ? 'CEST' : 'CET'
  // Australia: daylight saving runs over the southern summer, so compare with
  // the smaller of the January and July offsets rather than January alone.
  if (AU_ZONES[zone]) {
    const [std, dst] = AU_ZONES[zone]
    if (!dst) return std
    const jan = offsetMinutes(zone, new Date(date.getFullYear(), 0, 1, 12))
    const jul = offsetMinutes(zone, new Date(date.getFullYear(), 6, 1, 12))
    const cur = offsetMinutes(zone, date)
    return (jan != null && jul != null && cur != null && cur > Math.min(jan, jul)) ? dst : std
  }
  // gmtOffsetLabel needs shortOffset support to build "GMT+N"; where that
  // itself isn't supported, fall back to the plain 'short' raw value above
  // rather than silently claiming "GMT" for a zone that isn't one.
  return gmtOffsetLabel(zone, date) || raw
}
// Apply to a hackathon (owner, 2026-10-31, D7): the fixed label sets for the
// two optional applicant fields, shared by the apply form and People's
// application cards so the two screens never drift on wording.
// Hyphen, not en dash: matches hackAgeRangeLabels in hack_apply.go exactly
// (the apply form shows the server's own fields.age_range labels when
// present; this is only the fallback/for People's facts line, and the two
// must read identically wherever they appear).
export const AGE_RANGE_LABELS = { under_18: 'Under 18', '18_24': '18-24', '25_34': '25-34', '35_plus': '35+' }
export const GENDER_LABELS = { woman: 'Woman or girl', man: 'Man or boy', non_binary: 'Non-binary', another: 'Another gender', prefer_not_to_say: 'Prefer not to say' }
// The applicant's stored ISO 3166-1 alpha-2 country code, shown as a name in
// the browser's own language (D6). '' in, '' out. Falls back to the bare
// code if Intl.DisplayNames can't resolve it (old engine, or an unknown
// code) -- never a blank for a code that is actually set, so a person
// reading People always sees something rather than nothing.
// Cosmetic #4 (Grok re-test, round 4): a non-standard navigator.language
// (seen live: 'en-US@posix') threw inside the Intl.DisplayNames constructor
// -- and a locale list throws on the whole list the moment any one entry is
// bad, so the 'en' fallback alongside it never got a chance to run, and
// every code fell straight to the bare-code catch. navigator.language is
// tried alone first; only if that construction itself throws does 'en' get
// its own, separate attempt.
export function countryName(code) {
  if (!code) return ''
  try { return new Intl.DisplayNames([navigator.language], { type: 'region' }).of(code) || code }
  catch {
    try { return new Intl.DisplayNames(['en'], { type: 'region' }).of(code) || code }
    catch { return code }
  }
}
// Cosmetic #1: a team or event name that already ends in s/S takes a bare
// apostrophe ("Alpha Testers'"), never another s ("Alpha Testers's").
// Shared so every screen that puts a team or event name through 's --
// organiser and participant/judge alike -- uses the same rule.
export function possessive(name) {
  const s = String(name || '')
  if (!s) return ''
  return s + (/s$/i.test(s) ? '’' : '’s')
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
// The host name of a link a participant typed in (a team's site, a video,
// a piece of code), to show next to the button that opens it -- plan
// section 6, "Links": "show the full host name next to the button", so a
// judge or organiser sees where a click is about to take them before it
// does. '' for anything safeHref would already refuse, so a caller never
// needs its own separate check.
export function hostLabel(url) {
  const href = safeHref(url)
  if (!href) return ''
  try { return new URL(href).hostname } catch { return '' }
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
// index of the button focused on open, or the string 'first-field' -- the
// first focusable element in `body` that isn't one of the footer buttons
// (every form dialog, via formDialog's own default), falling back to the
// first button when body has no field (review R3: a mouse click on the
// title field used to Tab straight to Cancel, and the body's own
// `autofocus` attribute is a no-op once the markup is inserted via
// innerHTML after the page has loaded).
// Review R3: Tab must cycle every focusable element in the dialog -- the
// body's own fields as well as the footer buttons -- not just the buttons,
// and Enter in a single-line text field (not a textarea, and not a
// checkbox/radio/file input, where Enter already has its own meaning)
// submits the dialog's primary action (the button carrying 'btn primary',
// i.e. Save) the same way a native <form> would.
const FOCUSABLE_SEL = 'a[href],button,input,select,textarea,[tabindex]'
function focusablesIn(box) {
  return Array.from(box.querySelectorAll(FOCUSABLE_SEL)).filter(el => !el.disabled && el.tabIndex !== -1 && !el.hidden)
}
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
    function primaryButton() { return els.find(b => b.classList.contains('primary')) || els[els.length - 1] }
    function keydown(e) {
      if (e.key === 'Escape') { e.preventDefault(); done(escapeValue); return }
      if (e.key === 'Tab') {
        const list = focusablesIn(box)
        if (!list.length) return
        e.preventDefault()
        const index = list.indexOf(document.activeElement)
        const next = index === -1 ? 0 : (index + (e.shiftKey ? list.length - 1 : 1)) % list.length
        list[next].focus()
        return
      }
      if (e.key === 'Enter' && !e.shiftKey) {
        const t = document.activeElement
        // A single-line field only: never a textarea (its own Enter means a
        // new line), never a checkbox/radio/file (Enter has no role there),
        // and never inside a <select> (native Enter/arrow behaviour stays).
        if (t && t.tagName === 'INPUT' && !['checkbox', 'radio', 'file'].includes(t.type)) {
          e.preventDefault()
          primaryButton()?.click()
        }
      }
    }
    scrim.addEventListener('click', e => { if (e.target === scrim) done(escapeValue) })
    document.addEventListener('keydown', keydown, true)
    document.body.append(scrim)
    if (focus === 'first-field') {
      const list = focusablesIn(box)
      const field = list.find(el => !acts.contains(el))
      ;(field || els[0])?.focus()
    } else {
      els[focus]?.focus()
    }
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

// Unsaved-changes guard for the one-Save forms (Basics, About): "another
// tab, the sidebar, Back" asks before leaving, closing the browser tab uses
// beforeunload. Bound once (module-level); `setDirtyGuard` is called on
// every render of a form sub-page with a fresh `getDirty` closure over
// that render's own fields, `clearDirtyGuard` by every other sub-page (and
// effectively superseded the moment a new Basics/About render calls
// setDirtyGuard again).
// R8 (self-expiry): `getDirty` is only ever trusted while the ctx that
// installed it is still alive (ctx.alive(), the same generation check
// every screen already uses) -- a route change (another tab, Back/Forward,
// a ctx.refresh()) leaves the old render's ctx dead, so `currentDirty()`
// drops the guard instead of asking about a form nobody is looking at any
// more.
// R9: Sign out (`[data-signout]`) is left to its own click handler
// (shell.js) rather than intercepted here as if it were an ordinary
// same-origin app link -- it has to actually run the sign-out flow, not a
// `ctx.go()` SPA navigation to '/'. A link to a page this app doesn't
// render as a screen (isAppRoute false, e.g. /directory, /get-started) gets
// a real navigation (location.assign) after confirming, instead of
// `ctx.go()`, which would otherwise mount the Home screen at the wrong URL.
// R8 (Back/Forward): app.js's popstate handler calls confirmDiscardForLeave
// before accepting the browser's Back/Forward -- see app.js's own note.
// Lives here, not in a screen util, because app.js (never a screen) needs
// to call confirmDiscardForLeave/clearDirtyGuard too, and app.js only ever
// imports this shared module.
// `lastPath` is the URL last known to be safely showing (no unconfirmed
// dirty form) -- tracked here, not in app.js or organiser-event-page.js,
// so every caller that changes the URL (app.js's go(), and organiser-
// event-page.js's own light-column subnav navigation, R19) can call
// notePath() right after and confirmDiscardForLeave's own "push the old
// URL back" always has an accurate path to restore, regardless of which
// module did the pushState.
let guardCtx = null, guardGetDirty = null, guardBound = false
let lastPath = typeof location !== 'undefined' ? location.pathname + location.search + location.hash : ''
export function notePath(path) { lastPath = path ?? (location.pathname + location.search + location.hash) }
export function setDirtyGuard(ctx, getDirty) { guardCtx = ctx; guardGetDirty = getDirty; bindGuardOnce() }
export function clearDirtyGuard() { guardGetDirty = null }
function currentDirty() {
  if (!guardGetDirty) return null
  if (guardCtx && guardCtx.alive && !guardCtx.alive()) { guardGetDirty = null; return null }
  return guardGetDirty()
}
function bindGuardOnce() {
  if (guardBound) return
  guardBound = true
  document.addEventListener('click', onGuardClick, true)
  window.addEventListener('beforeunload', e => {
    if (currentDirty()) { e.preventDefault(); e.returnValue = '' }
  })
}
async function onGuardClick(e) {
  const info = currentDirty()
  if (!info) return
  const a = e.target.closest('a[href]')
  if (!a || e.defaultPrevented || e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey || a.target || a.hasAttribute('download')) return
  let url
  try { url = new URL(a.href) } catch { return }
  if (url.origin !== location.origin) return
  // A same-document jump (an in-page #id link) and a link back to exactly
  // where we already are both need no guard.
  if (url.pathname === location.pathname && url.search === location.search) return
  e.preventDefault()
  e.stopPropagation()
  const ok = await askConfirm({ title: `Discard your changes to ${info.label}?`, body: 'Your changes will be lost.', confirmLabel: 'Discard', cancelLabel: 'Keep editing', danger: true })
  if (!ok) return
  guardGetDirty = null
  // R9: Sign out (`[data-signout]`) needs its own handler (shell.js) to
  // actually run -- re-dispatching the same click, now that the guard is
  // cleared, lets it go through for real on the second pass (this
  // listener itself sees it again first, via the capture phase, and no-ops
  // immediately since currentDirty() is now null), instead of treating it
  // as an ordinary same-origin app link and doing an SPA ctx.go('/'),
  // which used to land on the Home screen while still signed in. A link to
  // a page this SPA doesn't render as a screen at all (isAppRoute false,
  // e.g. /directory, /get-started) gets a real navigation instead of
  // ctx.go(), which would otherwise mount the wrong screen at that URL.
  if (a.hasAttribute('data-signout')) { a.click(); return }
  if (isAppRoute(url.pathname)) { lastPath = url.pathname + url.search + url.hash; guardCtx.go(url.pathname + url.search + url.hash) }
  else location.assign(url) // not a screen this SPA renders -- a real navigation, not ctx.go()
}
// Exported for app.js's popstate handler: resolves true if it is fine to
// proceed with a Back/Forward navigation that already changed `location`
// (no dirty form, or the person confirmed discarding it) -- in both cases
// `lastPath` is brought up to date. Resolves false if the person chose to
// keep editing, having already pushed the previous URL back onto
// `location` itself, so app.js only needs to skip calling route().
export async function confirmDiscardForLeave() {
  const info = currentDirty()
  const target = location.pathname + location.search + location.hash
  if (!info) { lastPath = target; return true }
  const from = lastPath
  const ok = await askConfirm({ title: `Discard your changes to ${info.label}?`, body: 'Your changes will be lost.', confirmLabel: 'Discard', cancelLabel: 'Keep editing', danger: true })
  if (!ok) { history.pushState({}, '', from); return false }
  guardGetDirty = null
  lastPath = target
  return true
}
