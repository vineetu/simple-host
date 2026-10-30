import { href } from '../nav.js'
import { errText, newTabLink, siteHref } from './_judge-util.js'

// A generic fallback: render() below sets the exact "Score: <entry>" title
// once the queue has loaded, since the team's name isn't known this early.
export function title() { return 'Score entry' }

// judge-scoring: full-focus scoring of one entry (docs/ux-atlas/prototype/
// judge-scoring*.html). No nav_id — only reached from judge-queue /
// judge-home with ?team=. Every point chip PUTs and saves immediately
// (score-criterion); there is no Save button for scores, ever, only
// Save & next, which also flushes the comment (save-and-next,
// comment-on-entry). The team's live/pinned site cannot be framed (the
// app's CSP is frame-src 'self'), so "open the entry's website" is the
// prominent button BUILD.md allows as the fallback, not an iframe.
export async function render(ctx) {
  const { main, api: client, ui, event: detail } = ctx
  const api = client.api
  const ev = detail.event
  const slug = ev.slug
  const teamID = ctx.params.get('team') || ''

  main.innerHTML = `<p class="muted" style="padding:22px 28px">Loading…</p>`
  if (!teamID) {
    main.innerHTML = `<div class="page"><div class="note bad" role="alert">No team was given to score.</div><p><a href="${ui.esc(href('judge', slug, 'queue'))}">Back to My teams</a></p></div>`
    return
  }

  let queueRes, rubricRes, scoresRes
  try {
    ;[queueRes, rubricRes, scoresRes] = await Promise.all([
      api('GET', `/v1/hack/events/${encodeURIComponent(slug)}/judge/queue`),
      api('GET', `/v1/hack/events/${encodeURIComponent(slug)}/rubric`),
      api('GET', `/v1/hack/events/${encodeURIComponent(slug)}/judge/scores/${encodeURIComponent(teamID)}`)
    ])
  } catch (e) {
    if (e.message === 'signed out') return
    main.innerHTML = `<div class="page"><div class="note bad" role="alert">Cannot reach the server.</div></div>`
    return
  }
  if (!queueRes.ok) {
    main.innerHTML = `<div class="page"><div class="note bad" role="alert">${ui.esc(errText(queueRes))}</div><p><a href="${ui.esc(href('judge', slug, 'queue'))}">Back to My teams</a></p></div>`
    return
  }
  const queue = queueRes.data.queue || []
  const index = queue.findIndex(r => r.team_id === teamID)
  if (index < 0) {
    main.innerHTML = `<div class="page"><div class="note bad" role="alert">That team isn’t in your queue any more.</div><p><a href="${ui.esc(href('judge', slug, 'queue'))}">Back to My teams</a></p></div>`
    return
  }
  const row = queue[index]
  const rubric = (rubricRes.ok && rubricRes.data.criteria) || []
  if (!rubric.length) {
    main.innerHTML = `<div class="page"><div class="note">The organiser has not set a rubric yet.</div><p><a href="${ui.esc(href('judge', slug, 'queue'))}">Back to My teams</a></p></div>`
    return
  }
  const locked = !!ev.judging_locked_at

  // Mutable scoring state for this team, seeded from the server.
  const scores = {} // criterion_id -> points
  ;(scoresRes.ok ? scoresRes.data.scores : []).forEach(s => { scores[s.criterion_id] = s.points })
  let comment = (scoresRes.ok && scoresRes.data.comment) || ''
  let savedComment = comment
  let commentTimer = 0
  let commentSave = null

  const title = row.title || row.team.name || 'Entry'
  const tagline = String(row.tagline || '').trim()
  const desc = String(row.description || '').trim()
  const video = newTabLink(row.video_url, 'Watch video', 'btn')
  const code = newTabLink(row.code_url, 'View code', 'btn')
  const site = siteHref(row)
  const openSite = site ? newTabLink(site, 'Open website', 'btn') : ''
  const submitted = row.updated_at ? `<p>As submitted ${ui.esc(ui.dateTime(row.updated_at, ev))}</p>` : ''

  const siteFrame = site
    ? `<div class="site-frame"><div class="stack"><p><b>${ui.esc(row.team.name || title)}</b></p><a class="btn primary" href="${ui.esc(site)}" target="_blank" rel="noopener noreferrer">Open ${ui.esc(row.team.name || 'website')} →</a></div></div>`
    : `<div class="site-frame"><p class="muted">Nothing published yet.</p></div>`

  const left = `<p class="crumbs"><a class="muted" href="${ui.esc(href('judge', slug, 'queue'))}">Back to My teams</a></p>` +
    (locked ? `<div class="note warn"><b>Scoring is locked.</b> Your scores are read-only.</div>` : '') +
    `<header class="ph"><div class="t"><h1>${ui.esc(title)}</h1><p class="small"><button type="button" class="btn quiet sm" id="jsc-goscore">Go to scoring ↓</button></p>${submitted}</div></header>` +
    (tagline ? `<p><b>${ui.esc(tagline)}</b></p>` : '') +
    (desc ? `<p>${ui.esc(desc)}</p>` : '') +
    (!title && !tagline && !desc ? '<p class="muted">No entry yet</p>' : '') +
    `<div class="row-flex">${video}${code}${openSite}</div>` +
    `<p class="section-title">Team website</p>${siteFrame}`

  function critRow(crit) {
    const current = scores[crit.id]
    if (locked) {
      return `<div class="crit"><b>${ui.esc(crit.name)}</b>${crit.description ? `<p class="small muted">${ui.esc(crit.description)}</p>` : ''}<p>Your score: <b>${current == null ? '—' : current} / ${crit.max_points}</b></p></div>`
    }
    let chips = ''
    for (let p = 0; p <= crit.max_points; p++) {
      chips += `<span role="radio" tabindex="0" aria-checked="${current === p ? 'true' : 'false'}" class="${current === p ? 'on' : ''}" data-crit="${ui.esc(crit.id)}" data-points="${p}">${p}</span>`
    }
    return `<div class="crit" data-crit-row="${ui.esc(crit.id)}"><b>${ui.esc(crit.name)}</b>${crit.description ? `<p class="small muted">${ui.esc(crit.description)}</p>` : ''}<div class="scale" role="radiogroup" aria-label="${ui.esc(crit.name)}">${chips}</div></div>`
  }

  document.title = `Score: ${title} · Simple Hack`

  const hasScore = Object.keys(scores).length > 0
  const last = index >= queue.length - 1
  const nextLabel = last ? 'Save & finish' : 'Save & next'

  const right = `<p class="small muted" id="jsc-progress">${index + 1} of ${queue.length}${row.done ? ', done' : ''}</p>` +
    `<h2 id="score">Rubric</h2><p class="small"><a class="muted" href="${ui.esc(href('judge', slug, 'rubric'))}">Rubric &amp; brief</a></p>` +
    `<div id="jsc-criteria">${rubric.map(critRow).join('')}</div>` +
    (locked
      ? `<div class="field"><label>Comment for the team</label><p>${comment ? ui.esc(comment) : '<span class="muted">No comment.</span>'}</p><p class="help">Not shown with your name.</p></div>`
      : `<div class="field"><label for="jsc-comment">Comment for the team</label><textarea class="input" id="jsc-comment" maxlength="1000" rows="3" placeholder="Share useful feedback"${!hasScore ? ' disabled' : ''}>${ui.esc(comment)}</textarea><p class="help" id="jsc-comment-help">${hasScore ? 'Optional. Not shown with your name. Saves automatically.' : 'Optional. Not shown with your name. Add a score before writing a comment.'}</p><p class="small muted" id="jsc-saved" aria-live="polite">${hasScore ? 'Saved' : 'Tap a score. It saves straight away.'}</p></div>`) +
    (locked ? '' : `<div class="danger-zone"><h2>Know this team?</h2><div class="setting"><button type="button" class="btn danger sm" id="jsc-conflict">I know someone on this team</button></div></div>`) +
    `<div class="sticky-foot"><button type="button" class="btn" id="jsc-prev"${index <= 0 ? ' disabled' : ''}>Previous</button>${locked ? '' : `<button type="button" class="btn primary" id="jsc-next">${nextLabel}</button>`}</div>`

  main.innerHTML = `<main class="page wide" style="padding:0;height:100%"><div class="focus"><section class="left">${left}</section><section class="right">${right}</section></div></main>`

  const goscore = main.querySelector('#jsc-goscore')
  if (goscore) goscore.addEventListener('click', () => { main.querySelector('#score').scrollIntoView({ behavior: 'smooth', block: 'start' }) })

  function goTeam(id) {
    ctx.go(`/e/${encodeURIComponent(slug)}/judge?s=scoring&team=${encodeURIComponent(id)}`)
  }

  async function flushComment() {
    clearTimeout(commentTimer)
    if (commentSave) await commentSave
    const box = main.querySelector('#jsc-comment')
    if (!box) return true
    const val = box.value
    if (val === savedComment) return true
    const pending = (async () => {
      let r
      try { r = await api('PUT', `/v1/hack/events/${encodeURIComponent(slug)}/judge/scores/${encodeURIComponent(teamID)}`, { scores: [], comment: val }) }
      catch (e) { if (e.message !== 'signed out') ui.toast('Cannot reach the server.', box, true); return false }
      if (!r.ok) { ui.toast(errText(r), box, true); return false }
      comment = val
      savedComment = val
      return true
    })()
    commentSave = pending
    try { return await pending } finally { if (commentSave === pending) commentSave = null }
  }

  if (!locked) {
    const commentBox = main.querySelector('#jsc-comment')
    commentBox.addEventListener('input', () => {
      clearTimeout(commentTimer)
      if (Object.keys(scores).length) commentTimer = setTimeout(flushComment, 900)
    })

    main.querySelector('#jsc-criteria').addEventListener('click', e => {
      const chip = e.target.closest('[role=radio]')
      if (chip) choose(chip)
    })
    main.querySelector('#jsc-criteria').addEventListener('keydown', e => {
      if (e.key !== 'Enter' && e.key !== ' ') return
      const chip = e.target.closest('[role=radio]')
      if (!chip) return
      e.preventDefault()
      choose(chip)
    })

    async function choose(chip) {
      const critID = chip.getAttribute('data-crit')
      const points = Number(chip.getAttribute('data-points'))
      const rowEl = chip.closest('[data-crit-row]')
      const prev = scores[critID]
      scores[critID] = points
      rowEl.querySelectorAll('[role=radio]').forEach(s => {
        const on = s === chip
        s.classList.toggle('on', on)
        s.setAttribute('aria-checked', on ? 'true' : 'false')
      })
      chip.setAttribute('aria-disabled', 'true')
      let r
      try { r = await api('PUT', `/v1/hack/events/${encodeURIComponent(slug)}/judge/scores/${encodeURIComponent(teamID)}`, { scores: [{ criterion_id: critID, points }] }) }
      catch (e) {
        chip.removeAttribute('aria-disabled')
        if (e.message !== 'signed out') ui.toast('Cannot reach the server. Try again.', rowEl, true)
        return
      }
      chip.removeAttribute('aria-disabled')
      if (!r.ok) {
        scores[critID] = prev
        rowEl.querySelectorAll('[role=radio]').forEach(s => {
          const p = Number(s.getAttribute('data-points'))
          const on = p === prev
          s.classList.toggle('on', on)
          s.setAttribute('aria-checked', on ? 'true' : 'false')
        })
        ui.toast(errText(r), rowEl, true)
        return
      }
      row.done = !!(r.data && r.data.complete)
      const box = main.querySelector('#jsc-comment')
      if (box.disabled) {
        box.disabled = false
        main.querySelector('#jsc-comment-help').textContent = 'Optional. Not shown with your name. Saves automatically.'
      }
      const saved = main.querySelector('#jsc-saved')
      if (saved) saved.textContent = 'Score saved'
      if (box.value !== savedComment) flushComment()
    }

    const conflictBtn = main.querySelector('#jsc-conflict')
    conflictBtn.addEventListener('click', async () => {
      const ok = await ui.confirm({
        title: `You know someone on ${title}?`,
        body: 'This team leaves your list and your score for it stops counting. You can undo this from My teams.',
        confirmLabel: 'Yes, I know this team',
        cancelLabel: 'Cancel',
        danger: true
      })
      if (!ok) return
      conflictBtn.disabled = true
      let r
      try { r = await api('POST', `/v1/hack/events/${encodeURIComponent(slug)}/conflicts`, { team_id: teamID }) }
      catch (e) { if (e.message !== 'signed out') ui.toast('Cannot reach the server.', conflictBtn, true); conflictBtn.disabled = false; return }
      if (!r.ok) { ui.toast(errText(r), conflictBtn, true); conflictBtn.disabled = false; return }
      const remaining = queue.filter(q => q.team_id !== teamID)
      if (!remaining.length) { ctx.go(`/e/${encodeURIComponent(slug)}/judge?s=queue`); return }
      const nextRow = remaining[Math.min(index, remaining.length - 1)]
      goTeam(nextRow.team_id)
    })

    main.querySelector('#jsc-next').addEventListener('click', async () => {
      const ok = await flushComment()
      if (!ok) return
      if (last) { ctx.go(`/e/${encodeURIComponent(slug)}/judge?s=queue`); return }
      goTeam(queue[index + 1].team_id)
    })
  }

  main.querySelector('#jsc-prev').addEventListener('click', async () => {
    if (index <= 0) return
    if (!locked) { const ok = await flushComment(); if (!ok) return }
    goTeam(queue[index - 1].team_id)
  })
}
