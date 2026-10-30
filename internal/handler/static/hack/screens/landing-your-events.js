import { href } from '../nav.js'
import { eventOrder } from '../shell.js'

export async function render(ctx) {
  const { main, events, ui } = ctx
  // A2: the "Leave this event" menu advertised a feature that doesn't
  // exist yet (SIM-340) -- removed rather than shown disabled with an
  // internal ticket id.
  // Part B #2: one link per row, not a title link plus a separate "next"
  // link plus two pills.
  const row = e => {
    const section = e.role === 'judge' && ['closed','judging'].includes(e.stage) ? 'queue' : e.role === 'participant' && e.stage === 'results' ? 'results' : undefined
    const next = e.role === 'organiser' ? 'Overview' : section === 'queue' ? 'Score teams' : section === 'results' ? 'See results' : 'Open'
    const blocked = e.taken_down && e.role !== 'organiser'
    const tag = blocked ? 'div' : 'a'
    const hrefAttr = blocked ? '' : ` href="${ui.esc(href(e.role,e.slug,section))}"`
    return `<${tag} class="r"${hrefAttr}><span class="ev-mark">${ui.esc(ui.initials(e.title))}</span><span class="main"><b>${ui.esc(e.title)}</b><span>${ui.esc(ui.roleLabel(e.role))} · ${ui.esc(e.taken_down ? 'Taken down' : ui.stageLabel(e.stage))}</span></span>${blocked ? '' : `<span class="meta">${ui.esc(next)}</span>`}</${tag}>`
  }
  const attention = e => !e.taken_down && ((e.role === 'organiser' && !['archived','ended'].includes(e.stage)) || (e.role === 'judge' && ['closed','judging'].includes(e.stage)))
  const list = eventOrder(events)
  main.innerHTML = `<div class="ph"><div class="t"><h1>Your events</h1></div>${list.length ? `<a class="btn primary" href="/events/new">Create event</a>` : ''}</div>` + (!list.length ? `<div class="locked"><h2>You’re not part of any event yet</h2><p><a class="btn primary" href="/events/new">Create event</a></p><p>Have a join or judge link? Open it to get started.</p></div>` : [true,false].map(needed => {
    const group = list.filter(e => attention(e) === needed)
    return group.length ? `<p class="section-title">${needed ? 'Needs attention' : 'Recent'}</p><div class="list">${group.map(row).join('')}</div>` : ''
  }).join(''))
  main.querySelectorAll('.ev-mark').forEach((mark,i) => {
    const color = list[i].color
    if (/^#[0-9a-f]{6}$/i.test(color || '')) { mark.style.setProperty('--ev',color); mark.style.color = ui.markInk(color) }
  })
}
