import { merge, label, nudgeHTML, keepNudgeFresh, timeOnly } from '../schedule.js'

export function title() { return 'Schedule' }

// Atlas participant-schedule (2026-11-02, a schedule that acts): the rows
// the organisers typed and the event's real times (start, end, the entry
// deadline or this team's own extension, the voting window, results) in one
// list, in time order, with Now and Next. A row starting within 15 minutes or
// on now also shows as a line at the top, the same line Home shows.
function whenText(ui, r, ev) {
  if (r.all_day) return ui.dateOnly(r.start_at, ev)
  if (!r.end_at) return ui.dateTime(r.start_at, ev)
  const sameDay = ui.dateOnly(r.start_at, ev) === ui.dateOnly(r.end_at, ev)
  return ui.dateTime(r.start_at, ev) + ' – ' + (sameDay ? timeOnly(ui, r.end_at, ev) : ui.dateTime(r.end_at, ev))
}

export async function render(ctx) {
  const { main, api: client, ui, event } = ctx
  const api = client.api
  const esc = ui.esc
  const ev = event.event
  main.innerHTML = `<div class="ph"><div class="t"><h1>Schedule</h1><p>Times are in ${esc(ev.time_zone || '')}.</p></div></div><div id="sched-top"></div><div id="sched-body"><p class="muted">Loading…</p></div>`
  const body = main.querySelector('#sched-body')
  const top = main.querySelector('#sched-top')
  let r
  try { r = await api('GET', '/v1/hack/events/' + encodeURIComponent(ev.slug) + '/content') }
  catch (e) {
    if (!client.isSignedOut(e)) body.innerHTML = `<div class="note bad" role="alert">Cannot reach the server.</div>`
    return
  }
  if (!ctx.alive()) return
  if (!r.ok) { body.innerHTML = `<div class="note bad" role="alert">Could not load the schedule.</div>`; return }
  const rows = merge(r.data && r.data.schedule, r.data && r.data.fixed_schedule)
  if (!rows.length) { body.innerHTML = `<div class="locked"><h2>Nothing scheduled yet</h2><p>Times show here once the organisers add them.</p></div>`; return }
  function paint() {
    label(rows)
    top.innerHTML = nudgeHTML(ui, ev, rows, '')
    body.innerHTML = `<ul class="sched">${rows.map(it => `<li><span class="what"><b>${esc(it.title)}${it.now ? ' <span class="pill stage">Now</span>' : it.next ? ' <span class="pill">Next</span>' : ''}</b>${it.description ? `<span>${esc(it.description)}</span>` : ''}</span><span class="when">${esc(whenText(ui, it, ev))}</span></li>`).join('')}</ul>`
  }
  paint()
  keepNudgeFresh(ctx, body, paint)
}
