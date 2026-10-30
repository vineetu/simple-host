import { href } from '../nav.js'

export function title() { return 'Entry' }

export async function render(ctx) {
  const { main, api: client, ui, event, role, params } = ctx
  const api = client.api
  const ev = event.event
  const slug = ev.slug
  const teamSlug = params.get('team') || ''
  const $ = id => main.querySelector('#' + id)
  const setLive = ui.live(main)

  let team = null, entryItem = null
  let platformTakedown = false

  async function load() {
    const [tr, er] = await Promise.all([
      api('GET', '/v1/hack/events/' + encodeURIComponent(slug) + '/teams'),
      api('GET', '/v1/hack/events/' + encodeURIComponent(slug) + '/entries')
    ])
    if (!tr.ok) { setLive(tr.data.error || 'Could not load the team', true); return false }
    team = (tr.data.teams || []).find(t => t.slug === teamSlug)
    if (!team) { setLive('Team not found.', true); return false }
    if (er.ok) entryItem = (er.data.entries || []).find(e => e.team.slug === teamSlug) || null
    return true
  }

  function field(label, value) {
    return `<dt>${ui.esc(label)}</dt><dd>${value ? ui.esc(value) : '(none)'}</dd>`
  }

  // #19/#18: the server (checkEntryURL) already enforces http/https on
  // video_url/code_url, so this is defence in depth, not a live hole -- but
  // linkField put the value straight into href with no client-side check,
  // unlike participant-entry.js's own safeHref. round2-shared.md now gives
  // this a shared home, ui.safeHref, so this uses that instead of its own copy.
  function linkField(label, value) {
    const link = value ? ui.safeHref(value) : ''
    return `<dt>${ui.esc(label)}</dt><dd>${link ? `<a href="${ui.esc(link)}" target="_blank" rel="noopener">${ui.esc(value)}</a>` : (value ? ui.esc(value) : '(none)')}</dd>`
  }

  function siteCard(lockReason) {
    if (!team.site || !team.site.exists) {
      return `<section class="card"><h2>Site</h2><p>No site published yet.</p></section>`
    }
    if (team.site.taken_down) {
      if (platformTakedown) {
        return `<section class="card"><h2>Site</h2><p><a href="${ui.esc(team.site.url)}" target="_blank" rel="noopener" class="mono">${ui.esc(team.site.url)}</a></p><div class="note warn"><b>Taken down by Simple Host.</b><p>You can’t restore this one.</p></div></section>`
      }
      const reason = team.site.taken_down_reason || 'No reason given.'
      return `<section class="card"><h2>Site</h2><p><a href="${ui.esc(team.site.url)}" target="_blank" rel="noopener" class="mono">${ui.esc(team.site.url)}</a></p><div class="note warn"><b>Taken down by you</b><p>Reason: ${ui.esc(reason)}</p></div><button class="btn" type="button" id="put-back"${lockReason ? ' disabled' : ''}>Put back</button></section>`
    }
    return `<section class="card"><h2>Site</h2><p><a href="${ui.esc(team.site.url)}" target="_blank" rel="noopener" class="mono">${ui.esc(team.site.url)}</a> <span class="pill ok">Live</span></p><p><a class="btn" href="${ui.esc(team.site.url)}" target="_blank" rel="noopener">Open site</a></p></section>
<section class="danger-zone"><h2>Danger zone</h2><div class="setting"><div class="t"><b>Take this site down</b><span>Visitors see a notice instead of the site. You can put it back.</span><div class="field"><label>Reason shown to the team (optional)<textarea class="input" id="takedown-reason" maxlength="300"${lockReason ? ' disabled' : ''}></textarea></label></div></div><button class="btn danger" type="button" id="take-down"${lockReason ? ' disabled' : ''}>Take this site down</button></div></section>`
  }

  function paint() {
    const e = entryItem || {}
    const lockReason = ui.writeLockReason(ev, event.admin_view)
    main.innerHTML = `<p class="crumbs"><a href="${ui.esc(href(role, slug, 'entries'))}">Entries</a> / ${ui.esc(team.name)}</p>
<div class="ph"><div class="t"><h1>Entry: ${ui.esc(team.name)}</h1><p>${team.name ? ui.esc(ui.possessive(team.name)) + ' ' : "This team's "}entry and site.</p></div></div>
${lockReason ? `<div class="note warn">${ui.esc(lockReason)}</div>` : ''}
<section class="card"><h2>Entry</h2><dl>
${field('Title', e.title)}
${field('Tagline', e.tagline)}
${field('Description', e.description)}
${linkField('Video link', e.video_url)}
${linkField('Code link', e.code_url)}
</dl><h3 class="section-title">Screenshot</h3>${e.has_screenshot ? `<div class="shot" id="shot-box">Loading screenshot…</div>` : `<p class="small muted">No screenshot.</p>`}</section>
${siteCard(lockReason)}`
    wire()
    if (e.has_screenshot) loadScreenshot()
  }

  async function loadScreenshot() {
    const box = $('shot-box')
    if (!box) return
    box.textContent = ''
    const img = document.createElement('img')
    img.alt = 'Entry screenshot'
    box.append(img)
    // ui.fetchBinary returns a data: URL (dataURL) alongside the blob now --
    // the CSP's img-src allows 'self' and data: but not blob:, which is why
    // this screenshot never showed for an organiser (Grok B3).
    const r = await ui.fetchBinary(ctx, '/v1/hack/events/' + encodeURIComponent(slug) + '/teams/' + encodeURIComponent(teamSlug) + '/screenshot')
    if (r.signedOut) return
    if (!r.ok) { img.remove(); box.textContent = 'Could not load the screenshot.'; return }
    img.src = r.dataURL
  }

  function wire() {
    const takeDown = $('take-down')
    if (takeDown) takeDown.addEventListener('click', onTakeDown)
    const putBack = $('put-back')
    if (putBack) putBack.addEventListener('click', onPutBack)
  }

  async function onTakeDown() {
    const reason = $('takedown-reason').value.trim()
    const ok = await ui.confirm({
      title: 'Take this site down?',
      body: 'Visitors see a ‘taken down’ message instead of the site. The team can see why if you add a reason. Take it down?',
      confirmLabel: 'Take this site down',
      danger: true
    })
    if (!ok) return
    let r
    try { r = await api('POST', '/v1/hack/events/' + encodeURIComponent(slug) + '/teams/' + encodeURIComponent(teamSlug) + '/takedown', { reason }) }
    catch (e) { if (!client.isSignedOut(e)) setLive('Cannot reach the server.', true); return }
    if (!r.ok) { setLive(r.data.error || 'Could not take the site down', true); return }
    team = r.data
    paint()
    ui.saved(main)
  }

  async function onPutBack() {
    setLive('')
    let r
    try { r = await api('POST', '/v1/hack/events/' + encodeURIComponent(slug) + '/teams/' + encodeURIComponent(teamSlug) + '/restore') }
    catch (e) { if (!client.isSignedOut(e)) setLive('Cannot reach the server.', true); return }
    if (!r.ok) {
      if (r.data.code === 'platform_takedown') { platformTakedown = true; paint(); return }
      setLive(r.data.error || 'Could not put the site back', true)
      return
    }
    team = r.data
    paint()
    ui.saved(main)
  }

  if (await load()) paint()
}
