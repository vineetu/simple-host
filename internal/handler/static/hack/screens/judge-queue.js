import { href } from '../nav.js'
import { errText, newTabLink, siteHref, scoreTotals } from './_judge-util.js'

export function title() { return 'My teams' }

// judge-queue ("My teams" — spec decision, 2026-10-30 walkthrough):
// three segments (To score / Scored / Teams I know) built from the real
// judge queue + the judge's own conflicts, matching
// docs/ux-atlas/prototype/judge-queue*.html.
export async function render(ctx) {
  const { main, api: client, ui, event: detail } = ctx
  const api = client.api
  const ev = detail.event
  const slug = ev.slug
  main.innerHTML = `<div class="ph"><div class="t"><h1>My teams</h1><p>Teams you are scoring.</p></div></div><p class="muted">Loading…</p>`

  let queueRes, confRes, rubricRes
  try {
    ;[queueRes, confRes, rubricRes] = await Promise.all([
      api('GET', `/v1/hack/events/${encodeURIComponent(slug)}/judge/queue`),
      api('GET', `/v1/hack/events/${encodeURIComponent(slug)}/conflicts`),
      api('GET', `/v1/hack/events/${encodeURIComponent(slug)}/rubric`)
    ])
  } catch (e) {
    if (client.isSignedOut(e)) return
    main.innerHTML += `<div class="note bad" role="alert">Cannot reach the server.</div>`
    return
  }
  if (!queueRes.ok || !confRes.ok) {
    main.innerHTML = `<div class="ph"><div class="t"><h1>My teams</h1></div></div><div class="note bad" role="alert">${ui.esc(errText(queueRes.ok ? confRes : queueRes))}</div><p><a href="${ui.esc(href('judge', slug, 'home'))}">Back to Home</a></p>`
    return
  }
  const queue = queueRes.data.queue || []
  const conflicts = confRes.data.conflicts || []
  const locked = !!ev.judging_locked_at
  const resultsOut = !!ev.results_published

  if (!ev.scoring_open && !locked && !resultsOut) {
    const when = ev.submission_deadline ? ui.dateTime(ev.submission_deadline, ev) : ''
    main.innerHTML = `<div class="ph"><div class="t"><h1>My teams</h1></div></div><section class="locked"><h2>Not open yet</h2><p>Judging opens once entries close${when ? ', ' + ui.esc(when) : ''}.</p><p><a href="${ui.esc(href('judge', slug, 'home'))}">Back to Home</a></p></section>`
    return
  }

  const toScore = queue.filter(r => !r.done)
  const scored = queue.filter(r => r.done)

  // The max side of the "scored X/Y" fraction is the same for every team
  // (one rubric for the whole event).
  const rubricMax = (rubricRes.ok ? rubricRes.data.criteria || [] : []).reduce((s, c) => s + (Number(c.max_points) || 0), 0)

  // Own score totals, only for the teams this judge has actually scored
  // (score-criterion's data has no ready-made aggregate, see _judge-util).
  const totals = {}
  await Promise.all(scored.map(async row => {
    try {
      const r = await api('GET', `/v1/hack/events/${encodeURIComponent(slug)}/judge/scores/${encodeURIComponent(row.team_id)}`)
      if (r.ok) totals[row.team_id] = scoreTotals(r.data.scores, [])
    } catch (e) {}
  }))

  // No in-page "Scoring is Open" pill: the status bar already says it
  // (shell.js). Only the locked/results-published notes, which carry extra
  // context the status bar doesn't, stay here.
  let banner = ''
  if (resultsOut) banner = `<div class="note ok"><p><b>Results are out.</b></p></div>`
  else if (locked) banner = `<div class="note warn"><p><b>Scoring is locked.</b> Scores can no longer be changed.</p></div>`

  function scoreLabel(row) {
    const t = totals[row.team_id]
    return t ? `${t.points}/${rubricMax}` : ''
  }
  function subLine(row) {
    const submitted = row.updated_at ? 'Submitted ' + ui.dateTime(row.updated_at, ev) : ''
    const tagline = String(row.tagline || '').trim()
    return [submitted, tagline].filter(Boolean).join(' · ')
  }
  function rowFor(row, mode, isFirst) {
    const title = row.title || row.team.name || 'Entry'
    const sub = subLine(row)
    const openBtn = newTabLink(siteHref(row), 'Open site', 'btn sm')
    const shotBtn = row.has_screenshot ? `<button type="button" class="btn sm" data-shot="${ui.esc(row.team_id)}" data-slug="${ui.esc(row.team.slug)}">View screenshot</button>` : ''
    const shotFrame = row.has_screenshot ? `<div class="site-frame" id="shot-${ui.esc(row.team_id)}" hidden><img alt="Screenshot of ${ui.esc(row.team.name || title)}" style="max-width:100%" hidden></div>` : ''
    const scoreHref = `/e/${encodeURIComponent(slug)}/judge?s=scoring&team=${encodeURIComponent(row.team_id)}`
    let action = ''
    if (mode === 'readonly') {
      action = row.done ? `<span class="pill ok">Your score: ${ui.esc(scoreLabel(row))}</span>` : `<span class="pill">Not scored</span>`
    } else if (mode === 'scored') {
      action = `<a class="btn sm" href="${ui.esc(scoreHref)}">Edit your score</a><span class="pill ok">Scored ${ui.esc(scoreLabel(row))}</span>`
    } else {
      action = `<a class="btn sm${isFirst ? ' primary' : ''}" href="${ui.esc(scoreHref)}">Score this team</a>`
    }
    return `<div class="r"><span class="main"><b>${ui.esc(title)}</b>${sub ? `<span>${ui.esc(sub)}</span>` : ''}</span>${openBtn}${shotBtn}${action}</div>${shotFrame}`
  }

  const mode = (resultsOut || locked) ? 'readonly' : 'open'
  const toScorePanel = toScore.length
    ? '<div class="list">' + toScore.map((r, i) => rowFor(r, mode === 'readonly' ? 'readonly' : 'to-score', i === 0)).join('') + '</div>'
    : `<div class="note">${conflicts.length && !scored.length ? 'Nothing to score — you know someone on every team on your list.' : 'Nothing left — you’ve scored everyone assigned to you.'}</div>`
  const scoredPanel = scored.length
    ? '<div class="list">' + scored.map(r => rowFor(r, mode === 'readonly' ? 'readonly' : 'scored')).join('') + '</div>'
    : `<div class="note">No teams scored yet.</div>`
  const conflictsPanel = conflicts.length
    ? '<div class="list">' + conflicts.map(c => `<div class="r" data-conflict="${ui.esc(c.team_id)}"><span class="main"><b>${ui.esc(c.team_name)}</b><span>You know someone on this team</span></span>${locked ? '' : `<button type="button" class="btn sm" data-remove-conflict="${ui.esc(c.team_id)}" data-name="${ui.esc(c.team_name)}">I don’t know this team after all</button>`}</div>`).join('') + '</div>'
    : `<div class="note">You haven’t said you know anyone on a team.</div>`

  const tabs = [
    { id: 'to-score', label: 'To score', count: toScore.length, panel: toScorePanel },
    { id: 'scored', label: 'Scored', count: scored.length, panel: scoredPanel },
    { id: 'conflicts', label: 'Teams I know', count: conflicts.length, panel: conflictsPanel }
  ]
  const active = tabs.find(t => t.count > 0) || tabs[0]

  main.innerHTML = `<div class="ph"><div class="t"><h1>My teams</h1><p>Teams you are scoring.</p></div></div>` +
    banner +
    `<nav class="subtabs" aria-label="My teams filters">${tabs.map(t => `<a href="#" data-tab="${t.id}"${t.id === active.id ? ' aria-current="page"' : ''}>${ui.esc(t.label)} <span class="count">${t.count}</span></a>`).join('')}</nav>` +
    tabs.map(t => `<section data-panel="${t.id}"${t.id === active.id ? '' : ' hidden'}>${t.panel}</section>`).join('')

  main.querySelectorAll('[data-tab]').forEach(a => a.addEventListener('click', e => {
    e.preventDefault()
    main.querySelectorAll('[data-tab]').forEach(x => x.removeAttribute('aria-current'))
    a.setAttribute('aria-current', 'page')
    const id = a.getAttribute('data-tab')
    main.querySelectorAll('[data-panel]').forEach(x => { x.hidden = x.getAttribute('data-panel') !== id })
  }))

  main.querySelectorAll('[data-shot]').forEach(btn => btn.addEventListener('click', async () => {
    const frame = main.querySelector('#shot-' + btn.getAttribute('data-shot'))
    if (!frame) return
    if (!frame.hidden) { frame.hidden = true; btn.textContent = 'View screenshot'; return }
    frame.hidden = false
    btn.textContent = 'Hide screenshot'
    const img = frame.querySelector('img')
    if (!img.getAttribute('data-blob')) {
      await ui.showImage(ctx, img, `/v1/hack/events/${encodeURIComponent(slug)}/teams/${encodeURIComponent(btn.getAttribute('data-slug'))}/screenshot`)
    }
  }))

  main.querySelectorAll('[data-remove-conflict]').forEach(btn => btn.addEventListener('click', async () => {
    const teamId = btn.getAttribute('data-remove-conflict')
    const name = btn.getAttribute('data-name') || 'this team'
    const ok = await ui.confirm({ title: `Score ${name} again?`, body: `Your score for ${name} will count again.`, confirmLabel: 'Score again' })
    if (!ok) return
    btn.disabled = true
    let r
    try { r = await api('DELETE', `/v1/hack/events/${encodeURIComponent(slug)}/conflicts/${encodeURIComponent(teamId)}`) }
    catch (e) { if (!client.isSignedOut(e)) ui.toast('Cannot reach the server.', btn, true); btn.disabled = false; return }
    if (r.status !== 204) { ui.toast(errText(r), btn, true); btn.disabled = false; return }
    return render(ctx)
  }))
}
