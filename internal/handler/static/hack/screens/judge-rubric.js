import { paragraphs } from './_judge-util.js'

export function title() { return 'Rubric & brief' }

// judge-rubric: a persistent, read-only reference (docs/ux-atlas/prototype/
// judge-rubric.html). Reachable any time, even before judging opens, so a
// judge can read it ahead of time (define-rubric's read side).
export async function render(ctx) {
  const { main, api: client, ui, event: detail } = ctx
  const api = client.api
  const ev = detail.event
  main.innerHTML = `<div class="ph"><div class="t"><h1>Rubric &amp; brief</h1><p>What each score means.</p></div></div><p class="muted">Loading…</p>`

  let r
  try { r = await api('GET', `/v1/hack/events/${encodeURIComponent(ev.slug)}/rubric`) }
  catch (e) {
    if (client.isSignedOut(e)) return
    main.innerHTML += `<div class="note bad" role="alert">Cannot reach the server.</div>`
    return
  }
  if (!r.ok) {
    main.innerHTML = `<div class="ph"><div class="t"><h1>Rubric &amp; brief</h1></div></div><div class="note bad" role="alert">${ui.esc(r.data && r.data.error || 'Could not load the rubric.')}</div>`
    return
  }
  const criteria = r.data.criteria || []
  const total = criteria.reduce((s, c) => s + (Number(c.max_points) || 0), 0)

  const rubricCard = !criteria.length
    ? `<section class="card"><h2>Rubric</h2><div class="note">The organiser hasn’t published a rubric yet. Check back closer to judging.</div></section>`
    : `<section class="card"><h2>Rubric</h2><p class="sub">${criteria.length} ${criteria.length === 1 ? 'criterion' : 'criteria'} · ${total} points total</p>${criteria.map(c => `<div class="crit"><div class="row-flex"><b>${ui.esc(c.name)}</b><span class="pill">${c.max_points} points</span></div>${c.description ? `<p class="small muted">${ui.esc(c.description)}</p>` : ''}</div>`).join('')}</section>`

  const about = paragraphs(ev.about).map(p => `<p>${ui.esc(p)}</p>`).join('')
  const rules = paragraphs(ev.rules).map(p => `<p>${ui.esc(p)}</p>`).join('')
  const prizes = paragraphs(ev.prizes).map(p => `<p>${ui.esc(p)}</p>`).join('')
  const aboutCard = (about || rules || prizes)
    ? `<section class="card">${about ? `<h3>About this event</h3>${about}` : ''}${rules ? `<hr class="rule"><h3>Rules</h3>${rules}` : ''}${prizes ? `<hr class="rule"><h3>Prizes</h3>${prizes}` : ''}</section>`
    : ''

  main.innerHTML = `<div class="ph"><div class="t"><h1>Rubric &amp; brief</h1><p>What each score means.</p></div></div>${rubricCard}${aboutCard}`
}
