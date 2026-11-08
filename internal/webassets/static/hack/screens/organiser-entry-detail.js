import { href } from '../nav.js'

export function title () { return 'Entry' }

// Rebuilt for the atlas "entries and participant journey" package
// (2026-11-02, slice 1): the full summary card (ui.entryCard) instead of a
// plain <dl>, a richer status block (Draft / Submitted / Incomplete /
// Excluded), and Exclude / Include for an auto-settled-but-incomplete entry.
//
// CONTRACT (coordinator decision, 2026-11-02 -- see participant-entry.js for
// the full note): `status: "not_submitted"` on an entryListItem (read while
// building this screen, internal/handler/hack_entries.go) is the Incomplete
// case -- the deadline sweep settled it without ever submitting it, still
// judged unless `excluded`. The server supplies the `incomplete` flag. Exclude
// (any entry, not only an incomplete one, per internal/db/hack_entries.go's
// own comment) and Include are confirmed landed at exactly
//   POST /v1/hack/events/{slug}/teams/{team}/entry/exclude {}
//   POST /v1/hack/events/{slug}/teams/{team}/entry/include {}
// returning the updated entryListItem.

const PLAIN = {
  title: 'project name', problem: 'the problem', tagline: 'the solution', audience: "who it's for",
  track: 'a track', tools: 'tools and AI', maturity: 'what works today',
  video_url: 'a video link', site: 'a published website', code_url: 'a code link', screenshot: 'a screenshot', description: 'a description',
  one_of: 'a video or website'
}
function labelList (fields) { return (fields || []).map(f => PLAIN[f] || f) }
function joinAnd (items) {
  if (!items.length) return ''
  if (items.length === 1) return items[0]
  return items.slice(0, -1).join(', ') + ' and ' + items[items.length - 1]
}

export async function render (ctx) {
  const { main, api: client, ui, event, role, params } = ctx
  const api = client.api
  const esc = ui.esc
  const ev = event.event
  const slug = ev.slug
  const teamSlug = params.get('team') || ''
  const $ = id => main.querySelector('#' + id)
  const setLive = ui.live(main)

  let team = null, entryItem = null, rules = { fields: {} }
  let platformTakedown = false

  async function load () {
    const [tr, er, rr] = await Promise.all([
      api('GET', '/v1/hack/events/' + encodeURIComponent(slug) + '/teams'),
      api('GET', '/v1/hack/events/' + encodeURIComponent(slug) + '/entries'),
      api('GET', '/v1/hack/events/' + encodeURIComponent(slug) + '/entry-rules')
    ])
    if (!tr.ok) { setLive(tr.data.error || 'Could not load the team', true); return false }
    team = (tr.data.teams || []).find(t => t.slug === teamSlug)
    if (!team) { setLive('Team not found.', true); return false }
    if (er.ok) entryItem = (er.data.entries || []).find(e => e.team.slug === teamSlug) || null
    if (rr.ok) rules = rr.data
    return true
  }

  function linkField (label, value) {
    const link = value ? ui.safeHref(value) : ''
    return `<dt>${esc(label)}</dt><dd>${link ? `<a href="${esc(link)}" target="_blank" rel="noopener">${esc(value)}</a>` : (value ? esc(value) : '(none)')}</dd>`
  }

  // --- Status block: Draft / Submitted / Not submitted / Incomplete -------

  function statusHTML (e, lockReason) {
    const extendHref = href(role, slug, 'entries') + '&team=' + encodeURIComponent(teamSlug)
    const missing = (e && e.missing) || []
    const giveMoreTime = `<a class="btn"${lockReason ? ' aria-disabled="true"' : ''} href="${esc(extendHref)}">Give more time</a>`
    if (!e) return `<div class="entry-status late"><div class="t"><b>No entry</b><span>This team hasn’t started one.</span></div>${giveMoreTime}</div>`
    // Excluded is orthogonal to status (internal/db/hack_entries.go): an
    // organiser can exclude any entry, not only an incomplete one, so this
    // is checked first regardless of status.
    if (e.excluded) {
      return `<div class="entry-status late"><div class="t"><b>Excluded from judging</b><span>You excluded this entry by hand. Judges won’t see it.</span></div><button class="btn" type="button" id="ed-include"${lockReason ? ' disabled' : ''}>Include in judging</button></div>`
    }
    if (e.incomplete) {
      // Coordinator decision (2026-11-02): the deadline sweep settled this
      // one without ever submitting it -- still judged, flagged, shown
      // verbatim here and on the participant's own Entry and Home.
      return `<div class="entry-status incomplete"><div class="t"><b>Not complete</b><span>It will still be judged, marked incomplete.${missing.length ? ' Missing: ' + esc(joinAnd(labelList(missing))) + '.' : ''}</span></div><div class="row-flex">${giveMoreTime}<button class="btn danger" type="button" id="ed-exclude"${lockReason ? ' disabled' : ''}>Exclude from judging</button></div></div>`
    }
    if (e.status === 'submitted') {
      const when = e.submitted_at ? ' · ' + esc(ui.dateTime(e.submitted_at, ev)) : ''
      const tightened = missing.length ? `<span>The organisers now also ask for ${esc(joinAnd(labelList(missing)))}. It stays submitted.</span>` : '<span>Judges can see this.</span>'
      return `<div class="entry-status submitted"><div class="t"><b>Submitted${when}</b>${tightened}</div></div>`
    }
    const deadlineBit = team.deadline ? ('Deadline' + (team.extended ? ' with their extension' : '') + ': ' + esc(ui.dateTime(team.deadline, ev)) + '.') : ''
    const missingBit = missing.length ? ('Missing: ' + esc(joinAnd(labelList(missing))) + '. ') : ''
    return `<div class="entry-status draft"><div class="t"><b>Draft · not submitted</b><span>${missingBit}${deadlineBit}</span></div>${giveMoreTime}</div>`
  }

  // --- Entry card + show-your-work + site -------------------------------

  function entrySectionHTML (e) {
    const names = (team.members || []).map(m => m.display_name).filter(Boolean).join(', ')
    const card = ui.entryCard({
      title: e.title, team_name: team.name, team_members: names,
      track: e.track, problem: e.problem, tagline: e.tagline, audience: e.audience,
      tools: e.tools, maturity: e.maturity
    }, { incomplete: e.incomplete && !e.excluded })
    const wrap = document.createElement('div')
    wrap.append(card)
    const showYourWork = `<section class="card"><h2>Show your work</h2><dl>
${linkField('Video link', e.video_url)}
${(rules.fields && rules.fields.code_url === 'off') ? '<dt>Code link</dt><dd>Off for this event</dd>' : linkField('Code link', e.code_url)}
</dl><h3 class="section-title">Screenshot</h3>${e.has_screenshot ? '<div class="shot" id="shot-box">Loading screenshot…</div>' : '<p class="small muted">No screenshot.</p>'}</section>`
    return wrap.innerHTML + showYourWork
  }

  function siteCard (lockReason) {
    if (!team.site || !team.site.exists) {
      return '<section class="card"><h2>Site</h2><p>No site published yet.</p></section>'
    }
    if (team.site.taken_down) {
      if (platformTakedown) {
        return `<section class="card"><h2>Site</h2><p><a href="${esc(team.site.url)}" target="_blank" rel="noopener" class="mono">${esc(team.site.url)}</a></p><div class="note warn"><b>Taken down by Simple Host.</b><p>You can’t restore this one.</p></div></section>`
      }
      const reason = team.site.taken_down_reason || 'No reason given.'
      return `<section class="card"><h2>Site</h2><p><a href="${esc(team.site.url)}" target="_blank" rel="noopener" class="mono">${esc(team.site.url)}</a></p><div class="note warn"><b>Taken down by you</b><p>Reason: ${esc(reason)}</p></div><button class="btn" type="button" id="put-back"${lockReason ? ' disabled' : ''}>Put back</button></section>`
    }
    return `<section class="card"><h2>Site</h2><p><a href="${esc(team.site.url)}" target="_blank" rel="noopener" class="mono">${esc(team.site.url)}</a> <span class="pill ok">Live</span></p><p><a class="btn" href="${esc(team.site.url)}" target="_blank" rel="noopener">Open site</a></p></section>
<section class="danger-zone"><h2>Danger zone</h2><div class="setting"><div class="t"><b>Take this site down</b><span>Visitors see a notice instead of the site. You can put it back.</span><div class="field"><label>Reason shown to the team (optional)<textarea class="input" id="takedown-reason" maxlength="300"${lockReason ? ' disabled' : ''}></textarea></label></div></div><button class="btn danger" type="button" id="take-down"${lockReason ? ' disabled' : ''}>Take this site down</button></div></section>`
  }

  function paint () {
    const e = entryItem || {}
    const lockReason = ui.writeLockReason(ev, event.admin_view)
    main.innerHTML = `<p class="crumbs"><a href="${esc(href(role, slug, 'entries'))}">Entries</a> / ${esc(team.name)}</p>
<div class="ph"><div class="t"><h1>${esc(team.name)}</h1><p>${team.name ? esc(ui.possessive(team.name)) + ' ' : "This team's "}entry and site.</p></div></div>
${lockReason ? `<div class="note warn">${esc(lockReason)}</div>` : ''}
${statusHTML(entryItem, lockReason)}
${entryItem ? entrySectionHTML(e) : '<section class="card"><h2>Entry</h2><p class="muted">No entry yet.</p></section>'}
${siteCard(lockReason)}
${e.description ? `<details class="card"><summary>More detail from the team</summary><p>${esc(e.description)}</p></details>` : ''}`
    wire(lockReason)
    if (e.has_screenshot) loadScreenshot()
  }

  async function loadScreenshot () {
    const box = $('shot-box')
    if (!box) return
    box.textContent = ''
    const img = document.createElement('img')
    img.alt = 'Entry screenshot'
    box.append(img)
    const r = await ui.fetchBinary(ctx, '/v1/hack/events/' + encodeURIComponent(slug) + '/teams/' + encodeURIComponent(teamSlug) + '/screenshot')
    if (r.signedOut) return
    if (!r.ok) { img.remove(); box.textContent = 'Could not load the screenshot.'; return }
    img.src = r.dataURL
  }

  function wire (lockReason) {
    const takeDown = $('take-down')
    if (takeDown) ui.forms.bind(ctx, takeDown, onTakeDown)
    const putBack = $('put-back')
    if (putBack) putBack.addEventListener('click', onPutBack)
    const exclude = $('ed-exclude')
    if (exclude) exclude.addEventListener('click', onExclude)
    const include = $('ed-include')
    if (include) include.addEventListener('click', onInclude)
  }

  async function onTakeDown () {
    const reason = $('takedown-reason').value.trim()
    const ok = await ui.confirm({
      title: 'Take this site down?',
      body: 'Visitors see a ‘taken down’ message instead of the site. The team can see why if you add a reason. Take it down?',
      confirmLabel: 'Take this site down',
      danger: true
    })
    if (!ok) return false
    let r
    try { r = await api('POST', '/v1/hack/events/' + encodeURIComponent(slug) + '/teams/' + encodeURIComponent(teamSlug) + '/takedown', { reason }) }
    catch (e) { if (!client.isSignedOut(e)) setLive('Cannot reach the server.', true); return }
    if (!r.ok) { setLive(r.data.error || 'Could not take the site down', true); return }
    team = r.data
    paint()
    ui.saved(main)
  }

  async function onPutBack () {
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

  async function onExclude () {
    const ok = await ui.confirm({
      title: 'Exclude this entry from judging?',
      body: 'Judges stop seeing it. The team can still see their own entry, marked incomplete. You can include it again any time.',
      confirmLabel: 'Exclude',
      danger: true
    })
    if (!ok) return false
    let r
    try { r = await api('POST', '/v1/hack/events/' + encodeURIComponent(slug) + '/teams/' + encodeURIComponent(teamSlug) + '/entry/exclude', {}) }
    catch (e) { if (!client.isSignedOut(e)) setLive('Cannot reach the server.', true); return }
    if (!r.ok) { setLive(r.data.error || 'Could not exclude this entry', true); return }
    await load()
    paint()
    ui.saved(main)
  }

  async function onInclude () {
    let r
    try { r = await api('POST', '/v1/hack/events/' + encodeURIComponent(slug) + '/teams/' + encodeURIComponent(teamSlug) + '/entry/include', {}) }
    catch (e) { if (!client.isSignedOut(e)) setLive('Cannot reach the server.', true); return }
    if (!r.ok) { setLive(r.data.error || 'Could not include this entry', true); return }
    await load()
    paint()
    ui.saved(main)
  }

  if (await load()) paint()
}
