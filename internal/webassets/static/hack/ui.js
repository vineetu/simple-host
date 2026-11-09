export const esc = value => String(value ?? '').replace(/[&<>"']/g, c => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]))
export const stages = { draft: 'Not open yet', open: 'Sign-up open', building: 'Building', closed: 'Entries closed', judging: 'Judging', results: 'Results', archived: 'Ended', ended: 'Ended' }
export const stageLabel = stage => stages[stage] || stage || ''
export const roleLabel = role => ({organiser:'Organiser',participant:'Participant',judge:'Judge'}[role] || '')
// A7: split on whitespace, but only count words that have an actual letter
// or number -- "Mock · Voting" otherwise takes the "·" as its second "word"
// and returns "M·" instead of "MV".
export const initials = name => String(name || '').split(/\s+/).filter(w => /[\p{L}\p{N}]/u.test(w)).slice(0,2).map(w => w.match(/[\p{L}\p{N}]/u)[0]).join('').toUpperCase()
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
// Event boundaries use minute precision, like the schedule pickers. An
// empty time -- Basics, or the API's own date-only save -- always means
// local midnight for both Starts and Ends (hack_create_event: "A date means
// midnight in time_zone"); a midnight Ends still lasts the whole of that day
// (isLocalMidnight/nextDeadline below), it just isn't stored any differently
// from a midnight Starts.
export function eventBoundaryInput(value, event) {
  const local = localInputValue(value, event)
  return { date: local.slice(0, 10), time: isLocalMidnight(value, event) ? '' : local.slice(11) }
}
export function eventBoundaryValue(date, time) {
  return date ? date + 'T' + (time || '00:00') : ''
}
export function eventBoundaryDate(value, event) {
  return dateTime(value, event, { dateOnly: isLocalMidnight(value, event) })
}
// True when an instant falls exactly on midnight in the event's zone: Basics
// saves a date-only start or end as that midnight. Checks seconds too, like
// the server's fixed-row classifier (events.Midnight/hackFixedSchedule), so
// the two never disagree on a stray 00:00:30.
export function isLocalMidnight(value, event) {
  const p = zonedParts(value, typeof event === 'string' ? event : event?.time_zone)
  return !!p && p.h === '00' && p.min === '00' && p.s === '00'
}
// The UTC instant of the next local midnight after `value`'s own local
// calendar date, in the event's zone -- the same "a date-only end lasts to
// here" boundary the server computes (hackFixedSortTime/events.EventOver).
// Not a fixed +24h: across a DST change the real gap is 23 or 25 hours, so
// this reads `value`'s local Y/M/D, advances the calendar date by one (plain
// arithmetic, no zone involved), then resolves that calendar date's local
// midnight back to a UTC instant with the standard two-pass offset lookup
// (guess assuming no DST shift, then correct once if the zone's offset at
// that guess turns out to differ). null when `value`/zone can't be read.
// This is a display/sort-only helper, not the save-round-trip inverse
// localInputValue's own comment warns against -- it never reaches the server.
export function nextLocalMidnight(value, event) {
  const zone = typeof event === 'string' ? event : event?.time_zone
  const p = zonedParts(value, zone)
  if (!p) return null
  const z = zoneKnown(zone) ? (zone || 'UTC') : 'UTC'
  // Tomorrow's Y/M/D, as plain calendar arithmetic (UTC here carries no
  // meaning beyond "a scratch Date to let day/month/year overflow roll
  // over correctly").
  const tomorrow = new Date(Date.UTC(Number(p.y), Number(p.m) - 1, Number(p.d) + 1))
  const guess = Date.UTC(tomorrow.getUTCFullYear(), tomorrow.getUTCMonth(), tomorrow.getUTCDate(), 0, 0, 0)
  const offset1 = offsetMinutes(z, new Date(guess))
  if (offset1 == null) return guess
  let instant = guess - offset1 * 60000
  const offset2 = offsetMinutes(z, new Date(instant))
  if (offset2 != null && offset2 !== offset1) instant = guess - offset2 * 60000
  return instant
}
// The next of start, entry deadline and end still ahead, as
// [label, value, dateOnly]. A date-only start or end (local midnight) shows
// as a date with no "12:00 am", and a date-only end lasts the whole of that
// day -- its own next local midnight (nextLocalMidnight, DST-correct; not a
// flat +24h) -- so it stays "next" until the day is over (full-test fix
// 2026-11-02).
export function nextDeadline(event) {
  const now = Date.now()
  return [['Starts', event?.starts_at], ['Entries close', event?.submission_deadline], ['Ends', event?.ends_at]]
    .filter(([, value]) => value && !Number.isNaN(new Date(value).getTime()))
    .map(([label, value]) => {
      const dateOnly = label !== 'Entries close' && isLocalMidnight(value, event)
      const endOfDay = label === 'Ends' && dateOnly ? nextLocalMidnight(value, event) : null
      const until = endOfDay ?? new Date(value).getTime()
      return [label, value, dateOnly, until]
    })
    .filter(r => r[3] > now).sort((a, b) => a[3] - b[3])
    .map(r => r.slice(0, 3))[0]
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
  scoring_not_open: 'Scoring opens when judging starts.',
  judging_setup_locked: 'Judging has started. Move the event back to Entries closed to change this.',
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
  invalid_answers: "Something in that request wasn't right. Reload and try again.",
  // Teams redesign (2026-10-31): admins, locking, the team link.
  team_admin_only: "Only the team's admins can do that.",
  team_locked: "This team isn't taking new people.",
  last_team_admin: 'Make someone else admin first.',
  team_kept: "This team can't take new members.",
  team_link_not_found: "That link or code doesn't match a team in this event."
}
// errText: without `codes`, the server's own message (every hack.go /
// hack_*.go refusal already writes a plain sentence) always wins, since it
// is usually more specific than anything a client could guess. Pass `codes`
// (a code -> copy map, usually
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
// writeLockReason: a platform-admin inspection or a taken-down event blocks
// every write; an archived (ended) event blocks the same controls with its
// own explanation; reads stay possible either way.
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
    new Intl.DateTimeFormat('en-US', { timeZone: zone, year: 'numeric', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', second: '2-digit', hourCycle: 'h23' })
      .formatToParts(d).forEach(p => { o[p.type] = p.value })
  } catch { return null }
  if (!o.year || !o.month || !o.day || !o.hour || !o.minute) return null
  // Some engines format local midnight as "24" instead of "00" with
  // hourCycle: 'h23'; only one of the three original implementations
  // (organiser-entries.js's localInputValue) guarded against this.
  return { y: o.year, m: o.month, d: o.day, h: o.hour === '24' ? '00' : o.hour, min: o.minute, s: o.second || '00' }
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

// Typed confirm: same shape as ui.confirm, but the confirm button stays
// disabled until the person types `matchText` exactly, same as Settings'
// own Delete event (organiser-settings.js) -- for a destructive route that
// already requires `confirm: <text>` server-side (SIM-340 Round-5 C3:
// Delete scores), so one stray click can't do it by accident. Hand-rolled
// like leave.js's own sheets rather than built on ui.dialog(): the confirm
// button's disabled state has to track an input as the person types, and
// dialog()'s button list is fixed the moment it opens.
export function confirmTyped({ title, body, matchText, confirmLabel = 'Confirm', cancelLabel = 'Cancel', fieldLabel, danger = true }) {
  return new Promise(resolve => {
    const previous = document.activeElement
    const scrim = document.createElement('div')
    scrim.className = 'scrim'
    const box = document.createElement('div')
    box.className = 'dialog'
    box.setAttribute('role', 'dialog')
    box.setAttribute('aria-modal', 'true')
    box.setAttribute('aria-labelledby', 'ct-h')
    const h2 = document.createElement('h2')
    h2.id = 'ct-h'
    h2.textContent = title
    box.append(h2)
    const nodes = body == null ? []
      : Array.isArray(body) ? body
      : body instanceof Node ? [body]
      : [Object.assign(document.createElement('p'), { textContent: body })]
    box.append(...nodes)
    const field = document.createElement('div')
    field.className = 'field'
    const inputId = 'ct-input'
    const label = document.createElement('label')
    label.htmlFor = inputId
    label.textContent = fieldLabel || `Type ${matchText} to confirm`
    const input = document.createElement('input')
    input.className = 'input mono'
    input.id = inputId
    input.type = 'text'
    field.append(label, input)
    box.append(field)
    const acts = document.createElement('div')
    acts.className = 'acts'
    const cancelBtn = document.createElement('button')
    cancelBtn.type = 'button'; cancelBtn.className = 'btn'; cancelBtn.textContent = cancelLabel
    const okBtn = document.createElement('button')
    okBtn.type = 'button'; okBtn.className = 'btn ' + (danger ? 'danger' : 'primary'); okBtn.textContent = confirmLabel
    okBtn.disabled = true
    acts.append(cancelBtn, okBtn)
    box.append(acts)
    scrim.append(box)
    function done(value) { document.removeEventListener('keydown', keydown, true); scrim.remove(); previous?.focus(); resolve(value) }
    function sync() { okBtn.disabled = input.value.trim() !== matchText }
    input.addEventListener('input', sync)
    cancelBtn.addEventListener('click', () => done(false))
    okBtn.addEventListener('click', () => { if (!okBtn.disabled) done(true) })
    function keydown(e) {
      if (e.key === 'Escape') { e.preventDefault(); done(false); return }
      if (e.key === 'Tab') {
        const list = focusablesIn(box)
        if (!list.length) return
        e.preventDefault()
        const i = list.indexOf(document.activeElement)
        list[(i + (e.shiftKey ? list.length - 1 : 1) + list.length) % list.length].focus()
      }
    }
    scrim.addEventListener('click', e => { if (e.target === scrim) done(false) })
    document.addEventListener('keydown', keydown, true)
    document.body.append(scrim)
    input.focus()
  })
}

// Registrations belong to the rendered DOM and context, including disabled fields.
const forms = new Set()
let guardBound = false, leavePending = null, replayingLink = false
let lastPath = typeof location !== 'undefined' ? location.pathname + location.search + location.hash : ''
export function notePath(path) { lastPath = path ?? (location.pathname + location.search + location.hash) }
function serializeForm(root) {
  return JSON.stringify(Array.from(root.querySelectorAll('input, textarea, select, [role="radio"], [data-dirty-value]'))
    .filter(n => !n.closest('[data-no-dirty]') && !['button', 'submit', 'reset', 'image'].includes(n.type))
    .map(n => [n.tagName, n.id, n.name, n.type,
      n.matches('[data-dirty-value]') ? n.dataset.dirtyValue : n.matches('[role="radio"]') ? n.getAttribute('aria-checked') :
      n.type === 'file' ? Array.from(n.files || [], f => f.name) :
      n.type === 'checkbox' || n.type === 'radio' ? [n.value, n.checked] :
      n.tagName === 'SELECT' ? Array.from(n.selectedOptions, o => o.value) : n.value]))
}
export function trackForm(ctx, root) {
  const registration = { ctx, root, baseline: serializeForm(root) }
  forms.add(registration)
  bindGuardOnce()
  return { snapshot() { return serializeForm(root) }, reset(snap) { registration.baseline = snap ?? serializeForm(root) } }
}
function currentDirty() {
  for (const f of forms) if (!f.root.isConnected || (f.ctx?.alive && !f.ctx.alive())) forms.delete(f)
  return Array.from(forms).some(f => serializeForm(f.root) !== f.baseline)
}
// Also called before programmatic navigation, before it changes the URL.
export function clearFormRegistrations() { forms.clear() }
export async function confirmFormLeave() {
  if (replayingLink) return true
  if (!currentDirty()) return true
  if (!leavePending) leavePending = dialog({
    title: 'Leave without saving?',
    buttons: [
      { label: 'Stay', value: false },
      { label: 'Leave', value: true, className: 'btn danger primary' }
    ], escapeValue: false, focus: 0
  }).finally(() => { leavePending = null })
  return leavePending
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
  if (replayingLink || !currentDirty()) return
  const a = e.target.closest('a[href]')
  if (!a || e.defaultPrevented || e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey || a.target || a.hasAttribute('download') || a.closest('[aria-disabled="true"], [data-menu], [disabled]')) return
  const url = new URL(a.href, location.href)
  if (!['http:', 'https:'].includes(url.protocol)) return
  if (url.origin !== location.origin) return
  if (url.pathname === location.pathname && url.search === location.search && url.hash && !a.hasAttribute('data-signout')) return
  e.preventDefault()
  e.stopPropagation()
  if (await confirmFormLeave()) {
    if (a.hasAttribute('data-signout')) clearFormRegistrations()
    replayingLink = true
    try {
      if (a.dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true, view: window }))) clearFormRegistrations()
    } finally { replayingLink = false }
  }
}
// --- Entries (atlas 2026-11-02, slice 1) --------------------------------

// Fixed tag suggestions for "Tools and AI you used" (owner decision,
// build-plan.md section 12: "a fixed suggestion list, not from other teams'
// tags"). Shared by the entry form's own suggestion buttons
// (participant-entry.js) and ui.entryCard's "ai" tag highlight below, so the
// two never disagree on which typed word counts as an AI tool.
export const AI_SUGGESTIONS = ['Claude', 'ChatGPT', 'Gemini', 'Lovable', 'Copilot', 'Cursor']
export const TOOL_SUGGESTIONS = ['Canva', 'Google Sheets', 'Figma', 'Supabase', 'Python', 'Airtable']
const AI_SUGGESTIONS_LOWER = AI_SUGGESTIONS.map(s => s.toLowerCase())

// The maturity choice's plain words and kit pill class, shared by the entry
// form's three choice cards and the compact preview/judge/organiser card.
export const MATURITY = {
  working: { label: 'Demo-ready', note: 'works end to end', cls: 'mat-working' },
  partial: { label: 'Partly working', note: 'some parts are mocked', cls: 'mat-partial' },
  concept: { label: 'Concept', note: 'designs or a plan, nothing runs yet', cls: 'mat-concept' }
}

// The one entry summary card renderer (build-plan.md section 6:
// "ui.entryCard(entry, {compact, missing}), since four screens use it").
// `entry` takes the shape the server's entryListItem/GET-entry views already
// share: { title, team_name, team_members (a plain "A, B and C" string, or
// '' solo/unknown), track: {name}|null, problem, tagline, audience,
// tools: string[], maturity: ''|'working'|'partial'|'concept' }. Any blank
// line reads "Not filled in yet" (the atlas's own wording), independent of
// whether that line is actually Required for this event -- the card is a
// preview of what judges see, not a rules checker.
// `opts.compact` is the smaller card used inline (Entry's live preview,
// organiser Entries' row-level peek); the full size is the one on Entry's
// read-only view, organiser Entry detail and (slice 2) judge scoring.
// `opts.incomplete` adds the "Incomplete" flag pill (coordinator decision
// 2026-11-02: an entry auto-submitted at the deadline while still missing
// something is judged anyway, flagged, rather than hidden).
export function entryCard (entry, opts = {}) {
  const e = entry || {}
  const art = document.createElement('article')
  art.className = 'entry-card' + (opts.compact ? ' compact' : '')
  const head = document.createElement('div')
  head.className = 'ec-head'
  const t = document.createElement('div')
  t.className = 't'
  const h = document.createElement(opts.compact ? 'h3' : 'h2')
  h.textContent = e.title || 'Untitled'
  t.append(h)
  if (e.team_name) {
    const p = document.createElement('p')
    p.className = 'ec-team'
    p.textContent = e.team_name + (e.team_members ? ' · ' + e.team_members : '')
    t.append(p)
  }
  head.append(t)
  const pills = document.createElement('div')
  pills.className = 'ec-pills'
  if (e.track && e.track.name) pills.append(el('span', { class: 'pill stage', text: e.track.name + ' track' }))
  if (opts.incomplete) pills.append(el('span', { class: 'pill warn', text: 'Incomplete' }))
  if (pills.childNodes.length) head.append(pills)
  art.append(head)
  const dl = document.createElement('dl')
  dl.className = 'ec-rows'
  function row (label, valueNode, isBlank) {
    dl.append(el('dt', { text: label }))
    const dd = document.createElement('dd')
    if (isBlank) { dd.className = 'missing'; dd.textContent = 'Not filled in yet' }
    else if (typeof valueNode === 'string') dd.textContent = valueNode
    else dd.append(valueNode)
    dl.append(dd)
  }
  row('Problem', e.problem, !e.problem)
  row('Solution', e.tagline, !e.tagline)
  row('For', e.audience, !e.audience)
  const tools = Array.isArray(e.tools) ? e.tools.filter(Boolean) : []
  if (tools.length) {
    const ul = document.createElement('ul')
    ul.className = 'tags'
    tools.forEach(tool => {
      const li = document.createElement('li')
      li.className = 'tag' + (AI_SUGGESTIONS_LOWER.includes(String(tool).toLowerCase()) ? ' ai' : '')
      li.textContent = tool
      ul.append(li)
    })
    row('Tools & AI', ul, false)
  } else row('Tools & AI', '', true)
  if (e.maturity && MATURITY[e.maturity]) {
    const span = document.createElement('span')
    const m = MATURITY[e.maturity]
    const pill = el('span', { class: 'pill ' + m.cls, text: m.label })
    const note = el('span', { class: 'muted small', text: ' ' + m.note })
    span.append(pill, note)
    row('Works today', span, false)
  } else row('Works today', '', true)
  art.append(dl)
  return art
}

// Live, ticking deadline countdown (build-plan.md section 6: "Countdown in
// a shared ui.countdown(el, deadline, serverNow), ticking each minute and
// each second in the last hour, with the amber and red classes": amber
// under 24 hours, red under 1 hour, per the atlas). `el` is
// the `<span class="countdown">` itself (prototype markup: a `.v` child for
// the number, read back out if present, else `el` is written to directly).
// `serverNow` corrects a wrong phone clock: the skew between it and
// `Date.now()` at call time is held fixed for the life of this countdown.
// `opts.quiet` (participant-home.js, once already submitted: the remaining
// time is "time left to make changes", not a deadline at risk) suppresses
// the amber/red classes -- the number still ticks, it just never colours.
// Stops its own timer once the element leaves the DOM (a re-render mounts a
// fresh element and a fresh countdown call; this one has nothing left to
// update) or once the deadline is reached.
export function countdown (el, deadline, serverNow, opts = {}) {
  if (!el) return
  const target = deadline ? new Date(deadline).getTime() : NaN
  if (!deadline || Number.isNaN(target)) { el.textContent = ''; return }
  const skew = serverNow ? new Date(serverNow).getTime() - Date.now() : 0
  const HOUR = 3600000, DAY = 86400000
  let timer = null
  function fmt (ms) {
    if (ms <= 0) return '0 s'
    let s = Math.round(ms / 1000)
    const d = Math.floor(s / 86400); s -= d * 86400
    const h = Math.floor(s / 3600); s -= h * 3600
    const m = Math.floor(s / 60); s -= m * 60
    if (d >= 2) return d + ' days'
    if (d === 1) return '1 d ' + h + ' h ' + m + ' m'
    if (h > 0) return h + ' h ' + m + ' m'
    if (m > 0) return m + ' m ' + String(s).padStart(2, '0') + ' s'
    return s + ' s'
  }
  function tick () {
    if (!el.isConnected) { if (timer) clearTimeout(timer); return }
    const ms = target - (Date.now() + skew)
    const slot = el.querySelector('.v') || el
    slot.textContent = fmt(ms)
    const urgent = !opts.quiet && ms > 0 && ms <= HOUR
    const soon = !opts.quiet && ms > 0 && !urgent && ms <= DAY
    el.classList.toggle('urgent', urgent)
    el.classList.toggle('soon', soon)
    if (ms <= 0) { if (timer) clearTimeout(timer); return }
    timer = setTimeout(tick, ms <= HOUR ? 1000 : 60000)
  }
  tick()
}

// Back/Forward has already changed location; Stay restores the previous URL.
export async function confirmDiscardForLeave() {
  const target = location.pathname + location.search + location.hash
  const from = lastPath
  if (!await confirmFormLeave()) { history.pushState({}, '', from); return false }
  clearFormRegistrations()
  lastPath = target
  return true
}
