export function title() { return 'Announcements' }

export async function render(ctx) {
  const { main, api: client, ui, event } = ctx
  const api = client.api
  const esc = ui.esc
  const ev = event.event
  main.innerHTML = `<div class="ph"><div class="t"><h1>Announcements</h1><p>News from the organisers.</p></div></div><div id="ann-body"><p class="muted">Loading…</p></div>`
  const body = main.querySelector('#ann-body')
  let r
  try { r = await api('GET', '/v1/hack/events/' + encodeURIComponent(ev.slug) + '/announcements') }
  catch (e) {
    if (!client.isSignedOut(e)) body.innerHTML = `<div class="note bad" role="alert">Cannot reach the server.</div>`
    return
  }
  if (!r.ok) { body.innerHTML = `<div class="note bad" role="alert">Could not load announcements.</div>`; return }
  const items = (r.data && r.data.announcements) || [] // already newest-first from the API
  if (!items.length) { body.innerHTML = `<div class="locked"><h2>No announcements yet</h2><p>News from the organisers shows here.</p></div>`; return }
  body.innerHTML = `<div class="list">${items.map(a => `<article class="r"><div class="main"><h2>${esc(a.title)}</h2><p class="small muted">${esc(ui.dateTime(a.created_at, ev, { timeStyle: undefined }))}</p><p>${esc(a.body)}</p></div></article>`).join('')}</div>`
}
