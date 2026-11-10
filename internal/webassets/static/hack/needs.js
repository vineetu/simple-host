// What waits on the person (atlas 2026-11-02, audit item 7): the server's
// needs_you items (GET /v1/hack/events/{slug}, GET /v1/hack/todos) turned
// into links, the participant's Needs you card, and the client-only unread
// count for Announcements. The server writes each item's text, so the
// website and the AI apps (hack_get_my_todos) always say the same thing.
import { href } from './nav.js'

// The screen an item opens, inside the person's own area of the event.
export function needHref(role, slug, item) {
  const section = item.section === 'home' ? undefined : item.section
  return href(role, slug, section)
}

// A short line under the item: the rest of the server's text after its first
// sentence (a take-down's reason, an admin's what-you-can-do), else where
// the link goes. .main b stays one line on desktop, so the long part wraps
// in the span instead.
// `queue` (judge section, added for judging_open/judging_reminder) and
// `team` already covering team_message -- notices contract 2026-11-05.
const OPEN = { team: 'Open Team', website: 'Open Website', entry: 'Open Entry', people: 'Review applications', home: '', queue: 'Open queue' }
export function splitNeed(item) {
  const text = String(item.text || '')
  if (item.kind === 'team_message') return [text, OPEN[item.section] || '']
  const i = text.indexOf('. ')
  if (i > 0) return [text.slice(0, i + 1), text.slice(i + 2)]
  return [text, OPEN[item.section] || '']
}

// Sum of the counts of these kinds, for a sidebar badge.
export function needCount(items, kinds) {
  return (items || []).filter(n => kinds.includes(n.kind)).reduce((s, n) => s + (n.count || 1), 0)
}

// Unseen news lines (the participant's Overview badge).
export function newsCount(items) {
  return (items || []).filter(n => n.dismissable).length
}

// The participant's Needs you card: one row per item, news rows with a
// Dismiss button. '' when nothing waits.
export function needsCard(ui, role, slug, items) {
  if (!items || !items.length) return ''
  const esc = ui.esc
  const rows = items.map(item => {
    const [title, sub] = splitNeed(item)
    const link = esc(needHref(role, slug, item))
    const label = `<b>${esc(title)}</b>${sub ? `<span>${esc(sub)}</span>` : ''}`
    if (!item.dismissable) return `<a class="r" href="${link}"><span class="main">${label}</span></a>`
    return `<div class="r" data-notice="${esc(item.id)}"><a class="main" href="${link}">${label}</a><button type="button" class="btn sm" data-dismiss="${esc(item.id)}" aria-label="Dismiss: ${esc(title)}">Dismiss</button></div>`
  }).join('')
  return `<section class="card" id="needs-you" aria-labelledby="needs-you-h"><h2 id="needs-you-h">Needs you</h2><div class="list">${rows}</div></section>`
}

// Wires the card's Dismiss buttons: POST .../notices/seen for that one line,
// then onChange() (the screen re-renders, so the sidebar badge follows).
export function bindDismiss(main, client, ui, slug, onChange) {
  main.querySelectorAll('[data-dismiss]').forEach(btn => btn.addEventListener('click', async () => {
    btn.disabled = true
    try {
      const r = await client.api('POST', '/v1/hack/events/' + encodeURIComponent(slug) + '/notices/seen', { ids: [btn.dataset.dismiss] })
      if (!r.ok) { btn.disabled = false; ui.toast(ui.errText(r, { fallback: 'Could not dismiss that. Try again.' }), btn, true); return }
      onChange()
    } catch (e) {
      btn.disabled = false
      if (!client.isSignedOut(e)) ui.toast('Cannot reach the server.', btn, true)
    }
  }))
}

// Announcements unread: the newest announcement time this browser has
// shown on the Announcements screen, per event (localStorage, so it is per
// browser by design -- no read state is kept on the server).
function seenKey(slug) { return 'sh_ann_seen:' + slug }
export function unreadAnnouncements(slug, times) {
  if (!Array.isArray(times) || !times.length) return 0
  let seen = null
  try { seen = localStorage.getItem(seenKey(slug)) } catch (e) { return 0 }
  if (!seen) return times.length
  const at = Date.parse(seen)
  return times.filter(t => Date.parse(t) > at).length
}
export function markAnnouncementsSeen(slug, times) {
  if (!Array.isArray(times) || !times.length) return
  try { localStorage.setItem(seenKey(slug), times[0]) } catch (e) {}
}
