// Shared helpers for the judge screens (home, queue, scoring, rubric,
// public-judge). Not a kit file: BUILD.md keeps screens/_judge-util.js as
// the one allowed place for cross-screen code so app.js/api.js/ui.js stay
// untouched.
import { esc } from '../ui.js'
import * as ui from '../ui.js'

// errText(r) / errText(r, fallback): the shared ui.errText, with this
// group's codes (ui.ERROR_MESSAGES already carries every code these screens
// can see, verbatim) taking priority over the server's own message text --
// matching this group's original behaviour. Kept as a one-line adapter so
// every call site here keeps its old, single-argument shape.
export const errText = (r, fallback) => ui.errText(r, { codes: ui.ERROR_MESSAGES, fallback })

// Only ever render http(s) links as real links. round2-shared.md gives this
// a shared home now (ui.safeHref, identical check) -- re-exported under its
// old name so every call site in this file and its importers keeps working.
export const safeHref = ui.safeHref

// The host name next to the label (plan section 6, "Links"): a judge or
// organiser sees where "Open site"/"Watch video"/"View code" actually
// goes, since every one of these is a link a participant typed in, before
// a click leaves the app.
export function newTabLink(url, label, cls) {
  const href = safeHref(url)
  if (!href) return ''
  const host = ui.hostLabel(href)
  const shown = host ? `${label} (${host})` : label
  return `<a class="${cls || 'btn'}" href="${esc(href)}" target="_blank" rel="noopener noreferrer">${esc(shown)}</a>`
}

// The team's own site for this judge to open: a pinned snapshot wins over
// the always-changing live site (open-entry-website-while-judging).
export function siteHref(row) {
  if (!row) return ''
  if (row.pinned_url) return safeHref(row.pinned_url)
  if (row.site_exists && row.site_url) return safeHref(row.site_url)
  return ''
}

// Sum of this judge's own points and the rubric's max, for a "scored X/Y"
// line. There is no server-computed aggregate for one judge's own total, so
// this is a plain sum across criteria, not the weighted event score.
export function scoreTotals(scores, rubric) {
  const max = (rubric || []).reduce((sum, c) => sum + (Number(c.max_points) || 0), 0)
  const points = (scores || []).reduce((sum, s) => sum + (Number(s.points) || 0), 0)
  return { points, max }
}

// #16: the public judge page showed every event's start/end with a time
// ("12:00 am – …") even though this invite preview only ever had a date to
// show, not a real kickoff/close time -- ui.dateOnly, not ui.dateTime.
export function dateRange(uiArg, info) {
  if (!info || !info.starts_at) return ''
  const shim = { time_zone: info.time_zone }
  const start = uiArg.dateOnly(info.starts_at, shim)
  const end = info.ends_at ? uiArg.dateOnly(info.ends_at, shim) : ''
  return end ? `${start} – ${end}` : start
}

// Splits free text (about/rules/prizes, coc) into paragraphs on blank
// lines, since the kit has no "preserve newlines" utility class.
export function paragraphs(text) {
  return String(text || '').split(/\n{2,}|\r\n\r\n/).map(s => s.trim()).filter(Boolean)
}
