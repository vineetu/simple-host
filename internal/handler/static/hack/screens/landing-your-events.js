import { href } from '../nav.js'
import { eventOrder } from '../shell.js'
export async function render(ctx) {
  const { main, events, ui } = ctx
  const row = e => {
    const section = e.role === 'judge' && ['closed','judging'].includes(e.stage) ? 'queue' : e.role === 'participant' && e.stage === 'results' ? 'results' : undefined
    const next = e.role === 'organiser' ? 'Open overview' : section === 'queue' ? 'Review your teams' : section === 'results' ? 'See results' : 'Open event'
    const blocked = e.taken_down && e.role !== 'organiser'
    return `<div class="r"><span class="ev-mark">${ui.esc(ui.initials(e.title))}</span><div class="main"><b>${blocked ? ui.esc(e.title) : `<a href="${ui.esc(href(e.role,e.slug,section))}">${ui.esc(e.title)}</a>`}</b><div class="row-flex"><span class="pill">${ui.esc(ui.roleLabel(e.role))}</span><span class="pill stage">${ui.esc(e.taken_down ? 'Taken down' : ui.stageLabel(e.stage))}</span></div>${blocked ? '' : `<a class="small" href="${ui.esc(href(e.role,e.slug,section))}">${next}</a>`}<details><summary class="small muted">Event menu</summary><p class="small muted">Leave this event — planned, SIM-340, not built yet.</p><button class="btn danger" disabled>Leave this event</button></details></div></div>`
  }
  const attention = e => !e.taken_down && ((e.role === 'organiser' && !['archived','ended'].includes(e.stage)) || (e.role === 'judge' && ['closed','judging'].includes(e.stage)))
  const list = eventOrder(events)
  main.innerHTML = `<div class="ph"><div class="t"><h1>Your events</h1></div></div>` + (!list.length ? `<div class="locked"><h2>You’re not part of any event yet</h2><p><a class="btn" href="/events/new">Create event</a></p><p>Have a join or judge link? Open it to get started.</p></div>` : [true,false].map(needed => {
    const group = list.filter(e => attention(e) === needed)
    return group.length ? `<p class="section-title">${needed ? 'Needs attention' : 'Recent'}</p><div class="list">${group.map(row).join('')}</div>` : ''
  }).join('') + `<hr class="rule"><a class="btn" href="/events/new">Create event</a>`)
  main.querySelectorAll('.ev-mark').forEach((mark,i) => {
    if (/^#[0-9a-f]{6}$/i.test(list[i].color || '')) mark.style.setProperty('--ev',list[i].color)
  })
}
