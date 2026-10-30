// Shared helpers for the judge screens (home, queue, scoring, rubric,
// public-judge). Not a kit file: BUILD.md keeps screens/_judge-util.js as
// the one allowed place for cross-screen code so app.js/api.js/ui.js stay
// untouched.
import { esc } from '../ui.js'

// Error codes these screens' endpoints can return, in the old app's plain
// language. Anything else falls back to the server's own message text.
export const ERRORS = {
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
  conflict_not_found: "That conflict wasn't found — it may already be gone.",
  criterion_not_found: 'That criterion is not in the event’s current rubric.',
  invalid_request: "Something in that request wasn't right. Reload and try again.",
  rate_limited: 'Too many changes at once. Wait a moment and try again.'
}

export function errText(r) {
  const code = r && r.data && r.data.code
  if (code && ERRORS[code]) return ERRORS[code]
  if (r && r.status >= 500) return 'Something went wrong. Try again.'
  if (r && r.data && typeof r.data.error === 'string' && r.data.error) return r.data.error
  return 'Something went wrong.'
}

// Only ever render http(s) links as real links.
export function safeHref(url) {
  const s = String(url || '').trim()
  return /^https?:\/\//i.test(s) ? s : ''
}

export function newTabLink(url, label, cls) {
  const href = safeHref(url)
  if (!href) return ''
  return `<a class="${cls || 'btn'}" href="${esc(href)}" target="_blank" rel="noopener noreferrer">${esc(label)}</a>`
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

export function dateRange(ui, info) {
  if (!info || !info.starts_at) return ''
  const shim = { time_zone: info.time_zone }
  const start = ui.dateTime(info.starts_at, shim)
  const end = info.ends_at ? ui.dateTime(info.ends_at, shim) : ''
  return end ? `${start} – ${end}` : start
}

// Splits free text (about/rules/prizes, coc) into paragraphs on blank
// lines, since the kit has no "preserve newlines" utility class.
export function paragraphs(text) {
  return String(text || '').split(/\n{2,}|\r\n\r\n/).map(s => s.trim()).filter(Boolean)
}
