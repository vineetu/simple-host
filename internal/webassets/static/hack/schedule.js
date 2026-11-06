// A schedule that acts (owner, 2026-11-02; atlas: organiser-event-page
// Schedule, participant-schedule, participant-home, organiser-overview).
//
// GET /content answers `schedule` (the rows the organiser typed, each with an
// optional `prompt`) and `fixed_schedule` (the event's real times, computed by
// the server from the settings, marked fixed: true, never stored). This module
// merges the two, labels Now and Next, works out the Home nudge ("Demos starts
// in 12 minutes", "Build time is on now") and words the organiser's next-step
// cue. Nothing here ever moves a stage: the schedule only prompts.
import { href } from './nav.js'
import { eventBoundaryIsDateOnly } from './ui.js'

// The forward moves a row may prompt, with the next-step button's own words
// (hack_schedule.go hackScheduleMoves). Ending the event is left out.
export const MOVES = {
  open: 'Open sign-up',
  building: 'Start building',
  closed: 'Close entries',
  judging: 'Start judging',
  results: 'Finish judging',
  publish_results: 'Publish results'
}

// Where the organiser changes a fixed row.
export function setInLink(setIn, slug) {
  if (setIn === 'basics') return { label: 'Basics', href: href('organiser', slug, 'event-page') + '&tab=basics' }
  if (setIn === 'entries') return { label: 'Entries', href: href('organiser', slug, 'entries') }
  if (setIn === 'results') return { label: 'Results', href: href('organiser', slug, 'results') }
  // judging_ends_at (notices contract, CHANGE 2026-11-05): set on
  // organiser-judging.js, next to the rubric and tie-break.
  if (setIn === 'judging') return { label: 'Judging', href: href('organiser', slug, 'judging') }
  return null
}

const ms = iso => new Date(iso).getTime()
const valid = iso => !!iso && !Number.isNaN(ms(iso))

// One list, in time order. Editable rows keep a reference to the stored
// object (`src`) so an edit or remove finds it by identity.
export function merge(schedule, fixed, event) {
  const rows = []
  for (const it of schedule || []) {
    if (!valid(it.start_at)) continue
    rows.push({ ...it, fixed: false, all_day: false, src: it })
  }
  for (const it of fixed || []) {
    if (!valid(it.start_at)) continue
    const boundary = it.key === 'event_start' || it.key === 'event_end'
    rows.push({ ...it, fixed: true, ...(boundary && event ? { all_day: eventBoundaryIsDateOnly(it.start_at, event, it.key === 'event_end') } : {}) })
  }
  return rows.sort((a, b) => ms(a.start_at) - ms(b.start_at))
}

// Now: a row with a window that contains this moment. Next: the first row
// still to start. A date-only row is neither, and a moment (a deadline) is
// never Now.
export function label(rows, now = Date.now()) {
  let nowIdx = -1, nextIdx = -1
  rows.forEach((r, i) => {
    r.now = false; r.next = false
    if (r.all_day) return
    const s = ms(r.start_at)
    const e = valid(r.end_at) ? ms(r.end_at) : null
    if (nowIdx < 0 && e !== null && s <= now && now < e) nowIdx = i
    if (nextIdx < 0 && s > now) nextIdx = i
  })
  if (nowIdx >= 0) rows[nowIdx].now = true
  if (nextIdx >= 0) rows[nextIdx].next = true
  return rows
}

const SOON_MS = 15 * 60 * 1000

// The Home nudge: the first row starting within 15 minutes, and the row on
// now that started last. Either may be null.
export function nudge(rows, now = Date.now()) {
  let soon = null, on = null
  for (const r of rows) {
    if (r.all_day) continue
    const s = ms(r.start_at)
    const e = valid(r.end_at) ? ms(r.end_at) : null
    if (s > now && s - now <= SOON_MS && (!soon || s < ms(soon.start_at))) soon = r
    if (e !== null && s <= now && now < e && (!on || s > ms(on.start_at))) on = r
  }
  return { soon, on }
}

function minutes(n) { return n + (n === 1 ? ' minute' : ' minutes') }

// "Demos starts in 12 minutes", or the fixed row's own wording.
export function soonText(r, now = Date.now()) {
  const m = minutes(Math.max(1, Math.ceil((ms(r.start_at) - now) / 60000)))
  switch (r.key) {
    case 'entry_deadline':
    case 'team_deadline': return 'Entries are due in ' + m
    case 'event_start': return 'The event starts in ' + m
    case 'event_end': return 'The event ends in ' + m
    case 'judging_end': return 'Judging closes in ' + m
    default: return r.title + ' starts in ' + m
  }
}
export function onText(r) { return r.title + ' is on now' }

// The time half of ui.dateTime ("6:00 pm"), so every time keeps one format.
export function timeOnly(ui, iso, ev) {
  const full = ui.dateTime(iso, ev)
  const i = full.lastIndexOf(', ')
  return i >= 0 ? full.slice(i + 2) : full
}
function sameDay(ui, a, b, ev) { return ui.dateOnly(a, ev) === ui.dateOnly(b, ev) }

// The nudge as a line of HTML ('' when there is nothing to say).
export function nudgeHTML(ui, ev, rows, scheduleHref, now = Date.now()) {
  const { soon, on } = nudge(rows, now)
  if (!soon && !on) return ''
  const parts = []
  if (soon) parts.push(`<b>${ui.esc(soonText(soon, now))}.</b>`)
  if (on) {
    const until = sameDay(ui, new Date(now).toISOString(), on.end_at, ev) ? timeOnly(ui, on.end_at, ev) : ui.dateTime(on.end_at, ev)
    parts.push(soon ? `${ui.esc(onText(on))}.` : `<b>${ui.esc(onText(on))}.</b> Until ${ui.esc(until)}.`)
  }
  const link = scheduleHref ? ` <a href="${ui.esc(scheduleHref)}">See schedule</a>` : ''
  return `<div class="note info" role="status" id="sched-nudge">${parts.join(' ')}${link}</div>`
}

// Keeps a nudge line current while its screen is up: the minutes count down
// and the line appears or goes as rows start and end. Stops on its own once
// the screen is gone.
export function keepNudgeFresh(ctx, host, paint) {
  const timer = setInterval(() => {
    if (!ctx.alive() || !host.isConnected) { clearInterval(timer); return }
    paint()
  }, 30000)
}

// The organiser's next-step heading when a schedule row lines up with the
// move (next_step.cue), in the event's zone. Mirrors the server's own cue
// text, which the AI apps read.
export function cueHeading(ui, ev, step, now = Date.now()) {
  const c = step && step.cue
  if (!c) return ''
  const label = MOVES[step.kind === 'publish_results' ? 'publish_results' : step.stage] || step.label
  const nowIso = new Date(now).toISOString()
  const at = sameDay(ui, c.start_at, nowIso, ev) ? timeOnly(ui, c.start_at, ev) : ui.dateTime(c.start_at, ev)
  let phrase
  switch (c.key) {
    case 'entry_deadline': phrase = c.now ? 'Entries are due now' : 'Entries were due at ' + at; break
    case 'event_start': phrase = c.now ? 'The event starts now' : 'The event started at ' + at; break
    case 'event_end': phrase = c.now ? 'The event ends now' : 'The event ended at ' + at; break
    case 'judging_end': phrase = c.now ? 'Judging closes now' : 'Judging closed at ' + at; break
    default: phrase = c.now ? c.title + ' is now' : c.title + ' was at ' + at
  }
  return phrase + '. ' + label + '?'
}
