import { offerStageAnnouncement } from './_stage-announce-dialog.js'

export function scoresChangedSincePublish(results, preview, previewOk = true) {
  if (!results || !previewOk) return false
  const totals = rows => new Map((rows || []).map(row => [row.team_id, row.total]))
  const pub = totals(results), cur = totals(preview)
  return [...new Set([...pub.keys(), ...cur.keys()])].some(id => pub.get(id) !== cur.get(id))
}

export async function loadStandings(ctx) {
  const base = '/v1/hack/events/' + encodeURIComponent(ctx.event.event.slug)
  const [published, preview] = await Promise.all([
    ctx.api.api('GET', base + '/results'),
    ctx.api.api('GET', base + '/judging/preview')
  ])
  return {
    published, preview,
    results: published.ok ? (published.data?.results || (Array.isArray(published.data) ? published.data : [])) : null,
    rows: preview.ok ? (preview.data?.results || []) : []
  }
}

function savedPlace(row, group, results) {
  const saved = (results || []).find(r => r.team_id === row.team_id)
  if (!saved || saved.tie_decider !== 'organiser' || saved.total !== row.total) return null
  const first = Number(row.rank)
  if (saved.rank < first || saved.rank >= first + group.length) return null
  if (!group.every(r => (results || []).some(old => old.team_id === r.team_id && old.total === r.total && old.rank >= first && old.rank < first + group.length))) return null
  return saved.rank
}

export async function publishResults(ctx, { setLive, button, confirmText }) {
  const { ui } = ctx
  const ev = ctx.event.event
  button.disabled = true
  try {
    const state = await loadStandings(ctx)
    if (!ctx.alive()) return
    for (const r of [state.published, state.preview]) {
      if (!r.ok) { setLive(ui.errText(r), 'err'); return }
    }
    const published = !!ev.results_published || !!state.results?.length
    const label = published ? 'Publish again' : 'Publish results'
    const body = [ui.el('p', { text: confirmText || 'This replaces the public snapshot with the standings right now. Continue?' })]
    const table = ui.el('table', { class: 't' })
    table.innerHTML = '<thead><tr><th>Rank</th><th>Team</th><th>Score</th></tr></thead>'
    const tbody = ui.el('tbody')
    const byRank = {}
    state.rows.forEach(row => {
      const tr = ui.el('tr')
      tr.append(ui.el('td', { text: row.rank == null ? 'Not scored' : '#' + row.rank }),
        ui.el('td', { text: row.team_name || 'A team' }),
        ui.el('td', { text: row.total == null ? 'No score' : String(row.total) }))
      tbody.appendChild(tr)
      if (row.tied && row.rank != null) (byRank[row.rank] ||= []).push(row)
    })
    table.appendChild(tbody)
    body.push(table)
    const inputs = []
    if (Object.keys(byRank).length) body.push(ui.el('p', { text: 'Some teams tie. Give each one a final place:' }))
    for (const [rank, rows] of Object.entries(byRank)) {
      body.push(ui.el('p', { class: 'small muted', text: 'Tied for #' + rank + ':' }))
      const group = ui.el('div', { class: 'tie-group' })
      rows.forEach(row => {
        const id = 'pub-tie-' + row.team_id
        const input = ui.el('input', { 'data-help': 'ties', class: 'input', id, type: 'number', min: '1', step: '1', value: String(savedPlace(row, rows, state.results) ?? row.rank) })
        group.append(ui.el('label', { for: id, text: row.team_name || 'A team' }), input)
        inputs.push({ team_id: row.team_id, input })
      })
      body.push(group)
    }
    if (!await ui.confirm({ title: label + '?', body, confirmLabel: label })) return
    const r = await ctx.api.api('POST', '/v1/hack/events/' + encodeURIComponent(ev.slug) + '/results/publish', {
      rank_overrides: inputs.map(({ team_id, input }) => ({ team_id, rank: Number(input.value) }))
    })
    if (!r.ok) { setLive(ui.errText(r), 'err'); return }
    if (ev.stage !== 'archived') await offerStageAnnouncement(ctx, {
      move: 'publish_results', back: false,
      emailableCount: ctx.event.organiser?.counts?.emailable_participants || 0, setLive
    })
    await ctx.refresh()
  } catch (e) {
    if (!ctx.api.isSignedOut(e)) setLive('Cannot reach the server.', 'err')
  } finally { button.disabled = false }
}
