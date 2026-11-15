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
