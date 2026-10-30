export function title() { return 'Schedule' }

// Labels Now/Next the way the prototype shows them: items are read in
// start-time order; the item whose window contains the current moment is
// Now, and the one right after it is Next. An item with no end_at borrows
// the next item's start as its own end, so back-to-back sessions still hand
// off cleanly.
function withLabels(schedule) {
  const items = schedule
    .map((it, i) => ({ ...it, _i: i }))
    .filter(it => it.start_at && !Number.isNaN(new Date(it.start_at).getTime()))
    .sort((a, b) => new Date(a.start_at) - new Date(b.start_at))
  const now = Date.now()
  let nowIndex = -1
  items.forEach((it, i) => {
    const start = new Date(it.start_at).getTime()
    const end = it.end_at && !Number.isNaN(new Date(it.end_at).getTime())
      ? new Date(it.end_at).getTime()
      : (items[i + 1] ? new Date(items[i + 1].start_at).getTime() : Infinity)
    it._now = now >= start && now < end
    if (it._now) nowIndex = i
  })
  const nextIndex = nowIndex >= 0 ? nowIndex + 1 : items.findIndex(it => new Date(it.start_at).getTime() > now)
  items.forEach((it, i) => { it._next = i === nextIndex && !it._now })
  return items
}

export async function render(ctx) {
  const { main, api: client, ui, event } = ctx
  const api = client.api
  const esc = ui.esc
  const ev = event.event
  main.innerHTML = `<div class="ph"><div class="t"><h1>Schedule</h1><p>${esc(ev.title)}</p></div></div><div id="sched-body"><p class="note">Loading…</p></div>`
  const body = main.querySelector('#sched-body')
  let r
  try { r = await api('GET', '/v1/hack/events/' + encodeURIComponent(ev.slug) + '/content') }
  catch (e) {
    if (!client.isSignedOut(e)) body.innerHTML = `<div class="note bad" role="alert">Cannot reach the server.</div>`
    return
  }
  if (!r.ok) { body.innerHTML = `<div class="note bad" role="alert">Could not load the schedule.</div>`; return }
  const schedule = (r.data && r.data.schedule) || []
  if (!schedule.length) { body.innerHTML = `<p class="note">Nothing scheduled yet. Check back soon.</p>`; return }
  const items = withLabels(schedule)
  body.innerHTML = `<div class="list">${items.map(it => `<div class="r"><div class="main"><b>${esc(it.title)}</b><span>${esc(ui.dateTime(it.start_at, ev))}${it.description ? ' · ' + esc(it.description) : ''}</span></div>${it._now ? '<span class="pill stage">Now</span>' : it._next ? '<span class="pill">Next</span>' : ''}</div>`).join('')}</div>`
}
