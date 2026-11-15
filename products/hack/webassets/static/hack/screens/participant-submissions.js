export function title() { return 'Submissions' }

export async function render(ctx) {
  const { main, api: client, ui, event } = ctx
  const ev = event.event
  const path = '/v1/hack/events/' + encodeURIComponent(ev.slug) + '/submissions'
  main.innerHTML = '<div class="ph"><div class="t"><h1>Submissions</h1><p>Explore what the teams submitted.</p></div></div><div id="submissions-body"><p class="note">Loading…</p></div>'
  const body = main.querySelector('#submissions-body')
  let res
  try { res = await client.api('GET', path) } catch (e) {
    if (ctx.alive() && !client.isSignedOut(e)) body.innerHTML = '<p class="note bad">Cannot reach the server.</p>'
    return
  }
  if (!ctx.alive()) return
  if (!res.ok) {
    body.innerHTML = res.status === 409 || res.data?.code === 'submissions_not_open'
      ? '<p class="note">Submissions open to everyone when judging starts.</p>'
      : `<p class="note bad">${ui.esc(ui.errText(res))}</p>`
    return
  }
  const rows = res.data.submissions || []
  if (!rows.length) { body.innerHTML = '<p class="note">No submissions to show yet.</p>'; return }
  body.innerHTML = '<div class="grid2"></div>'
  const grid = body.firstElementChild
  const observer = new IntersectionObserver(entries => {
    for (const entry of entries) {
      if (!entry.isIntersecting) continue
      observer.unobserve(entry.target)
      if (ctx.alive()) ui.showImage(ctx, entry.target.querySelector('img'), entry.target.dataset.path)
      else observer.disconnect()
    }
  })
  for (const row of rows) {
    const card = ui.entryCard(row, { compact: true })
    if (row.team === event.me?.team?.slug) {
      const pill = document.createElement('span')
      pill.className = 'pill accent'
      pill.textContent = 'Your team'
      let pills = card.querySelector('.ec-pills')
      if (!pills) {
        pills = document.createElement('div')
        pills.className = 'ec-pills'
        card.querySelector('.ec-head').append(pills)
      }
      pills.append(pill)
    }
    if (row.has_screenshot) {
      const frame = document.createElement('div')
      frame.className = 'site-frame'
      const img = document.createElement('img')
      img.alt = ''
      img.loading = 'lazy'
      frame.dataset.path = path + '/' + encodeURIComponent(row.team) + '/screenshot'
      frame.append(img)
      card.append(frame)
      observer.observe(frame)
    }
    const links = document.createElement('div')
    links.className = 'ec-links'
    for (const [url, label, cls] of [[row.site_url, 'Open website ↗', 'btn primary'], [row.video_url, 'Watch video ↗', 'btn quiet'], [row.code_url, 'View code ↗', 'btn quiet']]) {
      const href = ui.safeHref(url)
      if (!href) continue
      links.innerHTML += `<a class="${cls}" href="${ui.esc(href)}" target="_blank" rel="noopener noreferrer">${ui.esc(label)}</a>`
    }
    if (links.childNodes.length) card.append(links)
    grid.append(card)
  }
}
